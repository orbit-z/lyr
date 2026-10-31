package lyr

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func slashRoutes() []Route {
	return []Route{
		{Pattern: "/", File: "src/pages/index.lyr", Render: page("home")},
		{Pattern: "/blog/[slug]", File: "src/pages/blog/[slug].lyr", Render: func(c *Ctx, w *Writer) error {
			w.Raw("post " + c.Param("slug"))
			return nil
		}},
		{Pattern: "/search", File: "src/pages/search.lyr", Render: page("search")},
		{Pattern: "/rss.xml", File: "src/pages/rss.xml.lyr", Render: page("rss")},
		{Pattern: "/docs", File: "src/pages/docs.lyr", Render: page("docs page")},
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
	writeFile(t, filepath.Join(dir, "_lyr", "app-1234.js"), "js")
	s := testSite(t, slashRoutes(), WithStaticDir(dir), WithTrailingSlash(TrailingSlashAlways))
	for _, tt := range []struct{ target, body string }{
		{"/", "home"},
		{"/blog/hello/", "post hello"},
		{"/blog/v1.2/", "post v1.2"},
		{"/search/?q=go", "search"},
		{"/rss.xml", "rss"},
		{"/robots.txt", "robots"},
		{"/docs", "public docs"},
		{"/_lyr/app-1234.js", "js"},
	} {
		if rec := do(s.Handler(), "GET", tt.target); rec.Code != 200 || rec.Body.String() != tt.body {
			t.Errorf("GET %s: got %d %q, want 200 %q", tt.target, rec.Code, rec.Body, tt.body)
		}
	}
	// Never redirected: these reach next untouched.
	for _, target := range []string{
		"/blog/hello/x", "/blog/v1.2", "/rss.xml/", "/robots", "/x", "/blog//",
		"//evil.example", "/_lyr/x", "/_lyr/app-1234.js/",
	} {
		next := &nextHandler{}
		rec := do(s.Middleware(next), "GET", target)
		if !next.called || rec.Code != 299 {
			t.Errorf("GET %s: got %d %q, want next", target, rec.Code, rec.Header().Get("Location"))
		}
	}
	// Island URLs are answered by the island endpoint, never redirected.
	if rec := do(s.Handler(), "GET", "/_lyr/island/0000000000000000/x"); rec.Code != 404 {
		t.Errorf("island URL: got %d %q", rec.Code, rec.Header().Get("Location"))
	}
}

// Lyr first: a mux route that isn't a page in either form reaches next.
func TestTrailingSlashLyrFirst(t *testing.T) {
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
	t.Setenv("LYR_DEV", "")
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

// Tests of site.TrailingSlash, the redirect before the user's middleware
// (PLAN.md §4e "Redirecting before middleware").

// slashFirstSite is slashRoutes with public files (robots.txt, and docs
// and LICENSE without an extension, docs named like a page) and an
// /_lyr/ asset.
func slashFirstSite(t *testing.T, mode TrailingSlash) *Site {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "robots.txt"), "robots")
	writeFile(t, filepath.Join(dir, "docs"), "public docs")
	writeFile(t, filepath.Join(dir, "LICENSE"), "MIT")
	writeFile(t, filepath.Join(dir, "_lyr", "app-1234.js"), "js")
	return testSite(t, slashRoutes(), WithStaticDir(dir), WithTrailingSlash(mode))
}

