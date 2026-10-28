package orbit

import (
	"bytes"
	"fmt"
	"reflect"
	"strconv"
)

// Template literals (PLAN.md §2.10). In a template, a backtick string is a
// JavaScript template literal: `/p/${id}`. Codegen lowers one that is a
// whole attribute or content value into writes of its pieces, and one
// anywhere else into a string concatenation. These are the runtime helpers
// of that generated code; none of them is meant to be called by hand.

// URLMark returns the buffer offset where a URL attribute's value starts,
// for URLCheck. Generated code calls it around a template literal whose
// static prefix doesn't settle the URL's scheme at build time.
func (w *Writer) URLMark() int { return w.buf.Len() }

// URLCheck replaces the bytes written since mark (a URL attribute's value)
// with about:invalid#orbit if they start with a javascript: or vbscript:
// scheme, as AttrURL does for a whole string value.
func (w *Writer) URLCheck(mark int) {
	if w.err != nil || mark < 0 || mark > w.buf.Len() {
		return
	}
	if isUnsafeURL(w.buf.Bytes()[mark:]) {
		w.buf.Truncate(mark)
		w.buf.WriteString(invalidURL)
	}
}

// ClassBytes writes the classes in b as Class does: a template literal that
// is one class:list item (or a class merged into a class list), built in a
// stack buffer.
func (w *Writer) ClassBytes(b []byte) {
	if w.err != nil {
		return
	}
	for i := 0; i < len(b); {
		for i < len(b) && isClassSpace(b[i]) {
			i++
		}
		j := i
		for j < len(b) && !isClassSpace(b[j]) {
			j++
		}
		if j > i {
			w.classSep()
			escapeAttrBytes(w.buf, b[i:j])
		}
		i = j
	}
}

// AttrPiece writes a piece of a template literal in an attribute value:
// the text Expr would write for v, attribute-escaped (orbit.HTML too, since
// a template literal is text).
func (w *Writer) AttrPiece(v any) {
	if w.err != nil {
		return
	}
	switch v := v.(type) {
	case nil:
	case string:
		escapeAttrString(w.buf, v)
	case int:
		w.Int(int64(v))
	case int64:
		w.Int(v)
	case float64:
		w.Float(v)
	case bool:
		w.Bool(v)
	default:
		w.piece(v, escapeAttrBytes)
	}
}

// TextPiece writes a piece of a template literal in content: the text Expr
// would write for v, HTML-escaped. orbit.HTML is escaped too (a template
// literal is text), and a Renderer's output is escaped.
func (w *Writer) TextPiece(v any) {
	if w.err != nil {
		return
	}
	switch v := v.(type) {
	case nil:
	case string:
		escapeString(w.buf, v)
	case int:
		w.Int(int64(v))
	case int64:
		w.Int(v)
	case float64:
		w.Float(v)
	case bool:
		w.Bool(v)
	default:
		w.piece(v, escapeBytes)
	}
}

// piece writes v's text, formatted by Expr's rules into a scratch buffer
// and escaped with esc.
func (w *Writer) piece(v any, esc func(*bytes.Buffer, []byte)) {
	scratch := getBuffer()
	orig, limit := w.buf, w.limit
	w.buf, w.limit = scratch, 0 // a marked component renders inline
	w.value(v, false)
	w.buf, w.limit = orig, limit
	if w.err == nil {
		esc(w.buf, scratch.Bytes())
	}
	putBuffer(scratch)
}

// TemplateString returns the text of a template literal's parts: each
// formatted as Expr would write it, unescaped. Codegen emits it for a
// literal it couldn't type (the fallback); the typed pass replaces it with
// a concatenation.
func TemplateString(parts ...any) string {
	var arr [128]byte
	return string(appendParts(arr[:0], parts))
}

func appendParts(b []byte, parts []any) []byte {
	for _, p := range parts {
		b = AppendAny(b, p)
	}
	return b
}

// TemplateAs returns the text of a template literal's parts as a value of
// the type of witness (a component prop's field, read from a zero Props):
// a string kind gets the text, an interface a string. It is the fallback of
// a template literal passed whole to a prop; the typed pass replaces it with
// a concatenation, and reports a field of any other type. For orbit.HTML,
// orbit.URL and orbit.CSS, every part must be inert (a number or bool
// without String, Error or Format methods, a value of that type, or a
// static string part): otherwise TemplateAs panics, as it does for a field
// that can't hold a string.
func TemplateAs[T any](witness T, parts ...any) T {
	var out T
	rv := reflect.ValueOf(&out).Elem()
	switch any(witness).(type) {
	case HTML, URL, CSS:
		for i, p := range parts {
			if !inertPart(p, rv.Type(), i%2 == 0) {
				panic(fmt.Sprintf("orbit: a %T piece can't be part of an %s template literal", p, rv.Type()))
			}
		}
	}
	s := TemplateString(parts...)
	switch {
	case rv.Kind() == reflect.String:
		rv.SetString(s)
	case rv.Kind() == reflect.Interface && reflect.TypeOf(s).AssignableTo(rv.Type()):
		rv.Set(reflect.ValueOf(s))
	default:
		panic(fmt.Sprintf("orbit: a template literal is a string; the prop is %s", rv.Type()))
	}
	return out
}

