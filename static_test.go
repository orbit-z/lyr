package orbit

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, name, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// staticDir returns a dist/client-like directory, and a secret file next to
// it that traversal must not reach.
func staticDir(t *testing.T) string {
	t.Helper()
	base := t.TempDir()
	dir := filepath.Join(base, "client")
	writeFile(t, filepath.Join(base, "secret.txt"), "secret")
	writeFile(t, filepath.Join(dir, "_orbit", "app-1234.js"), "console.log(1)")
	writeFile(t, filepath.Join(dir, "_orbit", "island", "x"), "not a file")
	writeFile(t, filepath.Join(dir, "robots.txt"), "robots")
	writeFile(t, filepath.Join(dir, "about"), "public about")
	writeFile(t, filepath.Join(dir, "docs", "index.html"), "docs index")
	return dir
}

func TestStaticFiles(t *testing.T) {
	for _, dev := range []bool{false, true} {
		name := "prod"
		if dev {
			name = "dev"
		}
		t.Run(name, func(t *testing.T) {
			if dev {
				t.Setenv("ORBIT_DEV", "1")
			} else {
				t.Setenv("ORBIT_DEV", "")
			}
			dir := staticDir(t)
			routes := []Route{
				{Pattern: "/about", File: "src/pages/about.orbit", Render: page("page about")},
				{Pattern: "/docs", File: "src/pages/docs.orbit", Render: page("docs page")},
			}
			s := testSite(t, routes, WithStaticDir(dir))
			immutable, public := "public, max-age=31536000, immutable", "public, max-age=0"
			if dev {
				immutable, public = "no-cache", "no-cache"
			}
			tests := []struct {
				method, target string
				status         int
				body, cache    string
			}{
				{"GET", "/_orbit/app-1234.js", 200, "console.log(1)", immutable},
				{"HEAD", "/_orbit/app-1234.js", 200, "", immutable},
				{"GET", "/robots.txt", 200, "robots", public},
				{"GET", "/about", 200, "public about", public}, // files come before pages
				{"POST", "/about", 200, "page about", ""},      // pages answer other methods
				{"GET", "/docs", 200, "docs page", ""},         // directories aren't files
				{"GET", "/docs/index.html", 200, "docs index", public},
			}
			for _, tt := range tests {
				rec := do(s.Handler(), tt.method, tt.target)
				if rec.Code != tt.status || rec.Body.String() != tt.body || rec.Header().Get("Cache-Control") != tt.cache {
					t.Errorf("%s %s: got %d %q (Cache-Control %q), want %d %q (%q)", tt.method, tt.target,
						rec.Code, rec.Body, rec.Header().Get("Cache-Control"), tt.status, tt.body, tt.cache)
				}
			}
			if rec := do(s.Handler(), "GET", "/_orbit/app-1234.js"); rec.Header().Get("Content-Type") != "text/javascript; charset=utf-8" {
				t.Errorf("Content-Type = %q", rec.Header().Get("Content-Type"))
			}

			// Requests that must reach next: other methods on a file, the
			// island prefix, traversal, directories and file paths with a
			// trailing slash.
			for _, tt := range []struct{ method, target string }{
				{"POST", "/robots.txt"},
				{"PUT", "/_orbit/app-1234.js"},
				{"GET", "/_orbit/island/x"},
				{"GET", "/../secret.txt"},
				{"GET", "/%2e%2e/secret.txt"},
				{"GET", "/_orbit/../../secret.txt"},
				{"GET", "/_orbit/"},
				{"GET", "/_orbit"},
				{"GET", "/robots.txt/"},
				{"GET", "/docs/../robots.txt"},
				{"GET", "/missing.txt"},
			} {
				next := &nextHandler{}
				req := httptest.NewRequest(tt.method, "/", nil)
				req.URL.Path = unescapeForTest(t, tt.target)
				rec := httptest.NewRecorder()
				s.Middleware(next).ServeHTTP(rec, req)
				if !next.called {
					t.Errorf("%s %s: got %d %q, want next", tt.method, tt.target, rec.Code, rec.Body)
				}
			}
		})
	}
}

// unescapeForTest decodes %XX in a raw test path, as the server's request
// parsing would, without cleaning dot segments.
func unescapeForTest(t *testing.T, p string) string {
	t.Helper()
	b, ok := appendUnescape(nil, p)
	if !ok {
		t.Fatalf("bad test path %q", p)
	}
	return string(b)
}

func TestStaticRanges(t *testing.T) {
	s := testSite(t, nil, WithStaticDir(staticDir(t)))
	req := httptest.NewRequest("GET", "/robots.txt", nil)
	req.Header.Set("Range", "bytes=1-3")
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != 206 || rec.Body.String() != "obo" {
		t.Errorf("Range: got %d %q", rec.Code, rec.Body)
	}
	lm := do(s.Handler(), "GET", "/robots.txt").Header().Get("Last-Modified")
	req = httptest.NewRequest("GET", "/robots.txt", nil)
	req.Header.Set("If-Modified-Since", lm)
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if lm == "" || rec.Code != 304 {
		t.Errorf("If-Modified-Since %q: got %d", lm, rec.Code)
	}
}

