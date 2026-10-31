package compiler

// Spreading props into a component (PLAN.md §2.11): <Card {...x} />. Pass 1
// can't see the operand's type, so it emits forms that type-check for any
// operand (lyr__rt.SpreadCheck before the call on a callee without an
// lyr.RestAttrs field, RestFromAny and RestMergeAny in the rest value of
// one with it), each a SiteSpread call site. The typed pass decides what
// each spread is; when one sets props, pass 2 builds the Props in a block,
// field by field and in source order:
//
//	{
//		var lyr__p Card.Props
//		lyr__s1 := x
//		lyr__p.Title = lyr__s1.Title
//		lyr__p.Class = "mt-5"
//		if lyr__err := Card.Render(lyr__ctx, lyr__w, lyr__p, …); …
//	}

import (
	"strconv"
	"strings"
)

// NameP is the Props value a block call site builds.
const NameP = "lyr__p"

// cgCall is a component call site being emitted.
type cgCall struct {
	n *Component
	// qual is "Card." ("" for <Self>) and tag the tag name ("Self").
	qual, tag string
	fields    []cgField
	pre       []GoCode
	// checks are pass 1's spreads on a callee without a RestAttrs field,
	// and info the tag's spread description.
	checks []*Attr
	info   *SpreadTag
	// block is set when the Props are built in a block; restSet when a
	// props spread sets the RestAttrs field, which the tag's collected
	// attributes are then merged into.
	block, restSet bool
}

// blockCall reports whether the typed pass decided every spread of n and
// one of them sets props.
func (g *cgGen) blockCall(n *Component) bool {
	if g.in.Typed == nil || g.tolerant {
		return false
	}
	props := false
	for _, a := range n.Attrs {
		if a.Kind != AttrSpread || strings.TrimSpace(a.Code.Text) == "" {
			continue
		}
		switch g.in.Typed[a.Code.Start].Kind {
		case WriteSpreadProps:
			props = true
		case WriteSpreadAttrs:
		default:
			return false
		}
	}
	return props
}

// spreadsSet reports whether a props spread among fields sets the field
// key.
func spreadsSet(fields []cgField, key string) bool {
	for _, f := range fields {
		if f.sw == nil {
			continue
		}
		for _, sf := range f.sw.Fields {
			if sf.Name == key {
				return true
			}
		}
	}
	return false
}

// spreadText is a spread as messages show it: {...x}.
func spreadText(a *Attr) string {
	return "{..." + strings.TrimSpace(a.Code.Text) + "}"
}

// spreadArgs are the arguments before the operand of pass 1's spread
// forms: the Writer, the callee's zero Props, the position and the
// message's subject.
func (g *cgGen) spreadArgs(qual, tag string, a *Attr) string {
	return NameW + ", " + qual + "Props{}, " + strconv.Quote(g.position(a.Span.Start)) + ", " +
		strconv.Quote(spreadText(a)+" on <"+tag+">") + ", "
}

// spreadChecks emits pass 1's lyr__rt.SpreadCheck statements.
func (g *cgGen) spreadChecks(call *cgCall) {
	o := g.o
	for _, a := range call.checks {
		o.ind()
		start := len(o.b)
		o.str(NameRuntime + ".SpreadCheck(" + g.spreadArgs(call.qual, call.tag, a))
		arg := o.exact(a.Code.Span)
		o.str(")")
		g.sites = append(g.sites, CallSite{Kind: SiteSpread, Key: a.Code.Start, Node: call.n, Attr: a, Code: a.Code,
			Call: Span{start, len(o.b)}, Arg: arg, Spread: call.info})
		o.str("\n")
	}
}

// blockProps emits the start of a block call site: var lyr__p
// Card.Props, then each field in source order. With lit, the Props type
// is mapped to the tag name (a server:defer call site's DeferSite.Lit).
func (g *cgGen) blockProps(call *cgCall, lit *Span) {
	o := g.o
	o.ind()
	o.str("var " + NameP + " ")
	if lit != nil {
		*lit = o.inline(call.n.NameSpan, call.qual+"Props", SegSpan)
	} else {
		o.str(call.qual + "Props")
	}
	o.str("\n")
	for _, f := range call.fields {
		switch {
		case f.spread != nil:
			g.spreadFields(f)
		case f.rest != nil:
			o.ind()
			o.str(NameP + "." + f.key + " = ")
			if call.restSet {
				o.str(NameRuntime + ".RestMerge(")
			}
			g.restValue(call.n, f.rest)
			if call.restSet {
				o.str(", " + NameP + "." + f.key + ")")
			}
			o.str("\n")
		default:
			o.ind()
			o.str(NameP + ".")
			o.inline(f.attr.NameSpan, f.key, SegSpan)
			o.str(" = ")
			g.fieldValue(f)
			o.str("\n")
		}
	}
}

// spreadFields emits a props spread in a block: its operand, evaluated
// once, then one assignment per field (a SegSpread segment each).
func (g *cgGen) spreadFields(f cgField) {
	o := g.o
	a, sw := f.spread, f.sw
	tmp := ""
	if sw.Ptr || !sw.Copy && len(sw.Fields) > 0 {
		g.spreadTmps++
		tmp = "lyr__s" + strconv.Itoa(g.spreadTmps)
	}
	cs := CallSite{Kind: SiteSpread, Key: a.Code.Start, Node: f.node, Attr: a, Code: a.Code, Spread: f.tag}
	o.ind()
	switch {
	case sw.Ptr:
		o.str("if " + tmp + " := ")
		cs.Arg = o.exact(a.Code.Span)
		o.str("; " + tmp + " != nil {\n")
		o.depth++
		if sw.Copy {
			o.ind()
			o.inline(a.Span, NameP+" = *"+tmp, SegSpread)
			o.str("\n")
		} else {
			g.spreadAssigns(a, tmp, sw)
		}
		o.depth--
		o.ln("}")
	case sw.Copy:
		o.str(NameP + " = ")
		cs.Arg = o.exact(a.Code.Span)
		o.str("\n")
	case len(sw.Fields) == 0:
		o.str("_ = ")
		cs.Arg = o.exact(a.Code.Span)
		o.str("\n")
	default:
		o.str(tmp + " := ")
		cs.Arg = o.exact(a.Code.Span)
		o.str("\n")
		g.spreadAssigns(a, tmp, sw)
	}
	cs.Call = cs.Arg
	g.sites = append(g.sites, cs)
}

// spreadAssigns emits lyr__p.F = tmp.F for each field of sw.
func (g *cgGen) spreadAssigns(a *Attr, tmp string, sw *SpreadWrite) {
	o := g.o
	for _, sf := range sw.Fields {
		dst, src := NameP+"."+sf.Name, tmp+"."+sf.Name
		if sf.Opt {
			src = NameRuntime + ".Wrap(" + dst + ", " + src + ")"
		}
		o.ind()
		o.inline(a.Span, dst+" = "+src, SegSpread)
		o.str("\n")
	}
}
