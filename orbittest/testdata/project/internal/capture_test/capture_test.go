package capture_test

import (
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/orbit-z/orbit"
	"github.com/orbit-z/orbit/orbittest"

	"example.com/site/orbit_gen/app"
	"example.com/site/orbit_gen/decl/pages/capture"
)

var halves = regexp.MustCompile(`(?s)<div id="in-place">(.*)</div>\s*<div id="captured">(.*)</div>`)

// TestCaptureSameAsInPlace: orbit.RenderHTML of a slot with nested
// components, class:list, a client:* marker and a server:defer placeholder
// gives exactly the in-place HTML.
func TestCaptureSameAsInPlace(t *testing.T) {
	html := orbittest.Render(t, capture.Render, capture.Props{})
	m := halves.FindStringSubmatch(html)
	if m == nil {
		t.Fatalf("no halves in:\n%s", html)
	}
	for _, want := range []string{`class="rich featured"`, "<orbit-lazy", "<orbit-island", "Loading…", "A &amp; B"} {
		if !strings.Contains(m[1], want) {
			t.Errorf("in-place half lacks %q:\n%s", want, m[1])
		}
	}
	if m[1] != m[2] {
		t.Errorf("captured HTML differs:\nin place: %s\ncaptured: %s", m[1], m[2])
	}
}

// TestCaptureDepthAndErrorHandler: runaway recursion through captures stops
// at the depth limit, and the error reaches WithErrorHandler.
func TestCaptureDepthAndErrorHandler(t *testing.T) {
	var got error
	h := app.New(orbit.WithErrorHandler(func(w http.ResponseWriter, r *http.Request, err error) bool {
		got = err
		http.Error(w, "handled", http.StatusServiceUnavailable)
		return true
	})).Handler()
	rec := orbittest.Get(t, h, "/recurse")
	want := "orbit: render depth exceeded 1000: src/pages/recurse.orbit > src/components/Recurse.orbit > src/components/Cacher.orbit > src/components/Recurse.orbit"
	if rec.Code != http.StatusServiceUnavailable || got == nil || !strings.Contains(got.Error(), want) {
		t.Fatalf("got %d, err %v", rec.Code, got)
	}
	if rec := orbittest.Get(t, h, "/capture"); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "<link") {
		t.Errorf("GET /capture: %d\n%s", rec.Code, rec.Body)
	}
}
