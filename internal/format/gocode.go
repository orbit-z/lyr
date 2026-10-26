package format

import (
	"errors"
	goast "go/ast"
	goformat "go/format"
	goparser "go/parser"
	goscanner "go/scanner"
	gotoken "go/token"
	"strings"

	"github.com/orbit-z/orbit/internal/compiler"
)

// formatAll formats the regions in source order: the frontmatter, the
// expressions and control headers, and with Bun the scripts and styles. A
// Go syntax error goes to fx.errs and stops it. The indentation a region
// takes from its line (an expression's continuation lines, a block's
// closing tag) is read from the output so far, not from the source, so a
// region that starts on the last line of a reformatted one formats the same
// the second time.
func (fx *fileFmt) formatAll() {
	last := 0 // where the previous region ended, in the source
	cur := "" // the output's current line, up to last
	lineAt := func(off int) string { return appendLine(cur, lf(fx.src[last:off])) }
	var blocks []*region
	for _, r := range fx.regions {
		if r.kind == rScript || r.kind == rStyle {
			blocks = append(blocks, r)
		}
	}
	bi := 0
	for _, r := range fx.regions {
		for bi < len(blocks) && compiler.TagOf(blocks[bi].node).StartTag.Start <= r.span.Start {
			blocks[bi].indent = leadingWS(lineAt(compiler.TagOf(blocks[bi].node).StartTag.Start))
			bi++
		}
		switch r.kind {
		case rScript, rStyle:
			if fx.opts.Bun != "" {
				fx.block(r)
			}
		case rFrontmatter:
			fx.frontmatter(r)
		default:
			r.indent = leadingWS(lineAt(r.span.Start))
			fx.goRegion(r)
		}
		if len(fx.errs) > 0 {
			return
		}
		cur = lineAt(r.span.Start)
		text := lf(fx.src[r.span.Start:r.span.End])
		if r.done {
			text = r.out
		}
		cur = appendLine(cur, text)
		last = r.span.End
	}
}

// appendLine returns the current line after writing s.
func appendLine(line, s string) string {
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		return s[i+1:]
	}
	return line + s
}

// leadingWS returns the leading spaces and tabs of s.
func leadingWS(s string) string {
	return s[:len(s)-len(strings.TrimLeft(s, " \t"))]
}

// goErr records a go/format error. The error's positions are in a wrapper
// whose line 1 + lineAdj is the code's first line; colAdj is subtracted from
// columns on that first line. base is the code's offset in the file.
//
// With indented, every line of the code had a tab added (a statement list
// inside its function), and columns are shifted back by one.
func (fx *fileFmt) goErr(err error, base, lineAdj, colAdj int, indented ...bool) {
	var list goscanner.ErrorList
	if !errors.As(err, &list) || len(list) == 0 {
		fx.errs = append(fx.errs, errorAt(base, err.Error()))
		return
	}
	for i, e := range list {
		if i == 10 {
			break
		}
		line := e.Pos.Line - lineAdj
		col := e.Pos.Column
		if line <= 1 {
			line, col = 1, max(1, col-colAdj)
		}
		if len(indented) > 0 && indented[0] {
			col = max(1, col-1)
		}
		fx.errs = append(fx.errs, errorAt(offsetAt(fx.src, base, line, col), e.Msg))
	}
}

// offsetAt returns the offset of the 1-based line and byte column counted
// from base, clamped to the line.
func offsetAt(src string, base, line, col int) int {
	off := base
	for l := 1; l < line; l++ {
		i := strings.IndexByte(src[off:], '\n')
		if i < 0 {
			return len(src)
		}
		off += i + 1
	}
	end := len(src)
	if i := strings.IndexByte(src[off:], '\n'); i >= 0 {
		end = off + i
	}
	return min(off+col-1, end)
}

