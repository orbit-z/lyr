package lyr

import (
	"bytes"
	"errors"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
)

// nextHandler records whether it was called and writes a marker response.
type nextHandler struct{ called bool }

func (n *nextHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	n.called = true
	w.Header().Set("X-Next", "1")
	w.WriteHeader(299)
	w.Write([]byte("next"))
}

func page(body string) func(*Ctx, *Writer) error {
	return func(c *Ctx, w *Writer) error {
		w.Raw(body)
		return nil
	}
}

// testSite builds a site with no static files unless opts add some.
func testSite(t *testing.T, routes []Route, opts ...Option) *Site {
	t.Helper()
	t.Setenv("LYR_STATIC", "")
	t.Setenv("LYR_PUBLIC", "")
	if os.Getenv("LYR_DEV") == "" {
		t.Setenv("LYR_DEV", "")
	}
	return NewSite(routes, nil, append([]Option{WithStaticDir(t.TempDir())}, opts...)...)
}

func do(h http.Handler, method, target string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, target, nil))
	return rec
}

// captureLog redirects the log package's output for the test.
func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	flags, out := log.Flags(), log.Writer()
	log.SetFlags(0)
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetFlags(flags); log.SetOutput(out) })
	return &buf
}

func TestServePages(t *testing.T) {
	routes := []Route{
		{Pattern: "/", File: "src/pages/index.lyr", Render: page("<h1>home</h1>")},
		{Pattern: "/about", File: "src/pages/about.lyr", Render: page("about")},
		{Pattern: "/blog/[slug]", File: "src/pages/blog/[slug].lyr", Render: func(c *Ctx, w *Writer) error {
			w.String("post " + c.Param("slug"))
			return nil
		}},
		{Pattern: "/feed", File: "src/pages/feed.lyr", Render: func(c *Ctx, w *Writer) error {
			c.Header().Set("Content-Type", "application/rss+xml")
			c.Header().Set("X-Page", "feed")
			w.Raw("<rss/>")
			return nil
		}},
		{Pattern: "/created", File: "src/pages/created.lyr", Render: func(c *Ctx, w *Writer) error {
			c.SetStatus(http.StatusCreated)
			w.Raw("made")
			return nil
		}},
		{Pattern: "/method", File: "src/pages/method.lyr", Render: func(c *Ctx, w *Writer) error {
			w.Raw(c.Request.Method)
			return nil
		}},
	}
	s := testSite(t, routes)
	tests := []struct {
		method, target string
		status         int
		body, ctype    string
	}{
		{"GET", "/", 200, "<h1>home</h1>", "text/html; charset=utf-8"},
		{"GET", "/about", 200, "about", "text/html; charset=utf-8"},
		{"GET", "/blog/hello%20world", 200, "post hello world", "text/html; charset=utf-8"},
		{"GET", "/feed", 200, "<rss/>", "application/rss+xml"},
		{"GET", "/created", 201, "made", "text/html; charset=utf-8"},
		{"POST", "/method", 200, "POST", "text/html; charset=utf-8"},
		{"DELETE", "/method", 200, "DELETE", "text/html; charset=utf-8"},
		{"PATCH", "/about", 200, "about", "text/html; charset=utf-8"},
		{"HEAD", "/about", 200, "", "text/html; charset=utf-8"},
	}
	for _, tt := range tests {
		t.Run(tt.method+" "+tt.target, func(t *testing.T) {
			next := &nextHandler{}
			rec := do(s.Middleware(next), tt.method, tt.target)
			if next.called {
				t.Fatal("next was called")
			}
			if rec.Code != tt.status || rec.Body.String() != tt.body {
				t.Fatalf("got %d %q, want %d %q", rec.Code, rec.Body, tt.status, tt.body)
			}
			if got := rec.Header().Get("Content-Type"); got != tt.ctype {
				t.Errorf("Content-Type = %q, want %q", got, tt.ctype)
			}
			wantLen := len(tt.body)
			if tt.method == "HEAD" {
				wantLen = len("about")
			}
			if got := rec.Header().Get("Content-Length"); got != itoa(wantLen) {
				t.Errorf("Content-Length = %q, want %d", got, wantLen)
			}
		})
	}
	rec := do(s.Handler(), "GET", "/feed")
	if rec.Header().Get("X-Page") != "feed" {
		t.Errorf("page header not copied: %v", rec.Header())
	}
}