func TestSiteTrailingSlash(t *testing.T) {
	type redirect struct{ target, to string }
	for _, dev := range []string{"", "1"} {
		for _, m := range []struct {
			mode      TrailingSlash
			redirects []redirect
			passes    []string
		}{
			{TrailingSlashNever, []redirect{
				{"/blog/hello/", "/blog/hello"},
				{"/blog/hello/?q=go&x=1", "/blog/hello?q=go&x=1"},
				{"/blog/a%20b/", "/blog/a%20b"},
				{"/search/", "/search"},
				{"/rss.xml/", "/rss.xml"},
			}, []string{
				"/", "/blog/hello", "/search?q=go", "/docs", "/rss.xml", "/x",
				"/unknown/", "/x//", "/blog//", "//evil.example/", "/_lyr/x/", "/_lyr/app-1234.js/",
				"/_lyr/island/0000000000000000/x/", "/robots.txt",
			}},
			{TrailingSlashAlways, []redirect{
				{"/blog/hello", "/blog/hello/"},
				{"/blog/hello?q=go&x=1", "/blog/hello/?q=go&x=1"},
				{"/blog/a%20b", "/blog/a%20b/"},
				{"/search", "/search/"},
			}, []string{
				"/", "/blog/hello/", "/search/?q=go", "/rss.xml", "/robots.txt", "/LICENSE", "/_lyr/app-1234.js",
				"/blog/v1.2", "/rss.xml/", "/unknown", "/x//", "//evil.example", "/_lyr/x",
				"/_lyr/island/0000000000000000/x",
			}},
		} {
			t.Setenv("LYR_DEV", dev)
			s := slashFirstSite(t, m.mode)
			name := "mode=" + modeName[m.mode] + " dev=" + dev
			for _, method := range []string{"GET", "HEAD", "POST"} {
				gate := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					t.Errorf("%s: %s %s: the gate ran", name, method, r.URL)
				})
				redirects := m.redirects
				if m.mode == TrailingSlashAlways && method == "POST" {
					// public/docs is a file for GET and HEAD only.
					redirects = append(redirects, redirect{"/docs", "/docs/"})
				}
				for _, tt := range redirects {
					rec := do(s.TrailingSlash(gate), method, tt.target)
					if rec.Code != http.StatusPermanentRedirect || rec.Header().Get("Location") != tt.to ||
						rec.Body.Len() != 0 || rec.Header().Get("Cache-Control") != "" {
						t.Errorf("%s: %s %s: got %d %q %q, want 308 %q", name, method, tt.target,
							rec.Code, rec.Header().Get("Location"), rec.Body, tt.to)
					}
				}
				passes := m.passes
				if m.mode == TrailingSlashAlways && method != "POST" {
					passes = append(passes, "/docs")
				}
				for _, target := range passes {
					req := httptest.NewRequest(method, target, nil)
					var got *http.Request
					h := s.TrailingSlash(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						got = r
						w.WriteHeader(299)
					}))
					rec := httptest.NewRecorder()
					h.ServeHTTP(rec, req)
					if got != req || rec.Code != 299 {
						t.Errorf("%s: %s %s: got %d %q, want next with the same request", name, method, target,
							rec.Code, rec.Header().Get("Location"))
					}
				}
			}
		}
	}
}

// TestTrailingSlashAgrees: for TestSiteMatchAgrees's corpus, in both modes,
// prod and dev, site.TrailingSlash redirects exactly when site.Middleware
// does, to the same Location.
func TestTrailingSlashAgrees(t *testing.T) {
	captureLog(t)
	redirects := 0
	for _, dev := range []string{"", "1"} {
		for _, mode := range []TrailingSlash{TrailingSlashNever, TrailingSlashAlways} {
			t.Setenv("LYR_DEV", dev)
			s := matchSite(t, WithTrailingSlash(mode))
			first := s.TrailingSlash(&nextHandler{})
			inside := s.Middleware(&nextHandler{})
			for _, p := range matchCorpus {
				p = strings.Replace(p, "BUILD", s.keys.buildID, 1)
				for _, method := range matchMethods {
					a, b := do(first, method, p), do(inside, method, p)
					aRedir, bRedir := a.Code == http.StatusPermanentRedirect, b.Code == http.StatusPermanentRedirect
					// A page's own redirect (/redirect: 302) isn't the slash's.
					if aRedir != bRedir || aRedir && a.Header().Get("Location") != b.Header().Get("Location") {
						t.Errorf("%s %s dev=%s mode=%s: TrailingSlash %d %q, Middleware %d %q", method, p, dev, modeName[mode],
							a.Code, a.Header().Get("Location"), b.Code, b.Header().Get("Location"))
					}
					if aRedir {
						redirects++
					}
				}
			}
		}
	}
	if redirects == 0 {
		t.Error("the corpus has no redirect")
	}
}

// One redirect, then the page: site.TrailingSlash before site.Middleware,
// and the scaffold's shape (a mux with site.Handler() as its catch-all).
func TestTrailingSlashOneRedirect(t *testing.T) {
	for _, m := range []struct {
		mode      TrailingSlash
		from, to  string
		wantBody  string
		otherPath string
	}{
		{TrailingSlashNever, "/blog/hello/?q=1", "/blog/hello?q=1", "post hello", "/api/items/"},
		{TrailingSlashAlways, "/blog/hello?q=1", "/blog/hello/?q=1", "post hello", "/api/items"},
	} {
		s := slashFirstSite(t, m.mode)
		mux := http.NewServeMux()
		mux.HandleFunc(m.otherPath, func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("api")) })
		mux.Handle("/", s.Handler())
		for _, h := range []http.Handler{s.TrailingSlash(s.Middleware(&nextHandler{})), s.TrailingSlash(mux)} {
			rec := do(h, "GET", m.from)
			if rec.Code != http.StatusPermanentRedirect || rec.Header().Get("Location") != m.to {
				t.Fatalf("mode=%s: GET %s: got %d %q", modeName[m.mode], m.from, rec.Code, rec.Header().Get("Location"))
			}
			rec = do(h, "GET", m.to)
			if rec.Code != 200 || rec.Body.String() != m.wantBody {
				t.Errorf("mode=%s: GET %s: got %d %q", modeName[m.mode], m.to, rec.Code, rec.Body)
			}
		}
		// An app route in the other form keeps working (pages only).
		if rec := do(s.TrailingSlash(mux), "GET", m.otherPath); rec.Code != 200 || rec.Body.String() != "api" {
			t.Errorf("mode=%s: GET %s: got %d %q", modeName[m.mode], m.otherPath, rec.Code, rec.Body)
		}
	}
}

