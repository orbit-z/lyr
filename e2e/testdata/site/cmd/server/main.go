// Command server serves the e2e fixture under a strict Content Security
// Policy, with htmx at /htmx.js from the file $E2E_HTMX.
package main

import (
	"log"
	"net/http"
	"os"

	"github.com/orbit-z/lyr"

	"example.com/site/lyr_gen/app"
)

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /htmx.js", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/javascript")
		w.Header().Set("Cache-Control", "no-store")
		http.ServeFile(w, r, os.Getenv("E2E_HTMX"))
	})
	mux.Handle("/", app.New().Handler())
	if err := lyr.ListenAndServe(csp(mux)); err != nil {
		log.Fatal(err)
	}
}

// csp sets a strict policy: no inline script or style, no eval.
func csp(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy",
			"default-src 'self'; script-src 'self'; style-src 'self'; object-src 'none'; base-uri 'self'")
		next.ServeHTTP(w, r)
	})
}