// frontmatter formats the frontmatter region: the delimiter lines become
// "---", and each chunk of hoisted declarations or body statements is
// formatted on its own.
func (fx *fileFmt) frontmatter(r *region) {
	orig := fx.src[r.code.Start:r.code.End]
	code := lf(orig)
	var b strings.Builder
	b.WriteString("---\n")
	chunks := compiler.FrontmatterChunks(code)
	nerr := len(fx.errs)
	for i, c := range chunks {
		if i > 0 && (chunks[i-1].TrailingComment || strings.Count(code[chunks[i-1].Span.End:c.Span.Start], "\n") >= 2) {
			b.WriteString("\n")
		}
		text := code[c.Span.Start:c.Span.End]
		// The chunk's first line, counted in the code (\r\n and \n alike).
		base := offsetAt(fx.src, r.code.Start, strings.Count(code[:c.Span.Start], "\n")+1, 1)
		base += c.Span.Start - (strings.LastIndexByte(code[:c.Span.Start], '\n') + 1)
		out, ok := fx.chunk(text, c.Hoisted, base)
		if !ok {
			continue
		}
		b.WriteString(out)
		b.WriteString("\n")
	}
	if len(fx.errs) > nerr {
		return
	}
	b.WriteString("---")
	out := b.String()
	if !sameGo(code, out[4:len(out)-3], true) {
		fx.warns = append(fx.warns, warning(r.span, "formatting the frontmatter would change its code; left as is (please report this)"))
		return
	}
	r.out, r.done = out, true
}

// chunk formats one frontmatter chunk; base is its offset in the file.
func (fx *fileFmt) chunk(text string, hoisted bool, base int) (string, bool) {
	if hoisted {
		const prefix = "package p\n\n"
		out, err := goformat.Source([]byte(prefix + text))
		if err != nil {
			fx.goErr(err, base, 2, 0)
			return "", false
		}
		s, ok := strings.CutPrefix(string(out), prefix)
		if !ok {
			fx.errs = append(fx.errs, errorAt(base, "can't format this declaration (please report this)"))
			return "", false
		}
		return strings.TrimRight(s, "\n"), true
	}
	// A statement list, printed at the left margin. go/format.Source would
	// parse a partial source the same way but rejects a leading IIFE
	// (func() { … }()) as a malformed declaration, so the function body is
	// built here and dedented, leaving raw string lines alone.
	// The statements are indented one level, as in a Go file: gofmt treats
	// a comment at the left margin of a function body differently.
	const prefix = "package p\n\nfunc _() {\n"
	raw := rawLines(text)
	lines := strings.Split(text, "\n")
	for i, l := range lines {
		if !raw[i] && strings.TrimSpace(l) != "" {
			lines[i] = "\t" + l
		}
	}
	out, err := goformat.Source([]byte(prefix + strings.Join(lines, "\n") + "\n}\n"))
	if err != nil {
		fx.goErr(err, base, 3, 0, true)
		return "", false
	}
	s, ok := strings.CutPrefix(string(out), prefix)
	s, ok2 := strings.CutSuffix(s, "}\n")
	if !ok || !ok2 {
		fx.errs = append(fx.errs, errorAt(base, "can't format these statements (please report this)"))
		return "", false
	}
	s = reindent("\n"+strings.TrimRight(s, "\n"), 1, "")
	return strings.TrimPrefix(s, "\n"), true
}

// reindent re-indents formatted Go code: on every line but the first, drop
// leading tabs and write, for each remaining one, unit after base.
// Lines that start inside a raw string are copied as they are. drop tabs are
// removed first (the wrapper's own levels); unit "" means keep the tabs.
func reindent(s string, drop int, base string, unit ...string) string {
	if !strings.Contains(s, "\n") {
		return s
	}
	raw := rawLines(s)
	u := "\t"
	if len(unit) > 0 {
		u = unit[0]
	}
	lines := strings.Split(s, "\n")
	for i := 1; i < len(lines); i++ {
		if raw[i] {
			continue
		}
		l := lines[i]
		if strings.TrimSpace(l) == "" {
			lines[i] = ""
			continue
		}
		n := len(l) - len(strings.TrimLeft(l, "\t"))
		rest := l[n:]
		n = max(0, n-drop)
		lines[i] = base + strings.Repeat(u, n) + rest
	}
	return strings.Join(lines, "\n")
}

// rawLines reports, for each line of Go source s, whether it starts inside a
// raw string literal.
func rawLines(s string) map[int]bool {
	raw := map[int]bool{}
	if !strings.Contains(s, "`") {
		return raw
	}
	fset := gotoken.NewFileSet()
	file := fset.AddFile("", -1, len(s))
	var sc goscanner.Scanner
	sc.Init(file, []byte(s), nil, goscanner.ScanComments)
	for {
		pos, tok, lit := sc.Scan()
		if tok == gotoken.EOF {
			break
		}
		if tok == gotoken.STRING && strings.HasPrefix(lit, "`") && strings.Contains(lit, "\n") {
			first := file.Line(pos) - 1 // 0-based
			for k := 1; k <= strings.Count(lit, "\n"); k++ {
				raw[first+k] = true
			}
		}
	}
	return raw
}

