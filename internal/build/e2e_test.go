package build

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// TestE2E builds the fixture site with orbit build, once with the generated
// default server entry and once with its own cmd/server, runs dist/server
// and checks its responses (PLAN.md §13), then runs orbit preview on the
// same build.
func TestE2E(t *testing.T) {
	needE2E(t)
	orbitCLI(t)
	for _, userServer := range []bool{false, true} {
		name := "generated-entry"
		if userServer {
			name = "user-entry"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			dir := fixture(t, userServer)
			// A stale file from an earlier build is removed.
			writeFile(t, dir, "dist/client/stale.txt", "old")

			code, out := runOrbit(t, dir, "build")
			if code != 0 || out != "" {
				t.Fatalf("orbit build: exit %d\n%s", code, out)
			}
			if exists(filepath.Join(dir, "orbit_gen/cmd/server")) == userServer {
				t.Errorf("orbit_gen/cmd/server exists: %v, want %v", !userServer, !userServer)
			}
			for _, rel := range []string{"dist/server", "dist/client/robots.txt", "dist/client/_orbit"} {
				if !exists(filepath.Join(dir, rel)) {
					t.Errorf("no %s", rel)
				}
			}
			if exists(filepath.Join(dir, "dist/client/stale.txt")) {
				t.Errorf("stale dist/client/stale.txt is still there")
			}
			if html := htmlFiles(t, filepath.Join(dir, "dist")); len(html) > 0 {
				t.Errorf("HTML files in dist/: %v", html)
			}
			entries, _ := os.ReadDir(filepath.Join(dir, "dist"))
			if len(entries) != 2 {
				t.Errorf("dist/ has %d entries, want client and server", len(entries))
			}

			s := start(t, dir, filepath.Join(dir, "dist/server"))
			checkSite(t, s, userServer)
			if code := s.stop(); code != 0 {
				t.Errorf("dist/server: exit %d after SIGINT\n%s", code, s.stderr)
			}
			if t.Failed() {
				t.Logf("server output:\n%s", s.stderr)
			}

			if userServer {
				return
			}
			// orbit preview runs the same build.
			p := start(t, filepath.Join(dir, "src"), orbitCLI(t), "preview")
			if resp, body := p.get("/"); resp.StatusCode != 200 || !strings.Contains(body, "<title>Home</title>") {
				t.Errorf("preview GET /: %d\n%s", resp.StatusCode, body)
			}
			if code := p.stop(); code != 0 {
				t.Errorf("orbit preview: exit %d after SIGINT\n%s", code, p.stderr)
			}
		})
	}
}

