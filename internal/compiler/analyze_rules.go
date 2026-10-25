package compiler

// Analysis rules: reserved names, the decl/impl partition (PLAN.md §3.1),
// Props, import placement, clashes, page params and the terminating rule.

import (
	"go/ast"
	gotoken "go/token"
	"strings"
)

func (s *anState) reservedIdents() {
	for _, t := range s.toks {
		if t.tok == gotoken.IDENT && IsReservedIdent(t.lit) {
			s.reserved(t)
		}
	}
}

func (s *anState) reservedCodes(codes []GoCode) {
	for _, c := range codes {
		for _, t := range anScan(c.Text, c.Start) {
			if t.tok == gotoken.IDENT && IsReservedIdent(t.lit) {
				s.reserved(t)
			}
		}
	}
}

func (s *anState) reserved(t anTok) {
	s.errorf(Span{t.pos, t.end}, "%s is reserved: names starting with orbit__ or Orbit__ are used by generated code", t.lit)
}

// typeSpec returns the type spec named name declared by a.Decls[i], or nil.
func (s *anState) typeSpec(i int, name string) *ast.TypeSpec {
	gd, ok := s.declAST[i].(*ast.GenDecl)
	if !ok || gd.Tok != gotoken.TYPE {
		return nil
	}
	for _, spec := range gd.Specs {
		if ts, ok := spec.(*ast.TypeSpec); ok && ts.Name != nil && ts.Name.Name == name {
			return ts
		}
	}
	return nil
}

// partition places every hoisted declaration: Props and its transitive
// dependencies (references resolved in the decl file's scope, and the
// methods of every type placed in decl) go to decl, the rest to impl.
func (s *anState) partition() {
	n := len(s.a.Decls)
	type ref struct {
		to   int
		name string
	}
	refs := make([][]ref, n)
	sels := make([]map[string]bool, n)
	for i, d := range s.declAST {
		sels[i] = map[string]bool{}
		if d == nil {
			continue
		}
		ast.Inspect(d, func(nd ast.Node) bool {
			switch x := nd.(type) {
			case *ast.SelectorExpr:
				if id, ok := x.X.(*ast.Ident); ok && id.Obj == nil {
					sels[i][id.Name] = true
				}
			case *ast.Ident:
				if x.Obj != nil && s.declFile.Scope != nil && s.declFile.Scope.Lookup(x.Name) == x.Obj {
					if j, ok := s.names[x.Name]; ok && j != i {
						refs[i] = append(refs[i], ref{j, x.Name})
					}
				}
			}
			return true
		})
	}
	inDecl := make([]bool, n)
	if s.a.Info.Role == RoleComponent {
		var stack []int
		for i := range s.declAST {
			if s.typeSpec(i, "Props") != nil {
				stack = append(stack, i)
				break
			}
		}
		for len(stack) > 0 {
			i := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if inDecl[i] {
				continue
			}
			inDecl[i] = true
			for _, r := range refs[i] {
				stack = append(stack, r.to)
			}
			if s.a.Decls[i].Kind == HoistType {
				for _, name := range s.a.Decls[i].Names {
					stack = append(stack, s.methods[name]...)
				}
			}
		}
	}
	for i := range s.a.Decls {
		side := s.implSel
		if inDecl[i] {
			s.a.Decls[i].Place = PlaceDecl
			side = s.declSel
		} else {
			s.a.Decls[i].Place = PlaceImpl
			for _, r := range refs[i] {
				if inDecl[r.to] {
					s.implRefs[r.name] = true
				}
			}
		}
		for name := range sels[i] {
			side[name] = true
		}
	}
}

// checkDecls reports the rules on hoisted declarations and fills Props.
func (s *anState) checkDecls() {
	page := s.a.Info.Role == RolePage
	propsSeen := false
	for i, d := range s.declAST {
		h := &s.a.Decls[i]
		if fd, ok := d.(*ast.FuncDecl); ok && fd.Recv == nil && fd.Name != nil && fd.Name.Name == "init" {
			s.errorf(s.span(fd.Name), "func init isn't allowed in a component; use a package under internal/ for initialization")
		}
		for k, name := range h.Names {
			sp := h.NameSpans[k]
			switch {
			case name == "Render":
				s.errorf(sp, "Render is generated for every component; rename this declaration")
			case page && name == "Props" && h.Kind == HoistType:
				s.errorf(sp, "pages don't receive props; move this to a component or read params")
			case page && name == "Params" && h.Kind == HoistType:
				s.errorf(sp, "Params is generated from the page's route; rename this type")
			}
			if h.Place == PlaceDecl && !ast.IsExported(name) && (s.implIdent[name] || s.implRefs[name]) {
				s.errorf(sp, "%s is used by Props, so it lives in the component's declaration package and must be exported (rename it to %s)",
					name, anUpperFirst(name))
			}
		}
		if ts := s.typeSpec(i, "Props"); ts != nil && !page && !propsSeen {
			propsSeen = true
			s.props(ts, s.hasDeclErr(h.Span))
		}
	}
}

