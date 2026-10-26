// Package orbittest tests Orbit components and pages with go test.
//
// Render calls a component's generated Render and returns its HTML, using
// the same pooled writer and escaping as a real request. Get sends a GET
// through an http.Handler, such as app.New().Handler(). Snapshot compares
// HTML with a golden file under testdata/, rewritten with go test -update.
//
// A test imports a component through its declaration package and links all
// components with a blank import of orbit_gen/app:
//
//	import (
//		_ "example.com/blog/orbit_gen/app"
//		card "example.com/blog/orbit_gen/decl/components/card"
//	)
package orbittest

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/orbit-z/orbit"
	"github.com/orbit-z/orbit/internal/testhook"
)

// An Option sets up what Render or Get gives a component. An option passed to
// a function it doesn't apply to fails the test.
type Option struct {
	name     string // for messages, such as `WithSlot("footer", …)`
	forGet   bool   // the option applies to Get too
	apply    func(*config)
	notOnGet string // why it doesn't apply to Get
}

// config is what the options set.
type config struct {
	req    *http.Request
	locals []func(*http.Request) *http.Request // in option order
	slots  orbit.Slots
	names  []string // route params, in first-set order
	vals   []string
}

// WithSlot fills the slot name ("default", or a named slot) with raw HTML.
// A later WithSlot for the same name replaces the earlier content. Render
// only.
func WithSlot(name, html string) Option {
	content := orbit.Renderer(func(w *orbit.Writer) error {
		w.HTML(orbit.HTML(html))
		return nil
	})
	return Option{
		name:     "WithSlot(" + strconv.Quote(name) + ", …)",
		notOnGet: "a request has no slots",
		apply: func(c *config) {
			if name == "default" {
				c.slots.Default = content
				return
			}
			for i := range c.slots.Named {
				if c.slots.Named[i].Name == name {
					c.slots.Named[i].Render = content
					return
				}
			}
			c.slots.Named = append(c.slots.Named, orbit.NamedSlot{Name: name, Render: content})
		},
	}
}

// WithLocals sets the request's locals to l. With Render, components see l as
// locals; with Get, l is attached to the request, as orbit.WithLocals does,
// so orbit.EnsureLocals in your middleware returns it.
func WithLocals[L any](l *L) Option {
	return Option{
		name:   "WithLocals",
		forGet: true,
		apply: func(c *config) {
			c.locals = append(c.locals, func(r *http.Request) *http.Request { return orbit.WithLocals(r, l) })
		},
	}
}

// WithRequest sets the request. With Render, it's ctx.Request (method, URL
// and query, headers, cookies); without it, Render uses
// httptest.NewRequest("GET", "/", nil). With Get, a copy of r is sent, with
// its method set to GET and its URL set to the path. A later WithRequest
// replaces an earlier one.
func WithRequest(r *http.Request) Option {
	return Option{
		name:   "WithRequest",
		forGet: true,
		apply:  func(c *config) { c.req = r },
	}
}

// WithParam sets the route parameter name (as in the route, "slug" for
// [slug].orbit) to value, as the router would: ctx.Param(name) returns it,
// and a page's generated Render builds its typed params from it. Repeat it
// for each parameter; a later WithParam for the same name replaces the
// value, like http.Request.SetPathValue. Render only: with Get, the router
// sets params from the path.
func WithParam(name, value string) Option {
	return Option{
		name:     "WithParam(" + strconv.Quote(name) + ", …)",
		notOnGet: "the router sets params from the path",
		apply: func(c *config) {
			for i, n := range c.names {
				if n == name {
					c.vals[i] = value
					return
				}
			}
			c.names = append(c.names, name)
			c.vals = append(c.vals, value)
		},
	}
}

// configure applies opts for fn ("Render" or "Get"), failing t for an option
// that doesn't apply to fn.
func configure(t testing.TB, fn string, opts []Option) *config {
	t.Helper()
	c := new(config)
	for _, o := range opts {
		switch {
		case o.apply == nil:
			t.Fatalf("orbittest.%s: zero Option; use the With… functions", fn)
		case fn == "Get" && !o.forGet:
			t.Fatalf("orbittest.Get: %s doesn't apply to Get: %s", o.name, o.notOnGet)
		}
		o.apply(c)
	}
	return c
}

