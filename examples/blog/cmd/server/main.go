// Command server is the blog's server: Orbit's pages behind two custom
// routes, wrapped in logging and the middleware that sets typed locals.
package main

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"time"

	"github.com/orbit-z/orbit"

	"example.com/blog/internal/posts"
	"example.com/blog/orbit_gen/app"
	"example.com/blog/orbit_gen/decl/components/likebutton"
	"example.com/blog/src/locals"
)

func main() {
	site := app.New() // generated from src/: routes, pages and assets

	// Custom routes first: more specific patterns win over "/", so these
	// take precedence over any page at the same path.
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/posts", listPosts)
	mux.HandleFunc("POST /blog/{slug}/like", like)

	// Everything else goes to Orbit: pages, islands, /_orbit/* assets,
	// public/ files and the 404 page.
	mux.Handle("/", site.Handler())

	// logging runs first (so it logs the trailing-slash redirects too), then
	// site.TrailingSlash redirects the other form of a page URL before any
	// middleware sees it, then requestInfo, then the mux and Orbit.
	if err := orbit.ListenAndServe(logging(site.TrailingSlash(requestInfo(mux)))); err != nil {
		log.Fatal(err)
	}
}

// listPosts answers GET /api/posts with every post as JSON.
func listPosts(w http.ResponseWriter, r *http.Request) {
	all, err := posts.All()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(all); err != nil {
		log.Printf("listPosts: %v", err)
	}
}

// like answers POST /blog/{slug}/like with a freshly rendered LikeButton,
// which the button's script swaps in for the old one.
func like(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	n, err := posts.Like(slug)
	if errors.Is(err, posts.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	props := likebutton.Props{Slug: slug, Likes: n}
	if err := orbit.RenderComponent(w, r, likebutton.Render, props); err != nil {
		log.Printf("like: %v", err)
		http.Error(w, "render failed", http.StatusInternalServerError)
	}
}

// requestInfo gives each request an ID and reads the theme cookie, for
// pages (locals.RequestID, locals.Theme) and handlers alike.
func requestInfo(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r, l := orbit.EnsureLocals[locals.Locals](r)
		l.RequestID = rand.Text()
		l.Theme = "light"
		if c, err := r.Cookie("theme"); err == nil && c.Value == "dark" {
			l.Theme = "dark"
		}
		w.Header().Set("X-Request-ID", l.RequestID)
		next.ServeHTTP(w, r)
	})
}

// logging logs each request with its status, duration and request ID, and
// the route Orbit matched (/blog/[slug]), if any.
func logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r, l := orbit.EnsureLocals[locals.Locals](r)
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		start := time.Now()
		next.ServeHTTP(rec, r)
		route := ""
		if l.Route.Kind != orbit.RouteNone {
			route = " route=" + l.Route.Pattern
		}
		log.Printf("%s %s %d %s id=%s%s", r.Method, r.URL.Path, rec.status, time.Since(start), l.RequestID, route)
	})
}

// statusRecorder remembers the status code a handler wrote.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }
