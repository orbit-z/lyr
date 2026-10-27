package compiler

// The implementation of Analyze (part A of milestone 4). See DESIGN.md and
// PLAN.md §2.2, §3.1.
//
// The frontmatter is split into depth-0 statements with go/scanner. Three
// synthetic Go files are then parsed with go/parser, each keeping the
// .orbit offsets (a constant shift, or a segment table for template code):
//   - the import file: the import declarations only;
//   - the decl file: the hoisted type, const and func declarations, for the
//     Props closure (resolution against the file scope) and PropsInfo;
//   - the body file: the body statements inside a func, followed by every
//     template Go region in its control structure, for the statement spans,
//     echo names, the terminating rule and import usage (object resolution:
//     a selector on an unresolved identifier).

import (
	"fmt"
	"go/ast"
	goparser "go/parser"
	goscanner "go/scanner"
	gotoken "go/token"
	"path"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// anGlueCall is the function the body file wraps template expressions in.
// Its prefix keeps it apart from user names (which may not use it).
const anGlueCall = "orbit__e"

type anState struct {
	f        *File
	a        *Analysis
	src      string
	implicit []string
	toks     []anTok
	ranges   []anRange
	// hoisted is parallel to a.Decls: the range of each declaration.
	hoisted []anRange

	declFile *ast.File
	declTF   *gotoken.File
	// declAST[i] is the ast.Decl of a.Decls[i], or nil.
	declAST []ast.Decl
	// names maps a package-level name to the index in a.Decls declaring it.
	names map[string]int
	// methods maps a receiver type name to the indexes of its methods.
	methods map[string][]int

	// Usage, by side: names of unresolved identifiers that are the X of a
	// selector (import uses), and of all unresolved identifiers.
	declSel, implSel map[string]bool
	implIdent        map[string]bool
	// implRefs are the decl-file references from impl-placed declarations to
	// names declared by others.
	implRefs map[string]bool
	tagUses  map[string]bool

	bodyTF     *gotoken.File
	bodyPrefix int
	// declErrs are the .orbit offsets of the decl file's syntax errors.
	declErrs []int
}

func (s *anState) errorf(sp Span, format string, args ...any) {
	s.a.Diagnostics = append(s.a.Diagnostics, Diagnostic{SeverityError, sp, fmt.Sprintf(format, args...)})
}

func analyze(f *File, info FileInfo) *Analysis {
	a := &Analysis{Info: info, Props: PropsInfo{Listable: true}}
	s := &anState{
		f: f, a: a, src: f.Src, implicit: ImplicitNames,
		names: map[string]int{}, methods: map[string][]int{},
		declSel: map[string]bool{}, implSel: map[string]bool{}, implIdent: map[string]bool{},
		implRefs: map[string]bool{}, tagUses: map[string]bool{},
	}
	if info.Role == RolePage {
		s.implicit = PageImplicitNames
	}
	code := f.Frontmatter.Code
	if f.Frontmatter.Present && code.Start >= 0 && code.End <= len(f.Src) && code.Start <= code.End {
		s.toks = anScan(f.Src[code.Start:code.End], code.Start)
		s.ranges = anSplit(s.toks)
	}
	s.reservedIdents()
	s.parseImports()
	s.parseDecls()
	s.parseBody()
	s.partition()
	s.checkDecls()
	s.placeImports()
	s.checkClashes()
	if info.Role == RolePage {
		s.params()
	}
	sortDiagnostics(a.Diagnostics)
	return a
}

// blanked returns the frontmatter code with bytes replaced by spaces (line
// breaks kept), so offsets in it are offsets in the code: with only set,
// every byte outside the ranges of kinds k with keep(k); otherwise the bytes
// of the ranges with !keep(k) (separators and comments are kept).
func (s *anState) blanked(keep func(anStmtKind) bool, only bool) string {
	code := s.f.Frontmatter.Code
	b := []byte(s.src[code.Start:code.End])
	mark := make([]bool, len(b)) // kept (only) or blanked (!only)
	for _, r := range s.ranges {
		if keep(r.kind) == only {
			for i := r.span.Start; i < r.span.End; i++ {
				mark[i-code.Start] = true
			}
		}
	}
	for i := range b {
		if mark[i] != only && b[i] != '\n' {
			b[i] = ' '
		}
	}
	return string(b)
}

const anPkgPrefix = "package p;"

// parseFM parses prefix + text, where text starts at frontmatter offset 0.
func (s *anState) parseFM(text, prefix, suffix string, mode goparser.Mode) (*ast.File, *gotoken.File, []goscanner.Error) {
	fset := gotoken.NewFileSet()
	file, err := goparser.ParseFile(fset, "", prefix+text+suffix, mode|goparser.AllErrors)
	var errs []goscanner.Error
	if el, ok := err.(goscanner.ErrorList); ok {
		for _, e := range el {
			errs = append(errs, *e)
		}
	}
	if file == nil {
		return nil, nil, errs
	}
	return file, fset.File(file.Package), errs
}

// fmOff maps a position of a frontmatter-based synthetic file to a .orbit
// offset; ok is false outside the frontmatter part.
func (s *anState) fmOff(tf *gotoken.File, p gotoken.Pos, prefix int) (int, bool) {
	if tf == nil || !p.IsValid() || int(p) < tf.Base() || int(p) > tf.Base()+tf.Size() {
		return 0, false
	}
	off := tf.Offset(p) - prefix
	code := s.f.Frontmatter.Code
	if off < 0 || off > code.End-code.Start {
		return 0, false
	}
	return code.Start + off, true
}

// --- imports ---

func (s *anState) parseImports() {
	if len(s.ranges) == 0 {
		return
	}
	has := false
	for _, r := range s.ranges {
		has = has || r.kind == anImport
	}
	if !has {
		return
	}
	file, tf, _ := s.parseFM(s.blanked(func(k anStmtKind) bool { return k == anImport }, true), anPkgPrefix, "", goparser.ImportsOnly)
	if file == nil {
		return
	}
	off := func(p gotoken.Pos) int {
		o, _ := s.fmOff(tf, p, len(anPkgPrefix))
		return o
	}
	for _, spec := range file.Imports {
		if spec.Path == nil || !spec.Path.Pos().IsValid() {
			continue
		}
		pathSpan := Span{off(spec.Path.Pos()), off(spec.Path.End())}
		p, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			p = strings.Trim(spec.Path.Value, "\"`")
		}
		var name string
		var nameSpan Span
		if spec.Name != nil {
			name = spec.Name.Name
			nameSpan = Span{off(spec.Name.Pos()), off(spec.Name.End())}
		}
		full := Span{pathSpan.Start, pathSpan.End}
		if spec.Name != nil {
			full.Start = nameSpan.Start
		}
		ext := strings.ToLower(path.Ext(p))
		relative := IsPathImport(p)
		switch {
		case ext == ".orbit" || IsMarkdown(ext):
			s.orbitImport(name, nameSpan, p, pathSpan, full)
		case ext == ".tsx" || ext == ".jsx" || ext == ".svelte" || ext == ".vue":
			s.errorf(pathSpan, "framework components aren't supported; use a .orbit component with a <script>")
		case relative:
			s.errorf(pathSpan, "only .orbit and .md files can be imported by relative path; import client modules from a <script>")
		default:
			if name == "." {
				s.errorf(nameSpan, "dot imports aren't supported in a component; import the package by name")
			}
			s.a.GoImports = append(s.a.GoImports, GoImport{Name: name, Path: p, Span: full, PathSpan: pathSpan})
		}
	}
}

