// Package benchgo compares Orbit with templ and html/template rendering the
// same page. See README.md.
package benchgo

// The generated code is committed, so go test works without these steps.
// Rerun them after changing src/, templpage/page.templ or Orbit's compiler.
//go:generate go tool orbit generate
//go:generate go run github.com/a-h/templ/cmd/templ@v0.3.1070 generate
