package orbit

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

func slashRoutes() []Route {
	return []Route{
		{Pattern: "/", File: "src/pages/index.orbit", Render: page("home")},
		{Pattern: "/blog/[slug]", File: "src/pages/blog/[slug].orbit", Render: func(c *Ctx, w *Writer) error {
			w.Raw("post " + c.Param("slug"))
			return nil
		}},
		{Pattern: "/search", File: "src/pages/search.orbit", Render: page("search")},
		{Pattern: "/rss.xml", File: "src/pages/rss.xml.orbit", Render: page("rss")},
		{Pattern: "/docs", File: "src/pages/docs.orbit", Render: page("docs page")},
	}
}

// The redirect is a 308 with an empty body for every method, in both modes.
func TestTrailingSlashStatus(t *testing.T) {
	for _, m := range []struct {
		name           string
		opts           []Option
		target, wantTo string
	}{
		{"never", nil, "/blog/hello/?q=go", "/blog/hello?q=go"},
		{"never-explicit", []Option{WithTrailingSlash(TrailingSlashNever)}, "/search/", "/search"},
		{"always", []Option{WithTrailingSlash(TrailingSlashAlways)}, "/blog/hello?q=go", "/blog/hello/?q=go"},
	} {
		s := testSite(t, slashRoutes(), m.opts...)
		for _, method := range []string{"GET", "HEAD", "POST"} {
			next := &nextHandler{}
			rec := do(s.Middleware(next), method, m.target)
			if rec.Code != http.StatusPermanentRedirect || rec.Header().Get("Location") != m.wantTo || rec.Body.Len() != 0 || next.called {
				t.Errorf("%s: %s %s: got %d %q %q", m.name, method, m.target, rec.Code, rec.Header().Get("Location"), rec.Body)
			}
		}
	}
}

func TestTrailingSlashAlways(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "robots.txt"), "robots")
	writeFile(t, filepath.Join(dir, "docs"), "public docs") // files come before pages
	writeFile(t, filepath.Join(dir, "_orbit", "app-1234.js"), "js")
	s := testSite(t, slashRoutes(), WithStaticDir(dir), WithTrailingSlash(TrailingSlashAlways))
	for _, tt := range []struct{ target, body string }{
		{"/", "home"},
		{"/blog/hello/", "post hello"},
		{"/blog/v1.2/", "post v1.2"},
		{"/search/?q=go", "search"},
		{"/rss.xml", "rss"},
		{"/robots.txt", "robots"},
		{"/docs", "public docs"},
		{"/_orbit/app-1234.js", "js"},
	} {
		if rec := do(s.Handler(), "GET", tt.target); rec.Code != 200 || rec.Body.String() != tt.body {
			t.Errorf("GET %s: got %d %q, want 200 %q", tt.target, rec.Code, rec.Body, tt.body)
		}
	}
	// Never redirected: these reach next untouched.
	for _, target := range []string{
		"/blog/hello/x", "/blog/v1.2", "/rss.xml/", "/robots", "/x", "/blog//",
		"//evil.example", "/_orbit/x", "/_orbit/app-1234.js/",
	} {
		next := &nextHandler{}
		rec := do(s.Middleware(next), "GET", target)
		if !next.called || rec.Code != 299 {
			t.Errorf("GET %s: got %d %q, want next", target, rec.Code, rec.Header().Get("Location"))
		}
	}
	// Island URLs are answered by the island endpoint, never redirected.
	if rec := do(s.Handler(), "GET", "/_orbit/island/0000000000000000/x"); rec.Code != 404 {
		t.Errorf("island URL: got %d %q", rec.Code, rec.Header().Get("Location"))
	}
}

// Orbit first: a mux route that isn't a page in either form reaches next.
func TestTrailingSlashOrbitFirst(t *testing.T) {
	for _, m := range []struct {
		mode   TrailingSlash
		target string
	}{{TrailingSlashNever, "/api/items/"}, {TrailingSlashAlways, "/api/items"}} {
		mux := http.NewServeMux()
		mux.HandleFunc(m.target, func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("api")) })
		s := testSite(t, slashRoutes(), WithTrailingSlash(m.mode))
		rec := httptest.NewRecorder()
		s.Middleware(mux).ServeHTTP(rec, httptest.NewRequest("GET", m.target, nil))
		if rec.Code != 200 || rec.Body.String() != "api" {
			t.Errorf("mode %d: GET %s: got %d %q", m.mode, m.target, rec.Code, rec.Body)
		}
	}
}

// A caller's option after the generated one wins.
func TestWithTrailingSlashLastWins(t *testing.T) {
	s := testSite(t, slashRoutes(), WithTrailingSlash(TrailingSlashAlways), WithTrailingSlash(TrailingSlashNever))
	if rec := do(s.Handler(), "GET", "/search"); rec.Code != 200 {
		t.Errorf("GET /search: got %d", rec.Code)
	}
}

func TestServeAllocsAlways(t *testing.T) {
	if testing.Short() || raceEnabled {
		t.Skip("allocation counts (not under -short or -race)")
	}
	t.Setenv("ORBIT_DEV", "")
	s := testSite(t, allocRoutes, WithSite("https://example.com"), WithTrailingSlash(TrailingSlashAlways))
	h := s.Middleware(http.NotFoundHandler())
	for _, target := range []string{"/", "/blog/hello-world/", "/hello/"} {
		fw := newFakeResponseWriter()
		r := httptest.NewRequest("GET", target, nil)
		allocs := testing.AllocsPerRun(1000, func() {
			fw.reset()
			h.ServeHTTP(fw, r)
		})
		if fw.status != 200 {
			t.Fatalf("GET %s: got %d", target, fw.status)
		}
		if allocs != 1 {
			t.Errorf("serving %s: %v allocs, want 1 (Content-Length)", target, allocs)
		}
	}
}