func (s *anState) orbitImport(alias string, aliasSpan Span, p string, pathSpan, full Span) {
	switch alias {
	case ".":
		s.errorf(aliasSpan, "dot imports aren't supported in a component; import the package by name")
		return
	case "", "_":
		base := anUpperFirst(strings.TrimSuffix(path.Base(p), path.Ext(p)))
		at := pathSpan
		if alias != "" {
			at = aliasSpan
		}
		s.errorf(at, "a component import needs an alias: %s %s", base, strconv.Quote(p))
		return
	}
	resolved, ok := ResolveImportPath(s.a.Info.Path, p, s.a.Info.Aliases)
	if !ok {
		s.errorf(pathSpan, "%s", NoAliasError(p))
		return
	}
	if !strings.HasPrefix(resolved, "src/") {
		s.errorf(pathSpan, "component path %s is outside src/", p)
		return
	}
	if msg := AliasError(alias); msg != "" {
		s.errorf(aliasSpan, "%s", msg)
	}
	s.a.OrbitImports = append(s.a.OrbitImports, OrbitImport{
		Alias: alias, AliasSpan: aliasSpan, Path: p, Span: full, PathSpan: pathSpan, Resolved: resolved,
	})
}

// AliasError returns the error for an .orbit import alias that doesn't
// start with an uppercase letter (a lowercase tag is always an HTML
// element), or "" when it does. The LSP checks a renamed alias with it.
func AliasError(alias string) string {
	if r, _ := utf8.DecodeRuneInString(alias); !unicode.IsUpper(r) {
		return fmt.Sprintf("component alias %s must start with an uppercase letter (rename it to %s)", alias, anUpperFirst(alias))
	}
	return ""
}

