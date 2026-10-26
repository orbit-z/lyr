package build

import (
	"encoding/json"
	"html"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
)

// TestExampleBlog builds examples/blog the way a fresh clone does (no
// orbit_gen/), runs its own tests, builds it with orbit build and checks
// the running dist/server against PLAN.md §13, then checks that the
// --embed binary serves everything from an empty directory.
func TestExampleBlog(t *testing.T) {
	needE2E(t)
	t.Parallel()
	dir := exampleBlog(t)

	// Fresh clone: generate, then the plain go commands work.
	if code, out := runOrbit(t, dir, "generate"); code != 0 {
		t.Fatalf("orbit generate: exit %d\n%s", code, out)
	}
	gen := readTree(t, filepath.Join(dir, "orbit_gen"))
	goRun(t, dir, "vet", "./...")
	goRun(t, dir, "build", "./...")
	// LikeButton's script reads its typed data (PLAN.md §5c): orbit check
	// type-checks it against the generated module (or skips the TypeScript
	// half without Bun, with a notice).
	if !exists(filepath.Join(dir, "orbit_gen/types/src/components/LikeButton.orbit.d.ts")) {
		t.Error("orbit generate wrote no client module for LikeButton")
	}
	if code, out := runOrbit(t, dir, "check"); code != 0 {
		t.Errorf("orbit check: exit %d\n%s", code, out)
	}
	// The example's own orbittest tests (internal/views_test).
	if out := goRun(t, dir, "test", "-count=1", "./..."); !strings.Contains(out, "ok  \texample.com/blog/internal/views_test") {
		t.Errorf("go test ./... didn't run views_test:\n%s", out)
	}

	if code, out := runOrbit(t, dir, "build"); code != 0 || out != "" {
		t.Fatalf("orbit build: exit %d\n%s", code, out)
	}
	for _, rel := range []string{"dist/server", "dist/client/_orbit", "dist/client/favicon.svg", "dist/client/robots.txt"} {
		if !exists(filepath.Join(dir, rel)) {
			t.Errorf("no %s", rel)
		}
	}
	if html := htmlFiles(t, filepath.Join(dir, "dist")); len(html) > 0 {
		t.Errorf("HTML files in dist/: %v", html)
	}
	// The build flavor (PLAN.md §3.5): orbit_gen/ unchanged, the same
	// responses, and the example's tests pass on it too.
	checkBuildFlavor(t, dir, gen, "impl/pages/blog/slug_param/render.go")
	checkFlavors(t, dir, "example.com/blog", []string{"/", "/about", "/account", "/search?q=security", "/blog/hello", "/blog/safe-json", "/nope"})
	if out := goRun(t, dir, "test", "-count=1", "-overlay=.orbit/build/overlay.json", "./..."); !strings.Contains(out, "ok  \texample.com/blog/internal/views_test") {
		t.Errorf("go test -overlay ./... didn't run views_test:\n%s", out)
	}

	s := start(t, dir, filepath.Join(dir, "dist/server"))
	checkBlogPages(t, s)
	checkBlogPost(t, s)
	checkBlogMarkdown(t, s)
	checkBlogRoutes(t, s)
	if code := s.stop(); code != 0 {
		t.Errorf("dist/server: exit %d after SIGINT", code)
	}
	if !strings.Contains(s.stderr.String(), "POST /blog/hello/like 200") {
		t.Errorf("the logging middleware didn't log the like request")
	}
	if t.Failed() {
		t.Logf("server output:\n%s", s.stderr)
	}

	// Single binary: only dist/server, run from an empty directory.
	if code, out := runOrbit(t, dir, "build", "--embed"); code != 0 || out != "" {
		t.Fatalf("orbit build --embed: exit %d\n%s", code, out)
	}
	alone := t.TempDir()
	bin, err := os.ReadFile(filepath.Join(dir, "dist/server"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(alone, "server"), bin, 0o755); err != nil {
		t.Fatal(err)
	}
	e := start(t, alone, filepath.Join(alone, "server"))
	resp, post := e.get("/blog/hello")
	css := blogCSS.FindStringSubmatch(post)
	if resp.StatusCode != 200 || css == nil {
		t.Fatalf("embed: GET /blog/hello: %d\n%s", resp.StatusCode, post)
	}
	for _, path := range []string{css[1], "/robots.txt", "/favicon.svg"} {
		if resp, body := e.get(path); resp.StatusCode != 200 || body == "" {
			t.Errorf("embed: GET %s: %d", path, resp.StatusCode)
		}
	}
	if code := e.stop(); code != 0 {
		t.Errorf("embedded server: exit %d\n%s", code, e.stderr)
	}
}

// exampleBlog copies examples/blog into a temporary directory, without
// generated or built files, and points its replace directive at this
// repository.
func exampleBlog(t *testing.T) string {
	t.Helper()
	src := filepath.Join(repoRoot, "examples/blog")
	dir := t.TempDir()
	if err := os.CopyFS(dir, os.DirFS(src)); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"orbit_gen", ".orbit", "dist"} {
		if err := os.RemoveAll(filepath.Join(dir, rel)); err != nil {
			t.Fatal(err)
		}
	}
	gomod, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	const replace = "replace github.com/orbit-z/orbit => ../..\n"
	for _, want := range []string{"module example.com/blog\n", "\ngo 1.27\n", "\ntoolchain go1.27.3\n", "\ntool github.com/orbit-z/orbit/cmd/orbit\n", replace} {
		if !strings.Contains(string(gomod), want) {
			t.Fatalf("examples/blog/go.mod lacks %q:\n%s", want, gomod)
		}
	}
	abs := strings.Replace(string(gomod), replace, "replace github.com/orbit-z/orbit => "+filepath.ToSlash(repoRoot)+"\n", 1)
	writeFile(t, dir, "go.mod", abs)
	return dir
}

