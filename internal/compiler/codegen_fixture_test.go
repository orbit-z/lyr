package compiler

// A minimal stand-in for Analyze (part A, developed in parallel), so codegen
// tests can build Analysis values from .orbit sources. It handles what the
// test inputs use: top-level splitting, imports, hoisted types/consts/funcs
// and methods, a struct Props, placement by identifier references, echo
// names, a terminating return and page params. It reports no diagnostics.

import (
	"go/ast"
	goparser "go/parser"
	goscanner "go/scanner"
	gotoken "go/token"
	"path"
	"strconv"
	"strings"
	"testing"
)

type cgTok struct {
	off, end int
	tok      gotoken.Token
	lit      string
}

func cgTokens(code string) []cgTok {
	fset := gotoken.NewFileSet()
	file := fset.AddFile("", -1, len(code))
	var s goscanner.Scanner
	s.Init(file, []byte(code), nil, goscanner.ScanComments)
	var toks []cgTok
	for {
		pos, tok, lit := s.Scan()
		if tok == gotoken.EOF {
			return toks
		}
		off := file.Offset(pos)
		end := off
		switch {
		case tok == gotoken.SEMICOLON && lit == "\n":
		case lit != "":
			end = off + len(lit)
		default:
			end = off + len(tok.String())
		}
		toks = append(toks, cgTok{off, end, tok, lit})
	}
}

// cgSelectors returns the names x used as x.y in code.
func cgSelectors(code string) map[string]bool {
	out := map[string]bool{}
	toks := cgTokens(code)
	for i := 0; i+1 < len(toks); i++ {
		if toks[i].tok == gotoken.IDENT && toks[i+1].tok == gotoken.PERIOD && (i == 0 || toks[i-1].tok != gotoken.PERIOD) {
			out[toks[i].lit] = true
		}
	}
	return out
}

func cgIdents(code string) map[string]bool {
	out := map[string]bool{}
	for _, t := range cgTokens(code) {
		if t.tok == gotoken.IDENT {
			out[t.lit] = true
		}
	}
	return out
}

type cgStmt struct {
	span, doc Span // file offsets
	toks      []cgTok
}

// cgSplit splits frontmatter code into top-level statements.
func cgSplit(code string, base int) []cgStmt {
	toks := cgTokens(code)
	line := func(off int) int { return strings.Count(code[:off], "\n") }
	var out []cgStmt
	var cur []cgTok
	depth := 0
	doc := Span{-1, -1}
	docEnd := -10
	for _, t := range toks {
		if t.tok == gotoken.COMMENT && len(cur) == 0 {
			if doc.Start >= 0 && line(t.off) == docEnd+1 {
				doc.End = t.end
			} else {
				doc = Span{t.off, t.end}
			}
			docEnd = line(t.end)
			continue
		}
		if t.tok == gotoken.COMMENT {
			continue
		}
		switch t.tok {
		case gotoken.LPAREN, gotoken.LBRACK, gotoken.LBRACE:
			depth++
		case gotoken.RPAREN, gotoken.RBRACK, gotoken.RBRACE:
			depth--
		}
		if t.tok == gotoken.SEMICOLON && depth == 0 {
			if len(cur) > 0 {
				st := cgStmt{span: Span{base + cur[0].off, base + cur[len(cur)-1].end}, toks: cur}
				if doc.Start >= 0 && docEnd == line(cur[0].off)-1 {
					st.doc = Span{base + doc.Start, base + doc.End}
				}
				out = append(out, st)
			}
			cur = nil
			doc = Span{-1, -1}
			continue
		}
		cur = append(cur, t)
	}
	if len(cur) > 0 {
		out = append(out, cgStmt{span: Span{base + cur[0].off, base + cur[len(cur)-1].end}, toks: cur})
	}
	return out
}

const cgPkgHeader = "package p\n"

// cgParseDecl parses a hoisted statement as a file; off converts its
// positions to file offsets.
func cgParseDecl(text string, start int) (*ast.File, func(gotoken.Pos) int) {
	fset := gotoken.NewFileSet()
	f, _ := goparser.ParseFile(fset, "", cgPkgHeader+text, goparser.ParseComments)
	return f, func(p gotoken.Pos) int { return fset.Position(p).Offset - len(cgPkgHeader) + start }
}

