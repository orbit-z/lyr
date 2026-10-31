// Command server is the fixture's own server entry, copied to cmd/server by
// the test (PLAN.md §5b setup A): custom mux routes take precedence over
// the pages, a middleware adds a header to every response, and two
// middlewares fill in the typed locals with EnsureLocals.
package main

import (
	"log"
	"net/http"
	"strconv"
	"sync/atomic"

	"github.com/orbit-z/lyr"

	"example.com/site/lyr_gen/app"
	"example.com/site/lyr_gen/decl/components/likebutton"
	"example.com/site/src/locals"
)

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/ping", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("pong"))
	})
	mux.HandleFunc("POST /blog/{slug}/like", like)
	mux.Handle("/", app.New().Handler())
	if err := lyr.ListenAndServe(stamp(requestID(theme(mux)))); err != nil {
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

var lastID atomic.Int64

// requestID gives every request an ID, in its locals and in X-Request-ID.
func requestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r, l := lyr.EnsureLocals[locals.Locals](r)
		l.RequestID = "req-" + strconv.FormatInt(lastID.Add(1), 10)
		w.Header().Set("X-Request-ID", l.RequestID)
		next.ServeHTTP(w, r)
	})
}

// theme reads the theme cookie into the same locals.
func theme(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r, l := lyr.EnsureLocals[locals.Locals](r)
		l.Theme = "light"
		if c, err := r.Cookie("theme"); err == nil {
			l.Theme = c.Value
		}
		next.ServeHTTP(w, r)
	})
}

var likes atomic.Int64

// like answers an htmx request with a freshly rendered LikeButton.
func like(w http.ResponseWriter, r *http.Request) {
	if lyr.LocalsFromRequest[locals.Locals](r) == nil {
		http.Error(w, "no locals", http.StatusInternalServerError)
		return
	}
	props := likebutton.Props{Slug: r.PathValue("slug"), Likes: int(likes.Add(1))}
	if err := lyr.RenderComponent(w, r, likebutton.Render, props); err != nil {
		http.Error(w, "render failed", http.StatusInternalServerError)
	}
}
