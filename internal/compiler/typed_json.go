package compiler

// The JSON-serializable Props check of server:defer call sites (PLAN.md
// §3.4), run in the typed pass: Defer encodes props with encoding/json and
// the island endpoint decodes them back into Props, so every field of the
// target's Props must survive that round trip.

import (
	"fmt"
	"go/ast"
	gotoken "go/token"
	"go/types"
	"reflect"
)

// jsonIfaces are the method sets that make a type encode as itself.
type jsonIfaces struct {
	marshaler, unmarshaler, textMarshaler, textUnmarshaler *types.Interface
}

var jsonMethods = func() *jsonIfaces {
	bytes := types.NewSlice(types.Typ[types.Byte])
	errT := types.Universe.Lookup("error").Type()
	method := func(name string, params, results *types.Tuple) *types.Interface {
		sig := types.NewSignatureType(nil, nil, nil, params, results, false)
		fn := types.NewFunc(gotoken.NoPos, nil, name, sig)
		it := types.NewInterfaceType([]*types.Func{fn}, nil)
		it.Complete()
		return it
	}
	b := types.NewTuple(types.NewVar(gotoken.NoPos, nil, "b", bytes))
	be := types.NewTuple(types.NewVar(gotoken.NoPos, nil, "", bytes), types.NewVar(gotoken.NoPos, nil, "", errT))
	e := types.NewTuple(types.NewVar(gotoken.NoPos, nil, "", errT))
	return &jsonIfaces{
		marshaler:       method("MarshalJSON", nil, be),
		unmarshaler:     method("UnmarshalJSON", b, e),
		textMarshaler:   method("MarshalText", nil, be),
		textUnmarshaler: method("UnmarshalText", b, e),
	}
}()

// selfCoded reports whether t encodes and decodes through its own methods:
// json.Marshaler with json.Unmarshaler on the pointer, or the
// encoding.TextMarshaler/TextUnmarshaler pair (time.Time).
func selfCoded(t types.Type) bool {
	if _, ok := t.Underlying().(*types.Interface); ok {
		return false
	}
	ptr := types.NewPointer(t)
	m := jsonMethods
	marshals := func(it *types.Interface) bool { return types.Implements(t, it) || types.Implements(ptr, it) }
	return marshals(m.marshaler) && types.Implements(ptr, m.unmarshaler) ||
		marshals(m.textMarshaler) && types.Implements(ptr, m.textUnmarshaler)
}

// jsonProblem is the first part of a Props type that encoding/json can't
// round-trip: the field path and its type.
type jsonProblem struct {
	path string
	typ  types.Type
}

// jsonWalker walks a type with encoding/json's rules.
type jsonWalker struct {
	// stack holds the named types being walked, so recursive types end.
	stack map[*types.Named]bool
}

// check returns the first problem under t, reached at path, or nil.
func (w *jsonWalker) check(orig types.Type, path string) *jsonProblem {
	t := types.Unalias(orig)
	if elem, ok := optElem(t); ok {
		// Opt's MarshalJSON writes null or the value's JSON: check T.
		return w.check(elem, path)
	}
	if n, ok := t.(*types.Named); ok {
		if w.stack[n] {
			return nil
		}
		if selfCoded(n) {
			return nil
		}
		w.stack[n] = true
		defer delete(w.stack, n)
	} else if selfCoded(t) {
		return nil
	}
	bad := &jsonProblem{path, orig}
	switch u := t.Underlying().(type) {
	case *types.Basic:
		switch {
		case u.Info()&types.IsComplex != 0, u.Kind() == types.UnsafePointer:
			return bad
		}
		return nil
	case *types.Signature, *types.Chan, *types.Interface:
		return bad
	case *types.Pointer:
		return w.check(u.Elem(), path)
	case *types.Slice:
		return w.check(u.Elem(), path+"[]")
	case *types.Array:
		return w.check(u.Elem(), path+"[]")
	case *types.Map:
		if !jsonMapKey(u.Key()) {
			return bad
		}
		return w.check(u.Elem(), path+"[]")
	case *types.Struct:
		for i := range u.NumFields() {
			f := u.Field(i)
			tag := reflect.StructTag(u.Tag(i)).Get("json")
			if tag == "-" {
				continue
			}
			if f.Embedded() {
				ft := types.Unalias(f.Type())
				if p, ok := ft.(*types.Pointer); ok {
					ft = types.Unalias(p.Elem())
				}
				_, isStruct := ft.Underlying().(*types.Struct)
				if !f.Exported() && !isStruct {
					continue // encoding/json ignores it
				}
			} else if !f.Exported() {
				continue
			}
			if p := w.check(f.Type(), path+"."+f.Name()); p != nil {
				return p
			}
		}
		return nil
	}
	return nil // type parameters can't occur (Props isn't generic)
}