// goRegion formats an expression, an attribute's code or a control tag.
func (fx *fileFmt) goRegion(r *region) {
	code := lf(fx.src[r.code.Start:r.code.End])
	base := r.indent
	var out, formatted string
	switch r.kind {
	case rEndIf:
		out = "{/if}"
	case rEndFor:
		out = "{/for}"
	case rElse:
		out = "{else}"
	case rEndSwitch:
		out = "{/switch}"
	case rDefault:
		out = "{default}"
	case rSwitch:
		if code == "" {
			out = "{switch}"
			break
		}
		f, ok := fx.header(r.kind, code, r.code.Start, base)
		if !ok {
			return
		}
		formatted = f
		out = "{switch " + f + "}"
	case rCase:
		f, ok := fx.caseList(code, lf(fx.src[r.clause.Start:r.clause.End]), r.code.Start, base)
		if !ok {
			return
		}
		formatted = f
		out = "{case " + f + "}"
	case rShorthand:
		out, formatted = "{"+code+"}", code
	case rExpr, rAttrExpr, rSpread:
		if code == "" {
			out = "{}"
			break
		}
		f, ok := fx.expr(code, r.code.Start, base)
		if !ok {
			return
		}
		formatted = f
		switch r.kind {
		case rSpread:
			out = "{..." + f + "}"
		default:
			out = "{" + f + "}"
		}
	case rIf, rElseIf, rFor:
		f, ok := fx.header(r.kind, code, r.code.Start, base)
		if !ok {
			return
		}
		formatted = f
		switch r.kind {
		case rIf:
			out = "{if " + f + "}"
		case rElseIf:
			out = "{else if " + f + "}"
		default:
			out = "{for " + f + "}"
		}
	}
	header := r.kind == rIf || r.kind == rElseIf || r.kind == rFor || r.kind == rSwitch
	if formatted != "" && !sameGo(code, formatted, false) && !(header && sameGo(stripParens(code), stripParens(formatted), false)) {
		fx.warns = append(fx.warns, warning(r.span, "formatting this "+regionName(r.kind)+" would change its code; left as is (please report this)"))
		return
	}
	r.out, r.done = out, true
}

func regionName(k regionKind) string {
	switch k {
	case rIf:
		return "{if}"
	case rElseIf:
		return "{else if}"
	case rFor:
		return "{for}"
	case rSwitch:
		return "{switch}"
	case rCase:
		return "{case}"
	case rScript:
		return "<script>"
	case rStyle:
		return "<style>"
	}
	return "expression"
}

// expr formats a template expression as a standalone value.
func (fx *fileFmt) expr(code string, off int, base string) (string, bool) {
	const prefix = "package p\n\nvar _ = "
	out, err := goformat.Source([]byte(prefix + code + "\n"))
	if err != nil {
		fx.goErr(err, off, 2, len("var _ = "))
		return "", false
	}
	s, ok := strings.CutPrefix(string(out), prefix)
	if !ok {
		fx.errs = append(fx.errs, errorAt(off, "can't format this expression (please report this)"))
		return "", false
	}
	s = strings.TrimRight(s, "\n")
	s = reindent(s, 0, base, fx.unit)
	if endsInLineComment(s) {
		s += "\n" + base
	}
	return s, true
}

// header formats the condition of {if}/{else if} or the clause of {for}
// or {switch} inside a statement, and takes it back from the formatted
// statement.
func (fx *fileFmt) header(k regionKind, code string, off int, base string) (string, bool) {
	kw := "if"
	switch k {
	case rFor:
		kw = "for"
	case rSwitch:
		kw = "switch"
	}
	if endsInLineComment(code) {
		fx.errs = append(fx.errs, errorAt(off+strings.LastIndex(code, "//"), "a // comment can't end a {"+kw+"} header; use /* … */"))
		return "", false
	}
	const prefix = "package p\n\nfunc _() {\n\t"
	src := prefix + kw + " " + code + " {\n\t}\n}\n"
	out, err := goformat.Source([]byte(src))
	if err != nil {
		fx.goErr(err, off, 3, len("\t"+kw+" "))
		return "", false
	}
	fset := gotoken.NewFileSet()
	file, err := goparser.ParseFile(fset, "", out, goparser.ParseComments)
	if err != nil || len(file.Decls) != 1 {
		fx.errs = append(fx.errs, errorAt(off, "can't format this "+kw+" header (please report this)"))
		return "", false
	}
	fn, _ := file.Decls[0].(*goast.FuncDecl)
	if fn == nil || fn.Body == nil || len(fn.Body.List) != 1 {
		fx.errs = append(fx.errs, errorAt(off, "can't format this "+kw+" header (please report this)"))
		return "", false
	}
	var start, end gotoken.Pos
	switch st := fn.Body.List[0].(type) {
	case *goast.IfStmt:
		start, end = st.If+2, st.Body.Lbrace
	case *goast.ForStmt:
		start, end = st.For+3, st.Body.Lbrace
	case *goast.RangeStmt:
		start, end = st.For+3, st.Body.Lbrace
	case *goast.SwitchStmt:
		start, end = st.Switch+6, st.Body.Lbrace
	case *goast.TypeSwitchStmt:
		start, end = st.Switch+6, st.Body.Lbrace
	}
	if !start.IsValid() {
		fx.errs = append(fx.errs, errorAt(off, "can't format this "+kw+" header (please report this)"))
		return "", false
	}
	tf := fset.File(start)
	s := strings.TrimSpace(string(out[tf.Offset(start):tf.Offset(end)]))
	s = reindent(s, 1, base, fx.unit)
	if endsInLineComment(s) {
		s += "\n" + base
	}
	return s, true
}

