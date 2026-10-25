package compiler

import (
	gotoken "go/token"
	"strconv"
	"strings"
)

// nodes emits a list of sibling nodes. c is their whitespace context
// (compressHTML); its list is set here.
func (g *cgGen) nodes(list []Node, c *wsCtx) {
	mode := g.in.Options.CompressHTML
	if mode == CompressHTML && !c.verbatim {
		list = wsPrep(list)
	}
	c.list = list
	for i, n := range list {
		g.node(n, c, i)
	}
}

func (g *cgGen) node(n Node, c *wsCtx, i int) {
	o := g.o
	switch n := n.(type) {
	case *Text:
		o.html(g.text(n, c, i))
	case *Expr:
		if n.Code.Text == "" {
			if g.tolerant {
				g.emptySite(n)
			}
			return
		}
		g.site(SiteContent, n, nil, n.Code)
	case *Comment:
	case *Doctype:
		o.html(g.src[n.Start:n.End])
	case *HTMLComment:
		o.html(g.src[n.Start:n.End])
	case *If:
		g.ifNode(n, c, i)
	case *For:
		o.flush()
		o.ind()
		o.str("for ")
		o.exact(n.Clause.Span)
		o.str(" {\n")
		o.depth++
		g.nodes(n.Children, c.child(i, wsLoop))
		o.flush()
		o.depth--
		o.ln("}")
	case *Element:
		g.element(n, c, i)
	case *Fragment:
		g.fragment(n, c, i)
	case *Component:
		g.component(n, c, i)
	case *Slot:
		g.outlet(n, c, i)
	case *Script:
		if n.Processed {
			return // bundled in milestone 6
		}
		g.rawElement(&n.Tag, n, n.Content, n.JSON)
	case *Style:
		if n.Processed {
			return
		}
		g.rawElement(&n.Tag, n, n.Content, false)
	}
}

func (g *cgGen) ifNode(n *If, c *wsCtx, i int) {
	o := g.o
	o.flush()
	for bi, br := range n.Branches {
		// A chain starts at the first branch, and again after a recovered
		// {else} that isn't last (Tolerant).
		start := bi == 0
		if bi > 0 && n.Branches[bi-1].Else {
			o.ln("}")
			start = true
		}
		o.ind()
		switch {
		case start && br.Else:
			o.str("{\n") // recovered {else} without {if}
		case start:
			o.str("if ")
			o.exact(br.Cond.Span)
			o.str(" {\n")
		case br.Else:
			o.str("} else {\n")
		default:
			o.str("} else if ")
			o.exact(br.Cond.Span)
			o.str(" {\n")
		}
		o.depth++
		g.nodes(br.Children, c.child(i, wsBranch))
		o.flush()
		o.depth--
	}
	o.ln("}")
}

// site emits a fallback-capable write: {expr}, set:text or an attribute
// expression, typed when Input.Typed says so.
func (g *cgGen) site(kind SiteKind, node Node, a *Attr, code GoCode) {
	o := g.o
	cs := CallSite{Kind: kind, Key: code.Start, Node: node, Attr: a, Code: code}
	if kind == SiteAttr {
		cs.AttrName = a.Name
		cs.URLAttr = IsURLAttr(a.Name)
	}
	method, conv := "Expr", ""
	if kind == SiteAttr {
		method = "Attr"
	}
	if tw, ok := g.in.Typed[code.Start]; ok && tw.Kind != WriteFallback {
		if tw.Kind == WriteSkip {
			at := Span{len(o.b), len(o.b)}
			cs.Call, cs.Arg = at, at
			g.sites = append(g.sites, cs)
			return
		}
		method, conv = tw.Kind.Method(), tw.Conv
	}
	o.flush()
	o.ind()
	start := len(o.b)
	o.str(NameW + "." + method + "(")
	if kind == SiteAttr {
		o.str(strconv.Quote(a.Name) + ",")
		if conv != "" {
			o.str(" ")
		}
	}
	if conv != "" {
		o.str(conv + "(")
	}
	o.str("\n")
	cs.Arg = o.exact(code.Span)
	o.str(",\n")
	o.ind()
	o.str(")")
	if conv != "" {
		o.str(")")
	}
	cs.Call = Span{start, len(o.b)}
	o.str("\n")
	g.sites = append(g.sites, cs)
}

