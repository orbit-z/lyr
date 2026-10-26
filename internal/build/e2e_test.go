package build

import (
	"os"
	"os/exec"
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
			// The island key and build ID are embedded with -ldflags=-X.
			info, err := exec.Command("go", "version", "-m", filepath.Join(dir, "dist/server")).Output()
			if err != nil || !regexp.MustCompile(`-X github.com/orbit-z/orbit.buildKey=[A-Za-z0-9+/]{43}= -X github.com/orbit-z/orbit.buildID=[0-9a-f]{16}`).Match(info) {
				t.Errorf("go version -m dist/server: %v\n%s", err, info)
			}

			s := start(t, dir, filepath.Join(dir, "dist/server"))
			checkSite(t, s, userServer)
			checkLazy(t, s, dir)
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
	checkAssets(t, s)
	if userServer {
		checkLocals(t, s)
	}
	if resp, _ := s.get("/blog/teapot"); resp.Header.Get("X-Fixture") != "teapot" {
		t.Errorf("GET /blog/teapot: X-Fixture %q", resp.Header.Get("X-Fixture"))
	}

	// One log line for the panic, at the panicking .orbit line.
	panicLine := regexp.MustCompile(`(?m)^.*orbit: GET /blog/boom: src/pages/blog/\[slug\]\.orbit:17: panic: kaboom$`)
	if n := len(panicLine.FindAllString(s.stderr.String(), -1)); n != 1 {
		t.Errorf("server stderr has %d panic lines, want 1:\n%s", n, s.stderr)
	}
	if n := strings.Count(s.stderr.String(), "/blog/boom"); n != 1 {
		t.Errorf("server stderr mentions /blog/boom %d times, want 1:\n%s", n, s.stderr)
	}
}

// checkLocals checks the typed locals end to end (PLAN.md §13 "Middleware
// and locals"): the user entry's middlewares fill them with EnsureLocals,
// and a page, a component in it, and a RenderComponent fragment from a
// custom handler all see the same values.
func checkLocals(t *testing.T, s *server) {
	t.Helper()
	resp, body := s.do("GET", "/account", "theme=dark")
	id := resp.Header.Get("X-Request-ID")
	if resp.StatusCode != 200 || !strings.HasPrefix(id, "req-") {
		t.Fatalf("GET /account: %d, X-Request-ID %q\n%s", resp.StatusCode, id, body)
	}
	for _, want := range []string{
		"<p>Theme: dark</p>",
		"<p>Request ID: <code>" + id + "</code></p>",
		`data-theme="dark" data-request="` + id + `" data-o-`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("GET /account: body lacks %q:\n%s", want, body)
		}
	}
	if !cssLink.MatchString(body) || !jsScript.MatchString(body) {
		t.Errorf("GET /account: no LikeButton assets:\n%s", body)
	}
	if _, body := s.get("/account"); !strings.Contains(body, "<p>Theme: light</p>") {
		t.Errorf("GET /account without a cookie: body lacks the light theme:\n%s", body)
	}

	resp, body = s.do("POST", "/blog/hello/like", "theme=dark")
	id = resp.Header.Get("X-Request-ID")
	want := regexp.MustCompile(`^<button class="like" hx-post="/blog/hello/like" data-theme="dark" data-request="` + regexp.QuoteMeta(id) + `" data-o-[0-9a-f]{8}>\d+ likes</button>\n*$`)
	if resp.StatusCode != 200 || !want.MatchString(body) {
		t.Errorf("POST /blog/hello/like: %d, X-Request-ID %q\n%s\nwant %s", resp.StatusCode, id, body, want)
	}
	if strings.Contains(body, "<link") || strings.Contains(body, "<script") {
		t.Errorf("POST /blog/hello/like: assets injected into the fragment:\n%s", body)
	}
	if ct, cl := resp.Header.Get("Content-Type"), resp.Header.Get("Content-Length"); ct != "text/html; charset=utf-8" || cl != strconv.Itoa(len(body)) {
		t.Errorf("POST /blog/hello/like: Content-Type %q, Content-Length %q for %d bytes", ct, cl, len(body))
	}
}

