package orbit

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
)

// Typed optional values (PLAN.md §2.6, "Typed optional values"): an Opt[T]
// is some value of type T, or none. As an attribute value, none omits the
// attribute; in content, it writes nothing. Generated code writes an
// Opt-typed attribute or content value as an if statement around the typed
// write of T, so it costs no allocation; the untyped paths (Attr, Expr,
// Spread, an orbit.Attrs value) see it through the unexported optValue
// methods.

// Opt is an optional value of type T: either some value, or none. The zero
// Opt is none. As an attribute value, none omits the attribute and some(v)
// is written as v; in content, none writes nothing.
//
// Build one with Some, None, If or NonZero. An Opt is comparable when T is:
// o == orbit.None[string]() and o == orbit.Some("x") work, and cmp.Or(a, b)
// returns the first one that is set.
//
// In JSON, none is null and some(v) is v's JSON; decoding null gives none.
// IsZero makes a `json:",omitzero"` field drop a none.
type Opt[T any] struct {
	v  T // the zero T when none, so == compares as expected
	ok bool
}

// Some returns an Opt holding v, even when v is T's zero value.
func Some[T any](v T) Opt[T] { return Opt[T]{v: v, ok: true} }

// None returns the Opt with no value, Opt[T]{}.
func None[T any]() Opt[T] { return Opt[T]{} }

// NonZero returns Some(v), or None when v is T's zero value: Astro's
// `x || undefined`. orbit.NonZero(id) is orbit.If(id != "", id) with id
// evaluated once.
func NonZero[T comparable](v T) Opt[T] {
	var zero T
	if v == zero {
		return Opt[T]{}
	}
	return Opt[T]{v: v, ok: true}
}

// Get returns the value and whether it is set; the zero T when none.
func (o Opt[T]) Get() (T, bool) { return o.v, o.ok }

// Or returns the value, or def when none (Astro's `x ?? def`).
func (o Opt[T]) Or(def T) T {
	if o.ok {
		return o.v
	}
	return def
}

// IsSome reports whether the value is set.
func (o Opt[T]) IsSome() bool { return o.ok }

// IsZero reports whether o is none. It makes a `json:",omitzero"` field
// drop a none.
func (o Opt[T]) IsZero() bool { return !o.ok }

// String returns fmt.Sprint of the value, or "" when none, so fmt and log
// lines print the value. Orbit doesn't use it to write an Opt: a template
// writes the value by its own type's rules.
func (o Opt[T]) String() string {
	if !o.ok {
		return ""
	}
	return fmt.Sprint(o.v)
}

// MarshalJSON encodes none as null and some(v) as v's JSON.
func (o Opt[T]) MarshalJSON() ([]byte, error) {
	if !o.ok {
		return []byte("null"), nil
	}
	return json.Marshal(o.v)
}

// UnmarshalJSON decodes null as none and anything else as some(v). A
// some(nil pointer, slice or map) encodes as null, so it comes back none.
func (o *Opt[T]) UnmarshalJSON(b []byte) error {
	if string(bytes.TrimSpace(b)) == "null" {
		*o = Opt[T]{}
		return nil
	}
	var v T
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	*o = Opt[T]{v: v, ok: true}
	return nil
}

// optValue is how the untyped paths (Attr, Expr, Spread, orbit.Attrs, a
// RestAny) see an Opt without knowing T. User types can't implement it.
type optValue interface {
	// optAttr writes nothing when none, else the attribute by Attr's rules
	// for the value.
	optAttr(w *Writer, name string)
	// optText writes nothing when none, else the value by Expr's rules
	// (text HTML-escaped if escape is set).
	optText(w *Writer, escape bool)
	// optAny returns the value boxed and whether it is set.
	optAny() (any, bool)
}

func (o Opt[T]) optAttr(w *Writer, name string) {
	if !o.ok {
		return
	}
	writeAttr(w, name, o.v)
}

// writeAttr writes the attribute name with value v by Attr's rules, with
// the common types unboxed (the list A keeps unboxed in an Opt).
func writeAttr[T any](w *Writer, name string, v T) {
	switch x := any(v).(type) {
	case string:
		w.attrValue(name, x, isURLAttr(name))
	case URL:
		w.attrValue(name, string(x), false)
	case HTML:
		w.attrValue(name, string(x), isURLAttr(name))
	case CSS:
		w.attrValue(name, string(x), isURLAttr(name))
	case JSONValue:
		w.attrJSON(name, x)
	case bool:
		w.attrBool(name, x)
	case int:
		w.attrOpen(name)
		w.Int(int64(x))
		w.buf.WriteByte('"')
	case int64:
		w.attrOpen(name)
		w.Int(x)
		w.buf.WriteByte('"')
	case float64:
		w.attrOpen(name)
		w.Float(x)
		w.buf.WriteByte('"')
	default:
		w.attr(name, any(v), isURLAttr(name))
	}
}

