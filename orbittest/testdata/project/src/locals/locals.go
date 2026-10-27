// Package locals is the request data; Orbit fills Route (PLAN.md §4g).
package locals

import "github.com/orbit-z/orbit"

type Locals struct {
	User  string
	Route orbit.RouteInfo
}