func itoa(n int) string { return strconv.Itoa(n) }

func TestContentLength(t *testing.T) {
	for _, n := range []int{0, 7, 100, 12345, 1 << 40} {
		if v := contentLength(n); len(v) != 1 || v[0] != strconv.Itoa(n) {
			t.Errorf("contentLength(%d) = %q", n, v)
		}
	}
}

func TestHeadInjection(t *testing.T) {
	routes := []Route{{
		Pattern: "/", File: "src/pages/index.lyr",
		Render: func(c *Ctx, w *Writer) error {
			w.Raw("<html><head><title>x</title>")
			w.HeadEnd()
			w.Raw("</head><body></body></html>")
			return nil
		},
		Assets: []Asset{{Kind: "css", URL: "/_lyr/a.css"}, {Kind: "js", URL: "/_lyr/a.js"}},
	}}
	rec := do(testSite(t, routes).Handler(), "GET", "/")
	want := `<html><head><title>x</title><link rel="stylesheet" href="/_lyr/a.css"><script type="module" src="/_lyr/a.js"></script></head><body></body></html>`
	if rec.Body.String() != want {
		t.Fatalf("body = %q", rec.Body)
	}
	if rec.Header().Get("Content-Length") != itoa(len(want)) {
		t.Errorf("Content-Length = %q", rec.Header().Get("Content-Length"))
	}
}

func TestServeRedirect(t *testing.T) {
	routes := []Route{
		{Pattern: "/login", File: "src/pages/login.lyr", Render: func(c *Ctx, w *Writer) error {
			w.Raw("partial")
			c.Header().Set("Set-Cookie", "a=b")
			return c.Redirect("/home", http.StatusFound)
		}},
		{Pattern: "/blog/old", File: "src/pages/blog/old.lyr", Render: func(c *Ctx, w *Writer) error {
			return c.Redirect("new", http.StatusMovedPermanently)
		}},
		{Pattern: "/ext", File: "src/pages/ext.lyr", Render: func(c *Ctx, w *Writer) error {
			return c.Redirect("https://example.com/x", http.StatusSeeOther)
		}},
	}
	s := testSite(t, routes)
	tests := []struct {
		target, location string
		status           int
	}{
		{"/login", "/home", 302},
		{"/blog/old", "/blog/new", 301},
		{"/ext", "https://example.com/x", 303},
	}
	for _, tt := range tests {
		rec := do(s.Handler(), "GET", tt.target)
		if rec.Code != tt.status || rec.Header().Get("Location") != tt.location {
			t.Errorf("%s: got %d %q, want %d %q", tt.target, rec.Code, rec.Header().Get("Location"), tt.status, tt.location)
		}
		if strings.Contains(rec.Body.String(), "partial") {
			t.Errorf("%s: partial page sent: %q", tt.target, rec.Body)
		}
	}
	if rec := do(s.Handler(), "GET", "/login"); rec.Header().Get("Set-Cookie") != "a=b" {
		t.Errorf("redirect lost the page's headers: %v", rec.Header())
	}
}

