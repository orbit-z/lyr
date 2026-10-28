package cache_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/orbit-z/orbit"
	"github.com/orbit-z/orbit/cache"
	"github.com/orbit-z/orbit/orbittest"

	"example.com/site/orbit_gen/app"
	"example.com/site/src/locals"
)

// Get through a site built with WithCache caches like production: MISS,
// then HIT, with the orbit.json rule and the page's tag (PLAN.md §4h).
func TestGetCaches(t *testing.T) {
	site := app.New(orbit.WithCache(cache.NewMemory(cache.MemoryOptions{})))
	h := site.Handler()
	if got := orbittest.Get(t, h, "/cached/a").Header().Get("X-Orbit-Cache"); got != "MISS" {
		t.Fatalf("first GET: X-Orbit-Cache %q", got)
	}
	rec := orbittest.Get(t, h, "/cached/a")
	if got := rec.Header().Get("X-Orbit-Cache"); got != "HIT" || !strings.Contains(rec.Body.String(), "<p>item=a user=</p>") {
		t.Fatalf("second GET: %q %q", got, rec.Body)
	}
	// Locals passed with WithLocals hold the request's handle.
	var l locals.Locals
	orbittest.Get(t, h, "/cached/b", orbittest.WithLocals(&l))
	if o := l.Cache.Options(); o.MaxAge.Seconds() != 60 || strings.Join(o.Tags, ",") != "cached,item:b" {
		t.Errorf("Locals.Cache %+v", o)
	}
	// Without WithCache nothing is cached.
	if got := orbittest.Get(t, app.New().Handler(), "/cached/a").Header().Get("X-Orbit-Cache"); got != "" {
		t.Errorf("no provider: X-Orbit-Cache %q", got)
	}
}

// Over HTTP: X-Orbit-Cache, a tag purge from an admin handler, and the
// middleware keeping a signed-in visitor out of the cache.
func TestHTTP(t *testing.T) {
	site := app.New(orbit.WithCache(cache.NewMemory(cache.MemoryOptions{})))
	mux := http.NewServeMux()
	mux.HandleFunc("POST /admin/purge", func(w http.ResponseWriter, r *http.Request) {
		if err := site.InvalidateCache(r.Context(), orbit.InvalidateOptions{Tags: []string{"item:" + r.FormValue("id")}}); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	})
	mux.Handle("/", site.Handler())
	auth := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r, l := orbit.EnsureLocals[locals.Locals](r)
		if c, err := r.Cookie("session"); err == nil {
			l.User = c.Value
			l.Cache.Disable()
		}
		mux.ServeHTTP(w, r)
	})
	srv := httptest.NewServer(auth)
	defer srv.Close()

	get := func(path, cookie string) (state, body string) {
		t.Helper()
		req, _ := http.NewRequest("GET", srv.URL+path, nil)
		if cookie != "" {
			req.Header.Set("Cookie", "session="+cookie)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.Header.Get("X-Orbit-Cache"), string(b)
	}
	for i, want := range []string{"MISS", "HIT"} {
		if state, _ := get("/cached/x", ""); state != want {
			t.Errorf("request %d: X-Orbit-Cache %q, want %q", i, state, want)
		}
	}
	if state, body := get("/cached/x", "ann"); state != "" || !strings.Contains(body, "user=ann") {
		t.Errorf("signed in: %q %q", state, body)
	}
	if state, body := get("/cached/x", ""); state != "HIT" || !strings.Contains(body, "user=</p>") {
		t.Errorf("anonymous after a signed-in visit: %q %q", state, body)
	}
	resp, err := http.Post(srv.URL+"/admin/purge?id=x", "text/plain", nil)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("purge: %v %v", resp, err)
	}
	resp.Body.Close()
	if state, _ := get("/cached/x", ""); state != "MISS" {
		t.Errorf("after the purge: X-Orbit-Cache %q", state)
	}
}
