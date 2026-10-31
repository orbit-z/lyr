// Package views_test tests the site's pages and components with lyrtest.
// Run go tool lyr generate first.
package views_test

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/orbit-z/lyr/lyrtest"

	"github.com/orbit-z/lyr/docs/site/internal/content"
	"github.com/orbit-z/lyr/docs/site/lyr_gen/app"
	"github.com/orbit-z/lyr/docs/site/lyr_gen/decl/components/sidebar"
	"github.com/orbit-z/lyr/docs/site/lyr_gen/decl/pages/search"
	"github.com/orbit-z/lyr/docs/site/lyr_gen/decl/pages/slug_rest"
	"github.com/orbit-z/lyr/docs/site/src/locals"
)

func TestDocPageRenders(t *testing.T) {
	html := lyrtest.Render(t, slug_rest.Render, slug_rest.Props{},
		lyrtest.WithParam("slug", "reference/cli"),
		lyrtest.WithRequest(httptest.NewRequest("GET", "/reference/cli", nil)))
	for _, want := range []string{
		"<title>CLI · Lyr docs</title>",
		`<h1 id="cli">CLI`,
		`id="lyr-build"`,
		`href="/configuration#site"`, // ../configuration.md#site, rewritten
	} {
		if !strings.Contains(html, want) {
			t.Errorf("missing %q", want)
		}
	}
}

var currentRe = regexp.MustCompile(`<a href="([^"]*)" aria-current="page"`)

func TestSidebarMarksCurrentPage(t *testing.T) {
	docs, err := content.Get()
	if err != nil {
		t.Fatal(err)
	}
	html := lyrtest.Render(t, sidebar.Render, sidebar.Props{Nav: docs.Nav, Current: "/routing"})
	got := currentRe.FindAllStringSubmatch(html, -1)
	if len(got) != 1 || got[0][1] != "/routing" {
		t.Errorf("aria-current links: %v, want only /routing", got)
	}

	// Through the whole site: the page's own link is the current one, in
	// both copies of the sidebar (beside the page, and in the mobile menu).
	page := lyrtest.Get(t, app.New().Handler(), "/reference/template-syntax").Body.String()
	got = currentRe.FindAllStringSubmatch(page, -1)
	if len(got) != 2 || got[0][1] != "/reference/template-syntax" || got[1][1] != "/reference/template-syntax" {
		t.Errorf("aria-current links: %v, want only /reference/template-syntax, twice", got)
	}
	if strings.Contains(page, "<details class=\"nav-menu\" open") {
		t.Errorf("the mobile menu is open by default")
	}
}

func TestStatusCodes(t *testing.T) {
	h := app.New().Handler()
	for _, tt := range []struct {
		path     string
		code     int
		location string
	}{
		{"/", 200, ""},
		{"/getting-started", 200, ""},
		{"/reference/runtime-api", 200, ""},
		{"/no-such-page", 404, ""},
		{"/reference/nope", 404, ""},
		{"/readme", 404, ""}, // README.md is the home page
		{"/Reference/CLI", 308, "/reference/cli"},
		{"/decisions", 404, ""}, // DECISIONS.md isn't on the site
		{"/DECISIONS", 404, ""},
		{"/changelog", 200, ""},
		{"/sitemap.xml", 200, ""},
		{"/robots.txt", 200, ""},
		{"/routing/", 308, "/routing"},
		{"/search", 200, ""},
	} {
		rec := lyrtest.Get(t, h, tt.path)
		if rec.Code != tt.code || rec.Header().Get("Location") != tt.location {
			t.Errorf("GET %s: %d %q, want %d %q", tt.path, rec.Code, rec.Header().Get("Location"), tt.code, tt.location)
		}
	}
	nf := lyrtest.Get(t, h, "/no-such-page").Body.String()
	if !strings.Contains(nf, "Page not found") || !strings.Contains(nf, "<code>/no-such-page</code>") {
		t.Errorf("404 page:\n%s", nf)
	}
}

