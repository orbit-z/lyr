package compiler

// The typed-write table (PLAN.md §3.3, ported from spike/typecheck/typed):
// for a call site's static type, the Writer method whose output is
// byte-identical to Expr's (or Attr's) runtime behavior (writer.go). Owned by
// agent C.

import (
	gotoken "go/token"
	"go/types"
)

// tyEnv holds the runtime types the table compares against, from one
// importer's view of the runtime package.
type tyEnv struct {
	html, url, renderer types.Type // orbit.HTML, orbit.URL, orbit.Renderer
	jsonValue           types.Type // orbit.JSONValue
	renderFunc          types.Type // func(*orbit.Writer) error
	stringer            *types.Interface
	errorIface          *types.Interface
	byteSlice           types.Type
}

// newTyEnv builds the table's types from the runtime package rt.
func newTyEnv(rt *types.Package) *tyEnv {
	sc := rt.Scope()
	lookup := func(name string) types.Type {
		if o, ok := sc.Lookup(name).(*types.TypeName); ok {
			return o.Type()
		}
		return nil
	}
	writer := lookup("Writer")
	errT := types.Universe.Lookup("error").Type()
	// fmt.Stringer, built structurally so fmt's package isn't needed.
	str := types.NewInterfaceType([]*types.Func{
		types.NewFunc(gotoken.NoPos, nil, "String", types.NewSignatureType(nil, nil, nil, nil,
			types.NewTuple(types.NewVar(gotoken.NoPos, nil, "", types.Typ[types.String])), false)),
	}, nil).Complete()
	e := &tyEnv{
		html:       lookup("HTML"),
		url:        lookup("URL"),
		jsonValue:  lookup("JSONValue"),
		renderer:   lookup("Renderer"),
		stringer:   str,
		errorIface: errT.Underlying().(*types.Interface),
		byteSlice:  types.NewSlice(types.Typ[types.Byte]),
	}
	if writer != nil {
		e.renderFunc = types.NewSignatureType(nil, nil, nil,
			types.NewTuple(types.NewVar(gotoken.NoPos, nil, "w", types.NewPointer(writer))),
			types.NewTuple(types.NewVar(gotoken.NoPos, nil, "", errT)), false)
	}
	return e
}

// identical is types.Identical with a nil-safe runtime type.
func identical(t, rt types.Type) bool { return rt != nil && types.Identical(t, rt) }

// tyStatic returns the type Expr's or Attr's argument has once boxed in an
// interface, with aliases resolved: a constant's default type. ok is false
// for an invalid type; nilv is true for untyped nil.
func tyStatic(tv types.TypeAndValue) (t types.Type, nilv, ok bool) {
	if tv.Type == nil {
		return nil, false, false
	}
	t = types.Unalias(tv.Type)
	if b, isB := t.(*types.Basic); isB {
		switch {
		case b.Kind() == types.UntypedNil:
			return t, true, true
		case b.Kind() == types.Invalid:
			return nil, false, false
		}
	}
	if tv.Value != nil || isUntyped(t) {
		t = types.Unalias(types.Default(t))
	}
	return t, false, true
}

func isUntyped(t types.Type) bool {
	b, ok := t.(*types.Basic)
	return ok && b.Info()&types.IsUntyped != 0
}

// hasFormat reports whether t has a Format method (fmt.Formatter), which
// fmt.Sprint would call instead of printing the underlying value. Pointer
// receivers count too, to stay on the safe side.
func hasFormat(t types.Type) bool {
	obj, _, _ := types.LookupFieldOrMethod(t, true, nil, "Format")
	_, isFunc := obj.(*types.Func)
	return isFunc
}

