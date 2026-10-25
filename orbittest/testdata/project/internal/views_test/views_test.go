package views_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/bertrandkhe/orbit/orbittest"

	"example.com/site/orbit_gen/app"
	card "example.com/site/orbit_gen/decl/components/card"
	"example.com/site/orbit_gen/decl/pages/blog/slug_param"
)

func TestCard(t *testing.T) {
	html := orbittest.Render(t, card.Render,
		card.Props{Title: "Hello <world>", Featured: true},
		orbittest.WithSlot("default", "<em>New</em>"))
	for _, want := range []string{">Hello &lt;world&gt;</h2>", "<em>New</em>", "data-featured", `class="card`} {
		if !strings.Contains(html, want) {
			t.Errorf("missing %q in:\n%s", want, html)
		}
	}
	if strings.Contains(html, "<link") {
		t.Errorf("Render injected head tags:\n%s", html)
	}
	orbittest.Snapshot(t, "card", html)
}

func TestPage(t *testing.T) {
	html := orbittest.Render(t, slug_param.Render, slug_param.Props{},
		orbittest.WithParam("slug", "hello"))
	for _, want := range []string{"<title>Post hello</title>", ">hello</h2>", "<p>slug=hello</p>"} {
		if !strings.Contains(html, want) {
			t.Errorf("missing %q in:\n%s", want, html)
		}
	}
	orbittest.Snapshot(t, "pages/post", html)
}

func TestGet(t *testing.T) {
	h := app.New().Handler()
	rec := orbittest.Get(t, h, "/blog/hello")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "<title>Post hello</title>") {
		t.Fatalf("GET /blog/hello: %d\n%s", rec.Code, rec.Body.String())
	}
	orbittest.Snapshot(t, "get/post", rec.Body.String())
	if rec := orbittest.Get(t, h, "/blog/nope"); rec.Code != http.StatusNotFound {
		t.Errorf("GET /blog/nope: %d, want 404", rec.Code)
	}
	if rec := orbittest.Get(t, h, "/blog/hello/"); rec.Code != http.StatusPermanentRedirect || rec.Header().Get("Location") != "/blog/hello" {
		t.Errorf("GET /blog/hello/: %d, Location %q", rec.Code, rec.Header().Get("Location"))
	}
}