// caseList formats the list of a {case} inside its switch (clause is the
// switch's clause as written), and takes it back from the formatted
// statement.
func (fx *fileFmt) caseList(code, clause string, off int, base string) (string, bool) {
	if endsInLineComment(code) {
		fx.errs = append(fx.errs, errorAt(off+strings.LastIndex(code, "//"), "a // comment can't end a {case} list; use /* … */"))
		return "", false
	}
	if endsInLineComment(clause) {
		return "", false // reported at the {switch}
	}
	head := "\tswitch " + clause + " {\n"
	const prefix = "package p\n\nfunc _() {\n"
	src := prefix + head + "\tcase " + code + ":\n\t}\n}\n"
	out, err := goformat.Source([]byte(src))
	if err != nil {
		fx.goErr(err, off, 4+strings.Count(clause, "\n"), len("\tcase "))
		return "", false
	}
	fail := func() (string, bool) {
		fx.errs = append(fx.errs, errorAt(off, "can't format this case list (please report this)"))
		return "", false
	}
	fset := gotoken.NewFileSet()
	file, err := goparser.ParseFile(fset, "", out, goparser.ParseComments)
	if err != nil || len(file.Decls) != 1 {
		return fail()
	}
	fn, _ := file.Decls[0].(*goast.FuncDecl)
	if fn == nil || fn.Body == nil || len(fn.Body.List) != 1 {
		return fail()
	}
	var body *goast.BlockStmt
	switch st := fn.Body.List[0].(type) {
	case *goast.SwitchStmt:
		body = st.Body
	case *goast.TypeSwitchStmt:
		body = st.Body
	}
	if body == nil || len(body.List) != 1 {
		return fail()
	}
	cc, _ := body.List[0].(*goast.CaseClause)
	if cc == nil || cc.List == nil {
		return fail()
	}
	tf := fset.File(cc.Case)
	s := strings.TrimSpace(string(out[tf.Offset(cc.Case+4):tf.Offset(cc.Colon)]))
	s = reindent(s, 1, base, fx.unit)
	if endsInLineComment(s) {
		s += "\n" + base
	}
	return s, true
}

// endsInLineComment reports whether Go code s ends with a // comment, which
// needs a line break before the closing brace.
func endsInLineComment(s string) bool {
	if !strings.Contains(s, "//") {
		return false
	}
	fset := gotoken.NewFileSet()
	file := fset.AddFile("", -1, len(s))
	var sc goscanner.Scanner
	sc.Init(file, []byte(s), nil, goscanner.ScanComments)
	last := ""
	for {
		_, tok, lit := sc.Scan()
		if tok == gotoken.EOF {
			break
		}
		if tok == gotoken.SEMICOLON && lit == "\n" {
			continue
		}
		last = ""
		if tok == gotoken.COMMENT {
			last = lit
		}
	}
	return strings.HasPrefix(last, "//")
}

// stripParens removes parentheses from Go code, for comparing control
// headers: gofmt drops redundant parentheses around a condition.
func stripParens(s string) string {
	var b strings.Builder
	for _, t := range goTokens(s) {
		switch t.tok {
		case gotoken.LPAREN, gotoken.RPAREN, gotoken.EOF:
			continue
		case gotoken.SEMICOLON:
			b.WriteString(";")
		default:
			if t.lit != "" {
				b.WriteString(t.lit)
			} else {
				b.WriteString(t.tok.String())
			}
		}
		b.WriteString(" ")
	}
	return b.String()
}