// classify picks the typed write for a content site ({expr}, set:text). It
// mirrors the case order of Writer.Expr, which matches exact dynamic types:
// type Slug string isn't a string there and goes through fmt.Sprint, so
// named types only get a typed write where fmt.Sprint prints the same bytes.
func (e *tyEnv) classify(tv types.TypeAndValue) TypedWrite {
	t, nilv, ok := tyStatic(tv)
	switch {
	case !ok:
		return TypedWrite{}
	case nilv:
		return TypedWrite{Kind: WriteSkip}
	case identical(t, e.html):
		return TypedWrite{Kind: WriteHTML}
	case types.Identical(t, types.Typ[types.String]):
		return TypedWrite{Kind: WriteString}
	case identical(t, e.url):
		return TypedWrite{Kind: WriteString, Conv: "string"}
	case types.Identical(t, e.byteSlice):
		return TypedWrite{Kind: WriteBytes}
	}
	if b, isB := t.(*types.Basic); isB { // an exact predeclared type
		if w, ok := basicWrite(b); ok {
			if w.Conv == b.Name() {
				w.Conv = ""
			}
			return w
		}
		return TypedWrite{} // complex, uintptr, unsafe.Pointer
	}
	switch {
	case identical(t, e.renderer):
		return TypedWrite{Kind: WriteRender}
	case identical(t, e.renderFunc):
		return TypedWrite{Kind: WriteRender, Conv: NameRuntime + ".Renderer"}
	}
	if types.IsInterface(t) {
		// A static interface: the dynamic value could be anything that
		// satisfies it. Only error is safe, since every dynamic value then
		// hits Expr's error case (or is nil); a Stringer-only interface
		// isn't, as a dynamic value that also implements error prints
		// Error().
		if types.Implements(t, e.errorIface) {
			return TypedWrite{Kind: WriteError}
		}
		return TypedWrite{}
	}
	// A concrete named or composite type: Expr's method cases, in order.
	if types.Implements(t, e.errorIface) {
		return TypedWrite{Kind: WriteError}
	}
	if types.Implements(t, e.stringer) {
		return TypedWrite{Kind: WriteStringer}
	}
	// No relevant method: Expr ends in fmt.Sprint, which prints a named
	// basic type like its underlying value (float32 with bitSize 32, as
	// Float32 does), unless it has a Format method.
	if _, named := t.(*types.Named); named && !hasFormat(t) {
		if b, isB := t.Underlying().(*types.Basic); isB {
			if w, ok := basicWrite(b); ok {
				return w
			}
		}
	}
	return TypedWrite{}
}

// basicWrite is the typed write for a value of basic type b, with the
// conversion to the method's parameter type (the caller drops it when b is
// that type). uintptr and complex types have none (PLAN.md §3.3 keeps them
// on Expr).
func basicWrite(b *types.Basic) (TypedWrite, bool) {
	info := b.Info()
	switch {
	case info&types.IsString != 0:
		return TypedWrite{Kind: WriteString, Conv: "string"}, true
	case info&types.IsInteger != 0 && info&types.IsUnsigned == 0:
		return TypedWrite{Kind: WriteInt, Conv: "int64"}, true
	case info&types.IsUnsigned != 0 && b.Kind() != types.Uintptr:
		return TypedWrite{Kind: WriteUint, Conv: "uint64"}, true
	case b.Kind() == types.Float64:
		return TypedWrite{Kind: WriteFloat, Conv: "float64"}, true
	case b.Kind() == types.Float32:
		return TypedWrite{Kind: WriteFloat32, Conv: "float32"}, true
	case info&types.IsBoolean != 0:
		return TypedWrite{Kind: WriteBool, Conv: "bool"}, true
	}
	return TypedWrite{}, false
}

// classifyAttr picks the typed write for an attribute site (a={expr}, {a}),
// mirroring Writer.Attr: string, HTML and named strings without
// error/String/Format methods are written as their text in Raw pieces
// (String, or URL in a URL attribute, which filters javascript: like Attr),
// URL unfiltered (String pieces), bools and
// named bools (Attr tests the reflect kind) as AttrBool, numbers (and named
// numbers without error/String/Format methods, which fmt prints as their
// value) as Raw pieces around Int, Uint, Float or Float32, untyped nil as
// nothing; everything else keeps Attr. On a true/false attribute (tfAttr,
// IsTrueFalseAttr), a bool is Bool pieces instead (name="true"/"false").
func (e *tyEnv) classifyAttr(tv types.TypeAndValue, urlAttr, tfAttr bool) TypedWrite {
	t, nilv, ok := tyStatic(tv)
	text := WriteAttrString
	if urlAttr {
		text = WriteAttrURL
	}
	boolKind := WriteAttrBool
	if tfAttr {
		boolKind = WriteAttrBoolText
	}
	switch {
	case !ok:
		return TypedWrite{}
	case nilv:
		return TypedWrite{Kind: WriteSkip}
	case identical(t, e.jsonValue):
		return TypedWrite{Kind: WriteAttrJSON}
	case types.Identical(t, types.Typ[types.String]):
		return TypedWrite{Kind: text}
	case identical(t, e.html):
		return TypedWrite{Kind: text, Conv: "string"}
	case identical(t, e.url):
		return TypedWrite{Kind: WriteAttrString, Conv: "string"}
	case types.Identical(t, types.Typ[types.Bool]):
		return TypedWrite{Kind: boolKind}
	}
	if b, isB := t.(*types.Basic); isB { // an exact predeclared type
		w := attrNumber(b)
		if w.Conv == b.Name() {
			w.Conv = ""
		}
		return w
	}
	if _, named := t.(*types.Named); !named || types.IsInterface(t) {
		return TypedWrite{}
	}
	b, isB := t.Underlying().(*types.Basic)
	if !isB {
		return TypedWrite{}
	}
	if b.Info()&types.IsBoolean != 0 {
		return TypedWrite{Kind: boolKind, Conv: "bool"}
	}
	if types.Implements(t, e.errorIface) || types.Implements(t, e.stringer) || hasFormat(t) {
		return TypedWrite{}
	}
	if b.Info()&types.IsString != 0 {
		return TypedWrite{Kind: text, Conv: "string"}
	}
	return attrNumber(b)
}