func TestStaticSymlinkEscape(t *testing.T) {
	dir := staticDir(t)
	if err := os.Symlink(filepath.Join(dir, "..", "secret.txt"), filepath.Join(dir, "leak.txt")); err != nil {
		t.Skip("symlinks unsupported:", err)
	}
	for _, dev := range []string{"", "1"} {
		t.Setenv("ORBIT_DEV", dev)
		next := &nextHandler{}
		rec := do(testSite(t, nil, WithStaticDir(dir)).Middleware(next), "GET", "/leak.txt")
		if !next.called {
			t.Errorf("dev=%q: symlink escaped the static dir: %d %q", dev, rec.Code, rec.Body)
		}
	}
}

func TestDevPublic(t *testing.T) {
	static := t.TempDir()
	public := t.TempDir()
	writeFile(t, filepath.Join(static, "_orbit", "a.css"), "css")
	writeFile(t, filepath.Join(static, "manifest.json"), "{}")
	writeFile(t, filepath.Join(public, "robots.txt"), "dev robots")
	t.Setenv("ORBIT_DEV", "1")
	t.Setenv("ORBIT_STATIC", static)
	t.Setenv("ORBIT_PUBLIC", public)
	s := NewSite(nil, nil)

	// Files added after startup are served in dev.
	writeFile(t, filepath.Join(public, "late.txt"), "late")
	writeFile(t, filepath.Join(static, "_orbit", "b.js"), "js")
	for target, body := range map[string]string{
		"/robots.txt":   "dev robots",
		"/late.txt":     "late",
		"/_orbit/a.css": "css",
		"/_orbit/b.js":  "js",
	} {
		rec := do(s.Handler(), "GET", target)
		if rec.Code != 200 || rec.Body.String() != body || rec.Header().Get("Cache-Control") != "no-cache" {
			t.Errorf("%s: got %d %q %q", target, rec.Code, rec.Body, rec.Header().Get("Cache-Control"))
		}
	}
	// The dev static dir's root (manifest.json) isn't public.
	if rec := do(s.Handler(), "GET", "/manifest.json"); rec.Code != 404 {
		t.Errorf("/manifest.json: got %d %q", rec.Code, rec.Body)
	}

	// ORBIT_PUBLIC is ignored outside dev.
	t.Setenv("ORBIT_DEV", "")
	writeFile(t, filepath.Join(static, "robots.txt"), "static robots")
	if rec := do(NewSite(nil, nil).Handler(), "GET", "/robots.txt"); rec.Body.String() != "static robots" {
		t.Errorf("prod /robots.txt: %q", rec.Body)
	}
}

func TestStaticPrecedence(t *testing.T) {
	mk := func(content string) string {
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, "which.txt"), content)
		return dir
	}
	envDir, optDir := mk("env"), mk("option")
	fsys := os.DirFS(mk("fs"))
	t.Setenv("ORBIT_DEV", "")

	tests := []struct {
		env  string
		opts []Option
		want string
	}{
		{envDir, []Option{WithStaticDir(optDir), WithStaticFS(fsys)}, "env"},
		{"", []Option{WithStaticDir(optDir), WithStaticFS(fsys)}, "option"},
		{"", []Option{WithStaticFS(fsys), WithStaticDir(optDir)}, "option"},
		{"", []Option{WithStaticFS(fsys)}, "fs"},
	}
	for i, tt := range tests {
		t.Setenv("ORBIT_STATIC", tt.env)
		rec := do(NewSite(nil, nil, tt.opts...).Handler(), "GET", "/which.txt")
		if rec.Body.String() != tt.want {
			t.Errorf("case %d: got %q, want %q", i, rec.Body, tt.want)
		}
	}

	// The default is client/ next to the test binary, which doesn't exist:
	// no files, and no error.
	t.Setenv("ORBIT_STATIC", "")
	if rec := do(NewSite(nil, nil).Handler(), "GET", "/which.txt"); rec.Code != 404 {
		t.Errorf("default: got %d %q", rec.Code, rec.Body)
	}
	// A missing directory is fine too.
	if rec := do(NewSite(nil, nil, WithStaticDir(filepath.Join(optDir, "nope"))).Handler(), "GET", "/which.txt"); rec.Code != 404 {
		t.Errorf("missing dir: got %d", rec.Code)
	}
}

func TestStaticDefaultNextToBinary(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Skip(err)
	}
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		exe = real
	}
	dir := filepath.Join(filepath.Dir(exe), "client")
	if _, err := os.Stat(dir); err == nil {
		t.Skip("client/ already exists next to the test binary")
	}
	writeFile(t, filepath.Join(dir, "hi.txt"), "next to binary")
	t.Cleanup(func() { os.RemoveAll(dir) })
	t.Setenv("ORBIT_STATIC", "")
	t.Setenv("ORBIT_DEV", "")
	t.Chdir(t.TempDir())
	if rec := do(NewSite(nil, nil).Handler(), "GET", "/hi.txt"); rec.Body.String() != "next to binary" {
		t.Errorf("got %d %q", rec.Code, rec.Body)
	}
}
