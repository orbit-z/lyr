package compiler

// The typed pass of a SiteOptProp (PLAN.md §2.6 (Q3)): the value of a
// component prop of type orbit.Opt[T], emitted in pass 1 as
// orbit__rt.OptOf(Alias.Props{}.Field, x), is passed as is when it's an
// Opt[T], wrapped when it's a T (or a constant T can hold), rewritten to
// WrapIf for an orbit.If whose value is an untyped constant, and an error
// otherwise.

import (
	"go/ast"
	"go/constant"
	"go/types"
)

// optProp classifies the OptOf call of site. The diagnostic is the error
// for a value of another type.
func (e *tyEnv) optProp(sm *tySpanMap, call *ast.CallExpr, site *CallSite, info *types.Info) (TypedWrite, *Diagnostic) {
	if len(call.Args) != 2 {
		return TypedWrite{}, nil
	}
	ftv, ok := info.Types[call.Args[0]]
	if !ok {
		return TypedWrite{}, nil
	}
	field := ftv.Type
	elem, isOpt := optElem(field)
	if !isOpt {
		return TypedWrite{}, nil // an alias pass 1 couldn't see; Go's rule
	}
	arg := call.Args[1]
	tv, ok := info.Types[arg]
	if !ok {
		return TypedWrite{}, nil
	}
	span := func(n ast.Node) Span {
		return sm.span(site, n)
	}
	if !isUntyped(tv.Type) && types.Identical(types.Unalias(tv.Type), types.Unalias(field)) {
		return TypedWrite{Kind: WriteOptAsIs}, nil
	}
	if ic, _, inst, ok := ifCall(arg, info); ok && !inst {
		if vtv := info.Types[ic.Args[1]]; vtv.Value != nil && constFits(vtv.Value, elem) {
			iw := &IfWrite{Fun: span(ic.Fun), Cond: span(ic.Args[0]), Val: span(ic.Args[1]), Const: true}
			return TypedWrite{Kind: WriteOptWrapIf, If: iw}, nil
		}
	}
	if tv.Value != nil && constFits(tv.Value, elem) || !isUntyped(tv.Type) && types.AssignableTo(tv.Type, elem) {
		return TypedWrite{Kind: WriteOptWrap}, nil
	}
	q := func(p *types.Package) string { return p.Name() }
	got := "untyped nil"
	if t, nilv, ok := tyStatic(tv); ok && !nilv {
		got = types.TypeString(t, q)
	}
	tag := "Self"
	if n, ok := site.Node.(*Component); ok && !n.Self {
		tag = n.Name
	}
	return TypedWrite{}, &Diagnostic{SeverityError, site.Code.Span,
		"prop " + site.AttrName + " of <" + tag + "> is " + types.TypeString(field, q) + "; got " + got}
}

// constFits reports whether the constant v can be a T: its kind matches
// T's underlying basic type (Go reports an overflow at the build).
func constFits(v constant.Value, t types.Type) bool {
	b, ok := t.Underlying().(*types.Basic)
	if !ok {
		return false
	}
	info := b.Info()
	switch v.Kind() {
	case constant.String:
		return info&types.IsString != 0
	case constant.Bool:
		return info&types.IsBoolean != 0
	case constant.Int:
		return info&(types.IsInteger|types.IsFloat) != 0
	case constant.Float:
		return info&types.IsFloat != 0
	}
	return false
}
