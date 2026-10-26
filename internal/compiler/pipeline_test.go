package compiler

// End-to-end tests of the milestone 4 pipeline with every part real:
// Parse → Analyze (A) → NewComponentInfo → Generate pass 1 (B) → TypedPass
// with a GoListLoader against the harness module (C) → Generate pass 2,
// then go build, go vet and a render through harness_test.go.

import (
	"bytes"
	"context"
	"fmt"
	"go/ast"
	goparser "go/parser"
	gotoken "go/token"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// plFile is one .orbit file through the pipeline.
type plFile struct {
	path  string
	f     *File
	a     *Analysis
	in    *Input
	pass1 *Output
	typed TypedUnit // pass 2 (Output is pass 1 when not Checked)
}

// plProject is a mini project through the pipeline.
type plProject struct {
	files    []*plFile // sorted by path
	index    ComponentIndex
	warnings []string
}

// plFront runs the front of the pipeline on srcs (project-relative path →
// .orbit source): parse, Analyze, the index from the real analyses, and
// pass-1 Generate.
func plFront(srcs map[string]string, opts Options) *plProject {
	p := &plProject{index: ComponentIndex{}}
	paths := make([]string, 0, len(srcs))
	for path := range srcs {
		paths = append(paths, path)
	}
	slices.Sort(paths)
	for _, path := range paths {
		f := Parse(srcs[path], opts.Mode)
		a := Analyze(f, cgFileInfo(path))
		p.index[path] = NewComponentInfo(f, a)
		p.files = append(p.files, &plFile{path: path, f: f, a: a})
	}
	for _, pf := range p.files {
		pf.in = &Input{File: pf.f, Analysis: pf.a, Index: p.index, Options: opts}
		pf.pass1 = Generate(pf.in)
	}
	return p
}

// diagnostics lists every parse, analysis and codegen diagnostic.
func (p *plProject) diagnostics() []string {
	var out []string
	for _, pf := range p.files {
		for _, ds := range [][]Diagnostic{pf.f.Diagnostics, pf.a.Diagnostics, pf.pass1.Diagnostics} {
			for _, d := range ds {
				out = append(out, d.Format(pf.path, pf.f.Lines))
			}
		}
	}
	return out
}

// typedPass runs TypedPass with loader over the pass-1 units.
func (p *plProject) typedPass(t testing.TB, loader ExportLoader) {
	t.Helper()
	units := make([]Unit, len(p.files))
	for i, pf := range p.files {
		units[i] = Unit{Input: pf.in, Pass1: pf.pass1}
	}
	res, err := TypedPass(context.Background(), units, loader)
	if err != nil {
		t.Fatal(err)
	}
	for i, u := range res.Units {
		p.files[i].typed = u
	}
	p.warnings = res.Warnings
}

// plPipeline runs the whole pipeline on srcs with the real GoListLoader in
// m (whose user Go packages must already be written).
func plPipeline(t testing.TB, m *genModule, srcs map[string]string, opts Options) *plProject {
	t.Helper()
	p := plFront(srcs, opts)
	if opts.Mode == Strict {
		p.typedPass(t, tyGoListLoader(m))
	}
	return p
}

// write writes pass 1 (typed false) or pass 2 of every file into m.
func (p *plProject) write(m *genModule, typed bool) {
	for _, pf := range p.files {
		if typed {
			m.writeOutput(pf.typed.Output)
		} else {
			m.writeOutput(pf.pass1)
		}
	}
}

// plReadCase reads the .orbit sources of a codegen case.
func plReadCase(t testing.TB, c *cgCase) map[string]string {
	t.Helper()
	srcs := map[string]string{}
	for _, p := range c.files {
		b, err := os.ReadFile(filepath.Join(c.dir, filepath.FromSlash(p)))
		if err != nil {
			t.Fatal(err)
		}
		srcs[p] = string(b)
	}
	return srcs
}

// TestPipelineTypedVsFallback runs every rendering codegen fixture through
// the real pipeline: pass 1 must match the goldens (so the real Analyze
// agrees with what codegen was built against), every unit must be
// type-checked, pass 2 must keep the call sites and the map invariants,
// and the typed build must vet clean and render byte-identical HTML to the
// pass-1 (Expr/Attr) build and to want.html.
func TestPipelineTypedVsFallback(t *testing.T) {
	dirs, err := filepath.Glob("testdata/codegen/*/main.go.txt")
	if err != nil {
		t.Fatal(err)
	}
	for _, mainPath := range dirs {
		dir := filepath.Dir(mainPath)
		t.Run(filepath.Base(dir), func(t *testing.T) {
			t.Parallel()
			c := loadCase(t, dir)
			m := newGenModule(t)
			p := plPipeline(t, m, plReadCase(t, c), c.opts)
			if d := p.diagnostics(); len(d) > 0 {
				t.Fatalf("diagnostics:\n%s", strings.Join(d, "\n"))
			}
			if len(p.warnings) > 0 {
				t.Fatalf("warnings: %v", p.warnings)
			}
			typedWrites := 0
			for _, pf := range p.files {
				pkg, _ := PackageFor(pf.path)
				base := filepath.Join(dir, strings.ReplaceAll(pkg.Rel, "/", "_"))
				for _, g := range []struct {
					file string
					got  []byte
				}{{".decl.go.golden", pf.pass1.Decl.Content}, {".render.go.golden", pf.pass1.Impl.Content}} {
					want, err := os.ReadFile(base + g.file)
					if err != nil {
						t.Fatal(err)
					}
					if !bytes.Equal(g.got, want) {
						t.Errorf("%s: pass 1 differs from %s%s", pf.path, base, g.file)
					}
				}
				if !pf.typed.Checked {
					t.Errorf("%s: not type-checked", pf.path)
					continue
				}
				out := pf.typed.Output
				if len(out.CallSites) != len(pf.pass1.CallSites) {
					t.Fatalf("%s: %d call sites in pass 2, %d in pass 1", pf.path, len(out.CallSites), len(pf.pass1.CallSites))
				}
				for i, cs := range out.CallSites {
					if cs.Key != pf.pass1.CallSites[i].Key {
						t.Errorf("%s: site %d key %d, pass 1 %d", pf.path, i, cs.Key, pf.pass1.CallSites[i].Key)
					}
				}
				for _, w := range pf.typed.Writes {
					if w.Kind != WriteFallback {
						typedWrites++
					}
				}
				in := *pf.in
				in.Typed = pf.typed.Writes
				checkOutput(t, &cgResult{path: pf.path, f: pf.f, a: pf.a, in: &in, out: out})
			}
			if typedWrites == 0 {
				t.Errorf("no typed writes chosen")
			}
			mainSrc, err := os.ReadFile(mainPath)
			if err != nil {
				t.Fatal(err)
			}
			want, err := os.ReadFile(filepath.Join(dir, "want.html"))
			if err != nil {
				t.Fatal(err)
			}
			var html [2]string
			for i, typed := range []bool{false, true} {
				p.write(m, typed)
				m.buildAndVet()
				html[i] = m.run(string(mainSrc))
			}
			if html[0] != html[1] {
				t.Errorf("typed HTML differs from the fallback:\n--- typed\n%s\n--- fallback\n%s", html[1], html[0])
			}
			if html[1] != string(want) {
				t.Errorf("typed HTML differs from want.html:\n--- typed\n%s\n--- want\n%s", html[1], want)
			}
		})
	}
}

// TestCleanGoOutputUserPackage: a user package named card next to a
// component Card (decl package card). go/types already quotes the paths
// whenever two packages named card meet in one check, even transitively
// (trans.orbit); on top of that, a bare card.X is rewritten to Card.X only
// when X is a name the decl package declares, and only in a file that
// imports no Go package named card (checked by hand at the end).
func TestCleanGoOutputUserPackage(t *testing.T) {
	m := newGenModule(t)
	m.write("internal/card/card.go", "package card\n\ntype Thing struct{ N int }\n\ntype Props struct{}\n")
	srcs := map[string]string{
		"src/components/Card.orbit": "---\ntype Props struct{ Title string }\n---\n<p>{props.Title}</p>\n",
		"src/components/Box.orbit":  "---\nimport \"example.com/blog/internal/card\"\n\ntype Props struct{ T card.Thing }\n---\n<p>{props.T.N}</p>\n",
		// Only the component: card.Props is Card.Props.
		"src/pages/only.orbit": "---\nimport Card \"../components/Card.orbit\"\n\nvar n int = Card.Props{}\n---\n<Card title=\"x\" /><p>{n}</p>\n",
		// The user package too: Go quotes both paths.
		"src/pages/both.orbit": "---\nimport \"example.com/blog/internal/card\"\nimport Card \"../components/Card.orbit\"\n\n" +
			"var s string = Card.Props{}\nvar n int = card.Thing{}\nvar u card.Thing = Card.Props{}\n---\n<Card title=\"x\" /><p>{n}{s}{u.N}</p>\n",
		// The user package only, with a Props of its own: nothing is
		// rewritten.
		"src/pages/user.orbit": "---\nimport \"example.com/blog/internal/card\"\nimport Card \"../components/Card.orbit\"\n\n" +
			"var n int = card.Props{}\n---\n<Card title=\"x\" /><p>{n}</p>\n",
		// The user package reached through Box.Props.
		"src/pages/trans.orbit": "---\nimport Card \"../components/Card.orbit\"\nimport Box \"../components/Box.orbit\"\n\n" +
			"var n int = Box.Props{}.T\n---\n<Card title=\"x\" /><Box /><p>{n}</p>\n",
	}
	p := plPipeline(t, m, srcs, Options{Module: testModule})
	if d := p.diagnostics(); len(d) > 0 {
		t.Fatalf("diagnostics:\n%s", strings.Join(d, "\n"))
	}
	p.write(m, true)
	build, vet := m.buildErrors()
	var analyses []*Analysis
	for _, pf := range p.files {
		analyses = append(analyses, pf.a)
	}
	names := NewErrorNames(testModule, analyses)
	for name, out := range map[string]string{"build": build, "vet": vet} {
		got := string(CleanGoOutput([]byte(out), names))
		for _, want := range []string{
			"src/pages/only.orbit:4:13: cannot use Card.Props{} (value of struct type Card.Props) as int value in variable declaration",
			"src/pages/both.orbit:5:16: cannot use Card.Props{} (value of struct type Card.Props) as string value in variable declaration",
			"src/pages/user.orbit:5:13: cannot use card.Props{} (value of struct type \"example.com/blog/internal/card\".Props) as int value in variable declaration",
			"src/pages/trans.orbit:5:13: cannot use Box.Props{}.T (value of struct type \"example.com/blog/internal/card\".Thing) as int value in variable declaration",
		} {
			if !hasLine(got, want) {
				t.Errorf("cleaned go %s lacks\n%s\n--- raw\n%s--- cleaned\n%s", name, want, out, got)
			}
		}
		if name == "build" {
			want := `src/pages/both.orbit:7:20: cannot use Card.Props{} (value of struct type Card.Props) as "example.com/blog/internal/card".Thing value in variable declaration`
			if !hasLine(got, want) {
				t.Errorf("cleaned go build lacks\n%s\n--- raw\n%s--- cleaned\n%s", want, out, got)
			}
		}
	}
	// Bare names with a user import of the same name stay as they are.
	n := &ErrorNames{Module: testModule,
		Own:      map[string]string{"src/pages/x.orbit": testModule + "/orbit_gen/decl/pages/x"},
		Aliases:  map[string]map[string]string{"src/pages/x.orbit": {testModule + "/orbit_gen/decl/components/card": "Card"}},
		Imported: map[string][]string{"src/pages/x.orbit": importNames(GoImport{Path: "example.com/lib/card/v2"})},
		Declared: map[string][]string{testModule + "/orbit_gen/decl/components/card": {"Props"}},
	}
	if got := string(CleanGoOutput([]byte("./src/pages/x.orbit:1:1: card.Props\n"), n)); got != "src/pages/x.orbit:1:1: card.Props\n" {
		t.Errorf("ambiguous bare name rewritten: %q", got)
	}
	n.Imported = nil
	if got := string(CleanGoOutput([]byte("./src/pages/x.orbit:1:1: card.Props, card.Thing\n"), n)); got != "src/pages/x.orbit:1:1: Card.Props, card.Thing\n" {
		t.Errorf("got %q", got)
	}
}

func TestImportNames(t *testing.T) {
	for _, tc := range []struct {
		im   GoImport
		want string
	}{
		{GoImport{Path: "example.com/x/card"}, "[card]"},
		{GoImport{Name: "c", Path: "example.com/x/card"}, "[c]"},
		{GoImport{Name: "_", Path: "example.com/x/card"}, "[]"},
		{GoImport{Path: "example.com/x/card/v2"}, "[card]"},
		{GoImport{Path: "github.com/x/go-card"}, "[go-card card]"},
		{GoImport{Path: "gopkg.in/yaml.v3"}, "[yaml.v3 yaml]"},
		{GoImport{Path: "fmt"}, "[fmt]"},
	} {
		if got := fmt.Sprint(importNames(tc.im)); got != tc.want {
			t.Errorf("importNames(%+v) = %s, want %s", tc.im, got, tc.want)
		}
	}
}

// FuzzGenerate: for any input, Parse → Analyze → Generate never panics in
// either mode; when every Go region the user wrote is valid Go on its own,
// the generated Go parses in Tolerant mode (and in Strict mode when nothing
// was reported), so the glue never breaks parsing; every source-map segment
// is in range, sorted, and round-trips; call sites hold their code.
func FuzzGenerate(f *testing.F) {
	for _, glob := range []string{"testdata/parse/*.orbit", "testdata/analyze/*.orbit", "testdata/codegen/*/src/*/*.orbit",
		"testdata/codegen/*/src/*/*/*.orbit", "testdata/positions/src/*/*.orbit"} {
		paths, _ := filepath.Glob(glob)
		for _, path := range paths {
			src, _ := os.ReadFile(path)
			f.Add(string(src))
		}
	}
	for _, s := range []string{
		"", "{", "<Card", "<Card title={", "<Card {...x} />", "<Self />", "<slot name=\"a\">x</slot>",
		"---\nimport Card \"../components/Card.orbit\"\n---\n<Card title={1} x>{y}</Card>",
		"---\nreturn nil\n---\n<p>{x}</p>", "{for x := range y}<li>{x}</li>{/for}", "{if a}{else if b}{else}{/if}",
		"<p set:html={x} set:text={y} />", "<head></head>", "<pre>  {x}  </pre>", "<a href={x} {y}>",
	} {
		f.Add(s)
	}
	card := "---\ntype Props struct {\n\tTitle string\n\tIsOpen bool\n}\n---\n<slot /><slot name=\"footer\" />"
	cf := Parse(card, Strict)
	index := ComponentIndex{"src/components/Card.orbit": NewComponentInfo(cf, Analyze(cf, cgFileInfo("src/components/Card.orbit")))}
	paths := []string{"src/components/X.orbit", "src/pages/blog/[slug].orbit"}
	f.Fuzz(func(t *testing.T, src string) {
		for _, mode := range []Mode{Strict, Tolerant} {
			for i, p := range paths {
				file := Parse(src, mode)
				a := Analyze(file, cgFileInfo(p))
				opts := Options{Module: testModule, Mode: mode, CompressHTML: Compression((len(src) + i) % 3)}
				out := Generate(&Input{File: file, Analysis: a, Index: index, Options: opts})
				reported := len(file.Diagnostics)+len(a.Diagnostics)+len(out.Diagnostics) > 0
				valid := fzUserGoValid(file, a)
				for _, gf := range []*GenFile{out.Decl, out.Impl} {
					if valid && (mode == Tolerant || !reported) {
						if _, err := goparser.ParseFile(gotoken.NewFileSet(), gf.Path, gf.Content, goparser.SkipObjectResolution); err != nil {
							t.Fatalf("%v %s: generated Go doesn't parse: %v\n%s", mode, gf.Path, err, gf.Content)
						}
					}
					fzCheckMap(t, src, gf)
				}
				for _, cs := range out.CallSites {
					if cs.Arg.Start < 0 || cs.Arg.End > len(out.Impl.Content) || cs.Arg.Start > cs.Arg.End ||
						string(out.Impl.Content[cs.Arg.Start:cs.Arg.End]) != cs.Code.Text || cs.Key != cs.Code.Start {
						t.Fatalf("call site %+v doesn't hold its code", cs)
					}
				}
			}
		}
	})
}

// fzCheckMap checks one file's source map: ranges in bounds, sorted and
// non-overlapping on the Go side, exact segments equal and every offset
// mapping both ways.
func fzCheckMap(t *testing.T, src string, gf *GenFile) {
	t.Helper()
	m := gf.Map
	if m == nil {
		t.Fatalf("%s: no source map", gf.Path)
	}
	prev := 0
	for _, s := range m.Segments {
		if s.GoStart < prev || s.GoStart > s.GoEnd || s.GoEnd > len(gf.Content) {
			t.Fatalf("%s: segment %+v out of range or order (prev end %d, len %d)", gf.Path, s, prev, len(gf.Content))
		}
		prev = s.GoEnd
		if s.Kind == SegEcho {
			if s.OrbitStart != -1 || s.OrbitEnd != -1 {
				t.Fatalf("%s: echo segment %+v has an orbit range", gf.Path, s)
			}
			if _, ok := m.OrbitOffset(s.GoStart); ok && s.GoStart < s.GoEnd {
				t.Fatalf("%s: echo segment %+v maps back", gf.Path, s)
			}
			continue
		}
		if s.OrbitStart < 0 || s.OrbitStart > s.OrbitEnd || s.OrbitEnd > len(src) {
			t.Fatalf("%s: segment %+v outside the .orbit file (len %d)", gf.Path, s, len(src))
		}
		if s.Kind == SegExact && src[s.OrbitStart:s.OrbitEnd] != string(gf.Content[s.GoStart:s.GoEnd]) {
			t.Fatalf("%s: exact segment %+v: %q != %q", gf.Path, s, src[s.OrbitStart:s.OrbitEnd], gf.Content[s.GoStart:s.GoEnd])
		}
		for g := s.GoStart; g < s.GoEnd; g++ {
			o, ok := m.OrbitOffset(g)
			if !ok || o < s.OrbitStart || o > s.OrbitEnd || s.Kind == SegExact && o != s.OrbitStart+g-s.GoStart {
				t.Fatalf("%s: OrbitOffset(%d) = %d, %v in segment %+v", gf.Path, g, o, ok, s)
			}
			back, ok := m.GoOffset(o)
			if !ok {
				t.Fatalf("%s: GoOffset(%d) not ok (segment %+v)", gf.Path, o, s)
			}
			if o2, ok := m.OrbitOffset(back); !ok && back != s.GoEnd || ok && o2 != o && s.Kind == SegExact {
				t.Fatalf("%s: %d -> %d -> %d -> %d, %v (segment %+v)", gf.Path, g, o, back, o2, ok, s)
			}
		}
	}
}

// fzUserGoValid reports whether every Go region of the file parses on its
// own: imports, hoisted declarations, body statements, template expressions,
// attribute expressions and control headers.
func fzUserGoValid(file *File, a *Analysis) bool {
	parses := func(src string) bool {
		_, err := goparser.ParseFile(gotoken.NewFileSet(), "", src, goparser.SkipObjectResolution)
		return err == nil
	}
	expr := func(c GoCode) bool {
		_, err := goparser.ParseExpr(c.Text)
		return err == nil
	}
	for _, d := range a.Decls {
		start := d.Span.Start
		if d.Doc.Len() > 0 {
			start = d.Doc.Start
		}
		if !parses("package p\n" + file.Src[start:d.Span.End]) {
			return false
		}
	}
	for _, im := range a.GoImports {
		if !parses("package p\nimport " + file.Src[im.Span.Start:im.Span.End]) {
			return false
		}
	}
	for _, st := range a.Body {
		if !parses("package p\nfunc _() {\n" + file.Src[st.Span.Start:st.Span.End] + "\n}") {
			return false
		}
	}
	// A header is valid when it makes exactly one if or for statement
	// whose body holds the statement after it.
	header := func(kw string, c GoCode) bool {
		f, err := goparser.ParseFile(gotoken.NewFileSet(), "", "package p\nfunc _() {\n"+kw+c.Text+" {\nx()\n}\n}", goparser.SkipObjectResolution)
		if err != nil || len(f.Decls) != 1 {
			return false
		}
		body := f.Decls[0].(*ast.FuncDecl).Body.List
		if len(body) != 1 {
			return false
		}
		switch st := body[0].(type) {
		case *ast.IfStmt:
			return kw == "if " && len(st.Body.List) == 1 && st.Else == nil
		case *ast.ForStmt:
			return kw == "for " && len(st.Body.List) == 1
		case *ast.RangeStmt:
			return kw == "for " && len(st.Body.List) == 1
		}
		return false
	}
	ok := true
	Walk(file.Template, func(n Node) bool {
		for _, c := range cgTemplateCodes(n) {
			ok = ok && !endsInLineComment(c.Text) // a codegen error
		}
		switch n := n.(type) {
		case *Expr:
			ok = ok && (strings.Trim(n.Code.Text, " \t\r\n") == "" || expr(n.Code)) // Go whitespace only
		case *Branch:
			ok = ok && (n.Else || header("if ", n.Cond))
		case *For:
			ok = ok && header("for ", n.Clause)
		case *Switch:
			// The header with each case list makes one switch statement
			// with one clause holding the statement after it.
			lists := []string{"default"}
			for _, cs := range n.Cases {
				if !cs.Default {
					lists = append(lists, "case "+cs.List.Text)
				}
			}
			for _, l := range lists {
				f, err := goparser.ParseFile(gotoken.NewFileSet(), "", "package p\nfunc _() {\nswitch "+n.Clause.Text+" {\n"+l+":\nx()\n}\n}", goparser.SkipObjectResolution)
				if err != nil || len(f.Decls) != 1 || len(f.Decls[0].(*ast.FuncDecl).Body.List) != 1 {
					ok = false
					break
				}
				var body *ast.BlockStmt
				switch st := f.Decls[0].(*ast.FuncDecl).Body.List[0].(type) {
				case *ast.SwitchStmt:
					body = st.Body
				case *ast.TypeSwitchStmt:
					body = st.Body
				}
				if body == nil || len(body.List) != 1 || len(body.List[0].(*ast.CaseClause).Body) != 1 {
					ok = false
					break
				}
			}
		}
		if tg := TagOf(n); tg != nil {
			for _, at := range tg.Attrs {
				if at.Kind == AttrExpr || at.Kind == AttrShorthand || at.Kind == AttrSpread {
					ok = ok && expr(at.Code)
				}
			}
		}
		return ok
	})
	return ok
}

// plBenchProject is a generated project of n components shaped like
// tyBenchUnits, written as .orbit sources: Props over internal/posts, about
// 30 mixed-type expressions and attributes, a loop, three component calls
// with slot content, and a page rendering the first component.
func plBenchProject(n int) map[string]string {
	exprs := []string{"props.Post.Title", "props.N", "props.Tags", "props.Status", "props.Price", "props.On",
		"href(props.Post)", "len(props.Tags)", "props.N + 1", `"lit"`, "42", "props.Post.Slug", "props.Price * 2",
		"strings.ToUpper(props.Post.Title)", "props.Status.String()", "props.Tags[0]", "props.On && props.N > 0",
		"float32(props.Price)", "uint8(props.N)", "props.Post", "nil", "props.Err", "props.HTML"}
	srcs := map[string]string{}
	for i := range n {
		var b strings.Builder
		b.WriteString("---\nimport (\n\t\"strings\"\n\n\t\"" + tyPostsPath + "\"\n)\n")
		for _, d := range []int{1, 7, 13} {
			fmt.Fprintf(&b, "import C%03d \"./C%03d.orbit\"\n", (i+d)%n, (i+d)%n)
		}
		b.WriteString("\n// Props are the inputs.\ntype Props struct {\n\tPost   posts.Post\n\tN      int\n\tTags   []string\n" +
			"\tStatus posts.Status\n\tPrice  float64\n\tOn     bool\n\tErr    error\n\tHTML   orbit.HTML\n}\n\n" +
			"func href(p posts.Post) string { return \"/p/\" + string(p.Slug) }\n\ntitle := strings.TrimSpace(props.Post.Title)\n---\n")
		b.WriteString("<article class=\"card\" data-on={props.On} title={title}>\n  <h2><a href={href(props.Post)}>{title}</a></h2>\n")
		for _, e := range exprs {
			fmt.Fprintf(&b, "  <p class=\"x\">{%s}</p>\n", e)
		}
		b.WriteString("  <ul>\n    {for _, tag := range props.Tags}\n      <li data-n={props.N}>{tag}</li>\n    {/for}\n  </ul>\n")
		b.WriteString("  {if props.N < 0}\n")
		for _, d := range []int{1, 7, 13} {
			fmt.Fprintf(&b, "    <C%03d n={props.N + 1} on><em>{props.Post.Title}</em><p slot=\"footer\">f</p></C%03d>\n", (i+d)%n, (i+d)%n)
		}
		b.WriteString("  {/if}\n  <slot />\n  <footer><slot name=\"footer\">none</slot></footer>\n</article>\n")
		srcs[fmt.Sprintf("src/components/C%03d.orbit", i)] = b.String()
	}
	srcs["src/pages/index.orbit"] = "---\nimport C000 \"../components/C000.orbit\"\n---\n<!DOCTYPE html>\n<html><head><title>x</title></head>\n" +
		"<body><C000 n={1}>hi</C000></body></html>\n"
	return srcs
}

// BenchmarkPipeline is the whole milestone 4 pipeline over a 50-component
// project (plus a page): parse, Analyze, the index, pass-1 Generate, then
// TypedPass with the real GoListLoader (go list -export included) and the
// pass-2 re-emit. Compare with the ~1 s dev-loop budget (CLAUDE.md); go
// build comes on top.
func BenchmarkPipeline(b *testing.B) {
	m := tyBenchModule(b)
	srcs := plBenchProject(50)
	opts := Options{Module: testModule}
	p := plPipeline(b, m, srcs, opts)
	if d := p.diagnostics(); len(d) > 0 {
		b.Fatalf("diagnostics:\n%s", strings.Join(d, "\n"))
	}
	sites := 0
	for _, pf := range p.files {
		if !pf.typed.Checked {
			b.Fatalf("%s not checked: %v", pf.path, p.warnings)
		}
		sites += len(pf.pass1.CallSites)
	}
	b.Run("front", func(b *testing.B) { // parse + analyze + index + pass 1
		for b.Loop() {
			plFront(srcs, opts)
		}
	})
	b.Run("full", func(b *testing.B) {
		for b.Loop() {
			plPipeline(b, m, srcs, opts)
		}
		b.ReportMetric(float64(sites), "sites")
	})
}

// TestPipelineBenchProject builds and vets the benchmark project, so the
// benchmark measures code that compiles.
func TestPipelineBenchProject(t *testing.T) {
	m := newGenModule(t)
	m.write("internal/posts/posts.go", tyPostsSrc)
	p := plPipeline(t, m, plBenchProject(50), Options{Module: testModule})
	if d := p.diagnostics(); len(d) > 0 {
		t.Fatalf("diagnostics:\n%s", strings.Join(d, "\n"))
	}
	for _, pf := range p.files {
		if !pf.typed.Checked {
			t.Fatalf("%s not checked: %v", pf.path, p.warnings)
		}
	}
	p.write(m, true)
	m.buildAndVet()
}

// TestAliasUsedOnlyInGo: an .orbit alias used only by Go code (no tag) is
// imported in the impl package, and an unused one is reported and emitted
// nowhere (DESIGN.md gap 1).
func TestAliasUsedOnlyInGo(t *testing.T) {
	p := plFront(map[string]string{
		"src/components/Card.orbit": "---\ntype Props struct{ Title string }\n---\n<p>{props.Title}</p>\n",
		"src/pages/index.orbit": "---\nimport Card \"../components/Card.orbit\"\nimport Unused \"../components/Card.orbit\"\n\n" +
			"x := Card.Props{Title: \"a\"}\n---\n<p>{x.Title}</p>\n",
	}, Options{Module: testModule})
	if d := p.diagnostics(); len(d) != 1 || !strings.Contains(d[0], "index.orbit:3:8: component Unused is imported but not used") {
		t.Errorf("diagnostics %q", d)
	}
	impl := string(p.files[1].pass1.Impl.Content)
	if !strings.Contains(impl, "Card \""+testModule+"/orbit_gen/decl/components/card\"") || strings.Contains(impl, "Unused") {
		t.Errorf("impl imports:\n%s", impl)
	}
}