func TestNotFound(t *testing.T) {
	missing := Route{Pattern: "/post", File: "src/pages/post.lyr", Render: func(c *Ctx, w *Writer) error {
		w.Raw("partial")
		c.Header().Set("X-Post", "1")
		return ErrNotFound
	}}
	wrapped := Route{Pattern: "/wrapped", File: "src/pages/wrapped.lyr", Render: func(c *Ctx, w *Writer) error {
		return errors.Join(errors.New("db"), ErrNotFound)
	}}
	notFound := Route{Pattern: "404", File: "src/pages/404.lyr", Render: func(c *Ctx, w *Writer) error {
		w.Raw("not found: " + c.URL.Path)
		return nil
	}}

	t.Run("with 404.lyr", func(t *testing.T) {
		s := testSite(t, []Route{missing, wrapped, notFound})
		for _, target := range []string{"/post", "/wrapped", "/nope"} {
			rec := do(s.Handler(), "GET", target)
			if rec.Code != 404 || rec.Body.String() != "not found: "+target {
				t.Errorf("%s: got %d %q", target, rec.Code, rec.Body)
			}
			if rec.Header().Get("Content-Type") != "text/html; charset=utf-8" || rec.Header().Get("X-Post") != "" {
				t.Errorf("%s: headers %v", target, rec.Header())
			}
		}
		rec := do(s.NotFound(), "POST", "/anything")
		if rec.Code != 404 || rec.Body.String() != "not found: /anything" {
			t.Errorf("NotFound: got %d %q", rec.Code, rec.Body)
		}
		// Through Middleware, an unknown path reaches next, not the 404 page.
		next := &nextHandler{}
		if rec := do(s.Middleware(next), "GET", "/nope"); !next.called || rec.Code != 299 {
			t.Errorf("unknown path didn't reach next: %d", rec.Code)
		}
	})

	t.Run("without 404.lyr", func(t *testing.T) {
		s := testSite(t, []Route{missing})
		for _, target := range []string{"/post", "/nope"} {
			rec := do(s.Handler(), "GET", target)
			if rec.Code != 404 || rec.Body.Len() != 0 {
				t.Errorf("%s: got %d %q", target, rec.Code, rec.Body)
			}
			if rec.Header().Get("Content-Length") != "0" {
				t.Errorf("%s: Content-Length %q", target, rec.Header().Get("Content-Length"))
			}
		}
	})

	t.Run("404 page status and errors", func(t *testing.T) {
		logs := captureLog(t)
		gone := Route{Pattern: "404", File: "src/pages/404.lyr", Render: func(c *Ctx, w *Writer) error {
			c.SetStatus(http.StatusGone)
			w.Raw("gone")
			return nil
		}}
		if rec := do(testSite(t, []Route{gone}).Handler(), "GET", "/x"); rec.Code != 410 || rec.Body.String() != "gone" {
			t.Errorf("404 page SetStatus: got %d %q", rec.Code, rec.Body)
		}
		loop := Route{Pattern: "404", File: "src/pages/404.lyr", Render: func(c *Ctx, w *Writer) error { return ErrNotFound }}
		if rec := do(testSite(t, []Route{missing, loop}).Handler(), "GET", "/post"); rec.Code != 404 || rec.Body.Len() != 0 {
			t.Errorf("404 page returning ErrNotFound: got %d %q", rec.Code, rec.Body)
		}
		broken := Route{Pattern: "404", File: "src/pages/404.lyr", Render: func(c *Ctx, w *Writer) error { return errors.New("boom") }}
		if rec := do(testSite(t, []Route{broken}).Handler(), "GET", "/x"); rec.Code != 500 {
			t.Errorf("failing 404 page: got %d", rec.Code)
		}
		if !strings.Contains(logs.String(), "boom") {
			t.Errorf("log = %q", logs)
		}
	})
}

