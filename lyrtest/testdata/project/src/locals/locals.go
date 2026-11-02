// Package locals is the request data; Lyr fills Route (PLAN.md §4g),
// Cache is the request's cache handle (§4h), and Session the visitor's
// session, bound by site.Sessions (§4i).
package locals

import "github.com/orbit-z/lyr"

type SessionData struct {
	UserID int64    `json:"userId,omitempty"`
	Cart   []string `json:"cart,omitempty"`
}

type Locals struct {
	User    string
	Route   lyr.RouteInfo
	Cache   lyr.Cache
	Session lyr.Session[SessionData]
}
