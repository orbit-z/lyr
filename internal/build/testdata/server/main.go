// Command server is the fixture's own server entry, copied to cmd/server by
// the test (PLAN.md §5b setup A): a custom mux route takes precedence over
// the pages, and a middleware adds a header to every response.
package main

import (
	"log"
	"net/http"

	"github.com/bertrandkhe/orbit"

	"example.com/site/orbit_gen/app"
)

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/ping", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("pong"))
	})
	mux.Handle("/", app.New().Handler())
	if err := orbit.ListenAndServe(stamp(mux)); err != nil {
		log.Fatal(err)
	}
}

// stamp adds X-Fixture-Server to every response.
func stamp(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Fixture-Server", "user")
		next.ServeHTTP(w, r)
	})
}
