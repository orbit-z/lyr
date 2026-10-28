package compiler

// Component props of type orbit.Opt[T] (PLAN.md §2.6 (Q3)): a static value,
// a bare attribute or a T expression is wrapped (Some), an Opt[T]
// expression is passed as is. Pass 1 sees the field's type as written in
// the target's Props; an expression that isn't a basic literal is a
// SiteOptProp, decided by the typed pass.

import (
	"go/ast"
	goparser "go/parser"
	"strconv"
	"strings"
)

// isOptProp reports whether the field key of props has a type written as
// orbit.Opt[…].
func isOptProp(props *PropsInfo, key string) bool {
	for i := range props.Fields {
		if props.Fields[i].Name == key {
			return IsOptType(props.Fields[i].Type)
		}
	}
	return false
}

// isBasicLit reports whether code is a Go basic literal ("x", 3, 1.5, 'r'),
// which pass 1 wraps directly: an untyped constant then takes T.
func isBasicLit(code string) bool {
	if strings.TrimSpace(code) == "" {
		return false
	}
	e, err := goparser.ParseExpr(tlPlaceholderText(code))
	if err != nil {
		return false
	}
	_, ok := ast.Unparen(e).(*ast.BasicLit)
	return ok
}

// optProp emits the value of a prop of type orbit.Opt[T] (field f), after
// "Key: ".
func (g *cgGen) optProp(f cgField) {
	o := g.o
	a := f.attr
	wrap := NameRuntime + ".Wrap(" + f.opt + ", "
	switch {
	case a.IsExpr() && isBasicLit(a.Code.Text):
		o.str(NameRuntime + ".Wrap(" + f.opt + ",\n")
		o.exact(a.Code.Span)
		o.str(",\n")
		o.ind()
		o.str(")")
	case a.IsExpr():
		g.optPropSite(f)
	case a.IsStatic():
		sp := a.ValueSpan
		if a.Kind == AttrQuoted {
			sp = Span{sp.Start - 1, min(sp.End+1, len(g.src))}
		}
		o.str(wrap)
		o.inline(sp, strconv.Quote(a.Value), SegSpan)
		o.str(")")
	default:
		o.str(wrap)
		o.str("/*line " + o.dirFile + ":" + o.pos(a.NameSpan.Start) + "*/true")
		o.reset()
		o.str(")")
	}
}

// optPropSite emits the SiteOptProp of an expression passed to a prop of
// type orbit.Opt[T]: OptOf in pass 1, then as the typed pass decided.
func (g *cgGen) optPropSite(f cgField) {
	o := g.o
	a := f.attr
	n, _ := f.node.(*Component)
	cs := CallSite{Kind: SiteOptProp, Key: a.Code.Start, Node: n, Attr: a, Code: a.Code, AttrName: a.Name}
	tw := g.in.Typed[a.Code.Start]
	start := len(o.b)
	switch tw.Kind {
	case WriteOptAsIs:
		cs.Arg = o.exact(a.Code.Span)
		cs.Call = cs.Arg
		g.sites = append(g.sites, cs)
		return
	case WriteOptWrapIf:
		iw := tw.If
		o.str(NameRuntime + ".WrapIf(" + f.opt + ", ")
		o.exact(iw.Fun)
		o.str("[bool],\n")
		o.exact(iw.Cond)
		o.str(",\n")
		cs.Arg = o.exact(iw.Val)
	default:
		method := "OptOf"
		if tw.Kind == WriteOptWrap {
			method = "Wrap"
		}
		o.str(NameRuntime + "." + method + "(" + f.opt + ",\n")
		cs.Arg = o.exact(a.Code.Span)
	}
	o.str(",\n")
	o.ind()
	o.str(")")
	cs.Call = Span{start, len(o.b)}
	g.sites = append(g.sites, cs)
}