// jsonMapKey reports whether encoding/json can use k as a map key: a string
// or integer kind, or an encoding.TextMarshaler.
func jsonMapKey(k types.Type) bool {
	if b, ok := k.Underlying().(*types.Basic); ok && b.Info()&(types.IsString|types.IsInteger) != 0 {
		return true
	}
	return types.Implements(k, jsonMethods.textMarshaler) && types.Implements(types.NewPointer(k), jsonMethods.textUnmarshaler)
}

// checkDeferProps runs the JSON check on the Props literal of every
// server:defer call site of a checked impl file, reporting one error per
// offending top-level field at the server:defer attribute.
func checkDeferProps(fset *gotoken.FileSet, f *ast.File, sites []DeferSite, info *types.Info) []Diagnostic {
	if len(sites) == 0 {
		return nil
	}
	byStart := make(map[int]*DeferSite, len(sites))
	for i := range sites {
		byStart[sites[i].Lit.Start] = &sites[i]
	}
	tf := fset.File(f.Pos())
	var ds []Diagnostic
	ast.Inspect(f, func(n ast.Node) bool {
		lit, ok := n.(*ast.CompositeLit)
		if !ok || lit.Type == nil {
			return true
		}
		site := byStart[tf.Offset(lit.Type.Pos())]
		if site == nil {
			return true
		}
		tv, ok := info.Types[lit]
		if !ok {
			return true
		}
		ds = append(ds, deferPropsErrors(site, tv.Type)...)
		return true
	})
	return ds
}

// deferPropsErrors checks one Props type.
func deferPropsErrors(site *DeferSite, props types.Type) []Diagnostic {
	st, ok := props.Underlying().(*types.Struct)
	if !ok {
		return nil
	}
	var pkg *types.Package
	if n, ok := types.Unalias(props).(*types.Named); ok {
		pkg = n.Obj().Pkg()
	}
	qual := func(p *types.Package) string {
		if p == pkg {
			return ""
		}
		return p.Name()
	}
	var ds []Diagnostic
	w := &jsonWalker{stack: map[*types.Named]bool{}}
	if n, ok := types.Unalias(props).(*types.Named); ok {
		w.stack[n] = true
	}
	for i := range st.NumFields() {
		f := st.Field(i)
		if reflect.StructTag(st.Tag(i)).Get("json") == "-" || !f.Exported() && !f.Embedded() {
			continue
		}
		// Check the field as a one-field struct, so the embedded-field
		// rules apply.
		one := types.NewStruct([]*types.Var{f}, []string{st.Tag(i)})
		if p := w.check(one, "Props"); p != nil {
			ds = append(ds, Diagnostic{Severity: SeverityError, Span: site.Attr, Message: fmt.Sprintf(
				"<%s server:defer>: %s has type %s, which can't be sent to a deferred component; props must be JSON-serializable",
				site.Tag, p.path, types.TypeString(p.typ, qual))})
		}
	}
	return ds
}