// attrNumber is the number-attribute write for a value of basic type b, with
// the conversion to the number write's parameter type, or the fallback for
// a type that isn't a number with a typed write.
func attrNumber(b *types.Basic) TypedWrite {
	w, ok := basicWrite(b)
	if !ok {
		return TypedWrite{}
	}
	switch w.Kind {
	case WriteInt:
		w.Kind = WriteAttrInt
	case WriteUint:
		w.Kind = WriteAttrUint
	case WriteFloat:
		w.Kind = WriteAttrFloat
	case WriteFloat32:
		w.Kind = WriteAttrFloat32
	default:
		return TypedWrite{}
	}
	return w
}

// classifyClass picks the write of a class:list item (PLAN.md §2.3): a
// string kind is a Class write, []string (or a named slice of string) a
// Classes write. ok is false for any other type, an error at the item.
func classifyClass(tv types.TypeAndValue) (w TypedWrite, ok bool) {
	t, nilv, valid := tyStatic(tv)
	switch {
	case !valid:
		return TypedWrite{}, true // a type error, reported by Go
	case nilv:
		return TypedWrite{}, false
	}
	switch u := t.Underlying().(type) {
	case *types.Basic:
		if u.Info()&types.IsString != 0 {
			if types.Identical(t, types.Typ[types.String]) {
				return TypedWrite{Kind: WriteClass}, true
			}
			return TypedWrite{Kind: WriteClass, Conv: "string"}, true
		}
	case *types.Slice:
		if types.Identical(u.Elem(), types.Typ[types.String]) {
			if _, named := t.(*types.Named); named {
				return TypedWrite{Kind: WriteClasses, Conv: "[]string"}, true
			}
			return TypedWrite{Kind: WriteClasses}, true
		}
	}
	return TypedWrite{}, false
}

// classTypeError is the error for a class:list item of type t (nil for
// untyped nil); what names the item ("class:list items", or the value of a
// class attribute merged into the list).
func classTypeError(t types.Type, what string) string {
	name := "untyped nil"
	if t != nil {
		name = types.TypeString(t, func(p *types.Package) string { return p.Name() })
	}
	return what + " must be a string or []string, not " + name
}

// restWrites maps an attribute write to the pass-through attribute write
// with the same output (PLAN.md §2.5).
var restWrites = map[WriteKind]WriteKind{
	WriteAttrString: WriteRestString, WriteAttrURL: WriteRestStringURL, WriteAttrBool: WriteRestBool,
	WriteAttrInt: WriteRestInt, WriteAttrUint: WriteRestUint, WriteAttrFloat: WriteRestFloat,
	WriteAttrFloat32: WriteRestFloat32, WriteAttrJSON: WriteRestJSON,
}

// classifyRest picks the write of a dynamic pass-through attribute: the
// typed constructor of the value Attr would write the same way (an
// orbit.URL stays trusted), or the RestAny fallback (untyped nil too: it's
// omitted at render time).
func (e *tyEnv) classifyRest(tv types.TypeAndValue, urlAttr bool) TypedWrite {
	if t, nilv, ok := tyStatic(tv); ok && !nilv && identical(t, e.url) {
		return TypedWrite{Kind: WriteRestURL}
	}
	// RestBool applies the true/false rule itself (it knows the name).
	w := e.classifyAttr(tv, urlAttr, false)
	k, ok := restWrites[w.Kind]
	if !ok {
		return TypedWrite{}
	}
	return TypedWrite{Kind: k, Conv: w.Conv}
}