// request returns the request for c: base with c's locals attached.
func (c *config) request(base *http.Request) *http.Request {
	r := base
	for _, attach := range c.locals {
		r = attach(r)
	}
	return r
}

// Render calls render, a component's or page's generated Render, with props
// and the slots, request, locals and route params set by opts, and returns
// the HTML. It fails the test with t.Fatal if rendering returns an error.
//
// The output is what production renders for the component: the same pooled
// writer and escaping. Your middleware doesn't run and no head injection
// happens (no <link> or <script> tags), as with orbit.RenderComponent.
//
// If the test package doesn't import orbit_gen/app, the component isn't
// linked to its code and the test fails with a hint saying so.
func Render[P any](t testing.TB, render func(*orbit.Ctx, *orbit.Writer, P, orbit.Slots) error, props P, opts ...Option) string {
	t.Helper()
	if render == nil {
		t.Fatal("orbittest.Render: render func is nil")
	}
	c := configure(t, "Render", opts)
	base := c.req
	if base == nil {
		base = httptest.NewRequest(http.MethodGet, "/", nil)
	}
	r := c.request(base)
	slots := c.slots

	html, err := renderLinked(t, func() (string, error) {
		return testhook.Render(r, c.names, c.vals, func(ctx, w any) error {
			return render(ctx.(*orbit.Ctx), w.(*orbit.Writer), props, slots)
		})
	})
	if err != nil {
		t.Fatalf("orbittest.Render: %v", err)
	}
	return html
}

// renderLinked runs f, turning the "not linked" panic of a generated Render
// wrapper into a test failure that repeats its hint. Other panics propagate,
// with their stack.
func renderLinked(t testing.TB, f func() (string, error)) (html string, err error) {
	t.Helper()
	failed := ""
	func() {
		defer func() {
			p := recover()
			if p == nil {
				return
			}
			if msg, ok := notLinked(p); ok {
				failed = msg
				return
			}
			panic(p)
		}()
		html, err = f()
	}()
	if failed != "" {
		t.Fatal(failed)
	}
	return html, err
}

// notLinked recognizes the panic of an unlinked Render wrapper,
// "orbit: <file> not linked; import <module>/orbit_gen/app", and returns
// orbittest's failure message for it.
func notLinked(p any) (string, bool) {
	s, ok := p.(string)
	if !ok {
		return "", false
	}
	rest, ok := strings.CutPrefix(s, "orbit: ")
	if !ok {
		return "", false
	}
	file, pkg, ok := strings.Cut(rest, " not linked; import ")
	if !ok || file == "" || pkg == "" {
		return "", false
	}
	return "orbittest: " + file + " not linked; import " + pkg + " in this test package:\n\n\timport _ " + strconv.Quote(pkg), true
}

// Get sends a GET request for path (such as "/blog/hello?x=1") to h and
// returns the recorded response. Routing, the middleware in h and head
// injection all apply, as in production. Redirects aren't followed.
//
// Only WithLocals and WithRequest apply to Get: with WithRequest, a copy of
// its request is sent with method GET and URL path, so its headers and
// cookies carry over.
func Get(t testing.TB, h http.Handler, path string, opts ...Option) *httptest.ResponseRecorder {
	t.Helper()
	if h == nil {
		t.Fatal("orbittest.Get: handler is nil")
	}
	c := configure(t, "Get", opts)
	u, err := url.ParseRequestURI(path)
	if err != nil || !strings.HasPrefix(path, "/") {
		t.Fatalf("orbittest.Get(%q): the path must start with / (such as \"/blog/hello\")", path)
	}
	var r *http.Request
	if c.req == nil {
		r = httptest.NewRequest(http.MethodGet, path, nil)
	} else {
		r = c.req.Clone(c.req.Context())
		r.Method = http.MethodGet
		r.URL = u
		r.RequestURI = path
		r.Body = http.NoBody
		r.ContentLength = 0
		r.GetBody = nil
		if r.Host == "" {
			r.Host = "example.com"
		}
	}
	r = c.request(r)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}