var (
	cssLink  = regexp.MustCompile(`<link rel="stylesheet" href="(/_orbit/[A-Za-z0-9_-]+-[A-Z0-9]{8}\.css)">`)
	jsScript = regexp.MustCompile(`<script type="module" src="(/_orbit/[A-Za-z0-9_-]+-[A-Z0-9]{8}\.js)"></script>`)
)

// checkAssets checks the bundles end to end (PLAN.md §5): head injection,
// immutable caching, TypeScript stripped, scoped CSS, untouched unprocessed
// scripts, no module script on pages without scripts.
func checkAssets(t *testing.T, s *server) {
	t.Helper()
	_, home := s.get("/")
	css, js := cssLink.FindStringSubmatch(home), jsScript.FindStringSubmatch(home)
	if css == nil || js == nil {
		t.Fatalf("GET /: no hashed stylesheet and module:\n%s", home)
	}
	head := home[:strings.Index(home, "</head>")]
	if !strings.HasPrefix(home, "<!DOCTYPE html>") || !strings.Contains(head, css[0]+js[0]) {
		t.Errorf("GET /: the assets aren't in <head>, stylesheet first:\n%s", home)
	}
	for _, want := range []string{
		`<script type="application/ld+json">{"@context": "https://schema.org", "@type": "WebSite"}</script>`,
		`<script>window.inlineRan = 1;</script>`,
	} {
		if !strings.Contains(home, want) {
			t.Errorf("GET /: lacks the unprocessed script %s:\n%s", want, home)
		}
	}
	bodies := map[string]string{}
	for _, url := range []string{css[1], js[1]} {
		resp, body := s.get(url)
		if resp.StatusCode != 200 || resp.Header.Get("Cache-Control") != "public, max-age=31536000, immutable" {
			t.Errorf("GET %s: %d, Cache-Control %q", url, resp.StatusCode, resp.Header.Get("Cache-Control"))
		}
		bodies[url] = body
	}
	if b := bodies[js[1]]; !strings.Contains(b, "querySelectorAll") || strings.Contains(b, "NodeListOf") || strings.Contains(b, ": string") || strings.Contains(b, " as number") {
		t.Errorf("JS bundle isn't stripped TypeScript:\n%s", b)
	}
	scope := regexp.MustCompile(`\.site:where\(\[data-o-[0-9a-f]{8}\]\)\{color:navy\}`)
	if b := bodies[css[1]]; !scope.MatchString(b) || !strings.Contains(b, ".card{border:1px solid gray}") {
		t.Errorf("CSS bundle lacks the scoped and the global style:\n%s", b)
	}

	// Styles only (Share's script is lazy): a stylesheet, no page module.
	_, post := s.get("/blog/hello")
	if !cssLink.MatchString(post) || jsScript.MatchString(post) {
		t.Errorf("GET /blog/hello: want a stylesheet and no module:\n%s", post)
	}
	// No </head>: the stylesheet goes right after the doctype.
	_, bare := s.get("/bare")
	if m := cssLink.FindString(bare); m == "" || !strings.HasPrefix(bare, "<!DOCTYPE html>"+m) {
		t.Errorf("GET /bare: the stylesheet isn't right after the doctype:\n%s", bare)
	}
	_, search := s.get("/search?q=x")
	if strings.Contains(search, "<link") || strings.Contains(search, "<script") {
		t.Errorf("GET /search: a page without assets got tags:\n%s", search)
	}
}

var (
	islandRe = regexp.MustCompile(`<orbit-island src="/_orbit/island/[0-9a-f]{16}/Comments-[0-9a-f]{8}" props="[A-Za-z0-9_-]+"><p[^>]*>Loading comments…</p></orbit-island>`)
	lazyRe   = regexp.MustCompile(`<orbit-lazy component="(Share-[0-9a-f]{8})" client="visible" opts="200px"><share-buttons[^>]*><button[^>]*>Share</button></share-buttons>\s*</orbit-lazy>`)
	lazyLine = regexp.MustCompile(`\{Kind: "lazy", URL: "(/_orbit/Share-[A-Z0-9]{8}\.js)", ID: "(Share-[0-9a-f]{8})"\}`)
)