// UnexportedPropError returns the error for an unexported Props field
// name, or "" for an exported one. The LSP checks a renamed field with it.
func UnexportedPropError(name string) string {
	if ast.IsExported(name) {
		return ""
	}
	return fmt.Sprintf("Props field %s must be exported, since a parent sets it (rename it to %s)", name, anUpperFirst(name))
}

// anUpperFirst upper-cases the first letter of name.
func anUpperFirst(name string) string {
	r, n := utf8.DecodeRuneInString(name)
	if n == 0 {
		return name
	}
	return string(unicode.ToUpper(r)) + name[n:]
}

// anPkgName guesses the package name of an import without an explicit name:
// the last path element, skipping a major-version element (/v2) and
// dropping a gopkg.in-style .vN suffix and a go- prefix or -go suffix.
func anPkgName(p string) string {
	elems := strings.Split(p, "/")
	last := elems[len(elems)-1]
	if len(elems) > 1 && len(last) > 1 && last[0] == 'v' && strings.Trim(last[1:], "0123456789") == "" {
		last = elems[len(elems)-2]
	}
	if i := strings.Index(last, ".v"); i > 0 && strings.Trim(last[i+2:], "0123456789") == "" {
		last = last[:i]
	}
	last = strings.TrimPrefix(last, "go-")
	last = strings.TrimSuffix(last, "-go")
	return pkgNameReplacer.Replace(last)
}

// pkgNameReplacer is built once (a Replacer builds its tables on first use).
var pkgNameReplacer = strings.NewReplacer("-", "_", ".", "_")

// --- hoisted declarations ---

func (s *anState) parseDecls() {
	for _, r := range s.ranges {
		var kind HoistKind
		switch r.kind {
		case anType:
			kind = HoistType
		case anConst:
			kind = HoistConst
		case anFunc:
			kind = HoistFunc
		default:
			continue
		}
		s.hoisted = append(s.hoisted, r)
		s.a.Decls = append(s.a.Decls, Hoisted{Kind: kind, Span: r.span, Doc: anDoc(s.toks, r.first, s.f.Lines), Place: PlaceImpl})
	}
	s.declAST = make([]ast.Decl, len(s.a.Decls))
	if len(s.a.Decls) == 0 {
		return
	}
	isDecl := func(k anStmtKind) bool { return k == anType || k == anConst || k == anFunc }
	file, tf, errs := s.parseFM(s.blanked(isDecl, true), anPkgPrefix, "", goparser.ParseComments)
	for _, e := range errs {
		s.declErrs = append(s.declErrs, s.f.Frontmatter.Code.Start+e.Pos.Offset-len(anPkgPrefix))
	}
	if file == nil {
		return
	}
	s.declFile, s.declTF = file, tf
	for _, d := range file.Decls {
		o, ok := s.off(d.Pos())
		if !ok {
			continue
		}
		for i, h := range s.a.Decls {
			if h.Span.Start <= o && o < h.Span.End && s.declAST[i] == nil {
				s.declAST[i] = d
			}
		}
	}
	for i, d := range s.declAST {
		h := &s.a.Decls[i]
		add := func(id *ast.Ident) {
			if id == nil || id.Name == "_" {
				return
			}
			sp := s.span(id)
			h.Names = append(h.Names, id.Name)
			h.NameSpans = append(h.NameSpans, sp)
			if _, dup := s.names[id.Name]; !dup {
				s.names[id.Name] = i
			}
		}
		switch d := d.(type) {
		case *ast.FuncDecl:
			if d.Recv != nil {
				if len(d.Recv.List) > 0 {
					h.Recv = anRecvName(d.Recv.List[0].Type)
					s.methods[h.Recv] = append(s.methods[h.Recv], i)
				}
			} else {
				add(d.Name)
			}
		case *ast.GenDecl:
			for _, spec := range d.Specs {
				switch spec := spec.(type) {
				case *ast.TypeSpec:
					add(spec.Name)
				case *ast.ValueSpec:
					for _, n := range spec.Names {
						add(n)
					}
				}
			}
		}
	}
}