// emptySite emits the empty orbit__w.Expr( ) of a {} in Tolerant mode, so
// gopls can complete in it.
func (g *cgGen) emptySite(n *Expr) {
	o := g.o
	o.flush()
	o.ind()
	start := len(o.b)
	o.str(NameW + ".Expr(\n")
	arg := o.exact(n.Code.Span)
	o.str("\n")
	o.ind()
	o.str(")")
	g.sites = append(g.sites, CallSite{Kind: SiteContent, Key: n.Code.Start, Node: n, Code: n.Code, Call: Span{start, len(o.b)}, Arg: arg})
	o.str("\n")
}

// write emits orbit__w.<method>(<code>) for writes that aren't call sites
// (set:html, set:json, spreads).
func (g *cgGen) write(method string, code GoCode) {
	o := g.o
	o.flush()
	o.ln(NameW + "." + method + "(")
	o.exact(code.Span)
	o.str(",\n")
	o.ln(")")
}

// discard emits "_ = (code)", so gopls sees the Go code of a broken
// construct (Tolerant mode only).
func (g *cgGen) discard(code GoCode) {
	if !g.tolerant || code.Text == "" {
		return
	}
	o := g.o
	o.flush()
	o.ind()
	o.str("_ = (")
	o.exact(code.Span)
	o.str(")\n")
}

// discardHeader emits a control header as an empty "if c {}" or
// "for c {}", since a header may hold an init statement (Tolerant only).
func (g *cgGen) discardHeader(kw string, code GoCode) {
	if !g.tolerant || code.Text == "" {
		return
	}
	o := g.o
	o.flush()
	o.ind()
	o.str(kw)
	o.exact(code.Span)
	o.str(" {\n")
	o.ln("}")
}

// htmlAttrs writes the attributes of an HTML element (or script/style) and
// returns its set: directive, if any.
func (g *cgGen) htmlAttrs(t *Tag, node Node) (set *Attr) {
	o := g.o
	for _, a := range t.Attrs {
		if g.skipAttr[a] {
			continue
		}
		switch {
		case a.Kind == AttrSpread:
			g.write("Spread", a.Code)
			continue
		case a.Directive.IsSet():
			if set == nil {
				set = a
			} else {
				g.discard(a.Code)
			}
			continue
		case a.Directive != NotDirective && a.Directive != SlotDirective:
			g.discard(a.Code) // is:*, server:*, client:*, Astro-only: parse errors or dropped
			continue
		}
		if a.IsExpr() {
			g.site(SiteAttr, node, a, a.Code)
			continue
		}
		o.html(" " + g.src[a.Start:a.End])
	}
	return set
}

// setContent emits the content of a set: directive.
func (g *cgGen) setContent(set *Attr, node Node) {
	if !set.IsExpr() || set.Code.Text == "" {
		return // reported by the parser
	}
	switch set.Directive {
	case SetHTML:
		g.write("HTML", set.Code)
	case SetJSON:
		g.write("JSON", set.Code)
	case SetText:
		g.site(SiteContent, node, set, set.Code)
	}
}

// unscopedElements never get the scope attribute: Astro's
// NeverScopedElements (document metadata and legacy frames), minus the
// ones Orbit models as other nodes (Fragment, slot, script, style).
var unscopedElements = map[string]bool{
	"base": true, "font": true, "frame": true, "frameset": true, "head": true,
	"link": true, "meta": true, "noframes": true, "noscript": true, "title": true,
}

var verbatimElements = map[string]bool{"pre": true, "textarea": true, "script": true, "style": true}

