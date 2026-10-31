package compiler

import (
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
)

// cgGen generates one .lyr file.
type cgGen struct {
	in       *Input
	f        *File
	a        *Analysis
	src      string
	pkg      GenPackage
	tolerant bool

	o     *cgOut // the file being written (impl, while the template is emitted)
	diags []Diagnostic
	sites []CallSite
	// ifRef is set once a lowered lyr.If site has kept a reference to
	// its callee (ifSite).
	ifRef bool
	// defers are the server:defer call sites, in order.
	defers []DeferSite

	// imports maps a .lyr import alias to its spec.
	imports map[string]*LyrImport
	// tagUsed holds the aliases the template uses as tags.
	tagUsed map[string]bool
	// skipAttr holds the slot="x" attributes of a component's direct
	// children, which aren't written.
	skipAttr map[*Attr]bool
	// scope is the file's ScopeAttr when it has a scoped style, else "".
	scope string
	// jsonSites are the JSON sites, in order; rtNames the runtime's
	// names in the file (runtimeNames), computed on first use.
	jsonSites []JSONSite
	rtNames   map[string]bool
	// directUsed holds the aliases called directly in the build flavor.
	directUsed map[string]bool
	// dynTags numbers the dynamic tags of the impl file (lyr__t1, …),
	// in emission order: stable across passes, and across a reformat that
	// moves offsets (lyr fmt compares the generated code).
	dynTags int
	// spreadTmps numbers the props spread operands (lyr__s1, …), the
	// same way.
	spreadTmps int
}

func cgGenerate(in *Input) (out *Output) {
	defer func() {
		if r := recover(); r != nil {
			out = cgFailed(in, fmt.Sprintf("internal error in code generation: %v", r))
		}
	}()
	if in == nil || in.File == nil || in.Analysis == nil {
		return cgFailed(in, "internal error in code generation: missing file or analysis")
	}
	g := &cgGen{
		in:       in,
		f:        in.File,
		a:        in.Analysis,
		src:      in.File.Src,
		tolerant: in.Options.Mode == Tolerant,
		imports:  map[string]*LyrImport{},
		tagUsed:  map[string]bool{},
		skipAttr: map[*Attr]bool{},

		directUsed: map[string]bool{},
	}
	if g.f.Lines == nil {
		g.f.Lines = NewLineIndex(g.src)
	}
	pkg, err := PackageFor(g.a.Info.Path)
	if err != nil {
		g.errorf(Span{}, "%v", err)
		pkg = GenPackage{Rel: "invalid", Name: "invalid"}
	}
	g.pkg = pkg
	if HasScopedStyle(g.f) {
		g.scope = ScopeAttr(g.a.Info.Path)
	}
	for i := range g.a.LyrImports {
		imp := &g.a.LyrImports[i]
		if _, dup := g.imports[imp.Alias]; !dup {
			g.imports[imp.Alias] = imp
		}
	}
	if g.f.Template != nil {
		Walk(g.f.Template, func(n Node) bool {
			if c, ok := n.(*Component); ok && !c.Self {
				g.tagUsed[c.Name] = true
			}
			// Go inserts a semicolon at the newline a // comment ends with,
			// which would break the glue after the code.
			for _, c := range cgTemplateCodes(n) {
				if endsInLineComment(c.Text) {
					g.errorf(c.Span, "a // comment can't end Go code in the template; use /* */")
				}
			}
			return true
		})
	}
	for _, imp := range g.a.LyrImports {
		if _, ok := g.in.Index[imp.Resolved]; !ok && imp.Place != 0 {
			g.errorf(imp.PathSpan, "no such component: %s", imp.Resolved)
		}
	}
	decl := g.genDecl()
	impl := g.genImpl()
	sortDiagnostics(g.diags)
	return &Output{Decl: decl, Impl: impl, CallSites: g.sites, DeferSites: g.defers, JSONSites: g.jsonSites, Diagnostics: g.diags}
}

// cgFailed is the output of an input codegen can't handle: two files with
// just the header, and the error.
func cgFailed(in *Input, msg string) *Output {
	file := func() *GenFile {
		return &GenFile{Content: []byte(GenHeader + "\n"), Map: &SourceMap{}}
	}
	return &Output{Decl: file(), Impl: file(), Diagnostics: []Diagnostic{{Severity: SeverityError, Message: msg}}}
}

func (g *cgGen) errorf(s Span, format string, args ...any) {
	g.diags = append(g.diags, Diagnostic{SeverityError, s, fmt.Sprintf(format, args...)})
}

func (g *cgGen) isPage() bool { return g.a.Info.Role == RolePage }