// cgAnalyze parses src and builds its Analysis.
func cgAnalyze(t testing.TB, orbitPath, src string, mode Mode) (*File, *Analysis) {
	t.Helper()
	f := Parse(src, mode)
	a := &Analysis{Info: FileInfo{Path: orbitPath}}
	if strings.HasPrefix(orbitPath, "src/pages/") {
		a.Info.Role = RolePage
		route := strings.TrimSuffix(strings.TrimPrefix(orbitPath, "src/pages"), ".orbit")
		route = strings.TrimSuffix(route, "/index")
		if route == "" {
			route = "/"
		}
		a.Info.Route = route
		for _, seg := range strings.Split(route, "/") {
			if strings.HasPrefix(seg, "[...") {
				name := seg[4 : len(seg)-1]
				a.Params = append(a.Params, ParamField{Field: FieldName(name), Param: name, Rest: true})
			} else if strings.HasPrefix(seg, "[") {
				name := seg[1 : len(seg)-1]
				a.Params = append(a.Params, ParamField{Field: FieldName(name), Param: name})
			}
		}
	}
	a.Props.Listable = true
	code := f.Frontmatter.Code
	var bodyTexts []string
	for _, st := range cgSplit(code.Text, code.Start) {
		text := src[st.span.Start:st.span.End]
		first := st.toks[0].tok
		isFunc := first == gotoken.FUNC && len(st.toks) > 1 && st.toks[1].tok == gotoken.IDENT
		isMethod := false
		if first == gotoken.FUNC && len(st.toks) > 1 && st.toks[1].tok == gotoken.LPAREN {
			d := 0
			for i := 1; i < len(st.toks); i++ {
				switch st.toks[i].tok {
				case gotoken.LPAREN:
					d++
				case gotoken.RPAREN:
					d--
				}
				if d == 0 {
					isMethod = i+1 < len(st.toks) && st.toks[i+1].tok == gotoken.IDENT
					break
				}
			}
		}
		switch {
		case first == gotoken.IMPORT:
			af, off := cgParseDecl(text, st.span.Start)
			for _, is := range af.Imports {
				p, _ := strconv.Unquote(is.Path.Value)
				span := Span{off(is.Pos()), off(is.End())}
				pathSpan := Span{off(is.Path.Pos()), off(is.Path.End())}
				if strings.HasSuffix(p, ".orbit") {
					oi := OrbitImport{Path: p, Span: span, PathSpan: pathSpan, Resolved: path.Join(path.Dir(orbitPath), p)}
					if is.Name != nil {
						oi.Alias = is.Name.Name
						oi.AliasSpan = Span{off(is.Name.Pos()), off(is.Name.End())}
					}
					a.OrbitImports = append(a.OrbitImports, oi)
					continue
				}
				gi := GoImport{Path: p, Span: span, PathSpan: pathSpan}
				if is.Name != nil {
					gi.Name = is.Name.Name
				}
				a.GoImports = append(a.GoImports, gi)
			}
		case first == gotoken.TYPE || first == gotoken.CONST || isFunc || isMethod:
			h := Hoisted{Span: st.span, Doc: st.doc, Place: PlaceImpl}
			af, off := cgParseDecl(text, st.span.Start)
			for _, d := range af.Decls {
				switch d := d.(type) {
				case *ast.FuncDecl:
					h.Kind = HoistFunc
					if d.Recv != nil {
						rt := d.Recv.List[0].Type
						if s, ok := rt.(*ast.StarExpr); ok {
							rt = s.X
						}
						if id, ok := rt.(*ast.Ident); ok {
							h.Recv = id.Name
						}
					} else {
						h.Names = append(h.Names, d.Name.Name)
						h.NameSpans = append(h.NameSpans, Span{off(d.Name.Pos()), off(d.Name.End())})
					}
				case *ast.GenDecl:
					h.Kind = HoistConst
					if d.Tok == gotoken.TYPE {
						h.Kind = HoistType
					}
					for _, sp := range d.Specs {
						switch sp := sp.(type) {
						case *ast.TypeSpec:
							h.Names = append(h.Names, sp.Name.Name)
							h.NameSpans = append(h.NameSpans, Span{off(sp.Name.Pos()), off(sp.Name.End())})
							if sp.Name.Name == "Props" && a.Info.Role != RolePage {
								a.Props.Declared = true
								a.Props.Span = Span{off(sp.Pos()), off(sp.End())}
								st, ok := sp.Type.(*ast.StructType)
								if !ok {
									a.Props.Listable = false
									break
								}
								for _, fl := range st.Fields.List {
									if len(fl.Names) == 0 {
										a.Props.Listable = false
										continue
									}
									for _, n := range fl.Names {
										a.Props.Fields = append(a.Props.Fields, PropField{
											Name: n.Name, NameSpan: Span{off(n.Pos()), off(n.End())},
											Type: src[off(fl.Type.Pos()):off(fl.Type.End())],
										})
									}
								}
							}
						case *ast.ValueSpec:
							for _, n := range sp.Names {
								h.Names = append(h.Names, n.Name)
								h.NameSpans = append(h.NameSpans, Span{off(n.Pos()), off(n.End())})
							}
						}
					}
				}
			}
			a.Decls = append(a.Decls, h)
		default:
			a.Body = append(a.Body, Stmt{Span: st.span})
			bodyTexts = append(bodyTexts, text)
		}
	}
	// Echo names and the terminating rule.
	seen := map[string]bool{}
	for i, text := range bodyTexts {
		fset := gotoken.NewFileSet()
		pf, _ := goparser.ParseFile(fset, "", cgPkgHeader+"func _() {\n"+text+"\n}", 0)
		if pf == nil || len(pf.Decls) == 0 {
			continue
		}
		fd, ok := pf.Decls[0].(*ast.FuncDecl)
		if !ok || fd.Body == nil || len(fd.Body.List) == 0 {
			continue
		}
		var names []string
		switch s := fd.Body.List[0].(type) {
		case *ast.AssignStmt:
			if s.Tok == gotoken.DEFINE {
				for _, e := range s.Lhs {
					if id, ok := e.(*ast.Ident); ok {
						names = append(names, id.Name)
					}
				}
			}
		case *ast.DeclStmt:
			if gd, ok := s.Decl.(*ast.GenDecl); ok && gd.Tok == gotoken.VAR {
				for _, sp := range gd.Specs {
					for _, n := range sp.(*ast.ValueSpec).Names {
						names = append(names, n.Name)
					}
				}
			}
		case *ast.ReturnStmt:
			if i == len(bodyTexts)-1 {
				a.Terminating = true
			}
		}
		for _, n := range names {
			if n != "_" && !seen[n] {
				seen[n] = true
				a.Echo = append(a.Echo, n)
			}
		}
	}
	// Placement: Props and what it references go to decl, with methods.
	inDecl := map[string]bool{"Props": true}
	for changed := true; changed; {
		changed = false
		for i := range a.Decls {
			d := &a.Decls[i]
			if d.Place == PlaceDecl {
				continue
			}
			hit := d.Recv != "" && inDecl[d.Recv]
			for _, n := range d.Names {
				hit = hit || inDecl[n]
			}
			if !hit {
				continue
			}
			d.Place = PlaceDecl
			changed = true
			for n := range cgIdents(src[d.Span.Start:d.Span.End]) {
				inDecl[n] = true
			}
		}
	}
	declSel, implSel := map[string]bool{}, map[string]bool{}
	for _, d := range a.Decls {
		dst := implSel
		if d.Place == PlaceDecl {
			dst = declSel
		}
		for n := range cgSelectors(src[d.Span.Start:d.Span.End]) {
			dst[n] = true
		}
	}
	for _, text := range bodyTexts {
		for n := range cgSelectors(text) {
			implSel[n] = true
		}
	}
	tags := map[string]bool{}
	if f.Template != nil {
		Walk(f.Template, func(n Node) bool {
			var codes []GoCode
			switch n := n.(type) {
			case *Expr:
				codes = append(codes, n.Code)
			case *Branch:
				codes = append(codes, n.Cond)
			case *For:
				codes = append(codes, n.Clause)
			case *Component:
				tags[n.Name] = true
			}
			if tg := TagOf(n); tg != nil {
				for _, at := range tg.Attrs {
					codes = append(codes, at.Code)
				}
			}
			for _, c := range codes {
				for n := range cgSelectors(c.Text) {
					implSel[n] = true
				}
			}
			return true
		})
	}
	orbitCovered := Placement(0)
	for i := range a.GoImports {
		gi := &a.GoImports[i]
		name := gi.Name
		if name == "" {
			name = path.Base(gi.Path)
		}
		if declSel[name] {
			gi.Place |= PlaceDecl
		}
		if implSel[name] || gi.Place == 0 {
			gi.Place |= PlaceImpl
		}
		if gi.Path == RuntimePath && name == "orbit" {
			orbitCovered |= gi.Place
		}
	}
	for i := range a.OrbitImports {
		oi := &a.OrbitImports[i]
		if declSel[oi.Alias] {
			oi.Place |= PlaceDecl
		}
		if implSel[oi.Alias] || tags[oi.Alias] || oi.Place == 0 {
			oi.Place |= PlaceImpl
		}
	}
	if declSel["orbit"] {
		a.ImplicitOrbit |= PlaceDecl
	}
	if implSel["orbit"] {
		a.ImplicitOrbit |= PlaceImpl
	}
	a.ImplicitOrbit &^= orbitCovered
	return f, a
}

// cgInfo builds the index entry of an analyzed file.
func cgInfo(a *Analysis) *ComponentInfo {
	pkg, _ := PackageFor(a.Info.Path)
	return &ComponentInfo{
		Path:  a.Info.Path,
		Role:  a.Info.Role,
		Pkg:   pkg,
		Name:  strings.TrimSuffix(path.Base(a.Info.Path), ".orbit"),
		ID:    ComponentID(a.Info.Path),
		Props: a.Props,
	}
}