func TestTrailingSlashAllocs(t *testing.T) {
	if testing.Short() || raceEnabled {
		t.Skip("allocation counts (not under -short or -race)")
	}
	t.Setenv("LYR_DEV", "")
	for _, m := range []struct {
		mode            TrailingSlash
		pass, redirects []string
	}{
		{TrailingSlashNever,
			[]string{"/", "/blog/hello", "/search?q=go", "/unknown/", "/_lyr/x/", "/x//", "//evil.example/", "/robots.txt"},
			[]string{"/blog/hello/", "/search/?q=go"}},
		{TrailingSlashAlways,
			[]string{"/", "/blog/hello/", "/unknown", "/rss.xml", "/robots.txt", "/docs", "/_lyr/app-1234.js",
				"/_lyr/island/0000000000000000/x", "//evil.example"},
			[]string{"/blog/hello", "/search?q=go"}},
	} {
		s := slashFirstSite(t, m.mode)
		next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(299) })
		first, inside := s.TrailingSlash(next), s.Middleware(next)
		fw := newFakeResponseWriter()
		count := func(h http.Handler, r *http.Request) float64 {
			return testing.AllocsPerRun(1000, func() {
				fw.reset()
				h.ServeHTTP(fw, r)
			})
		}
		for _, target := range m.pass {
			r := httptest.NewRequest("GET", target, nil)
			if allocs := count(first, r); allocs != 0 || fw.status != 299 {
				t.Errorf("mode=%s: GET %s: %d, %v allocs, want next and 0", modeName[m.mode], target, fw.status, allocs)
			}
		}
		for _, target := range m.redirects {
			r := httptest.NewRequest("GET", target, nil)
			want := count(inside, r)
			if allocs := count(first, r); allocs != want || fw.status != http.StatusPermanentRedirect {
				t.Errorf("mode=%s: GET %s: %d, %v allocs, want 308 and %v (site.Middleware's)", modeName[m.mode], target, fw.status, allocs, want)
			}
		}
	}
}

func benchTrailingSlash(b *testing.B, mode TrailingSlash, target string, wantStatus int) {
	b.Setenv("LYR_DEV", "")
	b.Setenv("LYR_STATIC", "")
	b.Setenv("LYR_PUBLIC", "")
	s := NewSite(slashRoutes(), nil, WithStaticDir(b.TempDir()), WithTrailingSlash(mode))
	h := s.TrailingSlash(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(299) }))
	fw := newFakeResponseWriter()
	r := httptest.NewRequest("GET", target, nil)
	b.ReportAllocs()
	for b.Loop() {
		fw.reset()
		h.ServeHTTP(fw, r)
	}
	if fw.status != wantStatus {
		b.Fatalf("GET %s: got %d", target, fw.status)
	}
}

// A canonical path: one byte check, then next.
func BenchmarkTrailingSlashPass(b *testing.B) {
	b.Run("never", func(b *testing.B) { benchTrailingSlash(b, TrailingSlashNever, "/blog/hello", 299) })
	b.Run("always", func(b *testing.B) { benchTrailingSlash(b, TrailingSlashAlways, "/blog/hello/", 299) })
}

// A non-canonical path that no page claims: the file lookup and the trie.
func BenchmarkTrailingSlashMiss(b *testing.B) {
	b.Run("never", func(b *testing.B) { benchTrailingSlash(b, TrailingSlashNever, "/api/items/", 299) })
	b.Run("always", func(b *testing.B) { benchTrailingSlash(b, TrailingSlashAlways, "/api/items", 299) })
}

func BenchmarkTrailingSlashRedirect(b *testing.B) {
	b.Run("never", func(b *testing.B) { benchTrailingSlash(b, TrailingSlashNever, "/blog/hello/", 308) })
	b.Run("always", func(b *testing.B) { benchTrailingSlash(b, TrailingSlashAlways, "/blog/hello", 308) })
}