// checkLazy checks server:defer and client:* end to end (PLAN.md §13 "Lazy
// loading"): the island placeholder with its fallback, the <orbit-lazy>
// wrapper, and Share's lazy entry in the compiled manifest, served from
// /_orbit/ and not in the page bundle. The token's value is the runtime's.
func checkLazy(t *testing.T, s *server, dir string) {
	t.Helper()
	_, post := s.get("/blog/hello")
	if !islandRe.MatchString(post) {
		t.Errorf("GET /blog/hello: no island placeholder with its fallback:\n%s", post)
	}
	m := lazyRe.FindStringSubmatch(post)
	if m == nil {
		t.Fatalf("GET /blog/hello: no <orbit-lazy> wrapper:\n%s", post)
	}
	manifest, err := os.ReadFile(filepath.Join(dir, "orbit_gen/manifest/manifest.go"))
	if err != nil {
		t.Fatal(err)
	}
	l := lazyLine.FindSubmatch(manifest)
	if l == nil || string(l[2]) != m[1] {
		t.Fatalf("manifest has no lazy asset for %s:\n%s", m[1], manifest)
	}
	resp, js := s.get(string(l[1]))
	if resp.StatusCode != 200 || !strings.Contains(js, "share-buttons") {
		t.Errorf("GET %s: %d\n%s", l[1], resp.StatusCode, js)
	}
	if _, home := s.get("/"); strings.Contains(home, "Share-") {
		t.Errorf("GET /: a page without Share lists its entry:\n%s", home)
	}
}

// TestBuildSourcemap checks "sourcemap": true: external .map files next to
// the bundles, not linked from them, with .orbit sources.
func TestBuildSourcemap(t *testing.T) {
	needE2E(t)
	t.Parallel()
	dir := fixture(t, false)
	writeFile(t, dir, "orbit.json", `{"site": "https://fixture.example.com", "sourcemap": true}`)
	if code, out := runOrbit(t, dir, "build"); code != 0 || out != "" {
		t.Fatalf("orbit build: exit %d\n%s", code, out)
	}
	entries, err := os.ReadDir(filepath.Join(dir, "dist/client/_orbit"))
	if err != nil {
		t.Fatal(err)
	}
	maps := 0
	for _, e := range entries {
		name := e.Name()
		data, err := os.ReadFile(filepath.Join(dir, "dist/client/_orbit", name))
		if err != nil {
			t.Fatal(err)
		}
		switch {
		case strings.HasSuffix(name, ".map"):
			maps++
			if !strings.Contains(string(data), `"/src/`) || !strings.Contains(string(data), ".orbit") {
				t.Errorf("%s has no .orbit source:\n%s", name, data)
			}
		case strings.Contains(string(data), "sourceMappingURL"):
			t.Errorf("%s links its source map", name)
		}
		if !strings.HasSuffix(name, ".map") && !exists(filepath.Join(dir, "dist/client/_orbit", name+".map")) {
			t.Errorf("%s has no .map", name)
		}
	}
	if maps == 0 {
		t.Errorf("no .map files in %v", entries)
	}
}

// TestBuildAssetError checks that an esbuild error fails orbit build at
// its .orbit position.
func TestBuildAssetError(t *testing.T) {
	needE2E(t)
	t.Parallel()
	dir := fixture(t, false)
	card := filepath.Join(dir, "src/components/Card.orbit")
	src, err := os.ReadFile(card)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, dir, "src/components/Card.orbit", strings.Replace(string(src), `from "../scripts/util"`, `from "../scripts/nope"`, 1))
	code, out := runOrbit(t, dir, "build")
	if want := `src/components/Card.orbit:10:25: Could not resolve "../scripts/nope"` + "\n"; code != 1 || out != want {
		t.Errorf("orbit build: exit %d\n%s\nwant exit 1\n%s", code, out, want)
	}
	if exists(filepath.Join(dir, "dist/server")) || exists(filepath.Join(dir, "orbit_gen")) {
		t.Errorf("a failed build wrote dist/server or orbit_gen")
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
	if !strings.Contains("\n"+out, "\n"+want+"\n") {
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