// off maps a decl-file position to a .orbit offset.
func (s *anState) off(p gotoken.Pos) (int, bool) { return s.fmOff(s.declTF, p, len(anPkgPrefix)) }

// span returns the .orbit span of a decl-file node.
func (s *anState) span(n ast.Node) Span {
	a, _ := s.off(n.Pos())
	b, _ := s.off(n.End())
	return Span{a, max(a, b)}
}

// anRecvName returns the base type name of a receiver type: T, *T, T[K].
func anRecvName(e ast.Expr) string {
	for {
		switch x := e.(type) {
		case *ast.StarExpr:
			e = x.X
		case *ast.ParenExpr:
			e = x.X
		case *ast.IndexExpr:
			e = x.X
		case *ast.IndexListExpr:
			e = x.X
		case *ast.Ident:
			return x.Name
		default:
			return ""
		}
	}
}

// --- the body file ---

// anSynth builds the template part of the body file, mapping its copied
// regions back to the .orbit file.
type anSynth struct {
	b    strings.Builder
	segs []anSeg
	// codes are the copied template Go regions, for the reserved-name scan.
	codes []GoCode
	// probes are the capitalized tag names, resolved for dynamic tags.
	probes []*anProbe
}

type anSeg struct{ goStart, orbit, n int }

func (y *anSynth) glue(t string) { y.b.WriteString(t) }

func (y *anSynth) copy(c GoCode) {
	y.segs = append(y.segs, anSeg{y.b.Len(), c.Start, len(c.Text)})
	y.codes = append(y.codes, c)
	y.b.WriteString(c.Text)
}

func (y *anSynth) expr(c GoCode) {
	if strings.TrimSpace(c.Text) == "" {
		return
	}
	y.glue(anGlueCall + "(")
	y.copy(c)
	y.glue(",\n)\n")
}

