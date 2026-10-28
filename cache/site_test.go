package cache_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/orbit-z/orbit"
	"github.com/orbit-z/orbit/cache"
)

// newSite builds a site with a Memory provider and two cached pages: / is
// tagged "home", /blog varies on Accept-Language.
func newSite(t *testing.T, mem *cache.MemoryProvider, renders *atomic.Int32) (*orbit.Site, http.Handler) {
	t.Helper()
	t.Setenv("ORBIT_DEV", "")
	t.Setenv("ORBIT_STATIC", t.TempDir())
	t.Setenv("ORBIT_PUBLIC", "")
	s := orbit.NewSite([]orbit.Route{
		{Pattern: "/", File: "src/pages/index.orbit", Render: func(c *orbit.Ctx, w *orbit.Writer) error {
			renders.Add(1)
			c.Cache().Set(orbit.CacheOptions{MaxAge: time.Hour, Tags: []string{"home"}})
			w.Raw("<!DOCTYPE html><html><head></head><body><p>home</p></body></html>")
			return nil
		}},
		{Pattern: "/blog", File: "src/pages/blog.orbit", Render: func(c *orbit.Ctx, w *orbit.Writer) error {
			renders.Add(1)
			c.Cache().Set(orbit.CacheOptions{MaxAge: time.Hour})
			c.Header().Set("Vary", "Accept-Language")
			w.Raw("<!DOCTYPE html><html><head></head><body><p>" + c.Request.Header.Get("Accept-Language") + "</p></body></html>")
			return nil
		}},
	}, nil, orbit.WithCache(mem))
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
	if got := rec.Header().Get("X-Orbit-Cache"); got != want {
		t.Errorf("X-Orbit-Cache = %q, want %q", got, want)
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

	if err := s.InvalidateCache(context.Background(), orbit.InvalidateOptions{Tags: []string{"home"}}); err != nil {
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

	if err := s.InvalidateCache(context.Background(), orbit.InvalidateOptions{Path: "/blog"}); err != nil {
		t.Fatal(err)
	}
	if got := mem.Len(); got != before-3 || got != 1 {
		t.Errorf("Len %d → %d, want the marker and both variants dropped", before, got)
	}
	wantState(t, get(h, "/blog", "en"), "MISS")
	wantState(t, get(h, "/blog", "fr"), "MISS")
	wantState(t, get(h, "/", ""), "HIT")
}
