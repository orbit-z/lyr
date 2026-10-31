package main

import (
	"log"
	"net/http"

	"github.com/orbit-z/lyr"
	"github.com/orbit-z/lyr/cache"

	"example.com/lyrbench/lyr_gen/app"
)

func main() {
	// Generated from src/: routes, pages and assets. The in-memory route
	// cache stores only pages that call ctx.Cache().Set (/fetch-cached).
	site := app.New(lyr.WithCache(cache.NewMemory(cache.MemoryOptions{})))

	mux := http.NewServeMux()
	// Add your own routes here. More specific patterns win over "/":
	// mux.HandleFunc("GET /api/hello", hello)

	// Everything else goes to Lyr: pages, /_lyr/* assets and public/ files.
	mux.Handle("/", site.Handler())

	// Wrap mux in your own net/http middleware (logging, auth, locals) here.
	if err := lyr.ListenAndServe(mux); err != nil {
		log.Fatal(err)
	}
}