func (g *cgGen) newFile(path, importPath, pkgName string) *GenFile {
	g.o = newCGOut(path, g.a.Info.Path, g.src, g.f.Lines)
	return &GenFile{Path: path, ImportPath: importPath, Package: pkgName}
}

func (g *cgGen) finish(gf *GenFile) *GenFile {
	gf.Content = g.o.b
	gf.Map = &SourceMap{Lyr: g.a.Info.Path, Gen: gf.Path, Segments: g.o.segs}
	return gf
}

const runtimeImport = NameRuntime + ` "` + RuntimePath + `"`
const lyrImport = `lyr "` + RuntimePath + `"`

// userImports writes the Go and .lyr imports for one package, each under
// an own-line directive, and the reset after them.
func (g *cgGen) userImports(place Placement) {
	o := g.o
	wrote := false
	type spec struct {
		off  int
		emit func()
	}
	var specs []spec
	for _, imp := range g.a.GoImports {
		if imp.Place&place == 0 {
			continue
		}
		specs = append(specs, spec{imp.Span.Start, func() { o.copySeg(imp.Span) }})
	}
	for _, imp := range g.a.LyrImports {
		if imp.Place&place == 0 { // unused: A's error, emitted nowhere (gap 1)
			continue
		}
		target, err := PackageFor(imp.Resolved)
		if err != nil {
			continue // reported by analysis
		}
		specs = append(specs, spec{imp.AliasSpan.Start, func() {
			o.copySeg(imp.AliasSpan)
			ws := g.src[imp.AliasSpan.End:imp.PathSpan.Start]
			if ws == "" || strings.ContainsAny(ws, "\n\r") {
				ws = " "
			}
			o.str(ws)
			gs := len(o.b)
			o.str(strconv.Quote(target.DeclImportPath(g.in.Options.Module)))
			o.segs = append(o.segs, Segment{Kind: SegSpan, LyrStart: imp.PathSpan.Start, LyrEnd: imp.PathSpan.End, GoStart: gs, GoEnd: len(o.b)})
		}})
	}
	// Source order, so segments stay sorted on both sides.
	for i := 1; i < len(specs); i++ {
		for j := i; j > 0 && specs[j].off < specs[j-1].off; j-- {
			specs[j], specs[j-1] = specs[j-1], specs[j]
		}
	}
	for i, s := range specs {
		if i > 0 {
			o.str("\n")
		}
		o.ownLine(s.off)
		s.emit()
		wrote = true
	}
	if wrote {
		o.endOwnLine()
	}
}

// hoisted writes the hoisted declarations placed in place, each framed by
// own-line directives.
func (g *cgGen) hoisted(place Placement) {
	o := g.o
	for _, d := range g.a.Decls {
		if d.Place&place == 0 {
			continue
		}
		s := d.Span
		if d.Doc.End > d.Doc.Start {
			s.Start = d.Doc.Start
		}
		o.ownLine(s.Start)
		o.copySeg(s)
		o.endOwnLine()
		o.str("\n")
	}
}

func (g *cgGen) genDecl() *GenFile {
	gf := g.newFile(g.pkg.DeclFile(), g.pkg.DeclImportPath(g.in.Options.Module), g.pkg.Name)
	o := g.o
	o.str(GenHeader + "\n\npackage " + g.pkg.Name + "\n\nimport (\n")
	o.ln("\t" + runtimeImport)
	if g.a.ImplicitLyr&PlaceDecl != 0 {
		o.ln("\t" + lyrImport)
	}
	g.userImports(PlaceDecl)
	o.str(")\n\n")
	g.hoisted(PlaceDecl)
	if !g.a.Props.Declared {
		o.str("type Props struct{}\n\n")
	}
	params := ""
	if g.isPage() {
		if len(g.a.Params) == 0 {
			o.str("type Params struct{}\n\n")
		} else {
			o.str("type Params struct {\n")
			for _, p := range g.a.Params {
				o.str("\t" + p.Field + " string\n")
			}
			o.str("}\n\n")
		}
		params = ", Params"
	}
	o.str("var " + NameHook + " func(*" + NameRuntime + ".Ctx, *" + NameRuntime + ".Writer, Props, " + NameRuntime + ".Slots" + params + ") error\n\n")
	o.str("func Render(" + NameCtx + " *" + NameRuntime + ".Ctx, " + NameW + " *" + NameRuntime + ".Writer, " + NameProps + " Props, " + NameSlots + " " + NameRuntime + ".Slots) error {\n")
	o.str("\tif " + NameHook + " == nil {\n")
	o.str("\t\tpanic(" + strconv.Quote("lyr: "+g.a.Info.Path+" not linked; import "+g.in.Options.Module+"/"+GenDir+"/app") + ")\n")
	o.str("\t}\n")
	call := "\treturn " + NameHook + "(" + NameCtx + ", " + NameW + ", " + NameProps + ", " + NameSlots
	switch {
	case !g.isPage():
		o.str(call + ")\n")
	case len(g.a.Params) == 0:
		o.str(call + ", Params{})\n")
	default:
		o.str(call + ", Params{\n")
		for _, p := range g.a.Params {
			o.str("\t\t" + p.Field + ": " + NameCtx + ".Param(" + strconv.Quote(p.Param) + "),\n")
		}
		o.str("\t})\n")
	}
	o.str("}\n")
	return g.finish(gf)
}

