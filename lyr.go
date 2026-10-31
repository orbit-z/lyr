// Package lyr is the runtime for Lyr, an SSR web framework with Astro's
// developer experience and Go on the server.
//
// Most of this package is called by code that the lyr CLI generates from
// .lyr components. The parts users call directly are the Site middleware,
// the locals helpers, RenderComponent and ListenAndServe.
//
// # Generated code
//
// The names whose doc comment starts with "For generated code." are called
// by the code that lyr generate writes. They aren't meant to be called by
// hand, may change between versions, and the language server (lyr lsp)
// doesn't offer them in completion. By group:
//
//   - Pass-through attributes and spreads: Rest, RestAttr, the Rest<Kind>
//     constructors (RestString, RestInt, …, RestOpt, RestOptURL), RestFrom,
//     RestMerge, RestFromAny, RestMergeAny, SpreadCheck, SpreadOf and
//     Writer.RestClass.
//   - Optional props and set:html: Wrap, WrapIf, OptOf and SetHTML.
//   - Template literals: TemplateString, TemplateAs, StringAs, TB and its
//     methods, the Append functions, and Writer.URLMark, Writer.URLCheck,
//     Writer.ClassBytes, Writer.AttrPiece and Writer.TextPiece.
//   - class:list: JoinClasses, ClassIf, ClassString, and Writer.Class,
//     Writer.Classes, Writer.ClassAny, Writer.ClassStart and
//     Writer.ClassEnd.
//   - Dynamic tags: DynTag, Writer.TagOpen and Writer.TagClose.
//   - Rendering: Writer.Enter, Writer.Leave, Writer.HeadEnd, ErrorAt and
//     LocalsOf.
//   - server:defer and server:concurrent: Defer, Island, NewIsland,
//     Concurrent and CtxOf.
//   - Site wiring in the generated app.New(): Route, Asset, RouteRule,
//     WithRouteRules, WithLocalsRoute and WithLocalsCache. NewSite itself
//     is public API, for tests and hand-built sites.
package lyr

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
// style={…} attribute: the compiler doesn't warn about it. Lyr writes it
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

// For generated code. Asset is a stylesheet or script that a page needs, injected into <head>.
// Kind is "css", "js", "lazy" or "modulepreload"; for "lazy", ID is the
// component ID whose marker loads it. A "modulepreload" asset is a shared
// chunk that the page's "js" module imports (PLAN.md §5e): it gets a
// <link rel="modulepreload"> after the module's <script>.
type Asset struct {
	Kind string
	URL  string
	ID   string
}

// For generated code. Route is one page in the generated route table.
type Route struct {
	Pattern string                    // e.g. "/blog/[slug]"
	File    string                    // e.g. "src/pages/blog/[slug].lyr"
	Render  func(*Ctx, *Writer) error // calls the page's Render with zero Props
	Assets  []Asset                   // css/js for head injection; lazy entries for the loader
	Islands bool                      // the page has a server:defer call site
}

// NoLocals is the locals type used when a project has no src/locals.Locals.
type NoLocals struct{}

// ErrNotFound, returned from a page's frontmatter, renders the 404 page.
var ErrNotFound = errors.New("lyr: not found")

// RedirectError is returned by Ctx.Redirect. Returning it from a page's
// frontmatter sends the redirect instead of the page.
type RedirectError struct {
	Location string
	Code     int
}

func (e *RedirectError) Error() string {
	return "lyr: redirect " + strconv.Itoa(e.Code) + " to " + e.Location
}
