// Package lyr is a stub of the Lyr runtime for the typecheck spike.
// It imports roughly what the real runtime will (net/http, fmt, strconv, ...)
// so cold-cache numbers are realistic, and implements Expr plus the typed
// writes for real so typed-vs-Expr output can be compared byte for byte.
package lyr

import (
	"bytes"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

type Params map[string]string
type HTML string
type URL string
type Attrs map[string]any

type Ctx struct {
	Request *http.Request
	URL     *url.URL
	Params  Params
	locals  any
}

func LocalsOf[L any](c *Ctx) *L {
	if l, ok := c.locals.(*L); ok {
		return l
	}
	l := new(L)
	c.locals = l
	return l
}

type NoLocals struct{}

type Renderer func(w *Writer) error
type NamedSlot struct {
	Name   string
	Render Renderer
}
type Slots struct {
	Default Renderer
	Named   []NamedSlot
}

func (s Slots) Get(name string) Renderer {
	if name == "default" {
		return s.Default
	}
	for _, n := range s.Named {
		if n.Name == name {
			return n.Render
		}
	}
	return nil
}

type Writer struct {
	buf   *bytes.Buffer
	err   error
	depth int
}

func NewWriter(buf *bytes.Buffer) *Writer { return &Writer{buf: buf} }

func (w *Writer) Err() error { return w.err }
func (w *Writer) Enter(component string) error {
	w.depth++
	if w.depth > 1000 {
		return fmt.Errorf("lyr: render depth exceeded in %s", component)
	}
	return nil
}
func (w *Writer) Leave() { w.depth-- }

func (w *Writer) Raw(s string) { w.buf.WriteString(s) }

func (w *Writer) esc(s string) {
	if !strings.ContainsAny(s, `<>&'"`) {
		w.buf.WriteString(s)
		return
	}
	w.buf.WriteString(html.EscapeString(s))
}

// Expr is the fallback; order per PLAN.md §4.
func (w *Writer) Expr(v any) {
	switch v := v.(type) {
	case nil:
	case HTML:
		w.buf.WriteString(string(v))
	case string:
		w.esc(v)
	case URL:
		w.esc(string(v))
	case []byte:
		w.esc(string(v))
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
	case float32:
		w.Float(float64(v))
	case float64:
		w.Float(v)
	case bool:
		w.Bool(v)
	case Renderer:
		w.Render(v)
	case func(*Writer) error:
		w.Render(v)
	case error:
		w.esc(v.Error())
	case fmt.Stringer:
		w.esc(v.String())
	case []string:
		for _, s := range v {
			w.esc(s)
		}
	case []any:
		for _, e := range v {
			w.Expr(e)
		}
	default:
		w.esc(fmt.Sprint(v))
	}
}

func (w *Writer) String(s string) { w.esc(s) }
func (w *Writer) Int(n int64)     { w.buf.WriteString(strconv.FormatInt(n, 10)) }
func (w *Writer) Uint(n uint64)   { w.buf.WriteString(strconv.FormatUint(n, 10)) }
func (w *Writer) Float(f float64) { w.buf.WriteString(strconv.FormatFloat(f, 'g', -1, 64)) }
func (w *Writer) Bool(b bool)     { w.buf.WriteString(strconv.FormatBool(b)) }
func (w *Writer) HTML(h HTML)     { w.buf.WriteString(string(h)) }
func (w *Writer) Bytes(b []byte)  { w.esc(string(b)) }
func (w *Writer) Error(e error) {
	if e != nil {
		w.esc(e.Error())
	}
}
func (w *Writer) Stringer(s fmt.Stringer) {
	if s != nil {
		w.esc(s.String())
	}
}
func (w *Writer) Render(r Renderer) {
	if r == nil || w.err != nil {
		return
	}
	if err := r(w); err != nil && w.err == nil {
		w.err = err
	}
}

func (w *Writer) Attr(name string, v any) {
	switch v := v.(type) {
	case nil:
	case bool:
		w.AttrBool(name, v)
	default:
		w.buf.WriteString(" " + name + `="`)
		var b bytes.Buffer
		NewWriter(&b).Expr(v)
		w.buf.WriteString(strings.ReplaceAll(b.String(), `"`, "&#34;"))
		w.buf.WriteString(`"`)
	}
}
func (w *Writer) AttrString(name, s string) {
	w.buf.WriteString(" " + name + `="` + html.EscapeString(s) + `"`)
}
func (w *Writer) AttrBool(name string, b bool) {
	if b {
		w.buf.WriteString(" " + name)
	}
}