// hasDeclErr reports whether the decl file has a syntax error in sp.
func (s *anState) hasDeclErr(sp Span) bool {
	for _, o := range s.declErrs {
		if sp.Start <= o && o <= sp.End {
			return true
		}
	}
	return false
}

// props fills PropsInfo from the Props type spec. With broken (a syntax
// error inside the declaration), the field rules aren't reported: the
// fields are a guess.
func (s *anState) props(ts *ast.TypeSpec, broken bool) {
	p := &s.a.Props
	p.Declared = true
	p.Span = s.span(ts)
	if ts.TypeParams != nil && len(ts.TypeParams.List) > 0 {
		s.errorf(s.span(ts.Name), "Props can't have type parameters")
		p.Listable = false
	}
	st, ok := ts.Type.(*ast.StructType)
	if !ok {
		p.Listable = false
		return
	}
	if st.Fields == nil {
		return
	}
	norm := map[string]string{}
	for _, fld := range st.Fields.List {
		if len(fld.Names) == 0 {
			p.Listable = false
			continue
		}
		ts := s.span(fld.Type)
		typ := s.src[ts.Start:ts.End]
		doc := ""
		if fld.Doc != nil {
			doc = fld.Doc.Text()
		}
		if fld.Comment != nil {
			doc += fld.Comment.Text()
		}
		for _, id := range fld.Names {
			if id.Name == "_" {
				continue
			}
			sp := s.span(id)
			if !ast.IsExported(id.Name) && !broken {
				s.errorf(sp, "Props field %s must be exported, since a parent sets it (rename it to %s)", id.Name, anUpperFirst(id.Name))
			}
			key := NormalizeName(id.Name)
			if prev, dup := norm[key]; dup && !broken {
				s.errorf(sp, "Props fields %s and %s both match the attribute name %s; rename one", prev, id.Name, key)
			} else {
				norm[key] = id.Name
			}
			p.Fields = append(p.Fields, PropField{Name: id.Name, NameSpan: sp, Type: typ, Doc: doc})
		}
	}
}

// placeImports places Go and .orbit imports by their uses, and reports
// unused .orbit imports.
func (s *anState) placeImports() {
	place := func(name string) Placement {
		var p Placement
		if s.declSel[name] {
			p |= PlaceDecl
		}
		if s.implSel[name] {
			p |= PlaceImpl
		}
		return p
	}
	var covered Placement
	for i := range s.a.GoImports {
		g := &s.a.GoImports[i]
		name := g.Name
		if name == "" {
			name = anPkgName(g.Path)
		}
		if name == "_" || name == "." {
			g.Place = PlaceImpl
			continue
		}
		g.Place = place(name)
		if g.Place == 0 {
			g.Place = PlaceImpl
		}
		if name == "orbit" {
			covered |= g.Place
		}
	}
	s.a.ImplicitOrbit = place("orbit") &^ covered
	for i := range s.a.OrbitImports {
		o := &s.a.OrbitImports[i]
		o.Place = place(o.Alias)
		if s.tagUses[o.Alias] {
			o.Place |= PlaceImpl
		}
		if o.Place == 0 {
			// Unused: emitted nowhere (DESIGN.md gap 1).
			if anUpperFirst(o.Alias) == o.Alias {
				s.errorf(o.AliasSpan, "component %s is imported but not used; remove the import", o.Alias)
			}
		}
	}
}

// checkClashes reports .orbit aliases, named Go imports and impl-placed
// names that equal an exported name of the declaration package.
func (s *anState) checkClashes() {
	page := s.a.Info.Role == RolePage
	exports := map[string]bool{}
	for _, h := range s.a.Decls {
		if h.Place == PlaceDecl {
			for _, n := range h.Names {
				if ast.IsExported(n) {
					exports[n] = true
				}
			}
		}
	}
	generated := func(n string) bool { return n == "Props" || n == "Render" || page && n == "Params" }
	check := func(name string, sp Span) {
		switch {
		case generated(name):
			s.errorf(sp, "%s clashes with the generated %s of the component's declaration package; rename it", name, name)
		case exports[name]:
			s.errorf(sp, "%s clashes with %s in the component's declaration package (Props uses it); rename one", name, name)
		}
	}
	for _, o := range s.a.OrbitImports {
		check(o.Alias, o.AliasSpan)
	}
	for _, g := range s.a.GoImports {
		if g.Name != "" && g.Name != "_" && g.Name != "." && g.Place&PlaceImpl != 0 {
			check(g.Name, Span{g.Span.Start, g.Span.Start + len(g.Name)})
		}
	}
	for _, h := range s.a.Decls {
		if h.Place != PlaceImpl {
			continue
		}
		for k, n := range h.Names {
			// Render and a page's Props/Params types have their own errors.
			if n == "Render" || page && h.Kind == HoistType && (n == "Props" || n == "Params") {
				continue
			}
			check(n, h.NameSpans[k])
		}
	}
}

