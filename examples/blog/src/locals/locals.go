// Package locals declares the request-scoped data that the middleware in
// cmd/server hands to pages and components.
package locals

import "github.com/orbit-z/orbit"

type Locals struct {
	RequestID string
	Theme     string // "light" or "dark"
	// Route is the page, 404 or island route that answered the request.
	// Orbit fills it; the logging middleware prints its pattern.
	Route orbit.RouteInfo
}
