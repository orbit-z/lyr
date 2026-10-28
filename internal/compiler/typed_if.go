package compiler

// The orbit.If lowering of the typed pass (PLAN.md §2.6): a call of the
// runtime's If that is the whole value of an HTML element attribute or of a
// content {expr} becomes an if statement around the typed write of its
// value, with the same bytes as Attr or Expr of the call's result.

import (
	"go/ast"
	"go/constant"
	"go/types"
	"strconv"
	"strings"
)

// mayCallIf reports whether any attribute or content site's code mentions
// If, so the type check records identifier uses only for files that may
// need them.
func mayCallIf(sites []CallSite) bool {
	for i := range sites {
		if k := sites[i].Kind; (k == SiteAttr || k == SiteContent || k == SiteOptProp) && strings.Contains(sites[i].Code.Text, "If") {
			return true
		}
	}
	return false
}

// lowerIf returns the lowered write of site when expr (its expression in
// the pass-1 file tf) is a call of the runtime's If, identified by its
// types.Object, so a user's own If or a shadowed orbit is left alone.
func (e *tyEnv) lowerIf(sm *tySpanMap, expr ast.Expr, site *CallSite, info *types.Info) (TypedWrite, bool) {
	call, sig, inst, ok := ifCall(expr, info)
	if !ok {
		return TypedWrite{}, false
	}
	vtv, ok := info.Types[call.Args[1]]
	if !ok {
		return TypedWrite{}, false
	}
	// The value's static type is If's instantiated T (an untyped constant
	// argument has its default type there), as Attr and Expr see it boxed.
	tv := types.TypeAndValue{Type: sig.Params().At(1).Type(), Value: vtv.Value}
	var w TypedWrite
	if site.Kind == SiteAttr {
		w = e.classifyAttr(tv, site.URLAttr, IsTrueFalseAttr(site.AttrName))
	} else {
		w = e.classify(tv)
	}
	// .orbit spans: the expression is copied byte for byte at site.Arg.
	span := func(n ast.Node) Span {
		return sm.span(site, n)
	}
	iw := &IfWrite{Fun: span(call.Fun), Cond: span(call.Args[0]), Val: span(call.Args[1]), Inst: inst, Const: vtv.Value != nil}
	if text, ok := foldIf(w, tv.Value, site); ok {
		iw.Folded, iw.Text = true, text
	}
	w.If = iw
	return w, true
}

// ifCall returns expr (through parentheses) when it's a 2-argument call
// of the runtime's If, identified by its types.Object, with its
// instantiated signature and whether type arguments are written.
func ifCall(expr ast.Expr, info *types.Info) (call *ast.CallExpr, sig *types.Signature, inst, ok bool) {
	if info.Uses == nil {
		return nil, nil, false, false
	}
	call, ok = ast.Unparen(expr).(*ast.CallExpr)
	if !ok || len(call.Args) != 2 || call.Ellipsis.IsValid() {
		return nil, nil, false, false
	}
	fun := ast.Unparen(call.Fun)
	switch f := fun.(type) {
	case *ast.IndexExpr:
		fun, inst = ast.Unparen(f.X), true
	case *ast.IndexListExpr:
		fun, inst = ast.Unparen(f.X), true
	}
	var id *ast.Ident
	switch f := fun.(type) {
	case *ast.Ident:
		id = f
	case *ast.SelectorExpr:
		id = f.Sel
	default:
		return nil, nil, false, false
	}
	fn, isFunc := info.Uses[id].(*types.Func)
	if !isFunc || fn.Name() != "If" || fn.Pkg() == nil || fn.Pkg().Path() != RuntimePath {
		return nil, nil, false, false
	}
	sig, ok = info.Types[call.Fun].Type.(*types.Signature)
	if !ok || sig.Params().Len() != 2 {
		return nil, nil, false, false
	}
	return call, sig, inst, true
}

// foldIf is the HTML a constant value v writes with w, when it's one Orbit
// folds at build time (strings, integers, bools; byte-identical to the
// runtime's writes).
func foldIf(w TypedWrite, v constant.Value, site *CallSite) (string, bool) {
	if v == nil {
		return "", false
	}
	var val string
	switch v.Kind() {
	case constant.String:
		val = constant.StringVal(v)
	case constant.Int:
		val = v.ExactString()
	case constant.Bool:
		val = strconv.FormatBool(constant.BoolVal(v))
	default:
		return "", false
	}
	open := " " + site.AttrName + `="`
	switch w.Kind {
	case WriteString:
		if v.Kind() != constant.String {
			return "", false
		}
		return textEscaper.Replace(val), true
	case WriteHTML:
		if v.Kind() != constant.String {
			return "", false
		}
		return val, true
	case WriteInt, WriteUint, WriteBool:
		if v.Kind() == constant.String {
			return "", false
		}
		return val, true
	case WriteAttrString:
		if v.Kind() != constant.String {
			return "", false
		}
		return open + attrValueEscaper.Replace(val) + `"`, true
	case WriteAttrURL:
		if v.Kind() != constant.String {
			return "", false
		}
		if isUnsafeURL(val) {
			val = invalidURL
		}
		return open + attrValueEscaper.Replace(val) + `"`, true
	case WriteAttrInt, WriteAttrUint, WriteAttrBoolText:
		if v.Kind() == constant.String {
			return "", false
		}
		return open + val + `"`, true
	case WriteAttrBool:
		if v.Kind() != constant.Bool {
			return "", false
		}
		if constant.BoolVal(v) {
			return " " + site.AttrName, true
		}
		return "", true
	}
	return "", false
}
