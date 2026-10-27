package orbit

import (
	"iter"
	"net/http"
)

// The matched route, for middleware and logging (PLAN.md §4g): a RouteInfo
// field in the project's Locals, filled by Orbit.

// RouteKind says what answered a request; see RouteInfo.
type RouteKind uint8

const (
	// RouteNone: Orbit didn't render this request (a static file, a
	// trailing-slash redirect, a request passed to next, or one that never
	// reached Orbit).
	RouteNone RouteKind = iota
	// RoutePage: a page route matched, whatever the outcome (200, a
	// redirect, an error, ErrNotFound).
	RoutePage
	// RouteNotFound: Site.NotFound answered, with 404.orbit or the empty
	// 404.
	RouteNotFound
	// RouteIsland: a /_orbit/island/ request.
	RouteIsland
)

// RouteInfo is the route that answered a request. Declare a field of this
// type in your Locals (src/locals), and Orbit fills it:
//
//	type Locals struct {
//		Route orbit.RouteInfo
//		// …
//	}
//
// Orbit fills it as soon as the request is matched and before rendering, so
// an error, a panic or a redirect still leaves it set. Middleware that
// attached the locals (EnsureLocals) reads it after next returns, and pages
// and components read locals.Route. Each request through Orbit sets it
// again, so a request that goes through Orbit twice keeps the last value.
//
// The strings stay valid after the request and may be kept. Read it on the
// request's goroutine (or after next returns): it isn't synchronized.
type RouteInfo struct {
	Kind RouteKind
	// Pattern is the route's pattern: "/blog/[slug]" for a page, "/404"
	// for Site.NotFound, "/_orbit/island/[buildID]/[id]" for an island.
	Pattern string
	// File is the page's .orbit file, src/pages/404.orbit, or the deferred
	// component's file; "" when there's none (the empty 404, an unknown
	// island).
	File string

	names []string  // the route's param names (static)
	n     int       // the number of values in buf
	buf   [8]string // the param values, when there are at most 8
	more  []string  // the param values, when there are more than 8
}

// Param returns the percent-decoded value of the route parameter name, like
// Ctx.Param, or "" when the route has no such parameter.
func (ri *RouteInfo) Param(name string) string {
	vals := ri.values()
	for i, n := range ri.names {
		if n == name && i < len(vals) {
			return vals[i]
		}
	}
	return ""
}

// Params returns the route's parameters, name and value, in the route's
// order:
//
//	for name, value := range l.Route.Params() { … }
func (ri *RouteInfo) Params() iter.Seq2[string, string] {
	return func(yield func(string, string) bool) {
		vals := ri.values()
		for i, n := range ri.names {
			if i >= len(vals) || !yield(n, vals[i]) {
				return
			}
		}
	}
}

func (ri *RouteInfo) values() []string {
	if ri.more != nil {
		return ri.more
	}
	return ri.buf[:ri.n]
}

// set copies ref into ri.
func (ri *RouteInfo) set(ref *routeRef) {
	ri.setTo(ref.kind, ref.pattern, ref.file, ref.names, ref.vals)
}

// setTo sets ri. Up to 8 values go into ri's own array; more allocate one
// slice. vals itself is never kept (separate parameters keep a caller's
// vals off the heap: escape analysis doesn't tell struct fields apart).
func (ri *RouteInfo) setTo(kind RouteKind, pattern, file string, names, vals []string) {
	ri.Kind, ri.Pattern, ri.File, ri.names = kind, pattern, file, names
	if len(vals) <= len(ri.buf) {
		ri.n = copy(ri.buf[:], vals)
		clear(ri.buf[ri.n:])
		ri.more = nil
		return
	}
	ri.n = 0
	clear(ri.buf[:])
	ri.more = append([]string(nil), vals...)
}

// routeRef is the route of the request a Ctx renders, kept on the Ctx while
// the request runs, when the site fills a RouteInfo; vals may point into the
// Ctx's storage.
type routeRef struct {
	kind          RouteKind
	pattern, file string
	names, vals   []string
	// field is WithLocalsRoute's func(*L) *RouteInfo, so LocalsOf can fill
	// a Locals it allocates.
	field any
}

// localsRoute is WithLocalsRoute's accessor.
type localsRoute struct {
	// attached returns the request's attached *L (as any) and its
	// RouteInfo, or nil, nil.
	attached func(r *http.Request) (any, *RouteInfo)
	field    any // func(*L) *RouteInfo
}

// WithLocalsRoute tells the site where the RouteInfo field of your Locals
// is. The generated app.New() passes it, before the caller's options, when
// src/locals declares one:
//
//	orbit.WithLocalsRoute(func(l *locals.Locals) *orbit.RouteInfo { return &l.Route })
//
// You don't need to call it yourself.
func WithLocalsRoute[L any](field func(*L) *RouteInfo) Option {
	lr := &localsRoute{
		attached: func(r *http.Request) (any, *RouteInfo) {
			if l := LocalsFromRequest[L](r); l != nil {
				return l, field(l)
			}
			return nil, nil
		},
		field: field,
	}
	return func(o *siteOptions) { o.route = lr }
}

// Island route values (RouteInfo of a /_orbit/island/ request).
const islandPattern = "/_orbit/island/[buildID]/[id]"

var islandParamNames = []string{"buildID", "id"}

// notFoundRoutePattern is RouteInfo.Pattern for Site.NotFound (Astro's
// routePattern for its 404 route).
const notFoundRoutePattern = "/404"

// fillRoute records c.route on the request's attached locals, if any, and
// caches them on c, so the page's LocalsOf doesn't look them up again.
// Without attached locals, LocalsOf fills the Locals it allocates. It's
// called only when s.route is set.
func (s *Site) fillRoute(r *http.Request, c *Ctx) {
	c.route.field = s.route.field
	l, ri := s.route.attached(r)
	if ri == nil {
		return
	}
	ri.set(&c.route)
	if c.locals == nil {
		c.locals = l
	}
}

// fillRouteOnly records a route on the request's attached locals, for
// answers that render nothing (the empty 404) or before an island renders.
func (s *Site) fillRouteOnly(r *http.Request, kind RouteKind, pattern, file string, names, vals []string) {
	if _, ri := s.route.attached(r); ri != nil {
		ri.setTo(kind, pattern, file, names, vals)
	}
}
