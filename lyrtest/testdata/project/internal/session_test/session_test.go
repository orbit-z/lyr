package session_test

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/orbit-z/lyr"
	"github.com/orbit-z/lyr/cache"
	"github.com/orbit-z/lyr/lyrtest"

	"example.com/site/lyr_gen/app"
	cart "example.com/site/lyr_gen/decl/pages/cart"
	"example.com/site/src/locals"
)

// Render with a seeded session (PLAN.md §4i).
func TestRenderSession(t *testing.T) {
	html := lyrtest.Render(t, cart.Render, cart.Props{},
		lyrtest.WithSession(func(d *locals.SessionData) { d.Cart = []string{"a", "b"}; d.UserID = 7 }))
	if !strings.Contains(html, "<p>cart=a,b user=7") {
		t.Errorf("%q", html)
	}
}

// Two requests through Get: the first sets the cookie, the second sends
// it back (copied into WithRequest).
func TestGetTwoRequests(t *testing.T) {
	site := app.New(lyr.WithSessions(cache.NewSessionStore(cache.SessionStoreOptions{}), lyr.SessionOptions{TTL: time.Hour}))
	h := site.Handler()
	rec := lyrtest.Get(t, h, "/cart?add=apple")
	c := rec.Result().Cookies()
	if len(c) != 1 || c[0].Name != "lyr-session" || !c[0].HttpOnly || !c[0].Secure {
		t.Fatalf("cookie %v", rec.Header()["Set-Cookie"])
	}
	r := httptest.NewRequest("GET", "/", nil)
	r.AddCookie(&http.Cookie{Name: c[0].Name, Value: c[0].Value})
	rec = lyrtest.Get(t, h, "/cart?add=pear", lyrtest.WithRequest(r))
	if !strings.Contains(rec.Body.String(), "cart=apple,pear") || rec.Header().Get("Set-Cookie") != "" {
		t.Errorf("second request: %q %v", rec.Body, rec.Header())
	}
}

// The docs' chain: lyr.Sequence(site.TrailingSlash, site.Sessions,
// auth(site))(mux), with the pages mounted on the mux, and a sign-in
// handler.
func TestChain(t *testing.T) {
	site := app.New(lyr.WithSessions(cache.NewSessionStore(cache.SessionStoreOptions{}), lyr.SessionOptions{}))
	auth := func(site *lyr.Site) func(http.Handler) http.Handler {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				r, l := lyr.EnsureLocals[locals.Locals](r)
				d, err := l.Session.Data()
				if err != nil {
					site.ServeError(w, r, err)
					return
				}
				if d.UserID != 0 {
					l.User = "u" + strconv.FormatInt(d.UserID, 10)
				}
				next.ServeHTTP(w, r)
			})
		}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /login", func(w http.ResponseWriter, r *http.Request) {
		_, l := lyr.EnsureLocals[locals.Locals](r)
		if err := l.Session.Regenerate(); err != nil {
			site.ServeError(w, r, err)
			return
		}
		d, err := l.Session.Data()
		if err != nil {
			site.ServeError(w, r, err)
			return
		}
		d.UserID = 42
		http.Redirect(w, r, "/cart", http.StatusSeeOther)
	})
	mux.Handle("/", site.Handler())
	h := lyr.Sequence(site.TrailingSlash, site.Sessions, auth(site))(mux)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/login", nil))
	c := rec.Result().Cookies()
	if rec.Code != 303 || len(c) != 1 {
		t.Fatalf("login: %d %v", rec.Code, rec.Header())
	}
	r := httptest.NewRequest("GET", "/cart", nil)
	r.AddCookie(c[0])
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	if !strings.Contains(rec.Body.String(), "user=42 user2=u42") {
		t.Errorf("signed in: %q", rec.Body)
	}
}
