// Package locals declares the request-scoped data that the middleware in
// cmd/server hands to pages and components.
package locals

type Locals struct {
	RequestID string
	Theme     string // "light" or "dark"
}
