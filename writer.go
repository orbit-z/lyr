package orbit

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"
)

// maxDepth is the deepest component nesting Enter allows.
const maxDepth = 1000

// depthStackShown is how many components of the stack a depth error names.
const depthStackShown = 10

// invalidURL replaces a filtered javascript:/vbscript: URL.
const invalidURL = "about:invalid#orbit"

// Writer writes HTML straight into a bytes.Buffer. Generated code calls its
// methods; you can also use it in a Renderer written in Go.
//
// A Writer keeps a sticky error: the first error from a write is kept, later
// writes do nothing, and Err returns it.
type Writer struct {
	buf   *bytes.Buffer
	err   error
	head  int      // offset of the first </head>, or -1
	stack []string // component stack, outermost first
	// tags are the page's head tags (headTags), written by the first
	// HeadEnd; "" once written, and for fragments.
	tags string
	// jenc is the reused encoder of JSON values (json.go), writing
	// through jsink; created on first use and kept with the pooled Writer.
	jenc  *json.Encoder
	jsink jsonSink
	// clsPending is set between ClassStart and the first class written
	// (classlist.go).
	clsPending bool
}

// NewWriter returns a Writer that writes into buf.
func NewWriter(buf *bytes.Buffer) *Writer {
	return &Writer{buf: buf, head: -1}
}

// reset prepares w for reuse with buf, keeping the stack's capacity.
func (w *Writer) reset(buf *bytes.Buffer) {
	w.buf = buf
	w.err = nil
	w.head = -1
	w.stack = w.stack[:0]
	w.tags = ""
	w.clsPending = false
}

// finishHead inserts the head tags after a leading doctype (or at the
// start) if HeadEnd never wrote them, as for a page without </head>.
func (w *Writer) finishHead() {
	if w.tags != "" {
		injectHead(w.buf, w.tags)
		w.tags = ""
	}
}

// headOffset returns the offset recorded by HeadEnd, or -1.
func (w *Writer) headOffset() int { return w.head }

// Err returns the sticky error, or nil.
func (w *Writer) Err() error { return w.err }

func (w *Writer) setErr(err error) {
	if w.err == nil {
		w.err = err
	}
}

// Raw writes s unescaped.
func (w *Writer) Raw(s string) {
	if w.err != nil {
		return
	}
	w.buf.WriteString(s)
}

// String writes s, HTML-escaped.
func (w *Writer) String(s string) {
	if w.err != nil {
		return
	}
	escapeString(w.buf, s)
}

// Int writes n in decimal.
func (w *Writer) Int(n int64) {
	if w.err != nil {
		return
	}
	w.buf.Write(strconv.AppendInt(w.buf.AvailableBuffer(), n, 10))
}

// Uint writes n in decimal.
func (w *Writer) Uint(n uint64) {
	if w.err != nil {
		return
	}
	w.buf.Write(strconv.AppendUint(w.buf.AvailableBuffer(), n, 10))
}

// Float writes f in its shortest form, as fmt.Sprint does (0.1, 2, 1e+06).
func (w *Writer) Float(f float64) {
	if w.err != nil {
		return
	}
	w.buf.Write(strconv.AppendFloat(w.buf.AvailableBuffer(), f, 'g', -1, 64))
}

// Float32 writes f in its shortest float32 form: float32(0.1) prints 0.1.
func (w *Writer) Float32(f float32) {
	if w.err != nil {
		return
	}
	w.buf.Write(strconv.AppendFloat(w.buf.AvailableBuffer(), float64(f), 'g', -1, 32))
}

// Bool writes true or false.
func (w *Writer) Bool(b bool) {
	if w.err != nil {
		return
	}
	w.buf.WriteString(strconv.FormatBool(b))
}

// HTML writes h unescaped. Only trusted markup should be converted to HTML.
func (w *Writer) HTML(h HTML) {
	if w.err != nil {
		return
	}
	w.buf.WriteString(string(h))
}

// Bytes writes b as a string, HTML-escaped.
func (w *Writer) Bytes(b []byte) {
	if w.err != nil {
		return
	}
	escapeBytes(w.buf, b)
}

// Error writes e.Error(), HTML-escaped. It doesn't fail the render. A nil
// error writes nothing.
func (w *Writer) Error(e error) {
	if w.err != nil || e == nil {
		return
	}
	escapeString(w.buf, errorString(e))
}

