package lsp

import (
	"fmt"
	"strings"

	"github.com/orbit-z/orbit/internal/compiler"
	"github.com/orbit-z/orbit/internal/project"
)

// Request routing by cursor region (PLAN.md §9): the orbit parser's AST
// says whether a position is Go code (proxied to gopls) or template markup
// (answered natively, or not at all).

// regionKind classifies a cursor position.
type regionKind uint8

const (
	// regionNone is template markup Orbit has nothing to say about.
	regionNone regionKind = iota
	// regionGo is Go code: the frontmatter, {expr}, attribute expressions,
	// spreads and control headers.
	regionGo
	// regionTag is a component's tag name (<Card).
	regionTag
	// regionProp is a prop's attribute name on a component tag.
	regionProp
	// regionImport is an .orbit import's alias or path in the frontmatter.
	regionImport
	// regionTagName is another tag name being typed (<di), or the position
	// right after a lone "<" in text.
	regionTagName
	// regionAttr is an attribute-name position in a start tag: between
	// attributes, or on the name of a directive or of a non-component's
	// attribute (a prop's name is regionProp).
	regionAttr
	// regionSlot is the value of a slot="…" attribute.
	regionSlot
)

// region is the classified cursor position.
type region struct {
	kind regionKind
	comp *compiler.Component
	attr *compiler.Attr
	imp  *compiler.OrbitImport
	// node is the tag-shaped node of a regionTag, regionTagName,
	// regionAttr, regionProp or regionSlot (nil after a lone "<").
	node compiler.Node
	// span is what a completion replaces: the tag name typed so far
	// (regionTag, regionTagName) or the slot value (regionSlot).
	span compiler.Span
	// parent is the component a regionSlot tag is passed to: the nearest
	// enclosing component with only control flow in between, else nil.
	parent *compiler.Component
	// frontmatter is true for a regionGo in the frontmatter.
	frontmatter bool
}

// classify returns the region at byte offset off of f.
func classify(f *project.File, off int) region {
	if f.AST == nil {
		return region{}
	}
	fm := f.AST.Frontmatter
	if fm.Present && fm.Code.Contains(off) {
		if f.Analysis != nil {
			for i := range f.Analysis.OrbitImports {
				im := &f.Analysis.OrbitImports[i]
				if im.AliasSpan.Contains(off) || im.PathSpan.Contains(off) {
					return region{kind: regionImport, imp: im}
				}
			}
		}
		return region{kind: regionGo, frontmatter: true}
	}
	var out region
	// visit reports whether it settled out (possibly as regionNone) at n
	// or below.
	var visit func(n compiler.Node, parent *compiler.Component) bool
	visit = func(n compiler.Node, parent *compiler.Component) bool {
		if !n.Bounds().Contains(off) {
			return false
		}
		switch n := n.(type) {
		case *compiler.Text:
			if off > n.Start && f.Src[off-1] == '<' {
				out = region{kind: regionTagName, span: compiler.Span{Start: off, End: off}}
				return true
			}
			return false
		case *compiler.Expr:
			if n.Code.Contains(off) {
				out = region{kind: regionGo}
				return true
			}
		case *compiler.Branch:
			if !n.Else && n.Header.Contains(off) && n.Cond.Contains(off) {
				out = region{kind: regionGo}
				return true
			}
		case *compiler.For:
			if n.Header.Contains(off) && n.Clause.Contains(off) {
				out = region{kind: regionGo}
				return true
			}
		}
		if t := compiler.TagOf(n); t != nil && inStartTag(f.Src, t, off) {
			out = classifyTag(n, t, off, parent)
			return true
		}
		switch n := n.(type) {
		case *compiler.Component:
			parent = n
		case *compiler.Element, *compiler.Slot, *compiler.Fragment, *compiler.Script, *compiler.Style:
			parent = nil
		}
		for _, c := range compiler.ChildNodes(n) {
			if visit(c, parent) {
				return true
			}
		}
		return false
	}
	visit(f.AST.Template, nil)
	return out
}

