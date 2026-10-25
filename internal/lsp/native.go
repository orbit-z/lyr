package lsp

import (
	"fmt"
	"strings"

	"github.com/bertrandkhe/orbit/internal/compiler"
	"github.com/bertrandkhe/orbit/internal/project"
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
)

// region is the classified cursor position.
type region struct {
	kind regionKind
	comp *compiler.Component
	attr *compiler.Attr
	imp  *compiler.OrbitImport
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
		return region{kind: regionGo}
	}
	var out region
	compiler.Walk(f.AST.Template, func(n compiler.Node) bool {
		if out.kind != regionNone || !n.Bounds().Contains(off) {
			return false
		}
		switch n := n.(type) {
		case *compiler.Expr:
			if n.Code.Contains(off) {
				out = region{kind: regionGo}
			}
		case *compiler.Branch:
			if !n.Else && n.Header.Contains(off) && n.Cond.Contains(off) {
				out = region{kind: regionGo}
			}
		case *compiler.For:
			if n.Header.Contains(off) && n.Clause.Contains(off) {
				out = region{kind: regionGo}
			}
		}
		if out.kind != regionNone {
			return false
		}
		if t := compiler.TagOf(n); t != nil && t.StartTag.Contains(off) {
			comp, _ := n.(*compiler.Component)
			if comp != nil && t.NameSpan.Contains(off) {
				out = region{kind: regionTag, comp: comp}
				return false
			}
			for _, a := range t.Attrs {
				switch {
				case (a.IsExpr() || a.Kind == compiler.AttrSpread) && a.Code.Contains(off):
					out = region{kind: regionGo, comp: comp, attr: a}
				case comp != nil && a.Directive == compiler.NotDirective && a.Kind != compiler.AttrSpread && a.NameSpan.Contains(off):
					out = region{kind: regionProp, comp: comp, attr: a}
				default:
					continue
				}
				return false
			}
		}
		return out.kind == regionNone
	})
	return out
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
	if len(info.Props.Fields) > 0 {
		b.WriteString("\n\nProps:\n")
		for _, pf := range info.Props.Fields {
			fmt.Fprintf(&b, "- `%s %s`", pf.Name, pf.Type)
			if doc := strings.TrimSpace(pf.Doc); doc != "" {
				fmt.Fprintf(&b, ": %s", strings.ReplaceAll(doc, "\n", " "))
			}
			b.WriteString("\n")
		}
	}
	if len(info.Slots) > 0 {
		fmt.Fprintf(&b, "\n\nSlots: %s", "`"+strings.Join(info.Slots, "`, `")+"`")
	}
	return map[string]any{
		"contents": map[string]any{"kind": "markdown", "value": b.String()},
		"range":    lines.rangeOf(reg.comp.NameSpan.Start, reg.comp.NameSpan.End, encUTF16),
	}
}
