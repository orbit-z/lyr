// Package lyr is a minimal runtime stub for the milestone-0 compile spike.
package lyr

import (
	"bytes"
	"fmt"
	"html"
	"strconv"
	"strings"
)

// Ctx is the per-request context (stub).
type Ctx struct {
	locals any
}

// NoLocals is used when src/middleware has no Locals type.
type NoLocals struct{}

// LocalsOf lazily allocates the typed locals.
func LocalsOf[L any](c *Ctx) *L {
	if l, ok := c.locals.(*L); ok {
		return l
	}
	l := new(L)
	c.locals = l
	return l
}

// Renderer renders a slot or fragment.
type Renderer func(w *Writer) error

// NamedSlot is one named slot.
type NamedSlot struct {
	Name   string
	Render Renderer
}

// Slots is passed by value; the zero value means no children.
type Slots struct {
	Default Renderer
	Named   []NamedSlot
}

// Get returns the named slot renderer, or nil.
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

// MaxDepth is the render-depth guard limit.
const MaxDepth = 1000

// Writer writes into a bytes.Buffer with a sticky error and a depth guard.
type Writer struct {
	buf   *bytes.Buffer
	err   error
	stack []string
}

// NewWriter wraps buf.
func NewWriter(buf *bytes.Buffer) *Writer { return &Writer{buf: buf} }

// Raw writes trusted static HTML.
func (w *Writer) Raw(s string) {
	if w.err == nil {
		w.buf.WriteString(s)
	}
}

// String writes an escaped string.
func (w *Writer) String(s string) {
	if w.err == nil {
		w.buf.WriteString(html.EscapeString(s))
	}
}

// Int writes an integer.
func (w *Writer) Int(n int64) {
	if w.err == nil {
		w.buf.WriteString(strconv.FormatInt(n, 10))
	}
}

// Expr is the untyped fallback.
func (w *Writer) Expr(v any) { w.String(fmt.Sprint(v)) }

// AttrString writes name="escaped".
func (w *Writer) AttrString(name, s string) {
	w.Raw(" " + name + "=\"" + html.EscapeString(s) + "\"")
}

// Err returns the sticky error.
func (w *Writer) Err() error { return w.err }

// Enter pushes a component onto the render stack, failing past MaxDepth.
func (w *Writer) Enter(component string) error {
	if len(w.stack) >= MaxDepth {
		w.err = fmt.Errorf("lyr: render depth exceeded %d: %s ...", MaxDepth,
			strings.Join(w.stack[len(w.stack)-5:], " > "))
		return w.err
	}
	w.stack = append(w.stack, component)
	return nil
}

// Leave pops the render stack.
func (w *Writer) Leave() { w.stack = w.stack[:len(w.stack)-1] }
