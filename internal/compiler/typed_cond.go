package compiler

// The lyr.Cond lowering of the typed pass (PLAN.md §2.6): a call of the
// runtime's Cond that is the whole value of an HTML element attribute, of
// a content {expr} or of a template literal's piece becomes an if
// statement around the typed write of each branch, a constant branch
// folded into the static HTML at build time, with the same bytes as the
// write of the call's result.

import (
	"go/ast"
	"go/types"
)

// lowerCond returns the lowered write of site when expr (its expression
// in the pass-1 file) is a call of the runtime's Cond, identified by its
// types.Object, that Lyr lowers: not explicitly instantiated, T not an
// lyr.Opt (lowered as an Opt value instead), T with a typed write, and
// every branch that isn't folded of type T exactly (its temporary then has
// T's type). Otherwise the call keeps the typed write of its result.
func (e *tyEnv) lowerCond(sm *tySpanMap, expr ast.Expr, site *CallSite, info *types.Info) (TypedWrite, bool) {
	call, sig, inst, ok := rtCall(expr, info, "Cond")
	if !ok || inst || len(call.Args) != 3 {
		return TypedWrite{}, false
	}
	t := sig.Params().At(1).Type()
	if _, isOpt := optElem(t); isOpt {
		return TypedWrite{}, false
	}
	var arms [2]types.TypeAndValue
	for i := range arms {
		tv, ok := info.Types[call.Args[1+i]]
		if !ok {
			return TypedWrite{}, false
		}
		arms[i] = tv
	}
	tt := types.TypeAndValue{Type: t}
	var w, base TypedWrite
	switch site.Kind {
	case SiteAttr:
		w = e.classifyAttr(tt, site.URLAttr, IsTrueFalseAttr(site.AttrName))
		base = w
	case SiteContent:
		w = e.classify(tt)
		base = w
	case SitePiece:
		var bad bool
		if w, bad = e.classifyPiece(tt, site.AttrName != ""); bad {
			return TypedWrite{}, false
		}
		// The folded text is the piece's, escaped by codegen.
		base = e.classify(tt)
		if base.Kind == WriteHTML {
			base = TypedWrite{Kind: WriteString, Conv: "string"}
		}
	default:
		return TypedWrite{}, false
	}
	if w.Kind == WriteFallback || w.Kind == WriteSkip {
		return TypedWrite{}, false
	}
	cw := &CondWrite{Fun: sm.span(site, call.Fun), Cond: sm.span(site, call.Args[0])}
	for i, arm := range []*CondArm{&cw.A, &cw.B} {
		arm.Val = sm.span(site, call.Args[1+i])
		if v := arms[i].Value; v != nil {
			if site.Kind == SitePiece {
				arm.Text, arm.Folded = foldPiece(v, base)
			} else {
				arm.Text, arm.Folded = foldConst(base, v, site.AttrName)
			}
			arm.Ref = arm.Folded && namesRef(call.Args[1+i], info)
		}
		if at := arms[i].Type; !arm.Folded && (at == nil || !types.Identical(types.Unalias(at), types.Unalias(t))) {
			return TypedWrite{}, false // its temporary wouldn't have T's type
		}
	}
	w.Cond = cw
	return w, true
}

// condConst reports whether expr is a call of the runtime's Cond whose two
// branches are constants: on a style or on* attribute it is trusted like
// the constants, as lyr.If(c, "constant") is (PLAN.md §2.6), whether or
// not the call is lowered.
func condConst(expr ast.Expr, info *types.Info) bool {
	call, _, _, ok := rtCall(expr, info, "Cond")
	if !ok || len(call.Args) != 3 {
		return false
	}
	for _, a := range call.Args[1:] {
		if info.Types[a].Value == nil {
			return false
		}
	}
	return true
}
