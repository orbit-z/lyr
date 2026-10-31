// Command server serves the Lyr documentation site.
//
// Environment: PORT or LYR_ADDR (where to listen, see lyr.ListenAndServe),
// SITE_URL (the site's public URL, overriding lyr.json's "site"; used for
// canonical URLs, Open Graph tags, the sitemap and robots.txt) and DOCS_DIR
// (read the Markdown from that directory instead of the embedded copy).
package main

import (
	"log"
	"net/http"
	"os"

	"github.com/orbit-z/lyr"

	"github.com/orbit-z/lyr/docs/site/internal/content"
	"github.com/orbit-z/lyr/docs/site/lyr_gen/app"
	"github.com/orbit-z/lyr/docs/site/src/locals"
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

	var opts []lyr.Option
	if u := os.Getenv("SITE_URL"); u != "" {
		opts = append(opts, lyr.WithSite(u))
	}
	site := app.New(opts...) // generated from src/: routes, pages and assets

	mux := http.NewServeMux()
	// For load balancers and platform probes: the distroless image has no
	// shell for a Docker HEALTHCHECK.
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write([]byte("ok\n"))
	})
	// Everything else goes to Lyr: pages, /_lyr/* assets and public/ files.
	mux.Handle("/", site.Handler())

	// Trailing-slash redirects first, as in Astro.
	if err := lyr.ListenAndServe(site.TrailingSlash(gzipped(theme(mux)))); err != nil {
		log.Fatal(err)
	}
}

// theme reads the visitor's "theme" cookie, set by the theme toggle, into
// the typed locals that the layout renders as <html data-theme>.
func theme(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r, l := lyr.EnsureLocals[locals.Locals](r)
		if c, err := r.Cookie("theme"); err == nil && (c.Value == "light" || c.Value == "dark") {
			l.Theme = c.Value
		}
		next.ServeHTTP(w, r)
	})
}
