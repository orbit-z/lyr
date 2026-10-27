// Package locals is the request data; Orbit fills Route (PLAN.md §4g), and
// Cache is the request's cache handle (§4h).
package locals

import "github.com/orbit-z/orbit"

type Locals struct {
	User  string
	Route orbit.RouteInfo
	Cache orbit.Cache
}