func (o Opt[T]) optText(w *Writer, escape bool) {
	if o.ok {
		writeText(w, o.v, escape)
	}
}

func (o Opt[T]) optAny() (any, bool) {
	if !o.ok {
		return nil, false
	}
	return o.v, true
}

// writeText writes v by Expr's rules, with the common types unboxed
// (the list A keeps unboxed in an Opt).
func writeText[T any](w *Writer, v T, escape bool) {
	switch x := any(v).(type) {
	case string:
		w.text(x, escape)
	case HTML:
		w.buf.WriteString(string(x))
	case URL:
		w.text(string(x), escape)
	case CSS:
		w.text(string(x), escape)
	case JSONValue:
		w.textJSON(x, escape)
	case bool:
		w.Bool(x)
	case int:
		w.Int(int64(x))
	case int64:
		w.Int(x)
	case float64:
		w.Float(x)
	default:
		w.value(any(v), escape)
	}
}

// The helpers below are called by generated code for a component prop of
// type Opt[T] (PLAN.md §2.6 (Q3)): the first argument is the prop's zero
// value, Alias.Props{}.Field, which gives T.

// SetHTML is set:html={x} before the build has checked x's type (PLAN.md
// §2.6): an HTML is written raw, an Opt[HTML] when it's set. The build
// replaces it with the HTML write (lowered for an Opt[HTML]), and refuses
// any other type; when it couldn't check (export data couldn't be read, with
// a warning), a value of another type is a render error at pos, never
// written unescaped. Generated code calls it.
func SetHTML[T ~string | Opt[HTML]](w *Writer, pos string, x T) {
	switch v := any(x).(type) {
	case HTML:
		w.HTML(v)
	case Opt[HTML]:
		if h, ok := v.Get(); ok {
			w.HTML(h)
		}
	default:
		w.setErr(&posError{pos: pos, err: fmt.Errorf("set:html needs an orbit.HTML (or an orbit.Opt[orbit.HTML]), not %T; it was skipped (see the warning above)", x)})
	}
}

// Wrap returns Some(v): a static value, a bare attribute or an expression
// of type T passed to a prop of type Opt[T]. Generated code calls it.
func Wrap[T any](_ Opt[T], v T) Opt[T] { return Opt[T]{v: v, ok: true} }

// WrapIf returns If(cond, v) as an Opt[T], for an orbit.If call whose value
// is an untyped constant passed to a prop of type Opt[T] (so it takes T,
// not the constant's default type). The second argument is the If written
// at the call site, which keeps its package used. Generated code calls it.
func WrapIf[T any](_ Opt[T], _ func(bool, bool) Opt[bool], cond bool, v T) Opt[T] {
	if cond {
		return Opt[T]{v: v, ok: true}
	}
	return Opt[T]{}
}

// OptOf returns v when it's an Opt[T] and Some(v) when it's a T, for a
// prop of type Opt[T] before the typed pass has checked the value (which
// then replaces the call with v, Wrap or WrapIf). Generated code calls it.
//
// An untyped constant passed through OptOf has its default type, so a
// value (or an Opt's value) of the same kind as T, or a number for a
// number, is converted to T, as the typed pass's Wrap and WrapIf would
// take it (rel={someConst} or rel={orbit.If(c, "x")} for an Opt[Slug]).
// Any other value gives None: the typed pass reports it.
func OptOf[T, V any](_ Opt[T], v V) Opt[T] {
	switch x := any(v).(type) {
	case Opt[T]:
		return x
	case T:
		return Opt[T]{v: x, ok: true}
	}
	return optConvert[T](any(v))
}

// optConvert is OptOf's slow path (pass 1 only): v, or the value of the
// Opt v, converted to T when it's of T's kind (or both are numbers).
func optConvert[T any](v any) Opt[T] {
	if o, ok := v.(optValue); ok {
		x, set := o.optAny()
		if !set {
			return Opt[T]{}
		}
		v = x
	}
	rv := reflect.ValueOf(v)
	tt := reflect.TypeFor[T]()
	if !rv.IsValid() || !rv.Type().ConvertibleTo(tt) {
		return Opt[T]{}
	}
	if k := rv.Kind(); k != tt.Kind() && !(isNumberKind(k) && isNumberKind(tt.Kind())) {
		return Opt[T]{}
	}
	t, _ := rv.Convert(tt).Interface().(T)
	return Opt[T]{v: t, ok: true}
}

func isNumberKind(k reflect.Kind) bool {
	return k >= reflect.Int && k <= reflect.Float64
}
