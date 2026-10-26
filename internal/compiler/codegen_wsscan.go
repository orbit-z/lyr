package compiler

import "strings"

// isBlockNode reports whether a node is a block-level boundary.
func isBlockNode(n Node) bool {
	switch n := n.(type) {
	case *Element:
		return blockElements[strings.ToLower(n.Name)]
	case *Doctype, *Script, *Style:
		return true
	}
	return false
}

// Neighbor classes: what can render next to a whitespace-only text. A scan
// returns the set of classes over every possible neighbor.
const (
	wsBlockN  uint8 = 1 << iota // a block boundary
	wsSpaceN                    // a kept text that starts (fwd) or ends (bwd) with whitespace
	wsInlineN                   // anything else
	wsBlankN                    // a whitespace-only text (only when the scan stops at blanks)
)

// wsDrop decides whether the whitespace-only text at c.list[i] is dropped
// ("html" mode). It is dropped when:
//   - every path to its left first meets another whitespace-only text: that
//     run is either kept, so it already separates, or dropped for a reason
//     that holds here too;
//   - one side is always kept whitespace;
//   - both sides are always block boundaries or kept whitespace.
func (c *wsCtx) wsDrop(i int) bool {
	budget := 512
	if c.scan(i-1, -1, true, &budget) == wsBlankN {
		return true
	}
	l := c.scan(i-1, -1, false, &budget)
	r := c.scan(i+1, 1, false, &budget)
	return l == wsSpaceN || r == wsSpaceN || l&wsInlineN == 0 && r&wsInlineN == 0
}

// scan collects the classes of what can render first from list[i] on, in
// direction dir (1 forward, -1 backward), continuing past the list's end
// as its context says. With stop, a whitespace-only text is a neighbor
// (wsBlankN) instead of being skipped. Running out of budget answers
// inline, which keeps the whitespace.
func (c *wsCtx) scan(i, dir int, stop bool, budget *int) uint8 {
	if *budget--; *budget < 0 {
		return wsInlineN
	}
	for ; i >= 0 && i < len(c.list); i += dir {
		switch n := c.list[i].(type) {
		case *Text:
			if isBlankText(n.Value) {
				if stop {
					return wsBlankN
				}
				continue
			}
			edge := n.Value[0]
			if dir < 0 {
				edge = n.Value[len(n.Value)-1]
			}
			if isHTMLSpace(edge) {
				return wsSpaceN
			}
			return wsInlineN
		case *Comment:
			continue
		case *For:
			// Entering a loop: the edge of its first (or last) iteration,
			// or, with zero iterations, what lies beyond it.
			inner := c.child(i, wsBranch)
			inner.list = wsPrep(n.Children)
			return inner.scan(inner.start(dir), dir, stop, budget) | c.scan(i+dir, dir, stop, budget)
		case *If:
			var m uint8
			for _, br := range n.Branches {
				inner := c.child(i, wsBranch)
				inner.list = wsPrep(br.Children)
				m |= inner.scan(inner.start(dir), dir, stop, budget)
			}
			if !n.Branches[len(n.Branches)-1].Else {
				m |= c.scan(i+dir, dir, stop, budget)
			}
			return m
		case *Switch:
			// Each case, or what lies beyond the switch when no case
			// matches (no {default}).
			var m uint8
			hasDefault := false
			for _, cs := range n.Cases {
				hasDefault = hasDefault || cs.Default
				inner := c.child(i, wsBranch)
				inner.list = wsPrep(cs.Children)
				m |= inner.scan(inner.start(dir), dir, stop, budget)
			}
			if !hasDefault {
				m |= c.scan(i+dir, dir, stop, budget)
			}
			return m
		default:
			if isBlockNode(n) {
				return wsBlockN
			}
			return wsInlineN
		}
	}
	switch c.kind {
	case wsBlock:
		return wsBlockN
	case wsBranch:
		return c.parent.scan(c.at+dir, dir, stop, budget)
	case wsLoop:
		// The next (or previous) iteration, or what lies beyond the loop.
		wrap := &wsCtx{parent: c.parent, at: c.at, kind: wsBranch, verbatim: c.verbatim, list: c.list}
		return wrap.scan(wrap.start(dir), dir, stop, budget) | c.parent.scan(c.at+dir, dir, stop, budget)
	}
	return wsInlineN
}

// start is the first index of a scan in direction dir.
func (c *wsCtx) start(dir int) int {
	if dir < 0 {
		return len(c.list) - 1
	}
	return 0
}
