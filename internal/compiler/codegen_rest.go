package compiler

// Pass-through attributes (PLAN.md §2.5): a component call site's
// attributes that match no field of a target with a lyr.RestAttrs field
// are collected into it (lyr__rt.Rest, static attributes pre-escaped at
// build time, dynamic ones as typed call sites), and {...props.Attrs} on an
// element writes them with lyr__w.SpreadRest, merging their class into
// the element's own.

import (
	"go/ast"
	goparser "go/parser"
	"strconv"
	"strings"
)

// cgRest is what a component call site passes to the target's RestAttrs
// field.
type cgRest struct {
	// attrs are the collected attributes, in source order.
	attrs []*Attr
	// cl is a class:list and classes the class attributes, kept apart.
	cl      *Attr
	classes []*Attr
	// spreads are the {...x} on the tag, merged in (attribute spreads, or
	// any spread in pass 1).
	spreads []*Attr
	// qual is the call site's "Card." ("" for <Self>) and tag its spread
	// description, for pass 1's spread forms.
	qual string
	tag  *SpreadTag
}

func (r *cgRest) any() bool {
	return len(r.attrs) > 0 || r.cl != nil || len(r.classes) > 0 || len(r.spreads) > 0
}

// codes is the Go code of r's attributes, for Tolerant mode when they
// aren't generated.
func (r *cgRest) codes() []GoCode {
	var c []GoCode
	for _, a := range r.attrs {
		c = append(c, a.Code)
	}
	for _, a := range r.classes {
		c = append(c, a.Code)
	}
	for _, a := range r.spreads {
		c = append(c, a.Code)
	}
	if r.cl != nil {
		for _, it := range r.cl.Classes {
			c = append(c, it.Code, it.Cond)
		}
	}
	return c
}

// hasProp reports whether the attribute name sets a field of props.
func hasProp(props *PropsInfo, name string) bool {
	_, err := MatchProp(props, name)
	return err == nil
}

// restValue emits the value of the target's RestAttrs field: the call
// site's own attributes as lyr__rt.Rest(class, tail, dyn...) (with the
// calling file's scope attribute), merged with each spread.
func (g *cgGen) restValue(n *Component, r *cgRest) {
	o := g.o
	own := len(r.attrs) > 0 || r.cl != nil || len(r.classes) > 0 || g.scope != ""
	merged := r.spreads
	if !own {
		merged = merged[1:]
	}
	// Each spread is a SiteSpread: RestFrom or RestMerge once the typed
	// pass found an attribute type, RestFromAny or RestMergeAny before
	// (PLAN.md §2.11). starts are the merges' call offsets, outermost (the
	// last spread's) first.
	starts := make([]int, len(merged))
	for i := range merged {
		starts[i] = len(o.b)
		if s := merged[len(merged)-1-i]; g.attrSpread(s) {
			o.str(NameRuntime + ".RestMerge(")
		} else {
			o.str(NameRuntime + ".RestMergeAny(" + g.spreadArgs(r.qual, r.tag.Tag, s))
		}
	}
	if own {
		g.restOwn(n, r)
	} else {
		s := r.spreads[0]
		start := len(o.b)
		if g.attrSpread(s) {
			o.str(NameRuntime + ".RestFrom(")
		} else {
			o.str(NameRuntime + ".RestFromAny(" + g.spreadArgs(r.qual, r.tag.Tag, s))
		}
		arg := o.exact(s.Code.Span)
		o.str(")")
		g.restSpreadSite(n, r, s, Span{start, len(o.b)}, arg)
	}
	for j, s := range merged {
		o.str(", ")
		arg := o.exact(s.Code.Span)
		o.str(")")
		g.restSpreadSite(n, r, s, Span{starts[len(merged)-1-j], len(o.b)}, arg)
	}
}

// attrSpread reports whether the typed pass found the spread a on a
// component tag to be of an attribute type.
func (g *cgGen) attrSpread(a *Attr) bool {
	return g.in.Typed[a.Code.Start].Kind == WriteSpreadAttrs
}

// restSpreadSite records the SiteSpread of a spread in a rest value.
func (g *cgGen) restSpreadSite(n *Component, r *cgRest, a *Attr, call, arg Span) {
	g.sites = append(g.sites, CallSite{Kind: SiteSpread, Key: a.Code.Start, Node: n, Attr: a, Code: a.Code, Call: call, Arg: arg, Spread: r.tag})
}

// restOwn emits lyr__rt.Rest(class, tail, dyn...) for the attributes
// written on the tag.
func (g *cgGen) restOwn(n *Component, r *cgRest) {
	o := g.o
	o.str(NameRuntime + ".Rest(")
	g.restClassArg(n, r)
	var run strings.Builder // the static attributes since the last dynamic one
	var dyn []func()
	for _, a := range r.attrs {
		switch {
		case a.IsStatic():
			run.WriteString(" " + a.Name + `="` + attrEscaper.Replace(a.Value) + `"`)
		case a.IsExpr():
			if a.Code.Text == "" {
				continue // {} or a={}: reported by the parser
			}
			pre, a := run.String(), a
			run.Reset()
			dyn = append(dyn, func() { g.restSite(n, a, pre) })
		default:
			run.WriteString(" " + a.Name)
		}
	}
	if g.scope != "" {
		run.WriteString(" " + g.scope)
	}
	o.str(", " + strconv.Quote(run.String()))
	if len(dyn) > 0 {
		o.depth++
		for _, d := range dyn {
			o.str(",\n")
			o.ind()
			d()
		}
		o.depth--
	}
	o.str(")")
}

