package orbit

import (
	"bytes"
	"encoding/json"
)

// JSONValue is a value to write as JSON, made by JSON. It's opaque; the zero
// value encodes as null.
//
// As an attribute value (data-post={orbit.JSON(post)}) it's written
// single-quoted, and a data-* attribute of an HTML element is typed for the
// component's scripts (import { data } from "./X.orbit"). In content it's
// written as HTML-escaped text, like a string. It also implements
// json.Marshaler, so it encodes as its value anywhere encoding/json sees it.
type JSONValue struct {
	v any
}

// JSON returns v as a JSONValue: it's written exactly as json.Marshal(v)
// encodes it, escaped for where it's written.
func JSON(v any) JSONValue { return JSONValue{v} }

// MarshalJSON encodes the value, as json.Marshal(v) does.
func (j JSONValue) MarshalJSON() ([]byte, error) { return json.Marshal(j.v) }

// jsonMode says how jsonSink escapes the encoder's output.
type jsonMode uint8

const (
	// jsonScript is set:json: the content of a <script> element, with <,
	// >, &, U+2028 and U+2029 as \u escapes (json.HTMLEscape).
	jsonScript jsonMode = iota
	// jsonAttrSingle is a single-quoted attribute value: ', <, > and & as
	// character references, so the browser decodes the exact JSON text.
	jsonAttrSingle
	// jsonText is content: the JSON text HTML-escaped like a string.
	jsonText
	// jsonRaw copies the JSON text as-is.
	jsonRaw
)

// jsonSink is the io.Writer behind a Writer's reused json.Encoder: it
// escapes the encoded value straight into the Writer's buffer, dropping the
// encoder's trailing newline. encoding/json writes a whole value in one
// Write, and nothing on an error.
type jsonSink struct {
	buf  *bytes.Buffer
	mode jsonMode
}

func (s *jsonSink) Write(p []byte) (int, error) {
	n := len(p)
	if n > 0 && p[n-1] == '\n' {
		p = p[:n-1]
	}
	switch s.mode {
	case jsonScript:
		// The encoder already escapes these by default; HTMLEscape makes
		// the guarantee independent of that default.
		json.HTMLEscape(s.buf, p)
	case jsonAttrSingle:
		escapeJSONAttr(s.buf, p)
	case jsonText:
		escapeBytes(s.buf, p)
	default:
		s.buf.Write(p)
	}
	return n, nil
}

// escapeJSONAttr writes JSON text p for a single-quoted attribute value: ',
// &, < and > become character references, everything else is copied.
func escapeJSONAttr(buf *bytes.Buffer, p []byte) {
	last := 0
	for i, c := range p {
		var esc string
		switch c {
		case '\'':
			esc = "&#39;"
		case '&':
			esc = "&amp;"
		case '<':
			esc = "&lt;"
		case '>':
			esc = "&gt;"
		default:
			continue
		}
		buf.Write(p[last:i])
		buf.WriteString(esc)
		last = i + 1
	}
	buf.Write(p[last:])
}

// encodeJSON encodes v into the buffer with the Writer's reused encoder,
// escaped by mode. On an error the buffer is truncated back to where it
// was, and the error is returned.
func (w *Writer) encodeJSON(v any, mode jsonMode) error {
	if w.jenc == nil {
		w.jenc = json.NewEncoder(&w.jsink)
	}
	w.jsink.buf, w.jsink.mode = w.buf, mode
	start := w.buf.Len()
	err := w.jenc.Encode(v)
	w.jsink.buf = nil
	if err != nil {
		w.buf.Truncate(start)
	}
	return err
}

// jsonError is the render error for a value encoding/json can't encode,
// prefixed with the .orbit position when there is one.
func jsonError(pos, what string, err error) error {
	if pos == "" {
		return &jsonErr{what: "orbit: " + what, err: err}
	}
	return &posError{pos: pos, err: &jsonErr{what: what, err: err}}
}

// jsonErr keeps the encoding/json error inspectable (errors.As).
type jsonErr struct {
	what string
	err  error
}

func (e *jsonErr) Error() string { return e.what + ": " + e.err.Error() }
func (e *jsonErr) Unwrap() error { return e.err }

// JSONAttr writes v as an attribute's value, the bytes between the single
// quotes that codegen writes around it (` name='`, JSONAttr, `'`): the JSON
// text with ', &, < and > as character references. pos is the .orbit
// position of the value, for an encoding error ("src/x.orbit:9:24:
// orbit.JSON: json: unsupported value: NaN"), which becomes the sticky
// error.
func (w *Writer) JSONAttr(pos string, v JSONValue) {
	if w.err != nil {
		return
	}
	if err := w.encodeJSON(v.v, jsonAttrSingle); err != nil {
		w.setErr(jsonError(pos, "orbit.JSON", err))
	}
}

// JSON writes v encoded with encoding/json, escaped for the content of a
// <script> element: <, >, &, U+2028 and U+2029 become \u escapes, so the
// output can't contain </script> or <!--. A JSONValue is written as its
// value. An encoding error becomes the sticky error, prefixed with pos, the
// .orbit position of the set:json.
func (w *Writer) JSON(pos string, v any) {
	if w.err != nil {
		return
	}
	if j, ok := v.(JSONValue); ok {
		v = j.v
	}
	if err := w.encodeJSON(v, jsonScript); err != nil {
		w.setErr(jsonError(pos, "set:json", err))
	}
}

// attrJSON is Attr for a JSONValue: always written, single-quoted.
func (w *Writer) attrJSON(name string, v JSONValue) {
	start := w.buf.Len()
	w.buf.WriteByte(' ')
	w.buf.WriteString(name)
	w.buf.WriteString(`='`)
	if err := w.encodeJSON(v.v, jsonAttrSingle); err != nil {
		w.buf.Truncate(start)
		w.setErr(jsonError("", "orbit.JSON", err))
		return
	}
	w.buf.WriteByte('\'')
}

// textJSON writes v's JSON text in content, HTML-escaped (Expr).
// Unescaped (escape false) is only reached inside an attribute's scratch
// buffer, which is escaped as a whole.
func (w *Writer) textJSON(v JSONValue, escape bool) {
	mode := jsonText
	if !escape {
		mode = jsonRaw
	}
	if err := w.encodeJSON(v.v, mode); err != nil {
		w.setErr(jsonError("", "orbit.JSON", err))
	}
}
