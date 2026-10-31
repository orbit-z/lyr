package concurrent_test

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/orbit-z/lyr/lyrtest"

	"example.com/site/lyr_gen/app"
	"example.com/site/lyr_gen/decl/pages/concurrent"
	"example.com/site/lyr_gen/decl/pages/sequential"
)

var cssName = regexp.MustCompile(`/_lyr/[a-z]+-[A-Z0-9]+\.css`)

// same normalizes the page path and the page's CSS file name.
func same(html string) string {
	return cssName.ReplaceAllString(strings.ReplaceAll(html, "sequential", "concurrent"), "page.css")
}

// TestConcurrentPage: two server:concurrent components that each sleep
// 30 ms give the same page as in order, the headers they set, and
// overlap (PLAN.md §3.6).
func TestConcurrentPage(t *testing.T) {
	h := app.New().Handler()
	want := lyrtest.Get(t, h, "/sequential")
	best := time.Hour
	for range 3 {
		start := time.Now()
		got := lyrtest.Get(t, h, "/concurrent")
		best = min(best, time.Since(start))
		if got.Code != 200 || same(got.Body.String()) != same(want.Body.String()) {
			t.Fatalf("got %d:\n%s\nwant:\n%s", got.Code, got.Body, want.Body)
		}
		if got.Header().Get("X-Weather") != "1" || got.Header().Get("X-Posts") != "1" {
			t.Errorf("headers %v", got.Header())
		}
	}
	if best >= 60*time.Millisecond {
		t.Errorf("two 30 ms components took %v, want well under 60 ms", best)
	}
	// lyrtest.Render joins the marked components too.
	html := lyrtest.Render(t, concurrent.Render, concurrent.Props{})
	if seq := lyrtest.Render(t, sequential.Render, sequential.Props{}); html != same(seq) {
		t.Errorf("Render:\n%s\nwant:\n%s", html, seq)
	}
}