// goRun runs the go command in dir and returns its output, failing the
// test if it fails.
func goRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("go", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), goEnv...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

var (
	blogCSS    = regexp.MustCompile(`<link rel="stylesheet" href="(/_orbit/blog-slug-[A-Z0-9]{8}\.css)">`)
	blogJS     = regexp.MustCompile(`<script type="module" src="(/_orbit/blog-slug-[A-Z0-9]{8}\.js)"></script>`)
	searchJS   = regexp.MustCompile(`<script type="module" src="(/_orbit/search-[A-Z0-9]{8}\.js)"></script>`)
	blogIsland = regexp.MustCompile(`<orbit-island src="(/_orbit/island/[0-9a-f]{16}/Comments-[0-9a-f]{8})" props="([A-Za-z0-9_-]+)"><p class="loading"[^>]*>Loading comments…</p></orbit-island>`)
	blogLazy   = regexp.MustCompile(`"lazy":\{"(ShareButtons-[0-9a-f]{8})":\["(/_orbit/ShareButtons-[A-Z0-9]{8}\.js)"\]\}`)
	titlesJSON = regexp.MustCompile(`<script type="application/json" id="post-titles-data">(.*?)</script>`)
)

// checkBlogPages checks the index, search, account and 404 pages, and the
// trailing-slash rules.
func checkBlogPages(t *testing.T, s *server) {
	t.Helper()
	tests := []struct {
		path     string
		status   int
		body     []string
		location string
	}{
		{path: "/", status: 200, body: []string{
			"<title>Posts · Orbit blog</title>",
			`href="/blog/hello"`,
			"Why &lt;/script&gt; can&#39;t break your JSON",
			// Left untouched: a script with attributes isn't processed.
			`<script type="application/ld+json">{"@context":"https://schema.org","@type":"Blog","name":"Orbit blog"}</script>`,
		}},
		{path: "/search?q=go", status: 200, body: []string{`value="go"`, ">go</strong>", `href="/blog/go-templates"`}},
		{path: "/search", status: 200, body: []string{"Type something to search."}},
		{path: "/blog/nope", status: 404, body: []string{"Page not found", "/blog/nope"}},
		{path: "/nope", status: 404, body: []string{"Page not found", "/nope"}},
		{path: "/blog/hello/", status: 308, location: "/blog/hello"},
		{path: "/search/?q=go", status: 308, location: "/search?q=go"},
		{path: "/robots.txt", status: 200, body: []string{"User-agent: *"}},
		// Files aren't redirected: a trailing slash is just another path.
		{path: "/robots.txt/", status: 404},
	}
	for _, tt := range tests {
		resp, body := s.get(tt.path)
		if resp.StatusCode != tt.status {
			t.Errorf("GET %s: status %d, want %d\n%s", tt.path, resp.StatusCode, tt.status, body)
			continue
		}
		if got := resp.Header.Get("Location"); got != tt.location {
			t.Errorf("GET %s: Location %q, want %q", tt.path, got, tt.location)
		}
		for _, want := range tt.body {
			if !strings.Contains(body, want) {
				t.Errorf("GET %s: no %q in:\n%s", tt.path, want, body)
			}
		}
		if resp.Header.Get("X-Request-Id") == "" {
			t.Errorf("GET %s: no X-Request-ID from the requestInfo middleware", tt.path)
		}
	}

	resp, _ := s.get("/search?q=go")
	if resp.Header.Get("Content-Type") != "text/html; charset=utf-8" || resp.Header.Get("Content-Length") == "" {
		t.Errorf("GET /search?q=go: headers %v", resp.Header)
	}

	// set:json: inert in the HTML, and JSON.parse gives back the titles.
	_, search := s.get("/search")
	m := titlesJSON.FindStringSubmatch(search)
	var titles []string
	if m == nil || strings.Contains(m[1], "</") || json.Unmarshal([]byte(m[1]), &titles) != nil || !slices.Contains(titles, "Why </script> can't break your JSON") {
		t.Errorf("GET /search: bad set:json block %q", m)
	}
	// The page script reads it: TypeScript, stripped of its types.
	if js := searchJS.FindStringSubmatch(search); js == nil {
		t.Errorf("GET /search: no page module:\n%s", search)
	} else if _, b := s.get(js[1]); !strings.Contains(b, "post-titles-data") || strings.Contains(b, "string[]") {
		t.Errorf("GET %s: not the stripped search script:\n%s", js[1], b)
	}

	// Locals from the net/http middleware, read by the page.
	resp, account := s.do("GET", "/account", "theme=dark")
	id := resp.Header.Get("X-Request-Id")
	if resp.StatusCode != 200 || id == "" || !strings.Contains(account, `data-theme="dark"`) ||
		!strings.Contains(account, ">dark</strong>") || !strings.Contains(account, "<code data-o-") || !strings.Contains(account, ">"+id+"</code>") {
		t.Errorf("GET /account with theme=dark: %d, X-Request-ID %q\n%s", resp.StatusCode, id, account)
	}
	if _, light := s.get("/account"); !strings.Contains(light, ">light</strong>") {
		t.Errorf("GET /account: no light theme:\n%s", light)
	}
}

