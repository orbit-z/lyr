package orbit

import (
	"context"
	"fmt"
	"net/http"
)

// localsKey is the request-context key for a *L. It's generic, so different
// locals types never collide.
type localsKey[L any] struct{}

// For generated code. LocalsOf defines locals: it returns the request's
// locals, never nil. If middleware attached an *L to c.Request (with
// EnsureLocals or WithLocals), it returns that pointer; otherwise it
// allocates a zero L on first use. The value is cached on c, so every
// component in the request shares it.
func LocalsOf[L any](c *Ctx) *L {
	if c.locals != nil {
		l, ok := c.locals.(*L)
		if !ok {
			panic(fmt.Sprintf("orbit: LocalsOf wants %T but this request's locals are a %T; "+
				"the generated code uses two locals types (run orbit generate)", (*L)(nil), c.locals))
		}
		return l
	}
	var l *L
	if c.Request != nil {
		l = LocalsFromRequest[L](c.Request)
	}
	if l == nil {
		l = new(L)
		if c.route.field != nil {
			// The site fills a RouteInfo field (WithLocalsRoute).
			if f, ok := c.route.field.(func(*L) *RouteInfo); ok {
				f(l).set(&c.route)
			}
		}
		if c.cacheField != nil {
			// The Cache field becomes the request's handle (WithLocalsCache).
			moveCacheToLocals(c, l)
		}
	}
	c.locals = l
	return l
}

// EnsureLocals is the call to use in net/http middleware:
//
//	r, l := orbit.EnsureLocals[locals.Locals](r)
//
// If r already carries an *L, it returns r unchanged and that pointer.
// Otherwise it attaches a new zero L and returns the new request. Every
// middleware in the chain can make the same call, and they all share one
// value. Pass the returned r on to next.
func EnsureLocals[L any](r *http.Request) (*http.Request, *L) {
	if l := LocalsFromRequest[L](r); l != nil {
		return r, l
	}
	l := new(L)
	return WithLocals(r, l), l
}

// WithLocals returns a shallow copy of r carrying l. If r already carries an
// *L, handlers further down see l, while middleware that ran earlier keeps its
// own pointer, as with context.WithValue. Prefer EnsureLocals, so that all
// middleware shares one value.
func WithLocals[L any](r *http.Request, l *L) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), localsKey[L]{}, l))
}

// LocalsFromRequest returns the *L attached to r, or nil if none is. Use it in
// your own handlers, and check for nil when they might run without the
// middleware that attaches locals.
func LocalsFromRequest[L any](r *http.Request) *L {
	l, _ := r.Context().Value(localsKey[L]{}).(*L)
	return l
}
