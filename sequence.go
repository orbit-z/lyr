package lyr

import (
	"fmt"
	"net/http"
)

// Sequence combines middleware into one, like Astro's sequence(): the
// middleware listed first runs first. Sequence(a, b, c)(h) is a(b(c(h))).
//
// The chain is built once, when the returned middleware is called, so a
// request pays nothing for Sequence itself. Method values such as
// site.TrailingSlash and site.Middleware fit as they are. Sequence() with
// no middleware returns next unchanged. A nil middleware panics.
func Sequence(mws ...func(http.Handler) http.Handler) func(http.Handler) http.Handler {
	for i, mw := range mws {
		if mw == nil {
			panic(fmt.Sprintf("lyr: Sequence: nil middleware at index %d", i))
		}
	}
	mws = append([]func(http.Handler) http.Handler(nil), mws...)
	return func(next http.Handler) http.Handler {
		for i := len(mws) - 1; i >= 0; i-- {
			next = mws[i](next)
		}
		return next
	}
}
