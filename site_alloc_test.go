package orbit

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Allocation tests for Orbit's own render and serve path (PLAN.md §4b): a
// typical page renders with 0 allocations and is served with exactly 1, the
// Content-Length value, which net/http may read after ServeHTTP returns and
// so can't be reused.

type allocCardProps struct {
	Title string
	Count int
}

// allocCardHook stands in for a decl package's hook: generated pages call
// components through a function variable.
var allocCardHook = allocCard

func allocCard(c *Ctx, w *Writer, p allocCardProps, s Slots) error {
	if err := w.Enter("src/components/Card.orbit"); err != nil {
		return err
	}
	defer w.Leave()
	w.Raw(`<article class="card"><h2`)
	w.AttrString("title", p.Title)
	w.Raw(`>`)
	w.String(p.Title)
	w.Raw(`</h2><span>`)
	w.Int(int64(p.Count))
	w.Raw(`</span>`)
	if s.Default != nil {
		if err := s.Default(w); err != nil {
			return err
		}
	}
	w.Raw(`</article>`)
	return nil
}

// allocSlot is slot content that uses no parent variables: a static func.
func allocSlot(w *Writer) error {
	w.Raw("<p>static slot content</p>")
	return nil
}

func allocPage(c *Ctx, w *Writer) error {
	w.Raw(`<!DOCTYPE html><html lang="en"><head><meta charset="utf-8"><title>`)
	w.String("Orbit & friends")
	w.Raw(`</title>`)
	w.HeadEnd()
	w.Raw(`</head><body><main>`)
	for i := range 10 {
		if err := allocCardHook(c, w, allocCardProps{Title: "Card title", Count: i * 1000}, Slots{}); err != nil {
			return err
		}
	}
	if err := allocCardHook(c, w, allocCardProps{Title: "With slot"}, Slots{Default: allocSlot}); err != nil {
		return err
	}
	w.Raw(`</main></body></html>`)
	return nil
}

func allocPostPage(c *Ctx, w *Writer) error {
	w.Raw(`<!DOCTYPE html><html><head><title>`)
	slug := c.Param("slug")
	w.String(slug)
	w.HeadEnd()
	w.Raw(`</title></head><body>`)
	if err := allocCardHook(c, w, allocCardProps{Title: slug, Count: 1}, Slots{Default: allocSlot}); err != nil {
		return err
	}
	w.Raw(`</body></html>`)
	return nil
}

var allocRoutes = []Route{
	{Pattern: "/", File: "src/pages/index.orbit", Render: allocPage},
	{Pattern: "/blog/[slug]", File: "src/pages/blog/[slug].orbit", Render: allocPostPage},
}

// fakeResponseWriter is a reusable, allocation-free http.ResponseWriter.
type fakeResponseWriter struct {
	h      http.Header
	status int
	n      int
}

func newFakeResponseWriter() *fakeResponseWriter {
	return &fakeResponseWriter{h: make(http.Header, 8)}
}

func (f *fakeResponseWriter) Header() http.Header  { return f.h }
func (f *fakeResponseWriter) WriteHeader(code int) { f.status = code }
func (f *fakeResponseWriter) Write(b []byte) (int, error) {
	f.n += len(b)
	return len(b), nil
}

func (f *fakeResponseWriter) reset() {
	clear(f.h)
	f.status, f.n = 0, 0
}

func TestRenderAllocs(t *testing.T) {
	if testing.Short() || raceEnabled {
		t.Skip("allocation counts (not under -short or -race)")
	}
	buf := bytes.NewBuffer(make([]byte, 0, 16<<10))
	w := NewWriter(buf)
	r := httptest.NewRequest("GET", "/", nil)
	c := acquireCtx(r, nil, false)
	defer releaseCtx(c)
	allocs := testing.AllocsPerRun(1000, func() {
		buf.Reset()
		w.reset(buf)
		if err := allocPage(c, w); err != nil {
			t.Fatal(err)
		}
	})
	if allocs != 0 {
		t.Errorf("rendering a typical page: %v allocs, want 0", allocs)
	}
}

func TestServeAllocs(t *testing.T) {
	if testing.Short() || raceEnabled {
		t.Skip("allocation counts (not under -short or -race)")
	}
	t.Setenv("ORBIT_DEV", "")
	s := testSite(t, allocRoutes, WithSite("https://example.com"))
	h := s.Middleware(http.NotFoundHandler())
	tests := []struct {
		target, wantStart string
	}{
		{"/", "<!DOCTYPE html>"},
		{"/blog/hello-world", "<!DOCTYPE html><html><head><title>hello-world"},
	}
	for _, tt := range tests {
		t.Run(tt.target, func(t *testing.T) {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest("GET", tt.target, nil))
			if rec.Code != 200 || !bytes.HasPrefix(rec.Body.Bytes(), []byte(tt.wantStart)) {
				t.Fatalf("got %d %q", rec.Code, rec.Body)
			}
			fw := newFakeResponseWriter()
			r := httptest.NewRequest("GET", tt.target, nil)
			allocs := testing.AllocsPerRun(1000, func() {
				fw.reset()
				h.ServeHTTP(fw, r)
			})
			if fw.status != 200 || fw.n != rec.Body.Len() {
				t.Fatalf("fake writer got %d, %d bytes", fw.status, fw.n)
			}
			// The one allocation is the Content-Length value.
			if allocs != 1 {
				t.Errorf("serving %s: %v allocs, want 1 (Content-Length)", tt.target, allocs)
			}
		})
	}
}

func BenchmarkServePage(b *testing.B) {
	b.Setenv("ORBIT_DEV", "")
	b.Setenv("ORBIT_STATIC", b.TempDir())
	s := NewSite(allocRoutes, nil)
	h := s.Middleware(http.NotFoundHandler())
	for _, target := range []string{"/", "/blog/hello-world"} {
		b.Run(target, func(b *testing.B) {
			fw := newFakeResponseWriter()
			r := httptest.NewRequest("GET", target, nil)
			b.ReportAllocs()
			for b.Loop() {
				fw.reset()
				h.ServeHTTP(fw, r)
			}
		})
	}
}

func BenchmarkRenderPage(b *testing.B) {
	buf := bytes.NewBuffer(make([]byte, 0, 16<<10))
	w := NewWriter(buf)
	c := acquireCtx(httptest.NewRequest("GET", "/", nil), nil, false)
	defer releaseCtx(c)
	b.ReportAllocs()
	for b.Loop() {
		buf.Reset()
		w.reset(buf)
		if err := allocPage(c, w); err != nil {
			b.Fatal(err)
		}
	}
}