func (g *cgGen) element(n *Element, c *wsCtx, i int) {
	o := g.o
	o.html("<" + n.Name)
	set := g.htmlAttrs(&n.Tag, n)
	if g.scope != "" && !unscopedElements[strings.ToLower(n.Name)] {
		o.html(" " + g.scope) // last, after spreads, as in Astro
	}
	o.html(">")
	if n.Void {
		if set != nil {
			g.setContent(set, n)
		}
		return
	}
	if set != nil {
		g.setContent(set, n)
		for _, ch := range n.Children {
			g.discardAll(ch)
		}
	} else {
		lower := strings.ToLower(n.Name)
		inner := c.sub(wsInline)
		if blockElements[lower] {
			inner.kind = wsBlock
		}
		inner.verbatim = inner.verbatim || verbatimElements[lower]
		g.nodes(n.Children, inner)
	}
	if strings.EqualFold(n.Name, "head") {
		o.flush()
		o.ln(NameW + ".HeadEnd()")
	}
	o.html("</" + n.Name + ">")
}

// discardAll emits the Go code under a node that isn't rendered (children
// of a set: element, which the parser rejects) for Tolerant mode.
func (g *cgGen) discardAll(n Node) {
	if !g.tolerant {
		return
	}
	Walk(n, func(n Node) bool {
		switch n := n.(type) {
		case *Expr:
			g.discard(n.Code)
		case *Branch:
			g.discardHeader("if ", n.Cond)
		case *For:
			g.discardHeader("for ", n.Clause)
		}
		if t := TagOf(n); t != nil {
			for _, a := range t.Attrs {
				g.discard(a.Code)
			}
		}
		return true
	})
}

// rawElement emits a script or style that isn't processed: copied as
// written, minus its directives, with set:json content for a JSON script.
func (g *cgGen) rawElement(t *Tag, node Node, content RawText, json bool) {
	o := g.o
	o.html("<" + t.Name)
	set := g.htmlAttrs(t, node)
	o.html(">")
	if json && set != nil && set.Directive == SetJSON {
		g.setContent(set, node)
	} else {
		o.html(content.Text)
	}
	o.html("</" + t.Name + ">")
}

func (g *cgGen) fragment(n *Fragment, c *wsCtx, i int) {
	var set *Attr
	for _, a := range n.Attrs {
		switch {
		case a.Directive == SetHTML || a.Directive == SetText:
			if set == nil {
				set = a
			}
		case a.Directive == SlotDirective && a.IsStatic():
		default:
			g.discard(a.Code) // the parser rejects other attributes
		}
	}
	if set != nil {
		g.setContent(set, n)
		for _, ch := range n.Children {
			g.discardAll(ch)
		}
		return
	}
	g.nodes(n.Children, c.sub(wsInline))
}

// outlet emits a <slot> outlet with its fallback.
func (g *cgGen) outlet(n *Slot, c *wsCtx, i int) {
	o := g.o
	o.flush()
	o.ind()
	o.str("if " + NameSlot + " := ")
	switch a := n.NameAttr; {
	case a == nil || a.IsStatic() && a.Value == "default":
		o.str(NameSlots + ".Default")
	case a.IsStatic():
		o.str(NameSlots + ".Get(" + strconv.Quote(a.Value) + ")")
	case a.IsExpr() && a.Code.Text != "":
		o.str(NameSlots + ".Get(")
		o.exact(a.Code.Span)
		o.str(")")
	default:
		o.str(NameSlots + ".Get(\"\")")
	}
	o.str("; " + NameSlot + " != nil {\n")
	o.depth++
	o.ln("if " + NameErr + " := " + NameSlot + "(" + NameW + "); " + NameErr + " != nil {")
	o.ln("\treturn " + NameErr)
	o.ln("}")
	o.depth--
	if !wsBlank(n.Children) {
		o.ln("} else {")
		o.depth++
		g.nodes(n.Children, c.sub(wsInline))
		o.flush()
		o.depth--
	}
	o.ln("}")
}

// wsBlank reports whether nodes render nothing but whitespace.
func wsBlank(nodes []Node) bool {
	for _, n := range nodes {
		switch n := n.(type) {
		case *Text:
			if strings.TrimSpace(n.Value) != "" {
				return false
			}
		case *Comment:
		default:
			return false
		}
	}
	return true
}

type cgField struct {
	key  string
	attr *Attr
}

type cgSlot struct {
	name  string
	nodes []Node
}

