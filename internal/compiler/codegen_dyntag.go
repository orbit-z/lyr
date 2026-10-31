package compiler

import "strconv"

// Dynamic tags (PLAN.md §2.8): <Tag …>…</Tag> with Tag a string variable.
// The tag name is evaluated once, at the start tag, by TagOpen; the
// attributes and the ">" are written only when it opened an element (not
// for lyr.Fragment); TagClose writes the end tag of a non-void element:
//
//	lyr__t1 := lyr__w.TagOpen("src/x.lyr:3:2", "Tag",
//	Tag,
//	true)
//	if lyr__t1.Attrs() {
//		…attributes…
//		lyr__w.Raw(` data-l-5c7d1e2f>`)
//	}
//	…children…
//	lyr__w.TagClose(lyr__t1)

// dynElement emits a dynamic tag.
func (g *cgGen) dynElement(n *Element, c *wsCtx, i int) {
	o := g.o
	g.dynTags++
	v := "lyr__t" + strconv.Itoa(g.dynTags)
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
// lyr__rt.SpreadOf(lyr__w, pos, x), for a lyr.Attrs (sorted) or an
// lyr.AttrList (in order) (PLAN.md §2.9).
func (g *cgGen) spreadOf(a *Attr) {
	o := g.o
	o.flush()
	o.ln(NameRuntime + ".SpreadOf(" + NameW + ", " + strconv.Quote(g.position(a.Start)) + ",")
	o.exact(a.Code.Span)
	o.str(",\n")
	o.ln(")")
}