var aboutCSS = regexp.MustCompile(`<link rel="stylesheet" href="(/_orbit/about-[A-Z0-9]{8}\.css)">`)

// checkBlogMarkdown checks the Markdown page /about (PLAN.md §2.4): its
// layout and frontmatter props, heading IDs, highlighted code and the
// highlight stylesheet first in its CSS; and src/content/intro.md imported
// by the index, with its frontmatter and its link rewritten.
func checkBlogMarkdown(t *testing.T, s *server) {
	t.Helper()
	resp, about := s.get("/about")
	for _, want := range []string{
		"<!DOCTYPE html>",
		"<title>About this blog · Orbit blog</title>",
		`<h2 id="how-its-built">How it&rsquo;s built</h2>`,
		`<pre class="chroma" tabindex="0" data-language="go"><code><span class="nx">all</span><span class="p">,</span>`,
		`<a href="#colophon">colophon</a>`,
		"Rendered by Orbit, request <code",
	} {
		if resp.StatusCode != 200 || !strings.Contains(about, want) {
			t.Fatalf("GET /about: %d, no %q in:\n%s", resp.StatusCode, want, about)
		}
	}
	css := aboutCSS.FindStringSubmatch(about)
	if css == nil {
		t.Fatalf("GET /about: no stylesheet:\n%s", about)
	}
	_, sheet := s.get(css[1])
	if !strings.HasPrefix(sheet, ".chroma{") || !strings.Contains(sheet, "light-dark(") || !strings.Contains(sheet, "--accent") {
		t.Errorf("GET %s: not the highlight CSS then the layout's:\n%s", css[1], sheet)
	}
	_, index := s.get("/")
	for _, want := range []string{
		`<p class="tagline"`,
		">Notes on Go, templates and the web</p>",
		`<div class="intro" data-o-`,
		`<p>Short posts about building <strong>Orbit</strong>`,
		`<a href="/about#how-its-built">about this blog</a>`,
	} {
		if !strings.Contains(index, want) {
			t.Errorf("GET /: no %q in:\n%s", want, index)
		}
	}
	if strings.Contains(index, "chroma") {
		t.Errorf("GET /: highlight CSS on a page without highlighted code:\n%s", index)
	}
}