// inertPart reports whether a part of a template literal converted to a
// trusted type t is inert: a static part (codegen passes the static text
// and the pieces alternately, starting with the text), a value of type t,
// or a number or bool without String, Error or Format methods.
func inertPart(p any, t reflect.Type, static bool) bool {
	if static {
		_, ok := p.(string)
		return ok
	}
	if p == nil {
		return true
	}
	pt := reflect.TypeOf(p)
	if pt == t {
		return true
	}
	switch pt.Kind() {
	case reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Float32, reflect.Float64:
	default:
		return false
	}
	pp := reflect.PointerTo(pt)
	for _, m := range [...]string{"String", "Error", "Format"} {
		if _, ok := pp.MethodByName(m); ok {
			return false
		}
	}
	return true
}

// StringAs converts s to the string type of witness: a template literal
// passed whole to a prop of a named string type (type Slug string,
// orbit.URL, …), whose name the generated file may not import.
func StringAs[T ~string](witness T, s string) T { return T(s) }

// TB builds the text of a template literal whose pieces aren't all
// strings, in a buffer that generated code allocates on the stack:
// orbit__rt.TB(make([]byte, 0, 64)).S("Chapter ").Int(int64(n)).String().
// Each method appends what the matching Writer method writes, unescaped.
type TB []byte

// S appends s.
func (b TB) S(s string) TB { return append(b, s...) }

// B appends p.
func (b TB) B(p []byte) TB { return append(b, p...) }

// Int appends n in decimal.
func (b TB) Int(n int64) TB { return strconv.AppendInt(b, n, 10) }

// Uint appends n in decimal.
func (b TB) Uint(n uint64) TB { return strconv.AppendUint(b, n, 10) }

// Float appends f as Writer.Float writes it.
func (b TB) Float(f float64) TB { return AppendFloat(b, f) }

// Float32 appends f as Writer.Float32 writes it.
func (b TB) Float32(f float32) TB { return AppendFloat32(b, f) }

// Bool appends true or false.
func (b TB) Bool(v bool) TB { return strconv.AppendBool(b, v) }

// Stringer appends s.String() (nothing for a nil s).
func (b TB) Stringer(s fmt.Stringer) TB { return AppendStringer(b, s) }

// Error appends e.Error() (nothing for a nil e).
func (b TB) Error(e error) TB { return AppendError(b, e) }

// Any appends v's text as Expr formats it.
func (b TB) Any(v any) TB { return AppendAny(b, v) }

// String returns the text built (one allocation).
func (b TB) String() string { return string(b) }

// Bytes returns the text built, for ClassBytes.
func (b TB) Bytes() []byte { return b }

// AppendFloat appends f in its shortest form, the bytes of Writer.Float.
func AppendFloat(b []byte, f float64) []byte { return strconv.AppendFloat(b, f, 'g', -1, 64) }

// AppendFloat32 appends f in its shortest float32 form, the bytes of
// Writer.Float32.
func AppendFloat32(b []byte, f float32) []byte {
	return strconv.AppendFloat(b, float64(f), 'g', -1, 32)
}

// AppendStringer appends s.String(), the text of Writer.Stringer: nothing
// for a nil s, <nil> for a nil pointer whose String panics.
func AppendStringer(b []byte, s fmt.Stringer) []byte {
	if s == nil {
		return b
	}
	return append(b, stringerString(s)...)
}

// AppendError appends e.Error(), the text of Writer.Error: nothing for a
// nil e.
func AppendError(b []byte, e error) []byte {
	if e == nil {
		return b
	}
	return append(b, errorString(e)...)
}

// AppendAny appends v's text as Writer.Expr formats it, unescaped: nothing
// for nil, orbit.HTML as its text, a Renderer's output.
func AppendAny(b []byte, v any) []byte {
	switch v := v.(type) {
	case nil:
		return b
	case string:
		return append(b, v...)
	case int:
		return strconv.AppendInt(b, int64(v), 10)
	case int64:
		return strconv.AppendInt(b, v, 10)
	case float64:
		return AppendFloat(b, v)
	case bool:
		return strconv.AppendBool(b, v)
	}
	scratch := getBuffer()
	w := Writer{buf: scratch, head: -1}
	w.value(v, false)
	b = append(b, scratch.Bytes()...)
	putBuffer(scratch)
	return b
}