func TestServerError(t *testing.T) {
	routes := []Route{
		{Pattern: "/err", File: "src/pages/err.lyr", Render: func(c *Ctx, w *Writer) error {
			w.Raw("partial")
			return errors.New("src/pages/err.lyr:3:5: db <down>")
		}},
		{Pattern: "/panic", File: "src/pages/panic.lyr", Render: func(c *Ctx, w *Writer) error {
			w.Raw("partial")
			panic("kaboom")
		}},
		{Pattern: "/badredirect", File: "src/pages/badredirect.lyr", Render: func(c *Ctx, w *Writer) error {
			return c.Redirect("/x", 200)
		}},
		{Pattern: "/sticky", File: "src/pages/sticky.lyr", Render: func(c *Ctx, w *Writer) error {
			w.setErr(errors.New("sticky failure"))
			return nil
		}},
	}
	tests := []struct {
		target, logged string
	}{
		{"/err", "lyr: GET /err: src/pages/err.lyr:3:5: db <down>\n"},
		{"/panic", "lyr: GET /panic: panic: kaboom\n"},
		{"/badredirect", "lyr: GET /badredirect: panic: lyr: Redirect to \"/x\" with status 200"},
		{"/sticky", "lyr: GET /sticky: sticky failure\n"},
	}
	for _, dev := range []bool{false, true} {
		name := "prod"
		if dev {
			name = "dev"
			t.Setenv("LYR_DEV", "1")
		}
		t.Run(name, func(t *testing.T) {
			s := testSite(t, routes)
			for _, tt := range tests {
				logs := captureLog(t)
				rec := do(s.Handler(), "GET", tt.target)
				body := rec.Body.String()
				if rec.Code != 500 || !strings.Contains(body, "<h1>500 Internal Server Error</h1>") || strings.Contains(body, "partial") {
					t.Errorf("%s: got %d %q", tt.target, rec.Code, body)
				}
				if rec.Header().Get("Content-Type") != "text/html; charset=utf-8" || rec.Header().Get("Content-Length") != itoa(len(body)) {
					t.Errorf("%s: headers %v", tt.target, rec.Header())
				}
				if !strings.HasPrefix(logs.String(), tt.logged) || strings.Count(logs.String(), "\n") != 1 {
					t.Errorf("%s: log = %q, want prefix %q", tt.target, logs, tt.logged)
				}
				shows := strings.Contains(body, "<pre>")
				if shows != dev {
					t.Errorf("%s: error shown = %v in %s: %q", tt.target, shows, name, body)
				}
				if dev && tt.target == "/err" && !strings.Contains(body, "db &lt;down&gt;") {
					t.Errorf("dev body not escaped: %q", body)
				}
			}
		})
	}
}

func TestPanicPosition(t *testing.T) {
	// A frame in a .lyr file (from a line directive) prefixes the error.
	routes := []Route{{Pattern: "/p", File: "src/pages/p.lyr", Render: lyrPanicker}}
	logs := captureLog(t)
	do(testSite(t, routes).Handler(), "GET", "/p")
	if want := "lyr: GET /p: src/pages/p.lyr:7: panic: from lyr\n"; logs.String() != want {
		t.Errorf("log = %q, want %q", logs, want)
	}
}

func TestErrAbortHandler(t *testing.T) {
	routes := []Route{{Pattern: "/abort", File: "src/pages/abort.lyr", Render: func(c *Ctx, w *Writer) error {
		panic(http.ErrAbortHandler)
	}}}
	s := testSite(t, routes)
	defer func() {
		if p := recover(); p != http.ErrAbortHandler {
			t.Fatalf("recovered %v, want http.ErrAbortHandler", p)
		}
	}()
	do(s.Handler(), "GET", "/abort")
	t.Fatal("no panic")
}

func TestTrailingSlash(t *testing.T) {
	routes := []Route{
		{Pattern: "/", File: "src/pages/index.lyr", Render: page("home")},
		{Pattern: "/blog/[slug]", File: "src/pages/blog/[slug].lyr", Render: page("post")},
		{Pattern: "/search", File: "src/pages/search.lyr", Render: page("search")},
	}
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "robots.txt"), "robots")
	s := testSite(t, routes, WithStaticDir(dir))
	tests := []struct {
		method, target, location string
	}{
		{"GET", "/blog/hello/", "/blog/hello"},
		{"POST", "/blog/hello/", "/blog/hello"},
		{"GET", "/search/?q=go&x=1", "/search?q=go&x=1"},
		{"GET", "/blog/caf%C3%A9/", "/blog/caf%C3%A9"},
	}
	for _, tt := range tests {
		next := &nextHandler{}
		rec := do(s.Middleware(next), tt.method, tt.target)
		if rec.Code != 308 || rec.Header().Get("Location") != tt.location || next.called {
			t.Errorf("%s %s: got %d %q", tt.method, tt.target, rec.Code, rec.Header().Get("Location"))
		}
	}
	// Not redirected: "/" is a page; the rest reach next untouched.
	if rec := do(s.Handler(), "GET", "/"); rec.Code != 200 || rec.Body.String() != "home" {
		t.Errorf("/: got %d %q", rec.Code, rec.Body)
	}
	for _, target := range []string{"/x/", "/robots.txt/", "/blog/", "//evil.example/", "/_lyr/x/"} {
		next := &nextHandler{}
		rec := do(s.Middleware(next), "GET", target)
		if !next.called || rec.Code != 299 {
			t.Errorf("%s: got %d, want next", target, rec.Code)
		}
	}
}

