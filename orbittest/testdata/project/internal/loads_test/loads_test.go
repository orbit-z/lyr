package loads_test

import (
	"bytes"
	"log"
	"strings"
	"testing"
	"time"

	"github.com/orbit-z/orbit/orbittest"

	"example.com/site/orbit_gen/app"
)

// TestLoadsPage: a page loading two 30 ms fetches with orbit.All overlaps
// them, and a failing load's error, 404 and panic reach the site, the
// error and the panic with the load's .orbit line (PLAN.md §4f).
func TestLoadsPage(t *testing.T) {
	h := app.New().Handler()
	best := time.Hour
	for range 3 {
		start := time.Now()
		rec := orbittest.Get(t, h, "/loads")
		best = min(best, time.Since(start))
		if body := rec.Body.String(); rec.Code != 200 || !strings.Contains(body, "<p>sunny</p>") ||
			!strings.Contains(body, "<li>hello</li><li>world</li>") {
			t.Fatalf("got %d:\n%s", rec.Code, body)
		}
	}
	if best >= 60*time.Millisecond {
		t.Errorf("two 30 ms loads took %v, want well under 60 ms", best)
	}

	var logs bytes.Buffer
	flags, out := log.Flags(), log.Writer()
	log.SetFlags(0)
	log.SetOutput(&logs)
	defer func() { log.SetFlags(flags); log.SetOutput(out) }()
	for _, tt := range []struct{ fail, log string }{
		// The failing load's func literal is on line 20; the panic on line 28.
		// The path is absolute: the test runs outside the project root.
		{"error", "/src/pages/loads.orbit:20: posts: unavailable\n"},
		{"panic", "/src/pages/loads.orbit:28: panic: assignment to entry in nil map\n"},
	} {
		logs.Reset()
		if rec := orbittest.Get(t, h, "/loads?fail="+tt.fail); rec.Code != 500 {
			t.Errorf("%s: got %d", tt.fail, rec.Code)
		}
		if l := logs.String(); !strings.HasPrefix(l, "orbit: GET /loads: ") || !strings.HasSuffix(l, tt.log) || strings.Count(l, "\n") != 1 {
			t.Errorf("%s: log %q, want one line ending with %q", tt.fail, l, tt.log)
		}
	}
	if rec := orbittest.Get(t, h, "/loads?fail=notfound"); rec.Code != 404 {
		t.Errorf("notfound: got %d", rec.Code)
	}
}