// restSite emits one dynamic attribute: the fallback
// lyr__rt.RestAny(pre, name, x), or the typed lyr__rt.RestString(…),
// RestInt(…), … chosen by the typed pass.
func (g *cgGen) restSite(n *Component, a *Attr, pre string) {
	o := g.o
	cs := CallSite{Kind: SiteRest, Key: a.Code.Start, Node: n, Attr: a, Code: a.Code, AttrName: a.Name, URLAttr: IsURLAttr(a.Name)}
	method, conv := "RestAny", ""
	if tw, ok := g.in.Typed[a.Code.Start]; ok && (tw.Kind >= WriteRestString && tw.Kind <= WriteRestJSON || tw.Kind == WriteRestOpt || tw.Kind == WriteRestOptURL) {
		method, conv = tw.Kind.Method(), tw.Conv
	}
	start := len(o.b)
	o.str(NameRuntime + "." + method + "(" + strconv.Quote(pre) + ", " + strconv.Quote(a.Name) + ",")
	if method == "RestJSON" {
		o.str(" " + strconv.Quote(g.position(attrValuePos(a))) + ",")
	}
	if conv != "" {
		o.str(" " + conv + "(")
	}
	o.str("\n")
	cs.Arg = o.exact(a.Code.Span)
	o.str(",\n")
	o.ind()
	o.str(")")
	if conv != "" {
		o.str(")")
	}
	cs.Call = Span{start, len(o.b)}
	g.sites = append(g.sites, cs)
}

// restClassArg emits the class argument of lyr__rt.Rest: the joined
// classes of the tag's class and class:list attributes, a string constant
// when they're static, else lyr__rt.JoinClasses of the items.
func (g *cgGen) restClassArg(n *Component, r *cgRest) {
	o := g.o
	if r.cl == nil && len(r.classes) == 0 {
		o.str(`""`)
		return
	}
	at := r.cl
	if at == nil {
		at = r.classes[0]
	}
	items := classItems(r.cl, r.classes)
	static := true
	var parts []string
	for i := range items {
		it := &items[i]
		if !it.static || it.cond.Text != "" {
			static = false
			break
		}
		if s := it.plain(); s != "" {
			parts = append(parts, s)
		}
	}
	if static {
		o.str(strconv.Quote(strings.Join(parts, " ")))
		return
	}
	o.str(NameRuntime + ".JoinClasses(")
	o.depth++
	for i := range items {
		it := &items[i]
		if i > 0 {
			o.str(", ")
		}
		if it.cond.Text != "" {
			o.str(NameRuntime + ".ClassIf(\n")
			o.exact(it.cond.Span)
			o.str(",\n")
			o.ind()
		}
		if it.static {
			o.str(strconv.Quote(it.plain()))
		} else {
			g.classSite(SiteClassProp, n, at, it.code)
		}
		if it.cond.Text != "" {
			o.str(")")
		}
	}
	o.depth--
	o.str(")")
}

// restOperand returns "props.<Rest>" when the spread a on an HTML element
// spreads the file's own RestAttrs field (PLAN.md §2.5), and "" otherwise
// (a lyr.Attrs spread).
func (g *cgGen) restOperand(a *Attr) string {
	rest := g.a.Props.Rest
	if rest == "" || a.Kind != AttrSpread || !strings.Contains(a.Code.Text, rest) {
		return ""
	}
	e, err := goparser.ParseExpr(tlPlaceholderText(a.Code.Text))
	if err != nil {
		return ""
	}
	for {
		p, ok := e.(*ast.ParenExpr)
		if !ok {
			break
		}
		e = p.X
	}
	sel, ok := e.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != rest {
		return ""
	}
	if x, ok := sel.X.(*ast.Ident); !ok || x.Name != "props" {
		return ""
	}
	return "props." + rest
}

// spreadRest emits {...props.Attrs} on an HTML element:
// lyr__w.SpreadRest(&props.Attrs, withClass), withClass set when the
// element has no class of its own to merge the rest's class into.
func (g *cgGen) spreadRest(a *Attr, withClass bool) {
	o := g.o
	o.flush()
	o.ln(NameW + ".SpreadRest(&")
	o.exact(a.Code.Span)
	o.str(",\n")
	o.ln(strconv.FormatBool(withClass) + ")")
}

// elementClasses finds an HTML element's class:list and the class
// attributes merged into it, and the operands of its rest spreads. With a
// rest spread, class attributes are merged even without a class:list, so
// the rest's class joins them. at is the attribute where the class is
// written, nil when there is none.
func (g *cgGen) elementClasses(t *Tag) (at, cl *Attr, merged []*Attr, rest []string) {
	cl, merged = classAttrs(t)
	for _, a := range t.Attrs {
		if op := g.restOperand(a); op != "" {
			rest = append(rest, op)
		}
	}
	if cl == nil && rest != nil {
		for _, a := range t.Attrs {
			if a.Kind != AttrSpread && a.Directive == NotDirective && a.Name == "class" {
				merged = append(merged, a)
			}
		}
	}
	switch {
	case cl != nil:
		at = cl
	case len(merged) > 0:
		at = merged[0]
	}
	return at, cl, merged, rest
}
