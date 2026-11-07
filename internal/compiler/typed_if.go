package compiler

// The lyr.If lowering of the typed pass (PLAN.md §2.6): a call of the
// runtime's If that is the whole value of an HTML element attribute or of a
// content {expr} becomes an if statement around the typed write of its
// value, with the same bytes as Attr or Expr of the call's result.

import (
	"go/ast"
	"go/constant"
	"go/types"
	"strings"
)

// mayCallIf reports whether any attribute or content site's code mentions
// If, any of those or a template literal's piece mentions Cond, or there's
// a set:html site (whose untyped constants are told by their identifiers,
// untypedConst), so the type check records identifier uses only for files
// that may need them.
func mayCallIf(sites []CallSite) bool {
	for i := range sites {
		k, code := sites[i].Kind, sites[i].Code.Text
		switch {
		case k == SiteHTML:
			return true
		case (k == SiteAttr || k == SiteContent || k == SiteOptProp) && strings.Contains(code, "If"):
			return true
		case (k == SiteAttr || k == SiteContent || k == SitePiece) && strings.Contains(code, "Cond"):
			return true
		}
	}
	return false
}

// lowerIf returns the lowered write of site when expr (its expression in
// the pass-1 file tf) is a call of the runtime's If, identified by its
// types.Object, so a user's own If or a shadowed lyr is left alone.
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
	// .lyr spans: the expression is copied byte for byte at site.Arg.
	span := func(n ast.Node) Span {
		return sm.span(site, n)
	}
	iw := &IfWrite{Fun: span(call.Fun), Cond: span(call.Args[0]), Val: span(call.Args[1]), Inst: inst, Const: vtv.Value != nil}
	if text, ok := foldIf(w, tv.Value, site); ok {
		iw.Folded, iw.Text = true, text
		iw.Ref = namesRef(call.Args[1], info)
	}
	w.If = iw
	return w, true
}

// ifCall returns expr (through parentheses) when it's a 2-argument call
// of the runtime's If, identified by its types.Object, with its
// instantiated signature and whether type arguments are written.
func ifCall(expr ast.Expr, info *types.Info) (call *ast.CallExpr, sig *types.Signature, inst, ok bool) {
	return rtCall(expr, info, "If")
}

// rtCall returns expr (through parentheses) when it's a call of the
// runtime's func name with as many arguments as its parameters,
// identified by its types.Object (so a user's own func or a shadowed lyr
// isn't one), with its instantiated signature and whether type arguments
// are written.
func rtCall(expr ast.Expr, info *types.Info, name string) (call *ast.CallExpr, sig *types.Signature, inst, ok bool) {
	if info.Uses == nil {
		return nil, nil, false, false
	}
	call, ok = ast.Unparen(expr).(*ast.CallExpr)
	if !ok || call.Ellipsis.IsValid() {
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
	if !isFunc || fn.Name() != name || fn.Pkg() == nil || fn.Pkg().Path() != RuntimePath {
		return nil, nil, false, false
	}
	sig, ok = info.Types[call.Fun].Type.(*types.Signature)
	if !ok || sig.Params().Len() != len(call.Args) {
		return nil, nil, false, false
	}
	return call, sig, inst, true
}

// foldIf is the HTML a constant value v writes with w at site, when Lyr
// folds it at build time (foldConst).
func foldIf(w TypedWrite, v constant.Value, site *CallSite) (string, bool) {
	return foldConst(w, v, site.AttrName)
}

// foldConst is the HTML a constant value v writes with the attribute or
// content write w (name is the attribute's), when it's one Lyr folds at
// build time: strings, integers, floats and bools, byte-identical to the
// runtime's writes (w is the typed write of v's type, so a type with
// String/Error/Format methods has a write that isn't folded).
func foldConst(w TypedWrite, v constant.Value, name string) (string, bool) {
	if v == nil {
		return "", false
	}
	open := " " + name + `="`
	switch w.Kind {
	case WriteHTML:
		if v.Kind() == constant.String {
			return constant.StringVal(v), true
		}
	case WriteString, WriteInt, WriteUint, WriteBool, WriteFloat, WriteFloat32:
		if text, ok := foldPiece(v, w); ok {
			if w.Kind == WriteString {
				text = textEscaper.Replace(text)
			}
			return text, true
		}
	case WriteAttrString, WriteAttrURL:
		if v.Kind() == constant.String {
			val := constant.StringVal(v)
			if w.Kind == WriteAttrURL && isUnsafeURL(val) {
				val = invalidURL
			}
			return open + attrValueEscaper.Replace(val) + `"`, true
		}
	case WriteAttrInt, WriteAttrUint, WriteAttrFloat, WriteAttrFloat32, WriteAttrBoolText:
		if text, ok := foldPiece(v, TypedWrite{Kind: attrValueKinds[w.Kind]}); ok {
			return open + text + `"`, true
		}
	case WriteAttrBool:
		if v.Kind() == constant.Bool {
			if constant.BoolVal(v) {
				return " " + name, true
			}
			return "", true
		}
	}
	return "", false
}

// attrValueKinds maps a number or true/false attribute write to the
// content write of its value (the bytes between the quotes).
var attrValueKinds = map[WriteKind]WriteKind{
	WriteAttrInt: WriteInt, WriteAttrUint: WriteUint, WriteAttrFloat: WriteFloat,
	WriteAttrFloat32: WriteFloat32, WriteAttrBoolText: WriteBool,
}

// namesRef reports whether the constant expression x names something
// besides constants, types and builtins: a package (pkg.Label) or a
// variable (len(arr), arr an array), which the generated code must keep
// using once the expression is folded away. Anything not recorded counts.
func namesRef(x ast.Expr, info *types.Info) bool {
	ref := false
	ast.Inspect(x, func(n ast.Node) bool {
		if ref {
			return false
		}
		switch n := n.(type) {
		case *ast.SelectorExpr:
			if id, ok := n.X.(*ast.Ident); ok {
				_, typed := info.Types[id] // a package name isn't
				ref = !typed || identRef(id, info)
			} else {
				ref = namesRef(n.X, info)
			}
			return false // Sel is a field, a method or a qualified name
		case *ast.Ident:
			ref = identRef(n, info)
		}
		return !ref
	})
	return ref
}

// identRef reports whether id isn't a constant, a type or a builtin.
func identRef(id *ast.Ident, info *types.Info) bool {
	if id.Name == "_" {
		return false
	}
	tv, ok := info.Types[id]
	return !ok || !(tv.Value != nil || tv.IsType() || tv.IsBuiltin() || tv.IsNil())
}
