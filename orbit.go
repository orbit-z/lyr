// Package orbit is the runtime for Orbit, an SSR web framework with Astro's
// developer experience and Go on the server.
//
// Most of this package is called by code that the orbit CLI generates from
// .orbit components. The parts users call directly are the Site middleware,
// the locals helpers, RenderComponent and ListenAndServe.
package orbit

import (
	"errors"
	"strconv"
)

// HTML is trusted markup. It's written as-is, without escaping.
type HTML string

// URL is a trusted URL. It skips the javascript:/vbscript: scheme filtering
// that URL attributes otherwise get.
type URL string

// CSS is CSS declarations (or a CSS value) from a trusted source, for a
// style={…} attribute: the compiler doesn't warn about it. Orbit writes it
// escaped like any string and never filters it, except that a URL
// attribute still filters a javascript: or vbscript: URL (only URL skips
// that). Like html/template's template.CSS, it's a promise by the code
// that builds it: never put unvalidated input into one.
type CSS string

// Attrs is a set of attributes for a spread ({...attrs}) on an HTML element.
type Attrs map[string]any

// Renderer writes a piece of HTML, such as a slot's content.
type Renderer func(w *Writer) error

// NamedSlot is the content passed to a named slot (slot="name").
type NamedSlot struct {
	Name   string
	Render Renderer
}

// Slots is the content a component receives as children. It's passed by
// value; the zero value means "no children" and costs no allocation.
type Slots struct {
	Default Renderer
	Named   []NamedSlot
}

// Get returns the content for a slot, or nil if none was passed.
// "default" returns the default slot.
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

// Has reports whether content was passed for a slot.
func (s Slots) Has(name string) bool { return s.Get(name) != nil }

// Asset is a stylesheet or script that a page needs, injected into <head>.
// Kind is "css", "js", "lazy" or "modulepreload"; for "lazy", ID is the
// component ID whose marker loads it. A "modulepreload" asset is a shared
// chunk that the page's "js" module imports (PLAN.md §5e): it gets a
// <link rel="modulepreload"> after the module's <script>.
type Asset struct {
	Kind string
	URL  string
	ID   string
}

// Route is one page in the generated route table.
type Route struct {
	Pattern string                    // e.g. "/blog/[slug]"
	File    string                    // e.g. "src/pages/blog/[slug].orbit"
	Render  func(*Ctx, *Writer) error // calls the page's Render with zero Props
	Assets  []Asset                   // css/js for head injection; lazy entries for the loader
	Islands bool                      // the page has a server:defer call site
}

// NoLocals is the locals type used when a project has no src/locals.Locals.
type NoLocals struct{}

// ErrNotFound, returned from a page's frontmatter, renders the 404 page.
var ErrNotFound = errors.New("orbit: not found")

// RedirectError is returned by Ctx.Redirect. Returning it from a page's
// frontmatter sends the redirect instead of the page.
type RedirectError struct {
	Location string
	Code     int
}

func (e *RedirectError) Error() string {
	return "orbit: redirect " + strconv.Itoa(e.Code) + " to " + e.Location
}