// component emits a component call (PLAN.md §3.2).
func (g *cgGen) component(n *Component, c *wsCtx, i int) {
	o := g.o
	var props *PropsInfo
	qual := "" // "Card." or "" for <Self>
	if n.Self {
		props = &g.a.Props
	} else {
		imp := g.imports[n.Name]
		if imp == nil {
			g.errorf(n.NameSpan, "unknown component <%s>: no .orbit import with this alias", n.Name)
			g.brokenComponent(n, c, i)
			return
		}
		info := g.in.Index[imp.Resolved]
		if info == nil {
			g.brokenComponent(n, c, i) // reported at the import path
			return
		}
		props = &info.Props
		qual = n.Name + "."
	}
	id := ComponentID(g.a.Info.Path)
	if !n.Self {
		id = g.in.Index[g.imports[n.Name].Resolved].ID
	}
	var fields []cgField
	var pre []GoCode // Go code of broken attributes, for Tolerant mode
	for _, a := range n.Attrs {
		if a.Kind == AttrSpread {
			pre = append(pre, a.Code)
			continue
		}
		if a.Directive != NotDirective {
			pre = append(pre, a.Code)
			continue
		}
		key, err := MatchProp(props, a.Name)
		if err != nil {
			g.errorf(a.NameSpan, "unknown prop %q on <%s> (%s)", a.Name, n.Name, propList(n.Name, props))
			pre = append(pre, a.Code)
			continue
		}
		if !gotoken.IsIdentifier(key) { // a non-listable Props: FieldName("1x")
			g.errorf(a.NameSpan, "unknown prop %q on <%s>: not a Go field name", a.Name, n.Name)
			pre = append(pre, a.Code)
			continue
		}
		fields = append(fields, cgField{key, a})
	}
	slots := g.groupSlots(n.Children)

	if n.Defer != nil && n.Client == nil {
		g.deferCall(n, qual, id, fields, pre, slots, c)
		return
	}
	lazy := n.Client != nil && n.Client.Directive.IsLazy() && n.Defer == nil
	if lazy {
		o.html(lazyOpen(id, n.Client))
	}
	defer func() {
		if lazy {
			o.html("</orbit-lazy>")
		}
	}()
	o.flush()
	for _, code := range pre {
		g.discard(code)
	}
	o.ind()
	o.str("if " + NameErr + " := ")
	if n.Self {
		o.inline(n.NameSpan, "Render", SegSpan)
	} else {
		o.inline(n.NameSpan, qual+"Render", SegSpan)
	}
	o.str("(" + NameCtx + ", " + NameW + ", " + qual + "Props{")
	g.propFields(fields)
	o.str("}, " + NameRuntime + ".Slots{")
	if len(slots) > 0 {
		o.str("\n")
		o.depth++
		var named []cgSlot
		for _, s := range slots {
			if s.name != "default" {
				named = append(named, s)
				continue
			}
			o.ind()
			o.str("Default: ")
			g.closure(s.nodes, c)
			o.str(",\n")
		}
		if len(named) > 0 {
			o.ln("Named: []" + NameRuntime + ".NamedSlot{")
			o.depth++
			for _, s := range named {
				o.ind()
				o.str("{Name: " + strconv.Quote(s.name) + ", Render: ")
				g.closure(s.nodes, c)
				o.str("},\n")
			}
			o.depth--
			o.ln("},")
		}
		o.depth--
		o.ind()
	}
	o.str("}); " + NameErr + " != nil {\n")
	o.ln("\treturn " + NameErr)
	o.ln("}")
}

// propFields emits the fields of a Props literal, after its "{".
func (g *cgGen) propFields(fields []cgField) {
	o := g.o
	if len(fields) == 0 {
		return
	}
	o.str("\n")
	o.depth++
	for _, f := range fields {
		a := f.attr
		o.ind()
		o.inline(a.NameSpan, f.key, SegSpan)
		o.str(": ")
		switch {
		case a.IsExpr():
			o.exact(a.Code.Span)
		case a.IsStatic():
			o.str(strconv.Quote(a.Value))
		default:
			o.str("true")
		}
		o.str(",\n")
	}
	o.depth--
	o.ind()
}