// params derives a page's Params fields from its route.
func (s *anState) params() {
	seen := map[string]string{}
	for _, seg := range strings.Split(s.a.Info.Route, "/") {
		var name string
		rest := false
		switch {
		case strings.HasPrefix(seg, "[...") && strings.HasSuffix(seg, "]") && len(seg) > 5:
			name, rest = seg[4:len(seg)-1], true
		case strings.HasPrefix(seg, "[") && strings.HasSuffix(seg, "]") && len(seg) > 2 && !strings.ContainsAny(seg[1:len(seg)-1], "[]"):
			name = seg[1 : len(seg)-1]
		default:
			continue
		}
		if !anValidParam(name) {
			s.errorf(Span{}, "route param %s can't become a Go field name; use letters, digits, - and _, starting with a letter", seg)
			continue
		}
		field := FieldName(name)
		if prev, dup := seen[field]; dup {
			s.errorf(Span{}, "route params %s and %s both become params.%s; rename one", prev, seg, field)
			continue
		}
		seen[field] = seg
		s.a.Params = append(s.a.Params, ParamField{Field: field, Param: name, Rest: rest})
	}
}

// anValidParam reports whether name matches [A-Za-z][A-Za-z0-9_-]*.
func anValidParam(name string) bool {
	for i := 0; i < len(name); i++ {
		c := name[i]
		letter := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
		if !letter && (i == 0 || !(c >= '0' && c <= '9' || c == '_' || c == '-')) {
			return false
		}
	}
	return name != ""
}

// anTerminating reports whether st is a terminating statement (Go spec,
// "Terminating statements"); label is st's label, if any.
func anTerminating(st ast.Stmt, label string) bool {
	switch st := st.(type) {
	case *ast.ReturnStmt:
		return true
	case *ast.BranchStmt:
		return st.Tok == gotoken.GOTO
	case *ast.ExprStmt:
		call, ok := ast.Unparen(st.X).(*ast.CallExpr)
		if !ok {
			return false
		}
		id, ok := ast.Unparen(call.Fun).(*ast.Ident)
		return ok && id.Name == "panic" && id.Obj == nil
	case *ast.BlockStmt:
		return anTermList(st.List)
	case *ast.IfStmt:
		return st.Else != nil && anTerminating(st.Body, "") && anTerminating(st.Else, "")
	case *ast.LabeledStmt:
		return st.Label != nil && anTerminating(st.Stmt, st.Label.Name)
	case *ast.ForStmt:
		return st.Cond == nil && !anHasBreak(st.Body, label, true)
	case *ast.SwitchStmt:
		return anTermCases(st.Body, label)
	case *ast.TypeSwitchStmt:
		return anTermCases(st.Body, label)
	case *ast.SelectStmt:
		if st.Body == nil || anHasBreak(st.Body, label, true) {
			return false
		}
		for _, c := range st.Body.List {
			cc, ok := c.(*ast.CommClause)
			if !ok || !anTermList(cc.Body) {
				return false
			}
		}
		return true
	}
	return false
}

func anTermList(list []ast.Stmt) bool {
	i := len(list) - 1
	for i >= 0 {
		if _, ok := list[i].(*ast.EmptyStmt); !ok {
			break
		}
		i--
	}
	return i >= 0 && anTerminating(list[i], "")
}

func anTermCases(body *ast.BlockStmt, label string) bool {
	if body == nil || anHasBreak(body, label, true) {
		return false
	}
	hasDefault := false
	for _, c := range body.List {
		cc, ok := c.(*ast.CaseClause)
		if !ok {
			return false
		}
		if cc.List == nil {
			hasDefault = true
		}
		if n := len(cc.Body); n > 0 {
			if br, ok := cc.Body[n-1].(*ast.BranchStmt); ok && br.Tok == gotoken.FALLTHROUGH {
				continue
			}
		}
		if !anTermList(cc.Body) {
			return false
		}
	}
	return hasDefault
}

// anHasBreak reports whether n contains a break that targets the enclosing
// statement: an unlabeled one outside nested for/switch/select (when
// implicit), or one with the statement's label.
func anHasBreak(n *ast.BlockStmt, label string, implicit bool) bool {
	if n == nil {
		return false
	}
	found := false
	ast.Inspect(n, func(x ast.Node) bool {
		if found || x == nil {
			return false
		}
		switch x := x.(type) {
		case *ast.BranchStmt:
			if x.Tok == gotoken.BREAK && (x.Label == nil && implicit || x.Label != nil && label != "" && x.Label.Name == label) {
				found = true
			}
		case *ast.FuncLit:
			return false
		case *ast.ForStmt:
			found = label != "" && anHasBreak(x.Body, label, false)
			return false
		case *ast.RangeStmt:
			found = label != "" && anHasBreak(x.Body, label, false)
			return false
		case *ast.SwitchStmt:
			found = label != "" && anHasBreak(x.Body, label, false)
			return false
		case *ast.TypeSwitchStmt:
			found = label != "" && anHasBreak(x.Body, label, false)
			return false
		case *ast.SelectStmt:
			found = label != "" && anHasBreak(x.Body, label, false)
			return false
		}
		return true
	})
	return found
}
