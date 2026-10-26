// Command server serves the Orbit documentation site.
package main

import (
	"log"
	"net/http"

	"github.com/orbit-z/orbit"

	"github.com/orbit-z/orbit/docs/site/internal/content"
	"github.com/orbit-z/orbit/docs/site/orbit_gen/app"
	"github.com/orbit-z/orbit/docs/site/src/locals"
)

func main() {
	// Load the docs at startup, so a broken docs directory fails here
	// rather than on the first request.
	docs, err := content.Get()
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("docs: %d pages from %s", len(docs.Pages), docs.Source)
	for _, p := range docs.Problems {
		log.Printf("docs: %s", p)
	}

	site := app.New() // generated from src/: routes, pages and assets

	mux := http.NewServeMux()
	// Everything goes to Orbit: pages, /_orbit/* assets and public/ files.
	mux.Handle("/", site.Handler())

	if err := orbit.ListenAndServe(theme(mux)); err != nil {
		log.Fatal(err)
	}
}

// theme reads the visitor's "theme" cookie, set by the theme toggle, into
// the typed locals that the layout renders as <html data-theme>.
func theme(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r, l := orbit.EnsureLocals[locals.Locals](r)
		if c, err := r.Cookie("theme"); err == nil && (c.Value == "light" || c.Value == "dark") {
			l.Theme = c.Value
		}
		next.ServeHTTP(w, r)
	})
}