// Stringer writes s.String(), HTML-escaped. A nil s writes nothing.
func (w *Writer) Stringer(s fmt.Stringer) {
	if w.err != nil || s == nil {
		return
	}
	escapeString(w.buf, stringerString(s))
}

// Render calls r, which writes into w. An error from r becomes the sticky
// error. A nil r writes nothing.
func (w *Writer) Render(r Renderer) {
	if w.err != nil || r == nil {
		return
	}
	if err := r(w); err != nil {
		w.setErr(err)
	}
}

// HeadEnd marks the place where the page's </head> starts: the page's
// asset tags are written there. Only the first call counts.
func (w *Writer) HeadEnd() {
	if w.head >= 0 {
		return
	}
	w.head = w.buf.Len()
	if w.tags != "" {
		if w.err == nil {
			w.buf.WriteString(w.tags)
		}
		w.tags = ""
	}
}

// Enter pushes component onto the component stack. Past a depth of 1000 it
// returns an error naming the stack instead, which stops runaway recursion
// before it overflows the Go stack. If the writer already has an error, Enter
// returns it. Call Leave when the component is done, only if Enter succeeded.
func (w *Writer) Enter(component string) error {
	if w.err != nil {
		return w.err
	}
	if len(w.stack) >= maxDepth {
		w.setErr(depthError(w.stack))
		return w.err
	}
	w.stack = append(w.stack, component)
	return nil
}

// Leave pops the component pushed by the matching Enter.
func (w *Writer) Leave() {
	if n := len(w.stack); n > 0 {
		w.stack = w.stack[:n-1]
	}
}

func depthError(stack []string) error {
	var b strings.Builder
	b.WriteString("orbit: render depth exceeded ")
	b.WriteString(strconv.Itoa(maxDepth))
	b.WriteString(": ")
	shown := stack[:min(len(stack), depthStackShown)]
	b.WriteString(strings.Join(shown, " > "))
	if len(shown) < len(stack) {
		b.WriteString(" > …")
	}
	return errors.New(b.String())
}

// Expr writes any value, choosing by its exact dynamic type:
//
//   - nil: nothing
//   - HTML: raw
//   - JSONValue: its JSON text, HTML-escaped
//   - string, URL: HTML-escaped
//   - []byte: HTML-escaped, as a string
//   - ints, floats, bools: strconv (float32 as a float32)
//   - Renderer, func(*Writer) error: called
//   - error: Error(), escaped
//   - fmt.Stringer: String(), escaped
//   - an unnamed slice: each element in turn, by these rules
//   - anything else: fmt.Sprint, escaped
//
// A named type such as type Slug string isn't a string here: it goes through
// fmt.Sprint, which prints the same text.
func (w *Writer) Expr(v any) {
	if w.err != nil {
		return
	}
	w.value(v, true)
}

// value writes v by the Expr rules, HTML-escaping text if escape is set and
// writing it as-is otherwise (attribute values are escaped as a whole later).
func (w *Writer) value(v any, escape bool) {
	if w.err != nil {
		return
	}
	switch v := v.(type) {
	case nil:
	case HTML:
		w.buf.WriteString(string(v))
	case JSONValue:
		w.textJSON(v, escape)
	case string:
		w.text(v, escape)
	case URL:
		w.text(string(v), escape)
	case []byte:
		if escape {
			escapeBytes(w.buf, v)
		} else {
			w.buf.Write(v)
		}
	case int:
		w.Int(int64(v))
	case int8:
		w.Int(int64(v))
	case int16:
		w.Int(int64(v))
	case int32:
		w.Int(int64(v))
	case int64:
		w.Int(v)
	case uint:
		w.Uint(uint64(v))
	case uint8:
		w.Uint(uint64(v))
	case uint16:
		w.Uint(uint64(v))
	case uint32:
		w.Uint(uint64(v))
	case uint64:
		w.Uint(v)
	case uintptr:
		w.Uint(uint64(v))
	case float64:
		w.Float(v)
	case float32:
		w.Float32(v)
	case bool:
		w.Bool(v)
	case Renderer:
		w.Render(v)
	case func(*Writer) error:
		w.Render(v)
	case error:
		w.text(errorString(v), escape)
	case fmt.Stringer:
		w.text(stringerString(v), escape)
	case []any:
		for _, e := range v {
			w.value(e, escape)
		}
	case []string:
		for _, e := range v {
			w.text(e, escape)
		}
	case []HTML:
		for _, e := range v {
			w.buf.WriteString(string(e))
		}
	case []Renderer:
		for _, e := range v {
			w.Render(e)
		}
	default:
		w.reflectValue(v, escape)
	}
}

