// Package locals declares the request-scoped data that middleware in
// cmd/server hands to pages and components.
package locals

type Locals struct {
	// Theme is "light" or "dark" when the visitor picked one with the theme
	// toggle (the "theme" cookie), or "" to follow prefers-color-scheme.
	Theme string
}