func TestNextUntouched(t *testing.T) {
	s := testSite(t, []Route{{Pattern: "/a", File: "src/pages/a.lyr", Render: page("a")}})
	var sawHeaders http.Header
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawHeaders = w.Header().Clone()
	})
	w := httptest.NewRecorder()
	w.Header().Set("X-Outer", "1")
	s.Middleware(next).ServeHTTP(w, httptest.NewRequest("GET", "/b", nil))
	if len(sawHeaders) != 1 || sawHeaders.Get("X-Outer") != "1" || w.Body.Len() != 0 {
		t.Errorf("next saw headers %v, body %q", sawHeaders, w.Body)
	}
}

func TestSiteURL(t *testing.T) {
	routes := []Route{{Pattern: "/", File: "src/pages/index.lyr", Render: func(c *Ctx, w *Writer) error {
		if c.Site != nil {
			w.Raw(c.Site.String())
		}
		w.Raw("|")
		if c.isDev() {
			w.Raw("dev")
		}
		return nil
	}}}
	if rec := do(testSite(t, routes).Handler(), "GET", "/"); rec.Body.String() != "|" {
		t.Errorf("without WithSite: %q", rec.Body)
	}
	s := testSite(t, routes, WithSite("https://a.example"), WithSite("https://b.example/base"))
	if rec := do(s.Handler(), "GET", "/"); rec.Body.String() != "https://b.example/base|" {
		t.Errorf("with WithSite: %q", rec.Body)
	}
	t.Setenv("LYR_DEV", "1")
	if rec := do(testSite(t, routes).Handler(), "GET", "/"); rec.Body.String() != "|dev" {
		t.Errorf("dev flag: %q", rec.Body)
	}
	for _, bad := range []string{"", "example.com", "/path", "https://", "://x"} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("WithSite(%q) didn't panic", bad)
				}
			}()
			WithSite(bad)
		}()
	}
}

func TestNewSiteInvalidRoutes(t *testing.T) {
	defer func() {
		p := recover()
		err, ok := p.(error)
		if !ok || !strings.Contains(err.Error(), "src/pages/b.lyr: route /a is already defined by src/pages/a.lyr") {
			t.Fatalf("panic = %v", p)
		}
	}()
	NewSite([]Route{
		{Pattern: "/a", File: "src/pages/a.lyr", Render: page("")},
		{Pattern: "/a", File: "src/pages/b.lyr", Render: page("")},
	}, nil)
}

func TestStaticFS(t *testing.T) {
	fsys := fstest.MapFS{
		"_lyr/app-1234.js": {Data: []byte("js")},
		"robots.txt":       {Data: []byte("robots")},
	}
	t.Setenv("LYR_STATIC", "")
	t.Setenv("LYR_DEV", "")
	s := NewSite(nil, nil, WithStaticFS(fsys))
	rec := do(s.Handler(), "GET", "/robots.txt")
	if rec.Code != 200 || rec.Body.String() != "robots" || rec.Header().Get("Etag") == "" {
		t.Fatalf("got %d %q %v", rec.Code, rec.Body, rec.Header())
	}
	req := httptest.NewRequest("GET", "/robots.txt", nil)
	req.Header.Set("If-None-Match", rec.Header().Get("Etag"))
	rec2 := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec2, req)
	if rec2.Code != 304 {
		t.Errorf("If-None-Match: got %d", rec2.Code)
	}
}