// deferCall emits a server:defer call site (PLAN.md §3.4): orbit__rt.Defer
// with the component ID, the Props literal and the fallback renderer
// (compiled here, in the parent, with its scope attribute) or nil.
func (g *cgGen) deferCall(n *Component, qual, id string, fields []cgField, pre []GoCode, slots []cgSlot, c *wsCtx) {
	o := g.o
	o.flush()
	for _, code := range pre {
		g.discard(code)
	}
	o.ind()
	o.str("if " + NameErr + " := " + NameRuntime + ".Defer(" + NameCtx + ", " + NameW + ", " + strconv.Quote(id) + ", ")
	lit := o.inline(n.NameSpan, qual+"Props", SegSpan)
	o.str("{")
	g.propFields(fields)
	o.str("}, ")
	var fallback []Node
	for _, s := range slots {
		if s.name == "fallback" {
			fallback = s.nodes
		}
	}
	if fallback == nil {
		o.str("nil")
	} else {
		g.closure(fallback, c)
	}
	o.str("); " + NameErr + " != nil {\n")
	o.ln("\treturn " + NameErr)
	o.ln("}")
	tag := n.Name
	if n.Self {
		tag = "Self"
	}
	g.defers = append(g.defers, DeferSite{Attr: n.Defer.Span, Tag: tag, Lit: lit})
}

// lazyOpen is the <orbit-lazy> start tag of a client:idle|visible|media
// call site, folded into the surrounding Raw (PLAN.md §3.4).
func lazyOpen(id string, a *Attr) string {
	s := `<orbit-lazy component="` + attrEscaper.Replace(id) + `" client="` + strings.TrimPrefix(a.Name, "client:") + `"`
	if a.Kind != AttrEmpty && !a.IsExpr() && a.Value != "" {
		s += ` opts="` + attrEscaper.Replace(a.Value) + `"`
	}
	return s + ">"
}

// attrEscaper escapes a static value for a double-quoted attribute. Like
// every static attribute value, it is otherwise copied as written
// (character references included).
var attrEscaper = strings.NewReplacer(`"`, "&quot;")

// propList is the list of valid props in the unknown-prop error.
func propList(tag string, p *PropsInfo) string {
	if len(p.Fields) == 0 {
		return tag + " takes no props"
	}
	names := make([]string, len(p.Fields))
	for i, f := range p.Fields {
		names[i] = LowerCamel(f.Name)
	}
	return "props: " + strings.Join(names, ", ")
}

// groupSlots routes a component's children to slots: slot="x" children to
// x (same names concatenated, in first-appearance order), the rest to the
// default slot, which is dropped when it's only whitespace.
func (g *cgGen) groupSlots(children []Node) []cgSlot {
	var slots []cgSlot
	index := map[string]int{}
	add := func(name string, n Node) {
		k, ok := index[name]
		if !ok {
			k = len(slots)
			index[name] = k
			slots = append(slots, cgSlot{name: name})
		}
		slots[k].nodes = append(slots[k].nodes, n)
	}
	for _, ch := range children {
		name := "default"
		if t := TagOf(ch); t != nil {
			if a := t.Attr("slot"); a != nil && a.IsStatic() && a.Value != "" {
				name = a.Value
				g.skipAttr[a] = true
			}
		}
		add(name, ch)
	}
	if k, ok := index["default"]; ok && wsBlank(slots[k].nodes) {
		slots = append(slots[:k], slots[k+1:]...)
	}
	return slots
}

// closure emits a slot renderer: func(orbit__w *orbit__rt.Writer) error {…}.
func (g *cgGen) closure(nodes []Node, c *wsCtx) {
	o := g.o
	o.str("func(" + NameW + " *" + NameRuntime + ".Writer) error {\n")
	o.depth++
	g.nodes(nodes, c.sub(wsInline))
	o.flush()
	o.ln("return " + NameW + ".Err()")
	o.depth--
	o.ind()
	o.str("}")
}

// brokenComponent emits what it can of a component that can't be called:
// its attribute code as "_ = (x)" lines (Tolerant) and its children inline.
func (g *cgGen) brokenComponent(n *Component, c *wsCtx, i int) {
	for _, a := range n.Attrs {
		g.discard(a.Code)
	}
	g.nodes(n.Children, c.sub(wsInline))
}
