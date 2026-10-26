// Package views_test tests the blog's components and pages with orbittest.
// Run `go tool orbit generate` first, so orbit_gen/ is up to date.
package views_test

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/orbit-z/orbit/orbittest"

	"example.com/blog/internal/posts"
	_ "example.com/blog/orbit_gen/app" // links every component's implementation
	"example.com/blog/orbit_gen/decl/components/card"
)

func TestMain(m *testing.M) {
	posts.CommentDelay = 0 // no need to wait for the simulated slow query
	os.Exit(m.Run())
}

var hello = posts.Post{
	Slug:      "hello",
	Title:     "Hello, <Orbit>",
	Tags:      []string{"news"},
	Published: time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC),
}

func TestCard(t *testing.T) {
	html := orbittest.Render(t, card.Render, card.Props{Post: hello},
		orbittest.WithSlot("default", "<p>A summary.</p>"))

	for _, want := range []string{
		`<a href="/blog/hello"`,
		"Hello, &lt;Orbit&gt;", // escaped
		`<time datetime="2026-10-20"`,
		">news</span>",
		"<p>A summary.</p>", // the default slot
	} {
		if !strings.Contains(html, want) {
			t.Errorf("missing %q in:\n%s", want, html)
		}
	}
}

func TestCardSnapshot(t *testing.T) {
	html := orbittest.Render(t, card.Render, card.Props{Post: hello},
		orbittest.WithSlot("default", "<p>A summary.</p>"))
	orbittest.Snapshot(t, "card", html)
}