func (g *cgGen) genImpl() *GenFile {
	g.dynTags = 0
	gf := g.newFile(g.pkg.ImplFile(), g.pkg.ImplImportPath(g.in.Options.Module), g.pkg.ImplName())
	o := g.o
	if g.f.Template != nil {
		o.lit, o.litFrom = g.litCode, g.f.Template.Start
	}
	direct := g.directImports()
	if g.in.Direct != nil && !g.tolerant {
		// The build flavor is read from its overlay location (PLAN.md §3.5).
		o.dirFile = DirectiveFile(BuildPath(gf.Path), g.a.Info.Path)
	}
	o.str(GenHeader + "\n\npackage " + g.pkg.ImplName() + "\n\nimport (\n")
	// The callees' impl imports share the runtime's line, so the build
	// flavor has the hook flavor's lines.
	o.str("\t" + runtimeImport)
	for _, alias := range direct {
		o.str("; " + NameDirect(alias) + " " + strconv.Quote(g.in.Index[g.imports[alias].Resolved].Pkg.ImplImportPath(g.in.Options.Module)))
	}
	o.str("\n")
	o.ln("\t. " + strconv.Quote(g.pkg.DeclImportPath(g.in.Options.Module)))
	if g.in.Options.Locals != "" && (g.declaresLocals() || g.hasConcurrent()) {
		o.ln("\t" + NameLocals + " " + strconv.Quote(g.in.Options.Locals))
	}
	g.userImports(PlaceImpl)
	if g.a.ImplicitLyr&PlaceImpl != 0 {
		o.ln("\t" + lyrImport)
	}
	o.str(")\n\n")
	g.hoisted(PlaceImpl)
	render := NameRender
	if g.in.Direct != nil && !g.tolerant {
		render = NameRenderImpl
	}
	o.str("var _ = func() int { " + NameHook + " = " + render + "; return 0 }()\n\n")

	params, paramsArg := "", ""
	if g.isPage() {
		params, paramsArg = ", "+NameParams+" Params", ", "+NameParams
	}
	o.str("func " + render + "(" + NameCtx + " *" + NameRuntime + ".Ctx, " + NameW + " *" + NameRuntime + ".Writer, " +
		NameProps + " Props, " + NameSlots + " " + NameRuntime + ".Slots" + params + ") error {\n")
	o.depth = 1
	o.ln("if " + NameErr + " := " + NameW + ".Enter(" + strconv.Quote(g.a.Info.Path) + "); " + NameErr + " != nil {")
	o.ln("\treturn " + NameErr)
	o.ln("}")
	o.ln("defer " + NameW + ".Leave()")
	if g.isPage() {
		o.ln("ctx, props, slots, params := " + NameCtx + ", " + NameProps + ", " + NameSlots + paramsArg)
	} else {
		o.ln("ctx, props, slots := " + NameCtx + ", " + NameProps + ", " + NameSlots)
	}
	blank := "_, _, _ = ctx, props, slots"
	if g.isPage() {
		blank = "_, _, _, _ = ctx, props, slots, params"
	}
	if g.declaresLocals() {
		locals := NameRuntime + ".NoLocals"
		if g.in.Options.Locals != "" {
			locals = NameLocals + ".Locals"
		}
		o.ln("locals := " + NameRuntime + ".LocalsOf[" + locals + "](" + NameCtx + ")")
		blank = "_, " + blank + ", locals"
	}
	o.ln(blank)
	body := g.a.Body
	var last *Stmt
	if g.a.Terminating && len(body) > 0 {
		last = &body[len(body)-1]
		body = body[:len(body)-1]
	}
	for _, s := range body {
		g.stmt(s)
	}
	for _, name := range g.a.Echo {
		o.echo(name)
	}
	if last != nil {
		g.stmt(*last)
	} else {
		if g.f.Template != nil {
			g.nodes(g.f.Template.Children, &wsCtx{kind: wsBlock})
		}
		o.flush()
		o.ln("return " + NameW + ".Err()")
	}
	o.depth = 0
	o.str("}\n")
	if len(g.directUsed) != len(direct) {
		g.errorf(Span{}, "internal error in code generation: direct calls to %v, imports of %v", g.directUsed, direct)
	}
	return g.finish(gf)
}