func (y *anSynth) nodes(ns []Node, tags map[string]bool) {
	for _, n := range ns {
		switch n := n.(type) {
		case *Expr:
			y.expr(n.Code)
		case *If:
			for i, br := range n.Branches {
				switch {
				case i == 0:
					y.glue("if ")
				case br.Else:
					y.glue("} else ")
				default:
					y.glue("} else if ")
				}
				if !br.Else || i == 0 {
					if strings.TrimSpace(br.Cond.Text) == "" {
						y.glue("true")
					} else {
						y.copy(br.Cond)
					}
				}
				y.glue(" {\n")
				y.nodes(br.Children, tags)
			}
			if len(n.Branches) > 0 {
				y.glue("}\n")
			}
		case *For:
			y.glue("for ")
			y.copy(n.Clause)
			y.glue(" {\n")
			y.nodes(n.Children, tags)
			y.glue("}\n")
		case *Switch:
			y.nodes(n.Pre, tags)
			y.glue("switch ")
			y.copy(n.Clause)
			y.glue(" {\n")
			for _, cs := range n.Cases {
				switch {
				case cs.Default:
					y.glue("default:\n")
				case strings.TrimSpace(cs.List.Text) == "":
					y.glue("case true:\n")
				default:
					y.glue("case ")
					y.copy(cs.List)
					y.glue(":\n")
				}
				y.nodes(cs.Children, tags)
			}
			y.glue("}\n")
		default:
			if c, ok := n.(*Component); ok && !c.Self {
				tags[c.Name] = true
				y.probe(n, &c.Tag)
			}
			if e, ok := n.(*Element); ok && e.Dynamic {
				y.probe(n, &e.Tag)
			}
			if t := TagOf(n); t != nil {
				for _, at := range t.Attrs {
					if at.Directive == ClassList && at.Kind == AttrExpr && strings.TrimSpace(at.Code.Text) != "" {
						// The items are the elements of a composite literal.
						y.glue(anGlueCall + "(_{")
						y.copy(at.Code)
						y.glue("},\n)\n")
						continue
					}
					if at.Kind == AttrExpr || at.Kind == AttrShorthand || at.Kind == AttrSpread {
						y.expr(at.Code)
					}
				}
			}
			y.nodes(ChildNodes(n), tags)
		}
	}
}

// anBodyStmt is one parsed top-level body statement.
type anBodyStmt struct {
	stmt ast.Stmt
	span Span
}

func (s *anState) parseBody() {
	var y anSynth
	if s.f.Template != nil {
		y.nodes(s.f.Template.Children, s.tagUses)
	}
	s.reservedCodes(y.codes)
	const prefix = anPkgPrefix + "func _(){"
	text := ""
	if len(s.ranges) > 0 {
		text = s.blanked(func(k anStmtKind) bool { return k == anBody }, false)
	}
	tmplStart := len(prefix) + len(text) + 1
	file, tf, errs := s.parseFM(text, prefix, "\n"+y.b.String()+"\n}\n", 0)
	s.bodyTF, s.bodyPrefix = tf, len(prefix)
	var scannerBody []anRange
	for _, r := range s.ranges {
		if r.kind == anBody {
			scannerBody = append(scannerBody, r)
		}
	}
	bodyErr := false
	for _, e := range errs {
		if e.Pos.Offset < tmplStart {
			bodyErr = true
		}
	}
	var stmts []anBodyStmt
	if file != nil {
		for _, d := range file.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Body == nil {
				continue
			}
			for _, st := range fd.Body.List {
				if _, empty := st.(*ast.EmptyStmt); empty {
					continue
				}
				a, ok1 := s.fmOff(tf, st.Pos(), len(prefix))
				b, ok2 := s.fmOff(tf, st.End(), len(prefix))
				if !ok1 {
					continue
				}
				if !ok2 || b < a {
					bodyErr = true
					b = a
				}
				stmts = append(stmts, anBodyStmt{st, Span{a, b}})
			}
		}
		s.usage(file)
		s.ctxRefs(file, tf, tmplStart, &y)
	}
	s.dynTags(file, tf, tmplStart, &y)
	s.a.UsesLocals = s.implIdent["locals"] || file == nil || len(errs) > 0
	s.a.CtxUnknown = file == nil || len(errs) > 0
	if bodyErr || file == nil {
		for _, r := range scannerBody {
			s.a.Body = append(s.a.Body, Stmt{Span: r.span})
		}
	} else {
		for _, st := range stmts {
			s.a.Body = append(s.a.Body, Stmt{Span: st.span, Returns: s.returns(st.stmt)})
		}
	}
	s.echo(stmts)
	if n := len(stmts); n > 0 && len(s.a.Body) > 0 {
		last := stmts[n-1]
		lastBody := s.a.Body[len(s.a.Body)-1].Span
		if lastBody.Start <= last.span.Start && last.span.Start < max(lastBody.End, lastBody.Start+1) && anTerminating(last.stmt, "") {
			s.a.Terminating = true
			if s.templateHasContent() {
				s.errorf(lastBody, "the frontmatter always returns here, so the template below is never rendered")
			}
		}
	}
}

