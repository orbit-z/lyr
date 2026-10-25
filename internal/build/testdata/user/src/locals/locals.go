// Package locals declares the request-scoped data that middleware hands to
// pages.
package locals

type Locals struct {
	RequestID string
	Theme     string
}