// NameDirect is the import name of the implementation package of the
// component imported as alias, in the build flavor: "lyr__Card"
// (PLAN.md §3.5). Aliases start with an uppercase letter, so it can't
// clash with Lyr's own lyr__ names.
func NameDirect(alias string) string { return "lyr__" + alias }

// directCall reports whether the build flavor calls n's implementation
// directly (PLAN.md §3.5).
func (g *cgGen) directCall(n *Component) bool {
	d := g.in.Direct
	if d == nil || g.tolerant || n.Defer != nil && n.Client == nil {
		return false
	}
	if n.Self {
		return d.Self && !g.isPage()
	}
	imp := g.imports[n.Name]
	if imp == nil {
		return false
	}
	info := g.in.Index[imp.Resolved]
	return info != nil && info.Role != RolePage && d.Callees[imp.Resolved]
}

// directImports returns the sorted aliases of the components the template
// calls directly at least once: the impl packages the build flavor imports.
func (g *cgGen) directImports() []string {
	if g.in.Direct == nil || g.tolerant || g.f.Template == nil || g.a.Terminating {
		return nil
	}
	set := map[string]bool{}
	Walk(g.f.Template, func(n Node) bool {
		if c, ok := n.(*Component); ok && !c.Self && g.directCall(c) {
			set[c.Name] = true
		}
		return true
	})
	return slices.Sorted(maps.Keys(set))
}

// hasConcurrent reports whether the emitted template has a
// server:concurrent call site, which resolves locals first (PLAN.md §3.6).
func (g *cgGen) hasConcurrent() bool {
	if g.f.Template == nil || g.a.Terminating {
		return false
	}
	found := false
	Walk(g.f.Template, func(n Node) bool {
		if c, ok := n.(*Component); ok && c.Concurrent != nil && (c.Defer == nil || c.Client != nil) {
			found = true
		}
		return !found
	})
	return found
}

// declaresLocals reports whether the render function declares locals: when
// the component's code references it (a request whose components never read
// locals then skips the lookup and the new(L)), and always in Tolerant mode,
// so gopls can complete and hover it while it's being typed.
func (g *cgGen) declaresLocals() bool { return g.tolerant || g.a.UsesLocals }

// stmt writes one body statement and its line break. Each return result is
// wrapped as lyr__rt.ErrorAt("<path>:L:C", <result>), so a returned error
// carries the .lyr position of its return; the statement's other code and
// each result are copied inline (kind=exact), the wrapper is glue.
func (g *cgGen) stmt(s Stmt) {
	o := g.o
	if len(s.Returns) == 0 {
		o.exact(s.Span)
		o.str("\n")
		return
	}
	// copy copies src[a:b] after the result of a return: the whitespace
	// after the glue as glue, so the segment starts on a token.
	copy := func(a, b int) {
		i := a
		for i < b && strings.IndexByte(" \t\r\n", g.src[i]) >= 0 {
			i++
		}
		o.str(g.src[a:i])
		if i < b {
			o.exact(Span{i, b})
		}
	}
	at := s.Span.Start
	for _, r := range s.Returns {
		if r.Result.Start < at || r.Result.End > s.Span.End {
			continue // out of order: copy it unwrapped
		}
		copy(at, r.Result.Start)
		o.str(NameRuntime + ".ErrorAt(" + strconv.Quote(g.a.Info.Path+":"+o.pos(r.Keyword)) + ", ")
		o.exact(r.Result)
		o.str(")")
		at = r.Result.End
	}
	copy(at, s.Span.End)
	o.str("\n")
}

// cgTemplateCodes lists the Go regions of one template node: an
// expression, a control header, attribute expressions and spreads.
func cgTemplateCodes(n Node) []GoCode {
	var codes []GoCode
	switch n := n.(type) {
	case *Expr:
		codes = append(codes, n.Code)
	case *Branch:
		if !n.Else {
			codes = append(codes, n.Cond)
		}
	case *For:
		codes = append(codes, n.Clause)
	case *Switch:
		codes = append(codes, n.Clause)
	case *Case:
		if !n.Default {
			codes = append(codes, n.List)
		}
	}
	if tg := TagOf(n); tg != nil {
		for _, a := range tg.Attrs {
			if a.Kind == AttrExpr || a.Kind == AttrShorthand || a.Kind == AttrSpread {
				codes = append(codes, a.Code)
			}
		}
	}
	return codes
}
