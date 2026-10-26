package views_test

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/orbit-z/orbit/orbittest"

	"example.com/blog/orbit_gen/app"
	"example.com/blog/orbit_gen/decl/components/comments"
	"example.com/blog/orbit_gen/decl/pages/account"
	"example.com/blog/orbit_gen/decl/pages/blog/slug_param"
	"example.com/blog/orbit_gen/decl/pages/search"
	"example.com/blog/src/locals"
)

func TestPostPage(t *testing.T) {
	html := orbittest.Render(t, slug_param.Render, slug_param.Props{},
		orbittest.WithParam("slug", "hello"))
	for _, want := range []string{
		"<title>Hello, Orbit · Orbit blog</title>",
		"<h1", ">Hello, Orbit</h1>",
		`<like-button data-slug="hello"`,
		`<orbit-lazy component="ShareButtons-`,
		"Loading comments…",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("missing %q in:\n%s", want, html)
		}
	}
}

func TestSearchPage(t *testing.T) {
	req := httptest.NewRequest("GET", "/search?q=security", nil)
	html := orbittest.Render(t, search.Render, search.Props{}, orbittest.WithRequest(req))
	if !strings.Contains(html, `href="/blog/safe-json"`) || strings.Contains(html, `href="/blog/hello"`) {
		t.Errorf("unexpected results:\n%s", html)
	}
}

func TestAccountPage(t *testing.T) {
	html := orbittest.Render(t, account.Render, account.Props{},
		orbittest.WithLocals(&locals.Locals{RequestID: "req-1", Theme: "dark"}))
	for _, want := range []string{`data-theme="dark"`, ">dark</strong>", ">req-1</code>"} {
		if !strings.Contains(html, want) {
			t.Errorf("missing %q in:\n%s", want, html)
		}
	}
}

func TestComments(t *testing.T) {
	html := orbittest.Render(t, comments.Render, comments.Props{PostID: 4})
	// Three levels of replies, and the javascript: URL filtered out.
	if n := strings.Count(html, `<ul class="thread"`); n != 3 {
		t.Errorf("%d nested threads, want 3:\n%s", n, html)
	}
	if !strings.Contains(html, `href="about:invalid#orbit"`) {
		t.Errorf("javascript: URL not filtered:\n%s", html)
	}
}

var islandRe = regexp.MustCompile(`<orbit-island src="([^"]+)" props="([^"]+)">`)

func TestSite(t *testing.T) {
	h := app.New().Handler()

	rec := orbittest.Get(t, h, "/blog/hello")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /blog/hello: status %d", rec.Code)
	}
	page := rec.Body.String()
	if !strings.Contains(page, ">Hello, Orbit</h1>") {
		t.Errorf("unexpected page:\n%s", page)
	}

	// The deferred comments: fetch the URL the browser would.
	m := islandRe.FindStringSubmatch(page)
	if m == nil {
		t.Fatalf("no island placeholder in:\n%s", page)
	}
	island := orbittest.Get(t, h, m[1]+"?p="+m[2])
	if island.Code != http.StatusOK || !strings.Contains(island.Body.String(), "Which is why they&#39;re deferred.") {
		t.Errorf("island: status %d\n%s", island.Code, island.Body.String())
	}

	if rec := orbittest.Get(t, h, "/blog/nope"); rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "Page not found") {
		t.Errorf("GET /blog/nope: status %d\n%s", rec.Code, rec.Body.String())
	}
	rec = orbittest.Get(t, h, "/blog/hello/")
	if rec.Code != http.StatusPermanentRedirect || rec.Header().Get("Location") != "/blog/hello" {
		t.Errorf("GET /blog/hello/: status %d, Location %q", rec.Code, rec.Header().Get("Location"))
	}
}
