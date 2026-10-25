// Package orbit is a tiny stub of the Orbit runtime, for the LSP spike only.
package orbit

import (
	"net/http"
	"strings"
)

// Ctx is the per-render context.
type Ctx struct{ Request *http.Request }

type NoLocals struct{}

func LocalsOf[L any](c *Ctx) *L { return new(L) }

type Writer struct {
	b   strings.Builder
	err error
}

func (w *Writer) Raw(s string)                 { w.b.WriteString(s) }
func (w *Writer) Expr(v any)                   {}
func (w *Writer) Attr(name string, v any)      {}
func (w *Writer) String(s string)              { w.b.WriteString(s) }
func (w *Writer) Enter(component string) error { return nil }
func (w *Writer) Leave()                       {}
func (w *Writer) Err() error                   { return w.err }

type Renderer func(w *Writer) error

type NamedSlot struct {
	Name   string
	Render Renderer
}

type Slots struct {
	Default Renderer
	Named   []NamedSlot
}
