// Package locals is the request data; Lyr fills Route (PLAN.md §4g), and
// Cache is the request's cache handle (§4h).
package locals

import "github.com/orbit-z/lyr"

type Locals struct {
	User  string
	Route lyr.RouteInfo
	Cache lyr.Cache
}
