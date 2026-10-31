package build

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestBuildTrailingSlashAlways builds the fixture site with
// "trailingSlash": "always", runs dist/server and checks the canonical
// URLs, the 308s and a Markdown page's link (PLAN.md §4e).
func TestBuildTrailingSlashAlways(t *testing.T) {
	needE2E(t)
	lyrCLI(t)
	t.Parallel()
	dir := fixture(t, false)
	writeFile(t, dir, "lyr.json", `{"site": "https://fixture.example.com", "trailingSlash": "always"}`)
	writeFile(t, dir, "src/pages/rss.xml.lyr", "---\nctx.Header().Set(\"Content-Type\", \"application/rss+xml\")\n---\n<rss></rss>\n")
	writeFile(t, dir, "src/pages/notes/a.md", "See [b](./b.md#setup).\n")
	writeFile(t, dir, "src/pages/notes/b.md", "# Setup\n")
	// A server entry with a gate before Lyr, behind site.TrailingSlash:
	// the redirect comes first, as in Astro (PLAN.md §4e "Redirecting
	// before middleware").
	writeFile(t, dir, "src/pages/private.lyr", "<p>private</p>\n")
	writeFile(t, dir, "cmd/server/main.go", `package main

import (
	"log"
	"net/http"
	"strings"

	"github.com/orbit-z/lyr"

	"example.com/site/lyr_gen/app"
)

func main() {
	site := app.New()
	mux := http.NewServeMux()
	mux.Handle("/", site.Handler())
	if err := lyr.ListenAndServe(site.TrailingSlash(gate(mux))); err != nil {
		log.Fatal(err)
	}
}

// gate sends every /private request to the sign-in page.
func gate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/private") {
			http.Redirect(w, r, "/signin/", http.StatusSeeOther)
			return
		}
		next.ServeHTTP(w, r)
	})
}
`)
	if code, out := runLyr(t, dir, "build"); code != 0 || out != "" {
		t.Fatalf("lyr build: exit %d\n%s", code, out)
	}
	s := start(t, dir, filepath.Join(dir, "dist/server"))
	defer s.stop()
	for _, tt := range []struct {
		method, path string
		status       int
		location     string
		body         string
	}{
		{"GET", "/", 200, "", "<title>Home</title>"},
		{"GET", "/blog/hello/", 200, "", ""},
		{"GET", "/blog/hello", 308, "/blog/hello/", ""},
		{"HEAD", "/blog/hello", 308, "/blog/hello/", ""},
		{"POST", "/search?q=go", 308, "/search/?q=go", ""},
		{"GET", "/search/?q=go", 200, "", ""},
		{"GET", "/rss.xml", 200, "", "<rss></rss>"},
		{"GET", "/notes/a/", 200, "", `<a href="/notes/b/#setup">b</a>`},
		{"GET", "/robots.txt", 200, "", ""},
		{"GET", "/private", 308, "/private/", ""},
		{"POST", "/private?x=1", 308, "/private/?x=1", ""},
		{"GET", "/private/", 303, "/signin/", ""},
	} {
		resp, body := s.do(tt.method, tt.path, "")
		if resp.StatusCode != tt.status || resp.Header.Get("Location") != tt.location || !strings.Contains(body, tt.body) {
			t.Errorf("%s %s: %d %q\n%s\nwant %d %q with %q", tt.method, tt.path, resp.StatusCode, resp.Header.Get("Location"), body, tt.status, tt.location, tt.body)
		}
		if tt.status == 308 && body != "" {
			t.Errorf("%s %s: body %q, want none", tt.method, tt.path, body)
		}
	}
	if t.Failed() {
		t.Logf("server output:\n%s", s.stderr)
	}
}
