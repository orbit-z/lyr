package cache_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/orbit-z/lyr"
	"github.com/orbit-z/lyr/cache"
)

// newSite builds a site with a Memory provider and two cached pages: / is
// tagged "home", /blog varies on Accept-Language.
func newSite(t *testing.T, mem *cache.MemoryProvider, renders *atomic.Int32) (*lyr.Site, http.Handler) {
	t.Helper()
	t.Setenv("LYR_DEV", "")
	t.Setenv("LYR_STATIC", t.TempDir())
	t.Setenv("LYR_PUBLIC", "")
	s := lyr.NewSite([]lyr.Route{
		{Pattern: "/", File: "src/pages/index.lyr", Render: func(c *lyr.Ctx, w *lyr.Writer) error {
			renders.Add(1)
			c.Cache().Set(lyr.CacheOptions{MaxAge: time.Hour, Tags: []string{"home"}})
			w.Raw("<!DOCTYPE html><html><head></head><body><p>home</p></body></html>")
			return nil
		}},
		{Pattern: "/blog", File: "src/pages/blog.lyr", Render: func(c *lyr.Ctx, w *lyr.Writer) error {
			renders.Add(1)
			c.Cache().Set(lyr.CacheOptions{MaxAge: time.Hour})
			c.Header().Set("Vary", "Accept-Language")
			w.Raw("<!DOCTYPE html><html><head></head><body><p>" + c.Request.Header.Get("Accept-Language") + "</p></body></html>")
			return nil
		}},
	}, nil, lyr.WithCache(mem))
	return s, s.Middleware(http.NotFoundHandler())
}

func get(h http.Handler, target, lang string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("GET", target, nil)
	if lang != "" {
		r.Header.Set("Accept-Language", lang)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

func wantState(t *testing.T, rec *httptest.ResponseRecorder, want string) {
	t.Helper()
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if got := rec.Header().Get("X-Lyr-Cache"); got != want {
		t.Errorf("X-Lyr-Cache = %q, want %q", got, want)
	}
}

func TestSiteMissThenHit(t *testing.T) {
	var renders atomic.Int32
	mem := cache.NewMemory(cache.MemoryOptions{})
	s, h := newSite(t, mem, &renders)

	wantState(t, get(h, "/", ""), "MISS")
	rec := get(h, "/", "")
	wantState(t, rec, "HIT")
	if rec.Body.String() != "<!DOCTYPE html><html><head></head><body><p>home</p></body></html>" {
		t.Errorf("HIT body %q", rec.Body)
	}
	if renders.Load() != 1 {
		t.Errorf("%d renders, want 1", renders.Load())
	}

	if err := s.InvalidateCache(context.Background(), lyr.InvalidateOptions{Tags: []string{"home"}}); err != nil {
		t.Fatal(err)
	}
	wantState(t, get(h, "/", ""), "MISS")
	wantState(t, get(h, "/", ""), "HIT")
}

func TestSitePathInvalidationDropsVariants(t *testing.T) {
	var renders atomic.Int32
	mem := cache.NewMemory(cache.MemoryOptions{})
	s, h := newSite(t, mem, &renders)

	for _, lang := range []string{"en", "fr"} {
		wantState(t, get(h, "/blog", lang), "MISS")
		rec := get(h, "/blog", lang)
		wantState(t, rec, "HIT")
		if want := "<p>" + lang + "</p>"; !strings.Contains(rec.Body.String(), want) {
			t.Errorf("%s variant: %q", lang, rec.Body)
		}
	}
	wantState(t, get(h, "/", ""), "MISS")
	before := mem.Len() // the marker, two variants and /

	if err := s.InvalidateCache(context.Background(), lyr.InvalidateOptions{Path: "/blog"}); err != nil {
		t.Fatal(err)
	}
	if got := mem.Len(); got != before-3 || got != 1 {
		t.Errorf("Len %d → %d, want the marker and both variants dropped", before, got)
	}
	wantState(t, get(h, "/blog", "en"), "MISS")
	wantState(t, get(h, "/blog", "fr"), "MISS")
	wantState(t, get(h, "/", ""), "HIT")
}

// TestSiteVaryBy: per-user copies (CacheOptions.VaryBy) in the Memory
// provider: each variant is an entry, and tag and path purges drop them.
func TestSiteVaryBy(t *testing.T) {
	type locals struct{ Cache lyr.Cache }
	t.Setenv("LYR_DEV", "")
	t.Setenv("LYR_STATIC", t.TempDir())
	t.Setenv("LYR_PUBLIC", "")
	mem := cache.NewMemory(cache.MemoryOptions{MaxEntries: 8}) // one shard: an exact bound
	s := lyr.NewSite([]lyr.Route{{Pattern: "/dash", File: "src/pages/dash.lyr", Render: func(c *lyr.Ctx, w *lyr.Writer) error {
		c.Cache().Set(lyr.CacheOptions{MaxAge: time.Hour, Tags: []string{"dash"}, VaryBy: []string{"user"}})
		w.Raw("<p>" + c.Request.Header.Get("X-User") + "</p>")
		return nil
	}}}, nil, lyr.WithCache(mem), lyr.WithLocalsCache(func(l *locals) *lyr.Cache { return &l.Cache }))
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r, l := lyr.EnsureLocals[locals](r)
		l.Cache.Context("user", r.Header.Get("X-User"))
		s.Middleware(http.NotFoundHandler()).ServeHTTP(w, r)
	})
	user := func(u string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "/dash", nil)
		r.Header.Set("X-User", u)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		return rec
	}
	for _, opt := range []lyr.InvalidateOptions{{Tags: []string{"dash"}}, {Path: "/dash"}} {
		for _, u := range []string{"ann", "bob"} {
			wantState(t, user(u), "MISS")
		}
		rec := user("ann")
		wantState(t, rec, "HIT")
		if rec.Body.String() != "<p>ann</p>" {
			t.Errorf("ann's copy: %q", rec.Body)
		}
		if mem.Len() != 3 {
			t.Errorf("%d entries, want the marker and two variants", mem.Len())
		}
		if err := s.InvalidateCache(context.Background(), opt); err != nil {
			t.Fatal(err)
		}
		if mem.Len() != 0 {
			t.Errorf("%+v left %d entries", opt, mem.Len())
		}
	}
	// MaxEntries counts each variant: 8 entries hold the marker and 7 users.
	for i := range 10 {
		user(strings.Repeat("u", i+1))
	}
	if mem.Len() != 8 {
		t.Errorf("%d entries, want 8", mem.Len())
	}
	wantState(t, user("uuuuuuuuuu"), "HIT")
	wantState(t, user("u"), "MISS") // evicted
}
