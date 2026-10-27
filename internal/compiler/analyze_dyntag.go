package compiler

// Dynamic tags (PLAN.md §2.8): a capitalized tag that isn't an import
// alias, <Self> or <Fragment>, and that names a variable or constant in
// scope, is an HTML element whose name is the variable's value. The parser
// can't tell, so it makes every capitalized tag a Component; the body file
// (analyze_impl.go) copies each one's name as an expression at the start
// tag, and go/parser's object resolution says whether it names a variable
// there. Analyze then turns those Components into Elements with Dynamic
// set.

import (
	"go/ast"
	gotoken "go/token"
	"slices"
)

// anProbe is a capitalized tag name copied into the body file.
type anProbe struct {
	goStart int // offset in the template part of the body file
	node    Node
	tag     *Tag
	id      *ast.Ident
}

// probe copies the name of a capitalized tag into the body file as an
// expression, to resolve it there. Not a template Go region (codes): it is
// checked as a tag name, not for reserved names.
func (y *anSynth) probe(n Node, t *Tag) {
	if t.NameSpan.Len() != len(t.Name) || !isIdent(t.Name) {
		return
	}
	y.glue(anGlueCall + "(")
	y.probes = append(y.probes, &anProbe{goStart: y.b.Len(), node: n, tag: t})
	y.segs = append(y.segs, anSeg{y.b.Len(), t.NameSpan.Start, len(t.Name)})
	y.b.WriteString(t.Name)
	y.glue(",\n)\n")
}

// dynTags decides which capitalized tags are dynamic tags, from the parsed
// body file (nil when it didn't parse: none are then), turns them into
// Elements, and reports the diagnostics that depend on it.
func (s *anState) dynTags(file *ast.File, tf *gotoken.File, tmplStart int, y *anSynth) {
	if file != nil && tf != nil && len(y.probes) > 0 {
		byOff := make(map[int]*anProbe, len(y.probes))
		for _, p := range y.probes {
			byOff[p.goStart] = p
		}
		ast.Inspect(file, func(n ast.Node) bool {
			if id, ok := n.(*ast.Ident); ok {
				if p := byOff[tf.Offset(id.Pos())-tmplStart]; p != nil && p.id == nil && id.Name == p.tag.Name {
					p.id = id
				}
			}
			return true
		})
	}
	aliases := map[string]bool{}
	for _, o := range s.a.OrbitImports {
		aliases[o.Alias] = true
	}
	dyn := map[*Component]bool{}
	clashed := map[string]bool{}
	for _, p := range y.probes {
		if e, ok := p.node.(*Element); ok {
			// Already converted by an earlier Analyze of this File.
			s.a.DynTags = append(s.a.DynTags, e.NameSpan)
			continue
		}
		c := p.node.(*Component)
		decl, ok := s.probeDecl(p, file, tf, tmplStart, y)
		switch {
		case !ok:
		case aliases[c.Name]:
			if !clashed[c.Name] {
				clashed[c.Name] = true
				s.errorf(decl, "%s is both a component import and a variable; rename one", c.Name)
			}
		default:
			dyn[c] = true
			s.a.DynTags = append(s.a.DynTags, c.NameSpan)
		}
	}
	if s.f.Template == nil {
		return
	}
	var drop []Diagnostic
	Walk(s.f.Template, func(n Node) bool {
		switch n := n.(type) {
		case *Component:
			if dyn[n] {
				drop = append(drop, n.asCall...)
				s.a.Diagnostics = append(s.a.Diagnostics, n.asTag...)
			}
		case *Element:
			if n.Dynamic {
				s.a.Diagnostics = append(s.a.Diagnostics, n.asTag...)
			}
		}
		return true
	})
	if len(drop) > 0 {
		s.f.Diagnostics = slices.DeleteFunc(s.f.Diagnostics, func(d Diagnostic) bool { return slices.Contains(drop, d) })
	}
	if len(dyn) > 0 {
		anConvert(s.f.Template, dyn)
	}
}

// probeDecl reports whether the probed name resolves to a variable or a
// constant, and where it is declared.
func (s *anState) probeDecl(p *anProbe, file *ast.File, tf *gotoken.File, tmplStart int, y *anSynth) (Span, bool) {
	id := p.id
	if id == nil {
		return Span{}, false
	}
	if obj := id.Obj; obj != nil {
		if obj.Kind != ast.Var && obj.Kind != ast.Con {
			return Span{}, false
		}
		decl := p.tag.NameSpan
		if dn, ok := obj.Decl.(ast.Node); ok {
			ast.Inspect(dn, func(n ast.Node) bool {
				if d, ok := n.(*ast.Ident); ok && d.Obj == obj && d.Name == id.Name {
					if sp, ok := s.bodyOrbitSpan(d, tf, tmplStart, y); ok {
						decl = sp
					}
					return false
				}
				return true
			})
		}
		return decl, true
	}
	if i, ok := s.names[id.Name]; ok && s.a.Decls[i].Kind == HoistConst {
		h := s.a.Decls[i]
		for k, n := range h.Names {
			if n == id.Name {
				return h.NameSpans[k], true
			}
		}
	}
	return Span{}, false
}

// bodyOrbitSpan maps an identifier of the body file to its .orbit span: in
// the frontmatter part, or in a copied template region.
func (s *anState) bodyOrbitSpan(id *ast.Ident, tf *gotoken.File, tmplStart int, y *anSynth) (Span, bool) {
	if a, ok := s.fmOff(tf, id.Pos(), s.bodyPrefix); ok && tf.Offset(id.Pos()) < tmplStart {
		return Span{a, a + len(id.Name)}, true
	}
	off := tf.Offset(id.Pos()) - tmplStart
	for _, sg := range y.segs {
		if sg.goStart <= off && off < sg.goStart+sg.n {
			a := sg.orbit + off - sg.goStart
			return Span{a, a + len(id.Name)}, true
		}
	}
	return Span{}, false
}

// anConvert replaces the dynamic Components under n with Elements.
func anConvert(n Node, dyn map[*Component]bool) {
	for _, list := range childLists(n) {
		for i, ch := range *list {
			if c, ok := ch.(*Component); ok && dyn[c] {
				e := &Element{Span: c.Span, Tag: c.Tag, Dynamic: true, Children: c.Children, asTag: c.asTag}
				(*list)[i] = e
				ch = e
			}
			anConvert(ch, dyn)
		}
	}
}

// childLists returns pointers to the child lists of n, for rewriting.
func childLists(n Node) []*[]Node {
	switch n := n.(type) {
	case *Template:
		return []*[]Node{&n.Children}
	case *If:
		out := make([]*[]Node, len(n.Branches))
		for i, b := range n.Branches {
			out[i] = &b.Children
		}
		return out
	case *For:
		return []*[]Node{&n.Children}
	case *Switch:
		out := []*[]Node{&n.Pre}
		for _, c := range n.Cases {
			out = append(out, &c.Children)
		}
		return out
	case *Element:
		return []*[]Node{&n.Children}
	case *Component:
		return []*[]Node{&n.Children}
	case *Slot:
		return []*[]Node{&n.Children}
	case *Fragment:
		return []*[]Node{&n.Children}
	}
	return nil
}
