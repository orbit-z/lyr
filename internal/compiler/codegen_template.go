package compiler

import (
	gotoken "go/token"
	"regexp"
	"slices"
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
	case *Switch:
		g.switchNode(n, c, i)
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
		g.rawElement(&n.Tag, n, n.Content)
	case *Style:
		if n.Processed {
			return
		}
		g.rawElement(&n.Tag, n, n.Content)
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

// switchNode emits a {switch} as a Go switch statement, each case's
// template as its body. What comes before the first case renders nothing
// (it is an error, or whitespace and comments).
func (g *cgGen) switchNode(n *Switch, c *wsCtx, i int) {
	o := g.o
	for _, pre := range n.Pre {
		g.discardAll(pre)
	}
	o.flush()
	o.ind()
	o.str("switch ")
	o.exact(n.Clause.Span)
	o.str(" {\n")
	seenDefault := false
	for _, cs := range n.Cases {
		switch {
		case cs.Default && seenDefault:
			// A second {default} (reported, Tolerant): its body joins the
			// clause before it.
		case cs.Default:
			seenDefault = true
			o.ln("default:")
		default:
			o.ind()
			o.str("case ")
			o.exact(cs.List.Span)
			o.str(":\n")
		}
		o.depth++
		g.nodes(cs.Children, c.child(i, wsBranch))
		o.flush()
		o.depth--
	}
	o.ln("}")
}

// site emits a fallback-capable write: {expr}, set:text or an attribute
// expression, typed when Input.Typed says so.
func (g *cgGen) site(kind SiteKind, node Node, a *Attr, code GoCode) {
	o := g.o
	if l := wholeLiteral(g.src, code); l != nil {
		// A whole-value template literal: its pieces' writes (PLAN.md
		// §2.10).
		g.litWrite(kind, node, a, l)
		return
	}
	cs := CallSite{Kind: kind, Key: code.Start, Node: node, Attr: a, Code: code}
	if kind == SiteAttr {
		cs.AttrName = a.Name
		cs.URLAttr = IsURLAttr(a.Name)
	}
	method, conv, pieces := "Expr", "", false
	if kind == SiteAttr {
		method = "Attr"
	}
	if tw, ok := g.in.Typed[code.Start]; ok && tw.If != nil {
		g.ifSite(cs, a, tw)
		return
	}
	if tw, ok := g.in.Typed[code.Start]; ok && tw.Kind != WriteFallback {
		if tw.Kind == WriteSkip {
			at := Span{len(o.b), len(o.b)}
			cs.Call, cs.Arg = at, at
			g.sites = append(g.sites, cs)
			return
		}
		method, conv = tw.Kind.Method(), tw.Conv
		pieces = tw.Kind.attrPieces()
	}
	quote := `"`
	if method == "JSONAttr" {
		quote = `'`
	}
	if pieces {
		o.html(" " + a.Name + "=" + quote)
	}
	o.flush()
	o.ind()
	start := len(o.b)
	o.str(NameW + "." + method + "(")
	if method == "JSONAttr" {
		o.str(strconv.Quote(g.position(attrValuePos(a))) + ",")
	}
	if kind == SiteAttr && !pieces {
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
	if pieces {
		o.html(quote)
	}
}

// ifSite emits a lowered orbit.If site (PLAN.md §2.6, IfWrite): both
// arguments into temporaries, in order, then the typed write of the value
// (or the folded constant) under the condition. Its call site's Call is the
// write in the if body, and Arg the value there (empty when folded).
func (g *cgGen) ifSite(cs CallSite, a *Attr, tw TypedWrite) {
	o := g.o
	iw := tw.If
	o.flush()
	if iw.Opt {
		// An orbit.Opt value: its expression stays, so nothing to keep.
		o.ln("{")
		o.depth++
		o.ind()
		o.str("orbit__o :=\n")
		o.exact(iw.Val)
		o.str("\n")
		o.ln("orbit__v, orbit__c := orbit__o.Get()")
		o.ln("if orbit__c {")
		o.depth++
		g.ifWrite(&cs, a, tw)
		o.depth--
		o.ln("}")
		o.depth--
		o.ln("}")
		g.sites = append(g.sites, cs)
		return
	}
	if !g.ifRef {
		// The lowering drops the call, which may be the file's only use
		// of the package; keep one (an instantiation, no code).
		g.ifRef = true
		o.ind()
		o.str("_ = ")
		o.exact(iw.Fun)
		if !iw.Inst {
			o.str("[bool]")
		}
		o.str("\n")
	}
	o.ln("{")
	o.depth++
	o.ind()
	if iw.Folded {
		o.str("orbit__c :=\n")
		o.exact(iw.Cond)
		o.str("\n")
	} else {
		o.str("orbit__c, orbit__v :=\n")
		o.exact(iw.Cond)
		o.str(",\n")
		o.exact(iw.Val)
		o.str("\n")
	}
	o.ln("if orbit__c {")
	o.depth++
	if iw.Folded {
		o.html(iw.Text)
		o.flush()
		at := Span{len(o.b), len(o.b)}
		cs.Call, cs.Arg = at, at
	} else {
		g.ifWrite(&cs, a, tw)
	}
	o.depth--
	o.ln("}")
	o.depth--
	o.ln("}")
	g.sites = append(g.sites, cs)
}

// ifWrite emits the write of orbit__v in a lowered If: tw's typed write, or
// Attr/Expr for a value with none.
func (g *cgGen) ifWrite(cs *CallSite, a *Attr, tw TypedWrite) {
	o := g.o
	method := "Expr"
	if cs.Kind == SiteAttr {
		method = "Attr"
	}
	pieces := false
	switch tw.Kind {
	case WriteFallback:
	case WriteSkip:
		at := Span{len(o.b), len(o.b)}
		cs.Call, cs.Arg = at, at
		return
	default:
		method, pieces = tw.Kind.Method(), tw.Kind.attrPieces()
	}
	quote := `"`
	if method == "JSONAttr" {
		quote = `'`
	}
	if pieces {
		o.html(" " + a.Name + "=" + quote)
		o.flush()
	}
	o.ind()
	start := len(o.b)
	o.str(NameW + "." + method + "(")
	if method == "JSONAttr" {
		o.str(strconv.Quote(g.position(attrValuePos(a))) + ", ")
	}
	if cs.Kind == SiteAttr && !pieces {
		o.str(strconv.Quote(a.Name) + ", ")
	}
	arg := "orbit__v"
	if tw.Conv != "" {
		arg = tw.Conv + "(" + arg + ")"
	}
	argStart := len(o.b)
	o.str(arg)
	cs.Arg = Span{argStart, len(o.b)}
	o.str(")")
	cs.Call = Span{start, len(o.b)}
	o.str("\n")
	if pieces {
		o.html(quote)
		o.flush()
	}
}

// position is the "<path>:L:C" of an offset in the .orbit file, as runtime
// errors print it (ErrorAt, JSONAttr, JSON).
func (g *cgGen) position(off int) string { return g.a.Info.Path + ":" + g.o.pos(off) }

// attrValuePos is the offset JSONAttr reports: the value's opening brace,
// or the attribute for {a}.
func attrValuePos(a *Attr) int {
	if a.ValueSpan.End > a.ValueSpan.Start {
		return a.ValueSpan.Start
	}
	return a.Start
}

// emptySite emits the empty orbit__w.Expr( ) of a {} in Tolerant mode, so
// gopls can complete in it.
func (g *cgGen) emptySite(n *Expr) {
	o := g.o
	o.flush()
	o.ind()
	start := len(o.b)
	o.str(NameW + ".Expr(\n")
	var arg Span
	if n.Code.Start == n.Code.End {
		// No //line directive before the zero-length segment: right after
		// one, gopls offers almost nothing (as for an empty ${}, piece);
		// there's no code for Go to report an error on.
		at := len(o.b)
		o.segs = append(o.segs, Segment{Kind: SegExact, OrbitStart: n.Code.Start, OrbitEnd: n.Code.End, GoStart: at, GoEnd: at})
		arg = Span{at, at}
	} else {
		arg = o.exact(n.Code.Span)
	}
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
	at, cl, merged, rest := g.elementClasses(t)
	for _, a := range t.Attrs {
		if g.skipAttr[a] {
			continue
		}
		switch {
		case at != nil && (a == cl || slices.Contains(merged, a)):
			if a == at {
				g.classList(node, at, cl, merged, rest)
			}
			continue
		case a.Kind == AttrSpread:
			if g.restOperand(a) != "" {
				g.spreadRest(a, at == nil)
			} else {
				g.spreadOf(a)
			}
			continue
		case a.Directive.IsSet():
			if set == nil {
				set = a
			} else {
				g.discardAttr(a)
			}
			continue
		case a.Directive != NotDirective && a.Directive != SlotDirective:
			g.discardAttr(a) // is:*, server:*, client:*, Astro-only: parse errors or dropped
			continue
		}
		if a.IsExpr() {
			g.site(SiteAttr, node, a, a.Code)
			if last := g.sites[len(g.sites)-1]; last.Kind == SiteAttr {
				g.jsonAttrSite(a, last.Arg) // a template literal is a string, never JSON
			}
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
		if l := wholeLiteral(g.src, set.Code); l != nil && !l.Const() {
			// Converted to orbit.HTML when every piece is inert (PLAN.md
			// §2.10 (Q4)).
			o := g.o
			o.flush()
			o.ln(NameW + ".HTML(")
			g.concat(l, NameRuntime+`.HTML("")`, false, node, set)
			o.str(",\n")
			o.ln(")")
			return
		}
		if isBasicLit(set.Code.Text) {
			g.write("HTML", set.Code)
			return
		}
		g.htmlSite(node, set)
	case SetJSON:
		g.setJSON(set, node)
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
	if n.Dynamic {
		g.dynElement(n, c, i)
		return
	}
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
		case *Switch:
			g.discardSwitch(n)
			return false
		}
		if t := TagOf(n); t != nil {
			for _, a := range t.Attrs {
				g.discardAttr(a)
			}
		}
		return true
	})
}

// discardSwitch emits the Go code of a {switch} that isn't rendered
// (Tolerant only): the switch with its case labels, and each case's
// discarded code in its body.
func (g *cgGen) discardSwitch(n *Switch) {
	o := g.o
	for _, pre := range n.Pre {
		g.discardAll(pre)
	}
	o.flush()
	o.ind()
	o.str("switch ")
	o.exact(n.Clause.Span)
	o.str(" {\n")
	seenDefault := false
	for _, cs := range n.Cases {
		switch {
		case cs.Default && seenDefault:
		case cs.Default:
			seenDefault = true
			o.ln("default:")
		default:
			o.ind()
			o.str("case ")
			o.exact(cs.List.Span)
			o.str(":\n")
		}
		o.depth++
		for _, k := range cs.Children {
			g.discardAll(k)
		}
		o.depth--
	}
	o.ln("}")
}

// rawElement emits a script or style that isn't processed: copied as
// written, minus its directives, or with the content of its set:html,
// set:text or set:json (as in Astro).
func (g *cgGen) rawElement(t *Tag, node Node, content RawText) {
	o := g.o
	o.html("<" + t.Name)
	set := g.htmlAttrs(t, node)
	o.html(">")
	if set != nil {
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
			g.discardAttr(a) // the parser rejects other attributes
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
	// merged are the class attributes merged into a class:list attr, and
	// node is its component.
	merged []*Attr
	node   Node
	// rest is the value of the target's RestAttrs field (attr is nil).
	rest *cgRest
	// opt is set for a field of type orbit.Opt[T] (PLAN.md §2.6 (Q3)):
	// the field's zero value, "Alias.Props{}.Field", which the wrapping
	// helpers take first.
	opt string
	// spread is a props spread of a block call site (PLAN.md §2.11), with
	// its typed plan and its tag (attr is nil).
	spread *Attr
	sw     *SpreadWrite
	tag    *SpreadTag
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
	// The unknown-prop error's hint, for a target that can take an
	// orbit.RestAttrs field (not a Markdown import).
	hint := "; add an orbit.RestAttrs field to pass HTML attributes through"
	if n.Self {
		props = &g.a.Props
	} else {
		imp := g.imports[n.Name]
		if imp == nil {
			g.errorf(n.NameSpan, "unknown component <%s>: no .orbit import with this alias (or declare a variable %s for a dynamic tag)", n.Name, n.Name)
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
		if strings.HasSuffix(imp.Resolved, ".md") {
			hint = ""
		}
	}
	id := ComponentID(g.a.Info.Path)
	if !n.Self {
		id = g.in.Index[g.imports[n.Name].Resolved].ID
	}
	var fields []cgField
	var pre []GoCode // Go code of broken attributes, for Tolerant mode
	cl, merged := classAttrs(&n.Tag)
	// A target with an orbit.RestAttrs field collects the attributes that
	// match no other field (PLAN.md §2.5); its class too when it has no
	// class prop.
	restField := props.Rest
	classRest := restField != "" && !hasProp(props, "class")
	tagName := n.Name
	if n.Self {
		tagName = "Self"
	}
	// Spreads on the tag (PLAN.md §2.11): block is set when the typed pass
	// decided each of them and one sets props, which are then built field
	// by field; checks are pass 1's spreads on a callee without a
	// RestAttrs field.
	info := &SpreadTag{Tag: tagName, Rest: restField}
	block := g.blockCall(n)
	var checks []*Attr
	rest := cgRest{qual: qual, tag: info}
	var explicitRest *Attr
	passed := map[string]bool{} // collected names, lowercased
	collect := func(a *Attr) bool {
		key := strings.ToLower(a.Name)
		if passed[key] {
			g.errorf(a.NameSpan, "attribute %s is passed twice to <%s>", a.Name, n.Name)
			pre = append(pre, a.Code)
			return false
		}
		passed[key] = true
		return true
	}
	for _, a := range n.Attrs {
		if a.Kind == AttrSpread {
			if strings.TrimSpace(a.Code.Text) == "" {
				continue // {...} is reported by the parser
			}
			if !n.Self && hint == "" { // a Markdown import takes no props
				g.errorf(a.Span, "unknown prop %s on <%s> (%s)", spreadText(a), n.Name, propList(n.Name, props))
				pre = append(pre, a.Code)
				continue
			}
			info.Items = append(info.Items, SpreadItem{Attr: a})
			if tw := g.in.Typed[a.Code.Start]; block && tw.Kind == WriteSpreadProps {
				fields = append(fields, cgField{spread: a, sw: tw.Spread, tag: info, node: n})
				continue
			}
			if restField != "" {
				rest.spreads = append(rest.spreads, a)
				continue
			}
			checks = append(checks, a)
			continue
		}
		if cl != nil && (a == cl || slices.Contains(merged, a)) {
			if a == cl {
				if classRest {
					passed["class"] = true
					rest.cl, rest.classes = cl, merged
				} else {
					k := len(fields)
					fields, pre = g.classField(n, props, cl, merged, fields, pre)
					if len(fields) > k {
						info.Items = append(info.Items, SpreadItem{Field: fields[k].key, Attr: cl})
					}
				}
			}
			continue
		}
		if a.Directive != NotDirective {
			pre = append(pre, a.Code)
			continue
		}
		if classRest && a.Name == "class" {
			if collect(a) {
				rest.classes = append(rest.classes, a)
			}
			continue
		}
		key, err := MatchProp(props, a.Name)
		if err != nil && restField != "" {
			if collect(a) {
				rest.attrs = append(rest.attrs, a)
			}
			continue
		}
		if err != nil {
			g.errorf(a.NameSpan, "unknown prop %q on <%s> (%s%s)", a.Name, n.Name, propList(n.Name, props), hint)
			pre = append(pre, a.Code)
			continue
		}
		if !gotoken.IsIdentifier(key) { // a non-listable Props: FieldName("1x")
			g.errorf(a.NameSpan, "unknown prop %q on <%s>: not a Go field name", a.Name, n.Name)
			pre = append(pre, a.Code)
			continue
		}
		if key == restField {
			explicitRest = a
		}
		f := cgField{key: key, attr: a, node: n}
		if a.IsExpr() && wholeLiteral(g.src, a.Code) != nil {
			if t := propType(props, key); visiblyNotString(t) {
				g.errorf(a.Span, "a template literal is a string; prop %s of <%s> is %s", a.Name, n.Name, t)
			}
		}
		if isOptProp(props, key) {
			f.opt = qual + "Props{}." + key
		}
		fields = append(fields, f)
		info.Items = append(info.Items, SpreadItem{Field: key, Attr: a})
	}
	// A props spread that sets the RestAttrs field (forwarding a whole
	// Props) is merged with the tag's collected attributes, which win.
	restSet := block && restField != "" && spreadsSet(fields, restField)
	switch {
	case restField == "":
	case explicitRest != nil && rest.any():
		g.errorf(n.NameSpan, "<%s> gets %s={…} and pass-through attributes; use {...x} to merge them", n.Name, explicitRest.Name)
		pre = append(pre, rest.codes()...)
	case explicitRest == nil && (rest.any() || g.scope != ""):
		fields = append(fields, cgField{key: restField, rest: &rest, node: n})
	}
	slots := g.groupSlots(n.Children)
	call := cgCall{n: n, qual: qual, tag: tagName, fields: fields, pre: pre, checks: checks, info: info, block: block, restSet: restSet}

	if n.Defer != nil && n.Client == nil {
		g.deferCall(&call, id, slots, c)
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
	g.spreadChecks(&call)
	if block {
		o.ln("{")
		o.depth++
		g.blockProps(&call, nil)
	}
	defer func() {
		if block {
			o.depth--
			o.ln("}")
		}
	}()
	o.ind()
	conc := n.Concurrent != nil
	if conc && g.in.Options.Locals != "" {
		// Resolve the request's locals before the goroutine starts, so
		// they're shared (PLAN.md §3.6).
		o.str(NameRuntime + ".LocalsOf[" + NameLocals + ".Locals](" + NameCtx + "); ")
	}
	o.str("if " + NameErr + " := ")
	if conc {
		o.inline(n.NameSpan, NameRuntime+".Concurrent", SegSpan)
		o.str("(" + NameCtx + ", " + NameW + ", ")
	}
	switch {
	case !g.directCall(n):
		o.inline(n.NameSpan, qual+"Render", SegSpan)
	case n.Self:
		o.inline(n.NameSpan, NameRenderImpl, SegSpan)
	default:
		g.directUsed[n.Name] = true
		o.inline(n.NameSpan, NameDirect(n.Name)+"."+NameRenderImpl, SegSpan)
	}
	if conc {
		o.str(", ")
	} else {
		o.str("(" + NameCtx + ", " + NameW + ", ")
	}
	if block {
		o.str(NameP)
	} else {
		o.str(qual + "Props{")
		g.propFields(fields)
		o.str("}")
	}
	o.str(", " + NameRuntime + ".Slots{")
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
		o.ind()
		if f.rest != nil {
			o.str(f.key + ": ")
			g.restValue(f.node.(*Component), f.rest)
			o.str(",\n")
			continue
		}
		o.inline(f.attr.NameSpan, f.key, SegSpan)
		o.str(": ")
		g.fieldValue(f)
		o.str(",\n")
	}
	o.depth--
	o.ind()
}

// fieldValue emits the value of the prop field f set by an attribute.
func (g *cgGen) fieldValue(f cgField) {
	o := g.o
	a := f.attr
	if f.opt != "" && a.Directive != ClassList {
		g.optProp(f)
		return
	}
	switch {
	case a.Directive == ClassList:
		g.classProp(f.node, a, f.merged)
	case a.IsExpr():
		if l := wholeLiteral(g.src, a.Code); l != nil {
			g.propLiteral(f.node.(*Component), f.key, a, l)
		} else {
			o.exact(a.Code.Span)
		}
	case a.IsStatic():
		// A directive, so a type error (featured="yes" for a bool)
		// lands on the value; a quoted value's position is its quote.
		sp := a.ValueSpan
		if a.Kind == AttrQuoted {
			// An unclosed quote (tolerant mode) ends at the end of
			// the file: there's no closing quote to include.
			sp = Span{sp.Start - 1, min(sp.End+1, len(g.src))}
		}
		o.inline(sp, strconv.Quote(a.Value), SegSpan)
	default:
		// A bare attribute's true: a type error lands on its name.
		o.str("/*line " + o.dirFile + ":" + o.pos(a.NameSpan.Start) + "*/true")
		o.reset()
	}
}

// deferCall emits a server:defer call site (PLAN.md §3.4): orbit__rt.Defer
// with the component ID, the Props literal and the fallback renderer
// (compiled here, in the parent, with its scope attribute) or nil.
func (g *cgGen) deferCall(call *cgCall, id string, slots []cgSlot, c *wsCtx) {
	o := g.o
	n := call.n
	o.flush()
	for _, code := range call.pre {
		g.discard(code)
	}
	g.spreadChecks(call)
	var lit Span
	if call.block {
		o.ln("{")
		o.depth++
		g.blockProps(call, &lit)
	}
	o.ind()
	o.str("if " + NameErr + " := " + NameRuntime + ".Defer(" + NameCtx + ", " + NameW + ", " + strconv.Quote(id) + ", ")
	if call.block {
		o.str(NameP)
	} else {
		lit = o.inline(n.NameSpan, call.qual+"Props", SegSpan)
		o.str("{")
		g.propFields(call.fields)
		o.str("}")
	}
	o.str(", ")
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
	if call.block {
		o.depth--
		o.ln("}")
	}
	g.defers = append(g.defers, DeferSite{Attr: n.Defer.Span, Tag: call.tag, Lit: lit})
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
	o.str("func(" + NameW + " *" + NameRuntime + ".Writer) error {" + g.ctxPrologue(nodes) + "\n")
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
		g.discardAttr(a)
	}
	g.nodes(n.Children, c.sub(wsInline))
}

// ctxPrologue is the start of a slot closure whose content uses the Ctx:
// slot content can run in a server:concurrent component's goroutine, so it
// takes that goroutine's Ctx from its Writer (PLAN.md §3.6). It rebinds
// orbit__ctx when the content calls a component (or uses ctx), and ctx
// when the content references the implicit ctx. It's on the closure's
// first line, so no line moves.
func (g *cgGen) ctxPrologue(nodes []Node) string {
	comp, direct, any := false, false, false
	for _, n := range nodes {
		Walk(n, func(m Node) bool {
			if _, ok := m.(*Component); ok {
				comp = true
			}
			if g.refsCtx(m) {
				any = true
			}
			return true
		})
		Walk(n, func(m Node) bool {
			if g.refsCtx(m) {
				direct = true
			}
			_, ok := m.(*Component)
			return !ok // its children are slot closures of their own
		})
	}
	if !comp && !any {
		return ""
	}
	s := " " + NameCtx + " := " + NameRuntime + ".CtxOf(" + NameW + ", " + NameCtx + ")"
	switch {
	case any:
		s += "; ctx := " + NameCtx
		if !direct || g.tolerant {
			s += "; _ = ctx"
		}
	case g.tolerant:
		s += "; _ = " + NameCtx
	}
	return s
}

// refsCtx reports whether n's own Go code (an expression, a control header,
// attribute values) references the implicit ctx.
func (g *cgGen) refsCtx(n Node) bool {
	for _, c := range cgTemplateCodes(n) {
		if g.a.CtxUnknown {
			if ctxWord.MatchString(c.Text) {
				return true
			}
			continue
		}
		for _, off := range g.a.CtxRefs {
			if c.Start <= off && off < c.End {
				return true
			}
		}
	}
	return false
}

var ctxWord = regexp.MustCompile(`\bctx\b`)