// reflectValue handles the last-resort cases: other unnamed slices, element
// by element, and fmt.Sprint for everything else.
func (w *Writer) reflectValue(v any, escape bool) {
	rv := reflect.ValueOf(v)
	if rv.Kind() == reflect.Slice && rv.Type().Name() == "" {
		for i := range rv.Len() {
			w.value(rv.Index(i).Interface(), escape)
		}
		return
	}
	if escape {
		escapeString(w.buf, fmt.Sprint(v))
	} else {
		fmt.Fprint(w.buf, v)
	}
}

func (w *Writer) text(s string, escape bool) {
	if escape {
		escapeString(w.buf, s)
	} else {
		w.buf.WriteString(s)
	}
}

// errorString returns e.Error(). Like fmt, it prints <nil> when e is a nil
// pointer whose Error method panics.
func errorString(e error) (s string) {
	defer func() {
		if r := recover(); r != nil {
			s = nilPanic(e, r)
		}
	}()
	return e.Error()
}

// stringerString returns s.String(). Like fmt, it prints <nil> when s is a
// nil pointer whose String method panics.
func stringerString(s fmt.Stringer) (str string) {
	defer func() {
		if r := recover(); r != nil {
			str = nilPanic(s, r)
		}
	}()
	return s.String()
}

// nilPanic returns "<nil>" if v is a nil pointer, and re-panics with r
// otherwise.
func nilPanic(v any, r any) string {
	if rv := reflect.ValueOf(v); rv.Kind() == reflect.Pointer && rv.IsNil() {
		return "<nil>"
	}
	panic(r)
}

// Attributes

// Attr writes the attribute name with value v, with a leading space:
//
//   - true: the bare attribute ( name)
//   - false, nil: nothing
//   - JSONValue: always written, single-quoted ( name='json'), with ', &, <
//     and > as character references (JSONAttr)
//   - anything else: formatted as Expr does, then attribute-escaped and
//     quoted ( name="value"). Attribute values are never raw, so HTML is
//     escaped too.
//
// In a URL attribute (href, src, …), a javascript: or vbscript: URL is
// replaced with about:invalid#orbit, unless v has type URL.
func (w *Writer) Attr(name string, v any) {
	if w.err != nil {
		return
	}
	w.attr(name, v, isURLAttr(name))
}

func (w *Writer) attr(name string, v any, urlAttr bool) {
	switch v := v.(type) {
	case nil:
	case JSONValue:
		w.attrJSON(name, v)
	case bool:
		w.AttrBool(name, v)
	case string:
		w.attrValue(name, v, urlAttr)
	case URL:
		w.attrValue(name, string(v), false)
	case HTML:
		w.attrValue(name, string(v), urlAttr)
	case int:
		w.attrOpen(name)
		w.Int(int64(v))
		w.buf.WriteByte('"')
	case int64:
		w.attrOpen(name)
		w.Int(v)
		w.buf.WriteByte('"')
	case float64:
		w.attrOpen(name)
		w.Float(v)
		w.buf.WriteByte('"')
	default:
		// A named bool (type Flag bool) behaves like bool, so the generic
		// path matches the AttrBool that codegen emits for it.
		if rv := reflect.ValueOf(v); rv.Kind() == reflect.Bool {
			w.AttrBool(name, rv.Bool())
			return
		}
		// Format into a scratch buffer, then filter and escape the result
		// as one value.
		scratch := getBuffer()
		orig := w.buf
		w.buf = scratch
		w.value(v, false)
		w.buf = orig
		if w.err == nil {
			s := scratch.Bytes()
			if urlAttr && isUnsafeURL(s) {
				s = []byte(invalidURL)
			}
			w.attrOpen(name)
			escapeBytes(w.buf, s)
			w.buf.WriteByte('"')
		}
		putBuffer(scratch)
	}
}

// AttrString writes the attribute name="s", with s attribute-escaped. It
// doesn't filter URLs; codegen uses AttrURL for URL attributes.
func (w *Writer) AttrString(name, s string) {
	if w.err != nil {
		return
	}
	w.attrValue(name, s, false)
}

