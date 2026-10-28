package compiler

import (
	goparser "go/parser"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// TestTLiteralTyped pins the typed pass-2 output of the tliteral case
// (PLAN.md §2.10) and its diagnostics: whole-value literals on elements
// trusted on style and on* when every piece is inert, a passed-through
// on* with a string piece warned. The byte identity with the fallback is
// TestPipelineTypedVsFallback's.
func TestTLiteralTyped(t *testing.T) {
	c := loadCase(t, "testdata/codegen/tliteral")
	m := newGenModule(t)
	p := plPipeline(t, m, plReadCase(t, c), c.opts)
	var diags []string
	kinds := map[string]WriteKind{} // piece or concatenation code → its write (the last)
	for _, pf := range p.files {
		if !pf.typed.Checked {
			t.Fatalf("%s: not type-checked", pf.path)
		}
		for _, d := range pf.typed.Diagnostics {
			diags = append(diags, d.Format(pf.path, pf.f.Lines))
		}
		pkg, _ := PackageFor(pf.path)
		golden(t, "testdata/codegen/tliteral/"+strings.ReplaceAll(pkg.Rel, "/", "_")+".render.go.typed.golden", string(pf.typed.Output.Impl.Content))
		for _, cs := range pf.pass1.CallSites {
			if cs.Kind == SitePiece || cs.Kind == SiteConcat {
				kinds[cs.Code.Text] = pf.typed.Writes[cs.Key].Kind
			}
		}
	}
	want := []string{
		"src/pages/index.orbit:13:92: warning: onclick={…} is escaped for HTML, not for JavaScript; don't put untrusted values here",
	}
	if strings.Join(diags, "\n") != strings.Join(want, "\n") {
		t.Errorf("diagnostics:\n%s\nwant:\n%s", strings.Join(diags, "\n"), strings.Join(want, "\n"))
	}
	for code, want := range map[string]WriteKind{
		"slug": WriteAttrString, "n + 1": WriteInt, "f": WriteFloat, "f32": WriteFloat32, "on": WriteBool, "u": WriteUint,
		"c": WriteStringer, "err": WriteFallback, "nil": WriteSkip, "html": WriteAttrString, "k": WriteFold, `"x"`: WriteFold,
		"1.5": WriteFold, "'a'": WriteFold, "bs": WriteBytes, "`up ${name}`": WriteConcat, "`Close ${name}`": WriteConcat,
	} {
		if got, ok := kinds[code]; !ok || got != want {
			t.Errorf("%s: %v (found %v), want %v", code, got, ok, want)
		}
	}
}

// TestTLiteralAllocs checks PLAN.md §2.10's cost table: the tliteral
// case's allocs page (whole-value literals with string, int, float, bool
// and Stringer pieces in attributes and content, a URL attribute with a
// static prefix and one with a run-time check, a literal class merged with
// class:list and a literal class:list item) renders with 0 allocations in
// both flavors; a prop or a non-whole literal with a dynamic piece costs
// at most 1 (tlAllocTestSrc's other cases).
func TestTLiteralAllocs(t *testing.T) {
	c := loadCase(t, "testdata/codegen/tliteral")
	m := newGenModule(t)
	p := plPipeline(t, m, plReadCase(t, c), c.opts)
	var res []*cgResult
	for _, pf := range p.files {
		if !pf.typed.Checked {
			t.Fatalf("%s: not type-checked", pf.path)
		}
		res = append(res, &cgResult{path: pf.path, f: pf.f, a: pf.a, in: pf.in})
	}
	direct := directCalls(res)
	builds := map[string]*Output{}
	for _, pf := range p.files {
		m.writeOutput(pf.typed.Output)
		if d := direct[pf.path]; d != nil {
			in := *pf.in
			in.Typed, in.Direct = pf.typed.Writes, d
			builds[pf.path] = Generate(&in)
		}
	}
	m.writeOverlay(builds)
	m.write("alloc/alloc_test.go", tlAllocTestSrc)
	allocs := func(args ...string) string {
		out, err := m.goCmd(append(append([]string{"test", "-count=1", "-v"}, args...), "./alloc")...)
		if err != nil {
			t.Fatalf("go test %v: %v\n%s", args, err, out)
		}
		var got []string
		for _, l := range strings.Split(out, "\n") {
			if _, v, ok := strings.Cut(l, "allocs "); ok {
				got = append(got, strings.TrimSpace(v))
			}
		}
		return strings.Join(got, "; ")
	}
	const want = "page=0; prop=1; concat=1; const=0"
	if got := allocs("-overlay=" + OverlayFile); got != want {
		t.Errorf("build flavor: %s, want %s", got, want)
	}
	if got := allocs(); got != want {
		t.Errorf("hook flavor: %s, want %s", got, want)
	}
}

// tlAllocTestSrc renders the tliteral case's allocs page and prints its
// allocations per render, then those of the runtime's concatenation
// helpers as generated code uses them: a prop's TB chain, a non-whole
// literal's +, a constant.
const tlAllocTestSrc = `package alloc

import (
	"bytes"
	"testing"

	allocs "example.com/blog/orbit_gen/decl/pages/allocs"
	_ "example.com/blog/orbit_gen/impl/pages/allocs"
	"github.com/orbit-z/orbit"
)

type props struct{ Title string }

var sink props

//go:noinline
func use(p props) { sink = p }

func TestAllocs(t *testing.T) {
	var buf bytes.Buffer
	w := orbit.NewWriter(&buf)
	ctx := &orbit.Ctx{}
	n := testing.AllocsPerRun(100, func() {
		buf.Reset()
		if err := allocs.Render(ctx, w, allocs.Props{}, orbit.Slots{}); err != nil {
			t.Fatal(err)
		}
	})
	t.Logf("allocs page=%v", n)
	i, s := 300, "ab"
	t.Logf("allocs prop=%v", testing.AllocsPerRun(100, func() {
		use(props{Title: orbit.TB(make([]byte, 0, 64)).S("Chapter ").Int(int64(i)).String()})
	}))
	t.Logf("allocs concat=%v", testing.AllocsPerRun(100, func() {
		use(props{Title: ("x-" + s)})
	}))
	t.Logf("allocs const=%v", testing.AllocsPerRun(100, func() {
		use(props{Title: "Chapter 3"})
	}))
}
`

// TestTLiteralErrors: the typed pass's errors and warnings for template
// literals (PLAN.md §2.10): props that can't hold a string (pass 1 for a
// visibly non-string type, the typed pass for the rest), pieces that
// aren't inert for an orbit.URL/HTML/CSS target or set:html, Renderer
// pieces, and the style warning decided by the pieces' static types
// (Q4).
func TestTLiteralErrors(t *testing.T) {
	srcs := map[string]string{
		"src/components/Card.orbit": "---\nimport \"fmt\"\n\ntype Kind int\n\ntype Props struct {\n\tCount int\n\tKind  Kind\n\tHref  orbit.URL\n\tBody  orbit.HTML\n\tAny   fmt.Stringer\n}\n---\n<a href={props.Href}>{props.Count}{props.Kind}{props.Any}</a><i set:html={props.Body}></i>\n",
		"src/pages/index.orbit": "---\nimport \"fmt\"\n\nimport Card \"../components/Card.orbit\"\n\n" +
			"type Count int\n\nfunc (c Count) String() string { return \"c\" }\n\n" +
			"type PCount int\n\nfunc (c *PCount) String() string { return \"p\" }\n\n" +
			"type ECount int\n\nfunc (ECount) Error() string { return \"e\" }\n\n" +
			"type FCount int\n\nfunc (FCount) Format(fmt.State, rune) {}\n\n" +
			"type Pct int\n\nconst k = 3\n\nconst kc Count = 3\n\n" +
			"n, s, c, pc, ec, fc, pct := 1, \"s\", Count(1), PCount(2), ECount(3), FCount(4), Pct(5)\n" +
			"u := orbit.URL(\"/u\")\ncss := orbit.CSS(\"a:b\")\n---\n" +
			"<Card count={`x${n}`} />\n" + // 32: pass 1
			"<Card kind={`x${n}`} />\n" + // 33: typed
			"<Card href={`/b/${s}`} />\n" + // 34: string piece
			"<Card href={`/b/${n}/${k}/${u}/${pct}/${1.5}/${true}`} body={`<b>${n}</b>`} />\n" + // 35: inert
			"<Card href={`/${c}`} />\n<Card href={`/${pc}`} />\n<Card href={`/${ec}`} />\n<Card href={`/${fc}`} />\n<Card href={`/${kc}`} />\n" + // 36–40
			"<Card body={`${u}`} />\n" + // 41: a URL piece isn't inert for HTML
			"<p set:html={`<b>${s}</b>`}></p>\n" + // 42
			"<p>{`${slots.Get(\"x\")}`}</p>\n" + // 43: Renderer
			"<p style={`w:${n}%;${css}`} onclick={`f(${n}, ${k})`}></p>\n" + // 44: trusted
			"<p style={`w:${c}`}></p>\n<p style={`w:${s}`}></p>\n<p style={`w:${u}`}></p>\n" + // 45–47: warned
			"<p style={`w:${pct}%`}>{len(`${n}`)}</p>\n",
	}
	m := newGenModule(t)
	pl := plPipeline(t, m, srcs, Options{Module: testModule})
	var got []string
	for _, pf := range pl.files {
		for _, d := range pf.pass1.Diagnostics {
			got = append(got, "pass1 "+d.Format(pf.path, pf.f.Lines))
		}
		for _, d := range pf.typed.Diagnostics {
			got = append(got, d.Format(pf.path, pf.f.Lines))
		}
	}
	const url = " piece can't be part of an orbit.URL prop: make the prop a string (filtered where it's written) or convert the piece with orbit.URL(…)"
	const style = ": warning: style={…} is escaped for HTML, not for CSS; don't put untrusted values here"
	want := []string{
		// Pass 1 only: the typed pass doesn't repeat it.
		"pass1 src/pages/index.orbit:32:7: a template literal is a string; prop count of <Card> is int",
		"src/pages/index.orbit:33:7: a template literal is a string; prop kind of <Card> is card.Kind",
		"src/pages/index.orbit:34:19: a string" + url,
		"src/pages/index.orbit:36:17: a Count" + url,  // String on Count
		"src/pages/index.orbit:37:17: a PCount" + url, // String on *PCount
		"src/pages/index.orbit:38:17: an ECount" + url,
		"src/pages/index.orbit:39:17: a FCount" + url,
		"src/pages/index.orbit:40:17: a Count" + url, // a constant of a Stringer type
		"src/pages/index.orbit:41:16: an orbit.URL piece can't be part of an orbit.HTML prop: make the prop a string (filtered where it's written) or convert the piece with orbit.HTML(…)",
		"src/pages/index.orbit:42:20: a string piece can't be part of set:html: write the literal as {…} content, or convert the piece with orbit.HTML(…)",
		`src/pages/index.orbit:43:8: can't put slots.Get("x") (orbit.Renderer) in a template literal: it renders HTML, and a template literal is text`,
		"src/pages/index.orbit:45:4" + style,
		"src/pages/index.orbit:46:4" + style,
		"src/pages/index.orbit:47:4" + style,
	}
	slices.Sort(got)
	slices.Sort(want)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("got:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// tlDump describes a literal's parts: static text quoted, pieces in ${…},
// nested literals in their pieces as [lit …].
func tlDump(l *TemplateLiteral) string {
	var b strings.Builder
	for _, p := range l.Parts {
		if !p.Piece {
			b.WriteString(strconv.Quote(p.Text))
			continue
		}
		b.WriteString("${" + p.Text)
		for _, n := range p.Lits {
			b.WriteString(" [lit " + tlDump(n) + "]")
		}
		b.WriteString("}")
	}
	if !l.Closed {
		b.WriteString(" (unterminated)")
	}
	return b.String()
}

func TestParseTemplateLiteral(t *testing.T) {
	tests := []struct{ code, want string }{
		{"``", `""`},
		{"`a`", `"a"`},
		{"`a ${x} b`", `"a "${x}" b"`},
		{"`${x}`", `""${x}""`},
		{"`${ x }${y}`", `""${x}""${y}""`},
		{"`a ${f(`x ${y}`)} b`", `"a "${f(` + "`x ${y}`" + `) [lit "x "${y}""]}" b"`},
		{"`${f(`${g(`${z}`)}`)}`", `""${f(` + "`${g(`${z}`)}`" + `) [lit ""${g(` + "`${z}`" + `) [lit ""${z}""]}""]}""`},
		{"`${f(\"}\")}`", `""${f("}")}""`},
		{"`${\"`\"}`", `""${"` + "`" + `"}""`},
		{"`${'}'}`", `""${'}'}""`},
		{"`${ /* } */ x}`", `""${/* } */ x}""`},
		{"`${map[string]int{\"a\": 1}[\"a\"]}`", `""${map[string]int{"a": 1}["a"]}""`},
		{"`$x $ {x} ${`", `"$x $ {x} "${` + "`" + ` [lit "" (unterminated)]}"" (unterminated)`},
		{"`\\${x} \\` \\\\ \\$`", `"${x} ` + "`" + ` \\ $"`},
		{"`\\n\\r\\t\\b\\f\\v\\0\\'\\\"`", `"\n\r\t\b\f\v\x00'\""`},
		{"`\\x41\\u00e9\\u{1F600}\\uD83D\\uDE00`", `"Aé😀😀"`},
		{"`a\\\nb\\\r\nc`", `"abc"`},
		{"`a\r\nb\rc`", `"a\nb\nc"`},
		{"`multi\nline ${n}`", `"multi\nline "${n}""`},
	}
	for _, tt := range tests {
		var diags []Diagnostic
		lits := findLiterals(tt.code, Span{0, len(tt.code)}, &diags)
		if len(lits) != 1 {
			t.Errorf("%q: %d literals", tt.code, len(lits))
			continue
		}
		if got := tlDump(lits[0]); got != tt.want {
			t.Errorf("%q:\n got %s\nwant %s", tt.code, got, tt.want)
		}
	}
}

// TestTemplateLiteralPositions: each piece's span is its code in the
// source, on any line of a multi-line literal; static spans are the raw
// text.
func TestTemplateLiteralPositions(t *testing.T) {
	src := "<p>{f(`a\n  ${x}\nb ${g(`${y}`)}`)}</p>"
	var diags []Diagnostic
	lits := findLiterals(src, Span{4, len(src) - 5}, &diags)
	if len(diags) != 0 || len(lits) != 1 {
		t.Fatalf("%d literals, %v", len(lits), diags)
	}
	l := lits[0]
	for _, p := range l.Parts {
		if p.Piece && src[p.Start:p.End] != p.Text {
			t.Errorf("piece %q at %v holds %q", p.Text, p.Span, src[p.Start:p.End])
		}
	}
	if x := l.Parts[1]; x.Text != "x" || NewLineIndex(src).Position(x.Start) != (Position{Line: 2, Col: 5}) {
		t.Errorf("piece x: %+v at %+v", x, NewLineIndex(src).Position(x.Start))
	}
	if y := l.Parts[3].Lits[0].Parts[1]; y.Text != "y" || src[y.Start:y.End] != "y" {
		t.Errorf("nested piece y: %+v", y)
	}
	if l.Span != (Span{6, len(src) - 6}) || src[l.End-1] != '`' {
		t.Errorf("literal span %v", l.Span)
	}
}

func TestTemplateLiteralPlaceholder(t *testing.T) {
	tests := []struct{ code, want string }{
		{"f(x)", "f(x)"},
		{"`a`", "` `"},
		{"`a ${x} b ${y}`", "_(   x,     y )"},
		{"f(`a${g(`${y}`)}`, z)", "f(_(  g(_( y )) ), z)"},
		{"`\\``", "`  `"},
		{"`${x}`", "_( x )"},
	}
	for _, tt := range tests {
		got := tlPlaceholderText(tt.code)
		if got != tt.want || len(got) != len(tt.code) {
			t.Errorf("%q: %q, want %q", tt.code, got, tt.want)
		}
		if _, err := goparser.ParseExpr(got); err != nil {
			t.Errorf("%q: placeholder %q doesn't parse: %v", tt.code, got, err)
		}
	}
}

func TestTLURLPrefix(t *testing.T) {
	for prefix, want := range map[string]tlURL{
		"": tlURLCheck, "  ": tlURLCheck, "java": tlURLCheck, "JaVa\tScRi": tlURLCheck, "vb": tlURLCheck, "j": tlURLCheck,
		"javascript:": tlURLUnsafe, " \x01JAVASCRIPT:x": tlURLUnsafe, "vbscript:": tlURLUnsafe, "java\nscript:": tlURLUnsafe,
		"/": tlURLSafe, "#a": tlURLSafe, "?q=": tlURLSafe, "./x": tlURLSafe, "https://": tlURLSafe, "mailto:": tlURLSafe,
		"jav ": tlURLSafe, "javascript": tlURLCheck, "javascripts:": tlURLSafe,
	} {
		if got := tlURLPrefix(prefix); got != want {
			t.Errorf("tlURLPrefix(%q) = %d, want %d", prefix, got, want)
		}
	}
}

func FuzzTemplateLiteral(f *testing.F) {
	for _, s := range []string{
		"``", "`a`", "`a ${x} b`", "`${f(`x ${y}`)}`", "`${f(`${g(`${z}`)}`)}`", "`${\"}\"}`", "`${\"`\"}`",
		"`${ /* } */ x}`", "`\\${x}`", "`\\``", "`\\u{1F600}\\x41\\n`", "`a\\\nb`", "`${}`", "`${x`", "`a",
		"`${'`'}`", "`${x // }\n}`", "`$${x}$`",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, code string) {
		if code == "" || code[0] != '`' {
			code = "`" + code
		}
		src := "{" + code + "}"
		var diags []Diagnostic
		lits := findLiterals(src, Span{1, len(src) - 1}, &diags)
		if len(lits) == 0 {
			t.Fatalf("no literal in %q", src)
		}
		end := scanTemplateLiteral(src, 1)
		l := lits[0]
		// The scanner and the parser agree on where the literal ends.
		if end >= 0 && (!l.Closed || l.End != end+1) {
			t.Fatalf("%q: scan ends at %d, parse %v closed %v", src, end, l.Span, l.Closed)
		}
		if end < 0 && l.Closed {
			t.Fatalf("%q: scan fails, parse closes at %v", src, l.Span)
		}
		tlFuzzCheck(t, src, l, 0)
		// The placeholder keeps the length.
		if p := tlPlaceholder(src, Span{1, len(src) - 1}); len(p) != len(src)-2 {
			t.Fatalf("%q: placeholder %q", src, p)
		}
		_ = scanBraced(src, 0)
	})
}

// tlFuzzCheck checks a literal's parts: they alternate, tile the literal
// in order, a piece holds its source text and ends where scanBraced of
// {piece} does, and nested literals sit inside their pieces.
func tlFuzzCheck(t *testing.T, src string, l *TemplateLiteral, depth int) {
	if depth > 100 {
		t.Fatalf("nesting too deep")
	}
	if len(l.Parts)%2 != 1 {
		t.Fatalf("%q: %d parts", src, len(l.Parts))
	}
	prev := l.Start + 1
	for i, p := range l.Parts {
		if p.Piece != (i%2 == 1) {
			t.Fatalf("%q: part %d piece=%v", src, i, p.Piece)
		}
		if p.Start < prev || p.End < p.Start || p.End > l.End {
			t.Fatalf("%q: part %d at %v after %d in %v", src, i, p.Span, prev, l.Span)
		}
		if p.Piece {
			if src[p.Start:p.End] != p.Text {
				t.Fatalf("%q: piece %q holds %q", src, p.Text, src[p.Start:p.End])
			}
			if l.Closed {
				// The piece's braces: "${" before it (only whitespace
				// between), its '}' after it.
				open := strings.LastIndex(src[:p.Start], "${") + 1
				close := p.End
				for close < len(src) && isGoSpace(src[close]) {
					close++
				}
				if got := scanBraced(src, open); got != close {
					t.Fatalf("%q: piece %q ends at %d, scanBraced of its braces says %d", src, p.Text, close, got)
				}
			}
			for _, n := range p.Lits {
				if n.Start < p.Start || n.End > p.End {
					t.Fatalf("%q: nested %v outside piece %v", src, n.Span, p.Span)
				}
				tlFuzzCheck(t, src, n, depth+1)
			}
		}
		prev = p.End
	}
}

// fzPiecesValid reports whether every template literal in code is closed
// and lexically valid, with each piece one Go expression (as generated
// code needs), nested ones included.
func fzPiecesValid(code string) bool {
	var diags []Diagnostic
	lits := findLiterals(code, Span{0, len(code)}, &diags)
	if len(diags) > 0 {
		return false
	}
	var check func([]*TemplateLiteral) bool
	check = func(ls []*TemplateLiteral) bool {
		for _, l := range ls {
			if !l.Closed {
				return false
			}
			for _, p := range l.Parts {
				if !p.Piece {
					continue
				}
				if _, err := goparser.ParseExpr(tlPlaceholderText(p.Text)); err != nil || !check(p.Lits) {
					return false
				}
			}
		}
		return true
	}
	return check(lits)
}

// literalSite reports whether cs belongs to a template literal (PLAN.md
// §2.10) or holds one: its Arg isn't its code copied byte for byte (a
// concatenation's call, a rewritten expression), or its Key isn't its
// code's start.
func literalSite(cs CallSite) bool {
	switch cs.Kind {
	case SiteConcat, SiteLit:
		return true
	}
	return strings.IndexByte(cs.Code.Text, '`') >= 0
}

// TestTemplateLiteralNextLine: an unterminated literal that opens on the
// line after its "{" is outside the construct as recovered (cut at the end
// of the "{"'s line), so the "{" itself is reported (found by FuzzFormat).
func TestTemplateLiteralNextLine(t *testing.T) {
	for _, src := range []string{"<div title={\n`></div>", "<div class:list={\n`></div>", "<p>{\n`</p>"} {
		found := false
		for _, d := range Parse(src, Strict).Diagnostics {
			found = found || d.Severity == SeverityError
		}
		if !found {
			t.Errorf("%q: no error", src)
		}
	}
}