func TestSearch(t *testing.T) {
	req := httptest.NewRequest("GET", "/search?q=EnsureLocals", nil)
	html := lyrtest.Render(t, search.Render, search.Props{}, lyrtest.WithRequest(req))
	if !strings.Contains(html, `href="/middleware`) {
		t.Errorf("no result in middleware.md:\n%s", html)
	}
	if !regexp.MustCompile(`<mark[^>]*>EnsureLocals</mark>`).MatchString(html) {
		t.Errorf("match not highlighted")
	}

	req = httptest.NewRequest("GET", "/search?q=zzqxv+nothing", nil)
	html = lyrtest.Render(t, search.Render, search.Props{}, lyrtest.WithRequest(req))
	if !strings.Contains(html, "No results for") {
		t.Errorf("expected no results:\n%s", html)
	}

	// The query is escaped, in the text and in the search box.
	req = httptest.NewRequest("GET", `/search?q=<script>"`, nil)
	html = lyrtest.Render(t, search.Render, search.Props{}, lyrtest.WithRequest(req))
	if strings.Contains(html, "<script>\"") {
		t.Errorf("query not escaped")
	}
}

func TestThemeFromLocals(t *testing.T) {
	h := app.New().Handler()
	if body := lyrtest.Get(t, h, "/").Body.String(); strings.Contains(body, "data-theme=") {
		t.Errorf("data-theme set without a theme")
	}
	rec := lyrtest.Get(t, h, "/", lyrtest.WithLocals(&locals.Locals{Theme: "dark"}))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `<html lang="en" data-theme="dark"`) {
		t.Errorf("dark theme not rendered: %d\n%.300s", rec.Code, rec.Body.String())
	}
}

func TestLaunchPages(t *testing.T) {
	h := app.New().Handler()

	home := lyrtest.Get(t, h, "/")
	body := home.Body.String()
	for _, want := range []string{
		"Astro's developer experience, with much faster SSR",
		"go install github.com/orbit-z/lyr/cmd/lyr@latest",
		"14.6×", "57×",
		`href="https://github.com/orbit-z/lyr"`,
		`<link rel="canonical" href="https://lyr.example/">`,
		`<meta property="og:image" content="https://lyr.example/og.png">`,
		"not affiliated with",
		`class="version"`,
		`<span class="kn">`, // the highlighted example
	} {
		if !strings.Contains(body, want) {
			t.Errorf("home: missing %q", want)
		}
	}
	for _, path := range []string{"/", "/routing", "/no-such-page"} {
		if cc := lyrtest.Get(t, h, path).Header().Get("Cache-Control"); cc != "private, no-cache" {
			t.Errorf("%s: Cache-Control %q", path, cc)
		}
	}
	if nf := lyrtest.Get(t, h, "/nope").Body.String(); !strings.Contains(nf, `<meta name="robots" content="noindex">`) || strings.Contains(nf, "canonical") {
		t.Errorf("404 page indexable")
	}

	sm := lyrtest.Get(t, h, "/sitemap.xml")
	if ct := sm.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/xml") {
		t.Errorf("sitemap Content-Type %q", ct)
	}
	if b := sm.Body.String(); !strings.Contains(b, "<loc>https://lyr.example/getting-started</loc>") || strings.Contains(b, "decisions") {
		t.Errorf("sitemap:\n%s", b)
	}
	if b := lyrtest.Get(t, h, "/robots.txt").Body.String(); !strings.Contains(b, "Sitemap: https://lyr.example/sitemap.xml") {
		t.Errorf("robots.txt:\n%s", b)
	}

	// A typo still finds the page.
	req := httptest.NewRequest("GET", "/search?q=Tailwnd", nil)
	html := lyrtest.Render(t, search.Render, search.Props{}, lyrtest.WithRequest(req))
	if !strings.Contains(html, `<a href="/tailwind"`) {
		t.Errorf("no typo-tolerant result for Tailwnd")
	}
	// The page whose title matches comes first.
	req = httptest.NewRequest("GET", "/search?q=tailwind", nil)
	html = lyrtest.Render(t, search.Render, search.Props{}, lyrtest.WithRequest(req))
	if i := strings.Index(html, `<ol class="results"`); i < 0 || !strings.HasPrefix(html[strings.Index(html[i:], "<a href=")+i:], `<a href="/tailwind"`) {
		t.Errorf("tailwind: the Tailwind page isn't first")
	}
}