// inStartTag reports whether off is inside t's start tag: after the "<"
// and, when the tag is closed, not after its ">" or "/>".
func inStartTag(src string, t *compiler.Tag, off int) bool {
	st := t.StartTag
	if !st.Contains(off) || off == st.Start {
		return false
	}
	if st.End > st.Start && st.End <= len(src) && src[st.End-1] == '>' {
		end := st.End - 1
		if t.SelfClosing && end > st.Start && src[end-1] == '/' {
			end--
		}
		return off <= end
	}
	return true
}

// classifyTag classifies a position inside the start tag of n; parent is
// the component n is passed to as slot content.
func classifyTag(n compiler.Node, t *compiler.Tag, off int, parent *compiler.Component) region {
	comp, _ := n.(*compiler.Component)
	if t.NameSpan.Contains(off) {
		if comp != nil {
			return region{kind: regionTag, comp: comp, node: n, span: t.NameSpan}
		}
		return region{kind: regionTagName, node: n, span: t.NameSpan}
	}
	for _, a := range t.Attrs {
		switch {
		case (a.IsExpr() || a.Kind == compiler.AttrSpread) && a.Code.Contains(off):
			return region{kind: regionGo, comp: comp, attr: a, node: n}
		case a.IsStatic() && a.ValueSpan.Contains(off):
			if a.Directive == compiler.SlotDirective {
				return region{kind: regionSlot, comp: comp, attr: a, node: n, span: a.ValueSpan, parent: parent}
			}
			return region{}
		case a.Kind != compiler.AttrSpread && a.NameSpan.Contains(off):
			if comp != nil && a.Directive == compiler.NotDirective {
				return region{kind: regionProp, comp: comp, attr: a, node: n}
			}
			return region{kind: regionAttr, comp: comp, attr: a, node: n}
		case off > a.Start && off < a.End:
			return region{} // on "=", a quote or a brace
		}
	}
	return region{kind: regionAttr, comp: comp, node: n}
}

// target returns the index entry of the component a tag calls: the file
// itself for <Self>, nil for an unknown alias.
func (ws *workspace) target(f *project.File, c *compiler.Component) *compiler.ComponentInfo {
	if c.Self {
		return ws.proj.Index[f.Path]
	}
	for _, im := range f.Analysis.OrbitImports {
		if im.Alias == c.Name {
			return ws.proj.Index[im.Resolved]
		}
	}
	return nil
}

// location is an LSP Location.
type location struct {
	URI   string `json:"uri"`
	Range Range  `json:"range"`
}

// nativeDefinition answers definition for template regions: a tag goes to
// the component's file, an attribute to the Props field, an .orbit import
// to the imported file. ok is false when the region isn't native.
func (ws *workspace) nativeDefinition(f *project.File, reg region) (loc *location, ok bool) {
	switch reg.kind {
	case regionImport:
		if ws.byPath[reg.imp.Resolved] == nil {
			return nil, true
		}
		return &location{URI: ws.orbitURI(reg.imp.Resolved)}, true
	case regionTag:
		info := ws.target(f, reg.comp)
		if info == nil {
			return nil, true
		}
		return &location{URI: ws.orbitURI(info.Path)}, true
	case regionProp:
		info := ws.target(f, reg.comp)
		if info == nil {
			return nil, true
		}
		name, err := compiler.MatchProp(&info.Props, reg.attr.Name)
		if err != nil {
			return nil, true
		}
		for _, pf := range info.Props.Fields {
			if pf.Name == name {
				lines := ws.lines[info.Path]
				if lines == nil {
					return nil, true
				}
				return &location{URI: ws.orbitURI(info.Path), Range: lines.rangeOf(pf.NameSpan.Start, pf.NameSpan.End, encUTF16)}, true
			}
		}
		return nil, false // an embedded or non-listable field: ask gopls
	}
	return nil, false
}

// nativeHover answers hover on a component tag name: the component's file
// and its props, with their types and docs.
func (ws *workspace) nativeHover(f *project.File, reg region, lines *lineIndex) any {
	info := ws.target(f, reg.comp)
	if info == nil {
		return nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "```orbit\n<%s>\n```\n\nComponent `%s`", reg.comp.Name, info.Path)
	b.WriteString(propsAndSlots(info))
	return map[string]any{
		"contents": map[string]any{"kind": "markdown", "value": b.String()},
		"range":    lines.rangeOf(reg.comp.NameSpan.Start, reg.comp.NameSpan.End, encUTF16),
	}
}