// returns lists the single-result returns of a body statement that return
// from the render function: function literals are skipped.
func (s *anState) returns(st ast.Stmt) []Return {
	var rs []Return
	ast.Inspect(st, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.FuncLit:
			return false
		case *ast.ReturnStmt:
			if len(n.Results) != 1 {
				return false // bare or multi-value: Go reports it
			}
			kw, ok1 := s.fmOff(s.bodyTF, n.Return, s.bodyPrefix)
			a, ok2 := s.fmOff(s.bodyTF, n.Results[0].Pos(), s.bodyPrefix)
			b, ok3 := s.fmOff(s.bodyTF, n.Results[0].End(), s.bodyPrefix)
			if ok1 && ok2 && ok3 && kw < a && a < b {
				rs = append(rs, Return{Keyword: kw, Result: Span{a, b}})
			}
			return false
		}
		return true
	})
	return rs
}

// usage records the import and name uses of the body file (body and
// template), which are all implementation-side.
func (s *anState) usage(file *ast.File) {
	ast.Inspect(file, func(n ast.Node) bool {
		if sel, ok := n.(*ast.SelectorExpr); ok {
			if id, ok := sel.X.(*ast.Ident); ok && id.Obj == nil {
				s.implSel[id.Name] = true
			}
		}
		return true
	})
	for _, id := range file.Unresolved {
		if id.Name != anGlueCall {
			s.implIdent[id.Name] = true
		}
	}
}

// ctxRefs records the .orbit offsets of the template's uses of the implicit
// ctx: unresolved identifiers named ctx in the template part of the body
// file, which starts at file offset tmplStart.
func (s *anState) ctxRefs(file *ast.File, tf *gotoken.File, tmplStart int, y *anSynth) {
	for _, id := range file.Unresolved {
		if id.Name != "ctx" || tf == nil {
			continue
		}
		off := tf.Offset(id.Pos()) - tmplStart
		for _, sg := range y.segs {
			if sg.goStart <= off && off < sg.goStart+sg.n {
				s.a.CtxRefs = append(s.a.CtxRefs, sg.orbit+off-sg.goStart)
				break
			}
		}
	}
}

func (s *anState) templateHasContent() bool {
	if s.f.Template == nil {
		return false
	}
	for _, n := range s.f.Template.Children {
		switch n := n.(type) {
		case *Text:
			if strings.TrimSpace(n.Value) != "" {
				return true
			}
		case *Comment:
		default:
			return true
		}
	}
	return false
}

// echo collects the names declared at the body's top level and reports
// redeclared implicit names.
func (s *anState) echo(stmts []anBodyStmt) {
	seen := map[string]bool{}
	declare := func(id *ast.Ident) {
		if id == nil || id.Name == "_" {
			return
		}
		for _, n := range s.implicit {
			if id.Name == n {
				s.errorf(s.bodySpan(id), "%s is provided by Orbit and can't be redeclared here", id.Name)
			}
		}
		if !seen[id.Name] {
			seen[id.Name] = true
			s.a.Echo = append(s.a.Echo, id.Name)
		}
	}
	for _, bs := range stmts {
		st := bs.stmt
		for {
			l, ok := st.(*ast.LabeledStmt)
			if !ok {
				break
			}
			st = l.Stmt
		}
		switch st := st.(type) {
		case *ast.AssignStmt:
			if st.Tok == gotoken.DEFINE {
				for _, e := range st.Lhs {
					if id, ok := e.(*ast.Ident); ok {
						declare(id)
					}
				}
			}
		case *ast.DeclStmt:
			if gd, ok := st.Decl.(*ast.GenDecl); ok && gd.Tok == gotoken.VAR {
				for _, spec := range gd.Specs {
					if vs, ok := spec.(*ast.ValueSpec); ok {
						for _, n := range vs.Names {
							declare(n)
						}
					}
				}
			}
		}
	}
}

// bodySpan returns the .orbit span of a frontmatter identifier of the body
// file.
func (s *anState) bodySpan(id *ast.Ident) Span {
	a, _ := s.fmOff(s.bodyTF, id.Pos(), s.bodyPrefix)
	return Span{a, a + len(id.Name)}
}