// checkBlogPost checks /blog/hello: its assets, the lazy ShareButtons, the
// deferred Comments island and its endpoint.
// likeAttr is LikeButton's orbit.JSON attribute.
var likeAttr = regexp.MustCompile(`<like-button data-like='([^']*)'`)

func checkBlogPost(t *testing.T, s *server) {
	t.Helper()
	resp, post := s.get("/blog/hello")
	if resp.StatusCode != 200 || !strings.Contains(post, ">Hello, Orbit</h1>") || !strings.Contains(post, `<like-button data-like='{"slug":"hello","likes":`) {
		t.Fatalf("GET /blog/hello: %d\n%s", resp.StatusCode, post)
	}
	// The orbit.JSON attribute parses as JSON.
	if m := likeAttr.FindStringSubmatch(post); m == nil || json.Unmarshal([]byte(html.UnescapeString(m[1])), new(struct {
		Slug  string `json:"slug"`
		Likes int    `json:"likes"`
	})) != nil {
		t.Errorf("GET /blog/hello: data-like isn't JSON: %v", m)
	}

	// The hashed stylesheet and module, in <head>, after the doctype.
	css, js := blogCSS.FindStringSubmatch(post), blogJS.FindStringSubmatch(post)
	if css == nil || js == nil || !strings.HasPrefix(post, "<!DOCTYPE html>") || !strings.Contains(post, css[0]+js[0]+"</head>") {
		t.Fatalf("GET /blog/hello: no hashed assets at the end of <head>:\n%s", post)
	}
	for _, url := range []string{css[1], js[1]} {
		resp, _ := s.get(url)
		if resp.StatusCode != 200 || resp.Header.Get("Cache-Control") != "public, max-age=31536000, immutable" {
			t.Errorf("GET %s: %d, Cache-Control %q", url, resp.StatusCode, resp.Header.Get("Cache-Control"))
		}
		if resp, _ := s.get(url + "/"); resp.StatusCode != 404 {
			t.Errorf("GET %s/: %d, want 404 (no redirect)", url, resp.StatusCode)
		}
	}
	if _, b := s.get(css[1]); !strings.Contains(b, "[data-o-") {
		t.Errorf("GET %s: no scoped selectors:\n%s", css[1], b)
	}
	// LikeButton's script, with util.ts bundled in and its types stripped.
	if _, b := s.get(js[1]); !strings.Contains(b, "like-button") || !strings.Contains(b, `"like"`) ||
		strings.Contains(b, ": number") || strings.Contains(b, ": string") || strings.Contains(b, "Promise<void>") || strings.Contains(b, "import ") {
		t.Errorf("GET %s: not the bundled, stripped LikeButton script:\n%s", js[1], b)
	}

	// ShareButtons is lazy: in its own entry, not in the page module.
	lazy := blogLazy.FindStringSubmatch(post)
	if lazy == nil || !strings.Contains(post, `<orbit-lazy component="`+lazy[1]+`" client="visible" opts="200px"><share-buttons`) {
		t.Errorf("GET /blog/hello: no lazy ShareButtons:\n%s", post)
	} else {
		if _, b := s.get(lazy[2]); !strings.Contains(b, "share-buttons") || strings.Contains(b, "HTMLButtonElement>") {
			t.Errorf("GET %s: not the stripped ShareButtons script:\n%s", lazy[2], b)
		}
		if _, b := s.get(js[1]); strings.Contains(b, "share-buttons") {
			t.Errorf("GET %s: the page module has the lazy ShareButtons script", js[1])
		}
	}

	// Comments are deferred: the page holds the fallback and doesn't wait
	// for their slow query (posts.CommentDelay, 200 ms).
	m := blogIsland.FindStringSubmatch(post)
	if m == nil || !strings.Contains(post, "<style>orbit-island,orbit-lazy{display:contents}</style>") {
		t.Fatalf("GET /blog/hello: no island placeholder with its fallback, or no loader style:\n%s", post)
	}
	fastest := time.Hour
	for range 3 {
		begin := time.Now()
		s.get("/blog/hello")
		fastest = min(fastest, time.Since(begin))
	}
	if fastest >= 200*time.Millisecond {
		t.Errorf("GET /blog/hello took %v: it waits for the deferred comments", fastest)
	}

	resp, island := s.get(m[1] + "?p=" + m[2])
	for k, want := range map[string]string{
		"Content-Type":  "text/html; charset=utf-8",
		"Cache-Control": "private, no-store",
		"X-Robots-Tag":  "noindex",
	} {
		if got := resp.Header.Get(k); got != want {
			t.Errorf("island: %s %q, want %q", k, got, want)
		}
	}
	if resp.StatusCode != 200 || resp.Header.Get("Content-Length") == "" || resp.Header.Get("X-Request-Id") == "" {
		t.Errorf("island: %d, headers %v", resp.StatusCode, resp.Header)
	}
	// CommentThread renders itself three levels deep, and the spammer's
	// javascript: URL is filtered.
	if n := strings.Count(island, `<ul class="thread"`); n != 3 || !strings.Contains(island, "Which is why they&#39;re deferred.") ||
		!strings.Contains(island, `<a href="about:invalid#orbit" rel="nofollow ugc"`) ||
		strings.Contains(island, "<link") || strings.Contains(island, "<script") || strings.Contains(island, "<!DOCTYPE") {
		t.Errorf("island: %d nested threads, want 3:\n%s", n, island)
	}
}