func checkSite(t *testing.T, s *server, userServer bool) {
	t.Helper()
	tests := []struct {
		path     string
		status   int
		body     []string // substrings
		location string
	}{
		{path: "/", status: 200, body: []string{
			"<!DOCTYPE html>", "<title>Home</title>",
			`<p class="site">https://fixture.example.com</p>`,
			`<article class="card"><h2>a&lt;b</h2><p>count=1</p><em>slot a&lt;b</em></article>`,
			`<article class="card"><h2>c&amp;d</h2><p>count=2</p><em>slot c&amp;d</em></article>`,
			`<aside class="note">noted</aside>`,
		}},
		{path: "/search?q=go", status: 200, body: []string{"You searched for go"}},
		{path: "/search?q=%3Cb%3E", status: 200, body: []string{"You searched for &lt;b&gt;"}},
		{path: "/blog/hello", status: 200, body: []string{"<title>hello</title>", "<h1>Post hello</h1>"}},
		{path: "/blog/nope", status: 404, body: []string{"<h1>Fixture 404</h1>"}},
		{path: "/blog/old", status: 301, location: "/blog/hello"},
		{path: "/blog/teapot", status: 418, body: []string{"<h1>Post teapot</h1>"}},
		{path: "/blog/boom", status: 500, body: []string{"<h1>500 Internal Server Error</h1>"}},
		{path: "/blog/hello/", status: 308, location: "/blog/hello"},
		{path: "/search/?q=go", status: 308, location: "/search?q=go"},
		{path: "/robots.txt", status: 200, body: []string{"User-agent: *"}},
		{path: "/robots.txt/", status: 404},
		{path: "/_partials/Note", status: 404, body: []string{"<h1>Fixture 404</h1>"}},
		{path: "/nope", status: 404, body: []string{"<h1>Fixture 404</h1>"}},
	}
	if userServer {
		tests = append(tests, struct {
			path     string
			status   int
			body     []string
			location string
		}{path: "/api/ping", status: 200, body: []string{"pong"}})
	}
	for _, tt := range tests {
		resp, body := s.get(tt.path)
		if resp.StatusCode != tt.status {
			t.Errorf("GET %s: status %d, want %d\n%s", tt.path, resp.StatusCode, tt.status, body)
			continue
		}
		for _, want := range tt.body {
			if !strings.Contains(body, want) {
				t.Errorf("GET %s: body lacks %q:\n%s", tt.path, want, body)
			}
		}
		if got := resp.Header.Get("Location"); got != tt.location {
			t.Errorf("GET %s: Location %q, want %q", tt.path, got, tt.location)
		}
		if got := resp.Header.Get("X-Fixture-Server"); (got == "user") != userServer {
			t.Errorf("GET %s: X-Fixture-Server %q with user entry %v", tt.path, got, userServer)
		}
		if strings.HasPrefix(tt.path, "/robots") || tt.path == "/api/ping" || tt.status == 301 || tt.status == 308 {
			continue
		}
		if ct := resp.Header.Get("Content-Type"); ct != "text/html; charset=utf-8" {
			t.Errorf("GET %s: Content-Type %q", tt.path, ct)
		}
		if cl := resp.Header.Get("Content-Length"); cl != strconv.Itoa(len(body)) {
			t.Errorf("GET %s: Content-Length %q, body is %d bytes", tt.path, cl, len(body))
		}
	}
	if resp, _ := s.get("/blog/teapot"); resp.Header.Get("X-Fixture") != "teapot" {
		t.Errorf("GET /blog/teapot: X-Fixture %q", resp.Header.Get("X-Fixture"))
	}

	// One log line for the panic, at the panicking .orbit line.
	panicLine := regexp.MustCompile(`(?m)^.*orbit: GET /blog/boom: src/pages/blog/\[slug\]\.orbit:14: panic: kaboom$`)
	if n := len(panicLine.FindAllString(s.stderr.String(), -1)); n != 1 {
		t.Errorf("server stderr has %d panic lines, want 1:\n%s", n, s.stderr)
	}
	if n := strings.Count(s.stderr.String(), "/blog/boom"); n != 1 {
		t.Errorf("server stderr mentions /blog/boom %d times, want 1:\n%s", n, s.stderr)
	}
}

// TestBuildError checks that a type error fails orbit build with the
// error at its .orbit position, in the user's names, and writes no
// dist/server.
func TestBuildError(t *testing.T) {
	needE2E(t)
	t.Parallel()
	dir := fixture(t, false)
	card := filepath.Join(dir, "src/components/Card.orbit")
	src, err := os.ReadFile(card)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, dir, "src/components/Card.orbit", strings.Replace(string(src), "{props.Title}", "{props.Titel}", 1))

	code, out := runOrbit(t, dir, "build")
	if code != 1 {
		t.Fatalf("orbit build: exit %d, want 1\n%s", code, out)
	}
	want := "src/components/Card.orbit:7:34: props.Titel undefined (type Props has no field or method Titel)"
	if !strings.Contains(out, "\n"+want+"\n") {
		t.Errorf("output lacks %q:\n%s", want, out)
	}
	for _, bad := range []string{"orbit__", "./src/", "orbit_gen/decl", "orbit_gen/impl/components/card/", "card.Props"} {
		if strings.Contains(out, bad) {
			t.Errorf("output contains %q:\n%s", bad, out)
		}
	}
	if exists(filepath.Join(dir, "dist/server")) || exists(filepath.Join(dir, "dist/client")) {
		t.Errorf("a failed build wrote dist/server or dist/client")
	}
	if entries, _ := os.ReadDir(filepath.Join(dir, "dist")); len(entries) > 0 {
		t.Errorf("dist/ has leftovers: %v", entries)
	}
}
