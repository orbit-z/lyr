package compiler

import "strings"

// compressHTML at codegen time (DESIGN.md, settled gap 2).
//
// "html": every whitespace run in template text becomes one space, or one
// newline if it has one. A whitespace-only text is dropped when everything
// that can render on both sides of it, looking through {if}, {switch} and
// {for} (each branch or case, zero or more iterations), is a block-level
// boundary: a
// block element's tag, a doctype, a script or style, or the template's
// start or end. Anything else (text, inline elements, expressions,
// components, slots, HTML comments, a slot's own boundaries) counts as
// inline, so words never join.
//
// "jsx": each text node gets JSX's rule: lines are trimmed except at the
// text's own start and end, whitespace-only lines are dropped, and the
// remaining lines are joined with one space. So whitespace with a newline
// next to a tag or expression disappears, whitespace within a line stays.
//
// In both modes the content of <pre>, <textarea>, <script> and <style>,
// HTML comments, attribute values and {expr} output are left alone.

type wsKind uint8

const (
	wsBlock  wsKind = iota // a block element or the template: block on both ends
	wsInline               // an inline element, a slot closure, a fallback, a fragment
	wsLoop                 // a {for} body: the ends wrap to the next iteration and the outside
	wsBranch               // an {if} branch or {switch} case: the ends are the outside of the block
)

// wsCtx is a sibling list being emitted, with what surrounds it.
type wsCtx struct {
	parent   *wsCtx
	at       int // index in parent.list of the node owning this list
	kind     wsKind
	verbatim bool // inside <pre> or <textarea>
	list     []Node
}

// sub is the context of a nested list with its own boundaries (an
// element's children, a slot closure, a fallback): kind, and verbatim
// inherited.
func (c *wsCtx) sub(kind wsKind) *wsCtx {
	return &wsCtx{kind: kind, verbatim: c.verbatim}
}

func (c *wsCtx) child(at int, kind wsKind) *wsCtx {
	return &wsCtx{parent: c, at: at, kind: kind, verbatim: c.verbatim}
}

// blockElements are the elements whose tags are block-level boundaries for
// whitespace: HTML's block and table elements and the head-only ones.
var blockElements = map[string]bool{}

func init() {
	for _, n := range strings.Fields(`html head body title meta link base style script noscript template
		address article aside blockquote details dialog dd div dl dt fieldset figcaption figure footer form
		h1 h2 h3 h4 h5 h6 header hgroup hr li main nav ol p pre section summary menu ul br
		table caption colgroup col thead tbody tfoot tr td th legend optgroup option`) {
		blockElements[n] = true
	}
}

func isHTMLSpace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r' || b == '\f'
}

func isBlankText(s string) bool {
	for i := 0; i < len(s); i++ {
		if !isHTMLSpace(s[i]) {
			return false
		}
	}
	return true
}

// wsPrep drops template comments and merges the texts around them, so
// "</div>\n{/* c */}\n<div>" is one whitespace run ("html" mode only).
func wsPrep(list []Node) []Node {
	needs := false
	for i, n := range list {
		if _, ok := n.(*Comment); ok {
			needs = true
			break
		}
		if _, ok := n.(*Text); ok && i > 0 {
			if _, ok := list[i-1].(*Text); ok {
				needs = true
				break
			}
		}
	}
	if !needs {
		return list
	}
	out := make([]Node, 0, len(list))
	for _, n := range list {
		switch n := n.(type) {
		case *Comment:
			continue
		case *Text:
			if k := len(out) - 1; k >= 0 {
				if prev, ok := out[k].(*Text); ok {
					out[k] = &Text{Span: Span{prev.Start, n.End}, Value: prev.Value + n.Value}
					continue
				}
			}
		}
		out = append(out, n)
	}
	return out
}

// text returns the output of a text node under the compressHTML mode.
func (g *cgGen) text(n *Text, c *wsCtx, i int) string {
	if c.verbatim {
		return n.Value
	}
	switch g.in.Options.CompressHTML {
	case CompressHTML:
		if isBlankText(n.Value) && c.wsDrop(i) {
			return ""
		}
		return collapseWS(n.Value)
	case CompressJSX:
		return jsxText(n.Value)
	}
	return n.Value
}

// collapseWS turns each whitespace run into one space, or one newline if
// the run has one.
func collapseWS(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if !isHTMLSpace(s[i]) {
			b.WriteByte(s[i])
			i++
			continue
		}
		nl := false
		for ; i < len(s) && isHTMLSpace(s[i]); i++ {
			nl = nl || s[i] == '\n'
		}
		if nl {
			b.WriteByte('\n')
		} else {
			b.WriteByte(' ')
		}
	}
	return b.String()
}

// jsxText applies JSX's whitespace rule to one text node (Babel's
// cleanJSXElementLiteralChild).
func jsxText(s string) string {
	lines := strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	lastNonEmpty := 0
	for i, l := range lines {
		if strings.Trim(l, " \t") != "" {
			lastNonEmpty = i
		}
	}
	var b strings.Builder
	for i, l := range lines {
		if i > 0 {
			l = strings.TrimLeft(l, " \t")
		}
		if i < len(lines)-1 {
			l = strings.TrimRight(l, " \t")
		}
		if l == "" {
			continue
		}
		b.WriteString(l)
		if i != lastNonEmpty {
			b.WriteByte(' ')
		}
	}
	return b.String()
}
