// Package views_test tests the site's pages and components with orbittest.
// Run go tool orbit generate first.
package views_test

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/bertrandkhe/orbit/orbittest"

	"github.com/bertrandkhe/orbit/docs/site/internal/content"
	"github.com/bertrandkhe/orbit/docs/site/orbit_gen/app"
	"github.com/bertrandkhe/orbit/docs/site/orbit_gen/decl/components/sidebar"
	"github.com/bertrandkhe/orbit/docs/site/orbit_gen/decl/pages/search"
	"github.com/bertrandkhe/orbit/docs/site/orbit_gen/decl/pages/slug_rest"
	"github.com/bertrandkhe/orbit/docs/site/src/locals"
)

func TestDocPageRenders(t *testing.T) {
	html := orbittest.Render(t, slug_rest.Render, slug_rest.Props{},
		orbittest.WithParam("slug", "reference/cli"),
		orbittest.WithRequest(httptest.NewRequest("GET", "/reference/cli", nil)))
	for _, want := range []string{
		"<title>CLI · Orbit docs</title>",
		`<h1 id="cli">CLI`,
		`id="orbit-build"`,
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
	html := orbittest.Render(t, sidebar.Render, sidebar.Props{Nav: docs.Nav, Current: "/routing"})
	got := currentRe.FindAllStringSubmatch(html, -1)
	if len(got) != 1 || got[0][1] != "/routing" {
		t.Errorf("aria-current links: %v, want only /routing", got)
	}

	// Through the whole site: the page's own link is the current one.
	page := orbittest.Get(t, app.New().Handler(), "/reference/template-syntax").Body.String()
	got = currentRe.FindAllStringSubmatch(page, -1)
	if len(got) != 1 || got[0][1] != "/reference/template-syntax" {
		t.Errorf("aria-current links: %v, want only /reference/template-syntax", got)
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
		{"/DECISIONS", 308, "/decisions"},
		{"/routing/", 308, "/routing"},
		{"/search", 200, ""},
	} {
		rec := orbittest.Get(t, h, tt.path)
		if rec.Code != tt.code || rec.Header().Get("Location") != tt.location {
			t.Errorf("GET %s: %d %q, want %d %q", tt.path, rec.Code, rec.Header().Get("Location"), tt.code, tt.location)
		}
	}
	nf := orbittest.Get(t, h, "/no-such-page").Body.String()
	if !strings.Contains(nf, "Page not found") || !strings.Contains(nf, "<code>/no-such-page</code>") {
		t.Errorf("404 page:\n%s", nf)
	}
}

func TestSearch(t *testing.T) {
	req := httptest.NewRequest("GET", "/search?q=EnsureLocals", nil)
	html := orbittest.Render(t, search.Render, search.Props{}, orbittest.WithRequest(req))
	if !strings.Contains(html, `href="/middleware`) {
		t.Errorf("no result in middleware.md:\n%s", html)
	}
	if !regexp.MustCompile(`<mark[^>]*>EnsureLocals</mark>`).MatchString(html) {
		t.Errorf("match not highlighted")
	}

	req = httptest.NewRequest("GET", "/search?q=zzqxv+nothing", nil)
	html = orbittest.Render(t, search.Render, search.Props{}, orbittest.WithRequest(req))
	if !strings.Contains(html, "No results for") {
		t.Errorf("expected no results:\n%s", html)
	}

	// The query is escaped, in the text and in the search box.
	req = httptest.NewRequest("GET", `/search?q=<script>"`, nil)
	html = orbittest.Render(t, search.Render, search.Props{}, orbittest.WithRequest(req))
	if strings.Contains(html, "<script>\"") {
		t.Errorf("query not escaped")
	}
}

func TestThemeFromLocals(t *testing.T) {
	h := app.New().Handler()
	if body := orbittest.Get(t, h, "/").Body.String(); strings.Contains(body, "data-theme=") {
		t.Errorf("data-theme set without a theme")
	}
	rec := orbittest.Get(t, h, "/", orbittest.WithLocals(&locals.Locals{Theme: "dark"}))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `<html lang="en" data-theme="dark"`) {
		t.Errorf("dark theme not rendered: %d\n%.300s", rec.Code, rec.Body.String())
	}
}