// AttrURL writes the attribute name="s", with s attribute-escaped and a
// javascript: or vbscript: URL replaced with about:invalid#orbit.
func (w *Writer) AttrURL(name, s string) {
	if w.err != nil {
		return
	}
	w.attrValue(name, s, true)
}

// URL writes s as a URL attribute's value, the bytes AttrURL writes between
// the quotes: s attribute-escaped, with a javascript: or vbscript: URL
// replaced with about:invalid#orbit. Codegen writes ` href="`, URL(s) and `"`
// for a typed string in a URL attribute.
func (w *Writer) URL(s string) {
	if w.err != nil {
		return
	}
	if isUnsafeURL(s) {
		s = invalidURL
	}
	escapeString(w.buf, s)
}

// AttrBool writes the bare attribute name if b is true, and nothing
// otherwise.
func (w *Writer) AttrBool(name string, b bool) {
	if w.err != nil || !b {
		return
	}
	w.buf.WriteByte(' ')
	w.buf.WriteString(name)
}

func (w *Writer) attrOpen(name string) {
	w.buf.WriteByte(' ')
	w.buf.WriteString(name)
	w.buf.WriteString(`="`)
}

func (w *Writer) attrValue(name, s string, filter bool) {
	if filter && isUnsafeURL(s) {
		s = invalidURL
	}
	w.attrOpen(name)
	escapeString(w.buf, s)
	w.buf.WriteByte('"')
}

// Spread writes every attribute in a, sorted by name, each by the Attr rules
// (URL attributes included). A name that doesn't match
// [A-Za-z_:][-A-Za-z0-9_:.]* is a render error, and nothing is written.
func (w *Writer) Spread(a Attrs) {
	if w.err != nil || len(a) == 0 {
		return
	}
	var arr [16]string
	names := arr[:0]
	for name := range a {
		if !validAttrName(name) {
			w.setErr(fmt.Errorf("orbit: invalid attribute name %q in spread", name))
			return
		}
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		w.attr(name, a[name], isURLAttr(name))
	}
}

// validAttrName reports whether name matches [A-Za-z_:][-A-Za-z0-9_:.]*.
func validAttrName(name string) bool {
	if name == "" {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		switch {
		case 'a' <= c && c <= 'z', 'A' <= c && c <= 'Z', c == '_', c == ':':
		case i > 0 && ('0' <= c && c <= '9' || c == '-' || c == '.'):
		default:
			return false
		}
	}
	return true
}

// urlAttrs are the attributes whose values are URLs: html/template's list,
// plus xlink:href.
var urlAttrs = [...]string{
	"action", "background", "cite", "codebase", "data", "formaction",
	"href", "icon", "longdesc", "manifest", "poster", "profile", "src",
	"usemap", "xmlns", "xlink:href",
}

// isURLAttr reports whether name (in any case) is a URL attribute.
func isURLAttr(name string) bool {
	for _, a := range urlAttrs {
		if len(a) == len(name) && strings.EqualFold(a, name) {
			return true
		}
	}
	return false
}

// isUnsafeURL reports whether u has a javascript: or vbscript: scheme,
// ignoring case. Like a browser's URL parser, it skips leading whitespace and
// control characters (and DEL), and ignores tabs and newlines anywhere in the
// scheme ("java\tscript:" is javascript:).
func isUnsafeURL[T ~string | ~[]byte](u T) bool {
	i := 0
	for i < len(u) && (u[i] <= ' ' || u[i] == 0x7f) {
		i++
	}
	return hasSchemeFold(u[i:], "javascript:") || hasSchemeFold(u[i:], "vbscript:")
}

// hasSchemeFold reports whether u starts with scheme (lowercase ASCII),
// ignoring ASCII case and any tab, CR or LF.
func hasSchemeFold[T ~string | ~[]byte](u T, scheme string) bool {
	j := 0
	for i := 0; i < len(u) && j < len(scheme); i++ {
		c := u[i]
		if c == '\t' || c == '\n' || c == '\r' {
			continue
		}
		if 'A' <= c && c <= 'Z' {
			c += 'a' - 'A'
		}
		if c != scheme[j] {
			return false
		}
		j++
	}
	return j == len(scheme)
}