// checkBlogRoutes checks the custom routes in cmd/server, which take
// precedence over the pages.
func checkBlogRoutes(t *testing.T, s *server) {
	t.Helper()
	resp, body := s.get("/api/posts")
	var list []struct{ Slug, Title string }
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "application/json" || json.Unmarshal([]byte(body), &list) != nil || len(list) != 4 || list[0].Slug != "hello" {
		t.Errorf("GET /api/posts: %d %q\n%s", resp.StatusCode, resp.Header.Get("Content-Type"), body)
	}
	for _, n := range []string{"1", "2"} {
		resp, body := s.do("POST", "/blog/hello/like", "")
		if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "text/html; charset=utf-8" ||
			!strings.HasPrefix(body, `<like-button data-like='{"slug":"hello","likes":`+n+`}'`) || strings.Contains(body, "<html") {
			t.Errorf("POST /blog/hello/like: %d\n%s", resp.StatusCode, body)
		}
	}
	if _, post := s.get("/blog/hello"); !strings.Contains(post, `"likes":2}'`) {
		t.Errorf("GET /blog/hello: the likes aren't counted")
	}
	if resp, _ := s.do("POST", "/blog/nope/like", ""); resp.StatusCode != http.StatusNotFound {
		t.Errorf("POST /blog/nope/like: %d, want 404", resp.StatusCode)
	}
}
