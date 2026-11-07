package compiler

// Page partials (PLAN.md §4k): the const partial flag, its errors, and the
// warning for a partial page's own <style>.

import (
	"go/ast"
	gotoken "go/token"
)

// PartialName is the frontmatter constant that makes a page a partial.
const PartialName = "partial"

// The partial-flag messages (DESIGN.md, docs/troubleshooting.md).
const (
	msgPartialValue     = "partial must be true or false: const partial = true makes this page a partial"
	msgPartialConst     = "partial must be a constant: write const partial = true to make this page a partial"
	msgPartialReserved  = "partial is reserved in a page for const partial = true; rename this declaration"
	msgPartialComponent = "partial only applies to pages (files under src/pages); remove it, or rename the constant"
	// MsgPartialStyle is the warning at a partial page's processed <style>.
	MsgPartialStyle = "a partial page ships no CSS, so this <style> is dropped; move it to a component the host page imports, or add is:inline"
)

// partialFlag reads a page's const partial and reports its misuses in
// hoisted declarations (the body's var and := are reported by echo).
func (s *anState) partialFlag() {
	page := s.a.Info.Role == RolePage
	for i, d := range s.declAST {
		h := &s.a.Decls[i]
		switch d := d.(type) {
		case *ast.FuncDecl:
			if page && d.Recv == nil && d.Name != nil && d.Name.Name == PartialName {
				s.errorf(s.span(d.Name), msgPartialReserved)
			}
		case *ast.GenDecl:
			switch d.Tok {
			case gotoken.TYPE:
				if !page {
					continue
				}
				for _, spec := range d.Specs {
					if ts, ok := spec.(*ast.TypeSpec); ok && ts.Name != nil && ts.Name.Name == PartialName {
						s.errorf(s.span(ts.Name), msgPartialReserved)
					}
				}
			case gotoken.CONST:
				for _, spec := range d.Specs {
					vs, ok := spec.(*ast.ValueSpec)
					if !ok {
						continue
					}
					for k, id := range vs.Names {
						if id.Name != PartialName {
							continue
						}
						if !page {
							s.errorf(s.span(id), msgPartialComponent)
							continue
						}
						s.partialSpec(vs, k)
					}
				}
			}
		case nil:
			// A declaration that doesn't parse: its names are still known.
			if page && h.Kind != HoistConst {
				for k, n := range h.Names {
					if n == PartialName {
						s.errorf(h.NameSpans[k], msgPartialReserved)
					}
				}
			}
		}
	}
}

// partialSpec checks the k-th name of a page's const spec, partial, and
// sets Analysis.Partial from its value.
func (s *anState) partialSpec(vs *ast.ValueSpec, k int) {
	if vs.Type != nil {
		if id, ok := vs.Type.(*ast.Ident); !ok || id.Name != "bool" {
			s.errorf(s.span(vs.Type), msgPartialValue)
			return
		}
	}
	if k >= len(vs.Values) {
		// An implicit repetition in a group, or a missing value.
		s.errorf(s.span(vs.Names[k]), msgPartialValue)
		return
	}
	v, ok := vs.Values[k].(*ast.Ident)
	if !ok || v.Name != "true" && v.Name != "false" {
		s.errorf(s.span(vs.Values[k]), msgPartialValue)
		return
	}
	s.a.Partial = v.Name == "true"
}

// partialStyles warns at each processed <style> of a partial page: a
// partial ships no CSS, so the block is dropped (PLAN.md §4k (Q4)).
func (s *anState) partialStyles() {
	if !s.a.Partial || s.f.Template == nil {
		return
	}
	Walk(s.f.Template, func(n Node) bool {
		if st, ok := n.(*Style); ok && st.Processed {
			s.a.Diagnostics = append(s.a.Diagnostics, Diagnostic{SeverityWarning, st.Tag.StartTag, MsgPartialStyle})
		}
		return true
	})
}
