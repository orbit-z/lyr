package compiler

import "strconv"

// Dynamic tags (PLAN.md §2.8): <Tag …>…</Tag> with Tag a string variable.
// The tag name is evaluated once, at the start tag, by TagOpen; the
// attributes and the ">" are written only when it opened an element (not
// for orbit.Fragment); TagClose writes the end tag of a non-void element:
//
//	orbit__t1 := orbit__w.TagOpen("src/x.orbit:3:2", "Tag",
//	Tag,
//	true)
//	if orbit__t1.Attrs() {
//		…attributes…
//		orbit__w.Raw(` data-o-5c7d1e2f>`)
//	}
//	…children…
//	orbit__w.TagClose(orbit__t1)

// dynElement emits a dynamic tag.
func (g *cgGen) dynElement(n *Element, c *wsCtx, i int) {
	o := g.o
	g.dynTags++
	v := "orbit__t" + strconv.Itoa(g.dynTags)
	var set *Attr
	for _, a := range n.Attrs {
		if (a.Directive == SetHTML || a.Directive == SetText) && set == nil {
			set = a
		}
	}
	content := set != nil || !wsBlank(n.Children)
	o.flush()
	o.ind()
	o.str(v + " := ")
	start := len(o.b)
	o.str(NameW + ".TagOpen(" + strconv.Quote(g.position(n.NameSpan.Start)) + ", " + strconv.Quote(n.Name) + ",\n")
	arg := o.exact(n.NameSpan)
	o.str(",\n")
	o.ind()
	o.str(strconv.FormatBool(content) + ")")
	g.sites = append(g.sites, CallSite{Kind: SiteTag, Key: n.NameSpan.Start, Node: n,
		Code: GoCode{Span: n.NameSpan, Text: n.Name}, Call: Span{start, len(o.b)}, Arg: arg})
	o.str("\n")
	o.ln("if " + v + ".Attrs() {")
	o.depth++
	set = g.htmlAttrs(&n.Tag, n)
	if g.scope != "" {
		o.html(" " + g.scope) // last, after spreads, as on elements
	}
	o.html(">")
	o.flush()
	o.depth--
	o.ln("}")
	if set != nil {
		g.setContent(set, n)
		for _, ch := range n.Children {
			g.discardAll(ch)
		}
	} else {
		// The name is unknown at build time: inline, never verbatim (a
		// dynamic pre gets no <pre> treatment, as in Astro).
		g.nodes(n.Children, c.sub(wsInline))
	}
	o.flush()
	if g.tolerant && n.EndTag.Len() > len("</>") {
		// The end tag's name, so that a rename of the variable edits it.
		if s := (Span{n.EndTag.Start + 2, n.EndTag.Start + 2 + len(n.Name)}); s.End <= len(g.src) && g.src[s.Start:s.End] == n.Name {
			o.ind()
			o.str("_ = ")
			o.exact(s)
			o.str("\n")
		}
	}
	o.ln(NameW + ".TagClose(" + v + ")")
}

// spreadOf emits a spread {...x} on an HTML element:
// orbit__rt.SpreadOf(orbit__w, pos, x), for an orbit.Attrs (sorted) or an
// orbit.AttrList (in order) (PLAN.md §2.9).
func (g *cgGen) spreadOf(a *Attr) {
	o := g.o
	o.flush()
	o.ln(NameRuntime + ".SpreadOf(" + NameW + ", " + strconv.Quote(g.position(a.Start)) + ",")
	o.exact(a.Code.Span)
	o.str(",\n")
	o.ln(")")
}
