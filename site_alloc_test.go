package lyr

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// Allocation tests for Lyr's own render and serve path (PLAN.md §4b): a
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
	if err := w.Enter("src/components/Card.lyr"); err != nil {
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
	w.String("Lyr & friends")
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

// A Markdown page as lyr generate compiles it (PLAN.md §2.4): the HTML is
// a constant, the layout's props (frontmatter and headings) are built once
// at package initialization, and the content is a package-level slot func.
type allocLayoutProps struct {
	Title    string
	Tags     []string
	Headings []Heading
}

var allocLayoutHook = allocLayout

func allocLayout(c *Ctx, w *Writer, p allocLayoutProps, s Slots) error {
	if err := w.Enter("src/layouts/Post.lyr"); err != nil {
		return err
	}
	defer w.Leave()
	w.Raw(`<!DOCTYPE html><html lang="en"><head><meta charset="utf-8"><title>`)
	w.String(p.Title)
	w.Raw(`</title>`)
	w.HeadEnd()
	w.Raw(`</head><body><nav><ul>`)
	for _, h := range p.Headings {
		w.Raw(`<li><a href="#`)
		w.URL(h.Slug)
		w.Raw(`">`)
		w.String(h.Text)
		w.Raw(`</a></li>`)
	}
	w.Raw(`</ul></nav><article>`)
	if s.Default != nil {
		if err := s.Default(w); err != nil {
			return err
		}
	}
	w.Raw(`</article></body></html>`)
	return nil
}

var allocMDHeadings = []Heading{{Depth: 2, Slug: "why-markdown", Text: "Why Markdown"}, {Depth: 2, Slug: "whats-next", Text: "What’s next"}}

var allocMDProps = func() (p allocLayoutProps) {
	p.Title = "Hello, Lyr"
	p.Tags = []string{"news", "go"}
	p.Headings = allocMDHeadings
	return p
}()

const allocMDHTML = "<p>Lyr compiles this file to Go.</p>\n<h2 id=\"why-markdown\">Why Markdown</h2>\n" +
	"<pre class=\"chroma\" tabindex=\"0\" data-language=\"go\"><code><span class=\"nx\">fmt</span></code></pre>\n" +
	"<h2 id=\"whats-next\">What&rsquo;s next</h2>\n"

func allocMDContent(w *Writer) error {
	w.Raw(allocMDHTML)
	return w.Err()
}

func allocMDPage(c *Ctx, w *Writer) error {
	if err := w.Enter("src/pages/blog/hello.md"); err != nil {
		return err
	}
	defer w.Leave()
	return allocLayoutHook(c, w, allocMDProps, Slots{Default: allocMDContent})
}

// allocPartialPage is a page partial's render (PLAN.md §4k): a fragment,
// served with its precomputed tail.
func allocPartialPage(c *Ctx, w *Writer) error {
	if err := w.Enter("src/pages/partial.lyr"); err != nil {
		return err
	}
	defer w.Leave()
	w.Raw(`<ul class="comments">`)
	for i := range 5 {
		if err := allocCardHook(c, w, allocCardProps{Title: "Comment", Count: i}, Slots{Default: allocSlot}); err != nil {
			return err
		}
	}
	w.Raw(`</ul>`)
	return nil
}

// partialAssets are a partial's: a runner entry, a lazy one, and the
// islands loader.
var partialAssets = []Asset{
	{Kind: "component", URL: "/_lyr/List-AAAA1111.js", ID: "List-1a2b3c4d"},
	{Kind: "component", URL: "/_lyr/Tip-BBBB2222.js", ID: "Tip-5e6f7a8b"},
	{Kind: "lazy", URL: "/_lyr/Tip-BBBB2222.js", ID: "Tip-5e6f7a8b"},
}

var allocRoutes = []Route{
	{Pattern: "/", File: "src/pages/index.lyr", Render: allocPage},
	{Pattern: "/blog/[slug]", File: "src/pages/blog/[slug].lyr", Render: allocPostPage},
	{Pattern: "/hello", File: "src/pages/blog/hello.md", Render: allocMDPage},
	{Pattern: "/assets", File: "src/pages/assets.lyr", Render: allocPage, Assets: benchAssets},
	{Pattern: "/partial", File: "src/pages/partial.lyr", Render: allocPartialPage, Assets: partialAssets, Islands: true, Partial: true},
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
	allocs = testing.AllocsPerRun(1000, func() {
		buf.Reset()
		w.reset(buf)
		if err := allocMDPage(c, w); err != nil {
			t.Fatal(err)
		}
	})
	if allocs != 0 {
		t.Errorf("rendering a Markdown page: %v allocs, want 0", allocs)
	}
	allocs = testing.AllocsPerRun(1000, func() {
		buf.Reset()
		w.reset(buf)
		if err := allocPartialPage(c, w); err != nil {
			t.Fatal(err)
		}
	})
	if allocs != 0 {
		t.Errorf("rendering a page partial: %v allocs, want 0", allocs)
	}
}

func TestServeAllocs(t *testing.T) {
	if testing.Short() || raceEnabled {
		t.Skip("allocation counts (not under -short or -race)")
	}
	t.Setenv("LYR_DEV", "")
	s := testSite(t, allocRoutes, WithSite("https://example.com"))
	h := s.Middleware(http.NotFoundHandler())
	tests := []struct {
		target, wantStart string
	}{
		{"/", "<!DOCTYPE html>"},
		{"/blog/hello-world", "<!DOCTYPE html><html><head><title>hello-world"},
		{"/hello", `<!DOCTYPE html><html lang="en"><head><meta charset="utf-8"><title>Hello, Lyr</title></head><body><nav><ul><li><a href="#why-markdown">Why Markdown</a></li>`},
		{"/assets", `<!DOCTYPE html><html lang="en"><head><meta charset="utf-8"><title>Lyr &amp; friends</title>` +
			`<link rel="stylesheet" href="/_lyr/index-3f9a2c1d.css"><script type="module" src="/_lyr/index-9b1e44a2.js"></script></head>`},
		// A page partial: no head tags, the tail at the end.
		{"/partial", `<ul class="comments"><article class="card">`},
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
	b.Setenv("LYR_DEV", "")
	b.Setenv("LYR_STATIC", b.TempDir())
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

// allocCardsPage is a large page: 500 cards.
func allocCardsPage(c *Ctx, w *Writer) error {
	w.Raw(`<!DOCTYPE html><html lang="en"><head><meta charset="utf-8"><title>Cards</title>`)
	w.HeadEnd()
	w.Raw(`</head><body><main>`)
	for i := range 500 {
		if err := allocCardHook(c, w, allocCardProps{Title: "Card & title", Count: i * 37}, Slots{Default: allocSlot}); err != nil {
			return err
		}
	}
	w.Raw(`</main></body></html>`)
	return nil
}

var benchAssets = []Asset{
	{Kind: "css", URL: "/_lyr/index-3f9a2c1d.css"},
	{Kind: "js", URL: "/_lyr/index-9b1e44a2.js"},
}

// BenchmarkServeAssets serves pages with a stylesheet and a script, so the
// asset tags are inserted at </head>.
func BenchmarkServeAssets(b *testing.B) {
	b.Setenv("LYR_DEV", "")
	b.Setenv("LYR_STATIC", b.TempDir())
	s := NewSite([]Route{
		{Pattern: "/", File: "src/pages/index.lyr", Render: allocPage, Assets: benchAssets},
		{Pattern: "/cards", File: "src/pages/cards.lyr", Render: allocCardsPage, Assets: benchAssets},
	}, nil)
	h := s.Middleware(http.NotFoundHandler())
	for _, target := range []string{"/", "/cards"} {
		b.Run(target, func(b *testing.B) {
			fw := newFakeResponseWriter()
			r := httptest.NewRequest("GET", target, nil)
			b.ReportAllocs()
			for b.Loop() {
				fw.reset()
				h.ServeHTTP(fw, r)
			}
			b.SetBytes(int64(fw.n))
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

// Route caching (PLAN.md §4h): a HIT, a 304 and a STALE cost 0
// allocations; a page that doesn't call Set costs the same as without a
// provider, and Set with constant tags costs 0 without a provider.

func allocCachedPage(c *Ctx, w *Writer) error {
	c.Cache().Set(CacheOptions{MaxAge: time.Hour, SWR: time.Hour, Tags: []string{"posts", "home"}})
	return allocPage(c, w)
}

var allocCacheRoutes = []Route{
	{Pattern: "/", File: "src/pages/index.lyr", Render: allocCachedPage},
	{Pattern: "/plain", File: "src/pages/plain.lyr", Render: allocPage},
	{Pattern: "/blog/[slug]", File: "src/pages/blog/[slug].lyr", Render: allocPostPage},
}

func TestCacheAllocs(t *testing.T) {
	if testing.Short() || raceEnabled {
		t.Skip("allocation counts (not under -short or -race)")
	}
	clock := useClock(t)
	st := newFakeStore()
	s := testSite(t, allocCacheRoutes, WithCache(st))
	h := s.Middleware(http.NotFoundHandler())
	allocs := func(r *http.Request, want int, wantState string, wantStatus int) {
		t.Helper()
		fw := newFakeResponseWriter()
		n := testing.AllocsPerRun(1000, func() {
			fw.reset()
			h.ServeHTTP(fw, r)
		})
		if fw.status != wantStatus || fw.h.Get("X-Lyr-Cache") != wantState {
			t.Fatalf("%s %s: %d %q, want %d %q", r.Method, r.URL, fw.status, fw.h.Get("X-Lyr-Cache"), wantStatus, wantState)
		}
		if n != float64(want) {
			t.Errorf("%s %s (%s %d): %v allocs, want %d", r.Method, r.URL, wantState, wantStatus, n, want)
		}
	}
	warm := httptest.NewRecorder()
	h.ServeHTTP(warm, httptest.NewRequest("GET", "/", nil))
	if warm.Header().Get("X-Lyr-Cache") != "MISS" {
		t.Fatalf("warm-up: %v", warm.Header())
	}
	allocs(httptest.NewRequest("GET", "/", nil), 0, "HIT", 200)

	inm := httptest.NewRequest("GET", "/", nil)
	inm.Header.Set("If-None-Match", `"other", `+warm.Header().Get("Etag"))
	allocs(inm, 0, "HIT", 304)

	q := "/?b=2&a=1&utm_source=x"
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", q, nil))
	allocs(httptest.NewRequest("GET", q, nil), 0, "HIT", 200)

	// STALE while a revalidation runs (the one that starts it allocates
	// its goroutine and request copy).
	clock.add(90 * time.Minute)
	st.entry("example.com/").revalidating.Store(true)
	allocs(httptest.NewRequest("GET", "/", nil), 0, "STALE", 200)

	// A page that never calls Set: the per-route bit skips the lookup.
	allocs(httptest.NewRequest("GET", "/plain", nil), 1, "", 200)
	allocs(httptest.NewRequest("GET", "/blog/hello-world", nil), 1, "", 200)

	// Without a provider, Set with constant tags adds nothing.
	s = testSite(t, allocCacheRoutes)
	h = s.Middleware(http.NotFoundHandler())
	warnedNoProvider.Store(true) // the one-time warning isn't counted
	t.Cleanup(func() { warnedNoProvider.Store(false) })
	allocs(httptest.NewRequest("GET", "/", nil), 1, "", 200)
}

// missStore never finds anything and drops what it's given: every request
// to a known route is a miss that stores.
type missStore struct{ fakeStore }

func (*missStore) Get(CacheKey) *CachedResponse  { return nil }
func (*missStore) Put(CacheKey, *CachedResponse) {}

func BenchmarkCache(b *testing.B) {
	b.Setenv("LYR_DEV", "")
	b.Setenv("LYR_STATIC", b.TempDir())
	serve := func(b *testing.B, h http.Handler, r *http.Request) {
		fw := newFakeResponseWriter()
		b.ReportAllocs()
		for b.Loop() {
			fw.reset()
			h.ServeHTTP(fw, r)
		}
	}
	st := newFakeStore()
	h := NewSite(allocCacheRoutes, nil, WithCache(st)).Middleware(http.NotFoundHandler())
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/?b=2&a=1&utm_source=x", nil))
	b.Run("hit", func(b *testing.B) { serve(b, h, httptest.NewRequest("GET", "/", nil)) })
	b.Run("hit-query", func(b *testing.B) { serve(b, h, httptest.NewRequest("GET", "/?b=2&a=1&utm_source=x", nil)) })
	b.Run("304", func(b *testing.B) {
		r := httptest.NewRequest("GET", "/", nil)
		r.Header.Set("If-None-Match", st.entry("example.com/").Header.Get("Etag"))
		serve(b, h, r)
	})
	b.Run("hit-parallel", func(b *testing.B) {
		b.ReportAllocs()
		b.RunParallel(func(pb *testing.PB) {
			fw := newFakeResponseWriter()
			r := httptest.NewRequest("GET", "/", nil)
			for pb.Next() {
				fw.reset()
				h.ServeHTTP(fw, r)
			}
		})
	})
	b.Run("stale", func(b *testing.B) {
		st := newFakeStore()
		h := NewSite(allocCacheRoutes, nil, WithCache(st)).Middleware(http.NotFoundHandler())
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
		e := st.entry("example.com/")
		e.revalidating.Store(true)
		e.Stored = e.Stored.Add(-90 * time.Minute)
		serve(b, h, httptest.NewRequest("GET", "/", nil))
	})
	b.Run("miss-store", func(b *testing.B) {
		h := NewSite(allocCacheRoutes, nil, WithCache(&missStore{})).Middleware(http.NotFoundHandler())
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil)) // the route is known
		serve(b, h, httptest.NewRequest("GET", "/", nil))
	})
	b.Run("provider-no-set", func(b *testing.B) { serve(b, h, httptest.NewRequest("GET", "/plain", nil)) })
	b.Run("no-provider", func(b *testing.B) {
		h := NewSite(allocCacheRoutes, nil).Middleware(http.NotFoundHandler())
		serve(b, h, httptest.NewRequest("GET", "/plain", nil))
	})
	b.Run("no-provider-set", func(b *testing.B) {
		warnedNoProvider.Store(true)
		h := NewSite(allocCacheRoutes, nil).Middleware(http.NotFoundHandler())
		serve(b, h, httptest.NewRequest("GET", "/", nil))
	})
}

// Caching by context (PLAN.md §4h "Caching by context (VaryBy)").

func allocVaryPage(c *Ctx, w *Writer) error {
	c.Cache().Set(CacheOptions{MaxAge: time.Hour, SWR: time.Hour, Tags: []string{"posts", "home"}, VaryBy: []string{"user", "role"}})
	return allocPage(c, w)
}

var allocVaryRoutes = []Route{
	{Pattern: "/", File: "src/pages/index.lyr", Render: allocCachedPage},
	{Pattern: "/vary", File: "src/pages/vary.lyr", Render: allocVaryPage},
}

// contextMiddleware is auth-like middleware that sets two context values
// on Locals attached earlier (reset per request, as a fresh Locals would
// be).
func contextMiddleware(s *Site) http.Handler {
	next := s.Middleware(http.NotFoundHandler())
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		l := LocalsFromRequest[cacheLocals](r)
		l.Cache = Cache{}
		l.Cache.Context("user", "u_123456")
		l.Cache.Context("role", "admin")
		next.ServeHTTP(w, r)
	})
}

func TestCacheContextAllocs(t *testing.T) {
	if testing.Short() || raceEnabled {
		t.Skip("allocation counts (not under -short or -race)")
	}
	var k Cache
	if n := testing.AllocsPerRun(1000, func() {
		k = Cache{}
		k.Context("user", "u_123456")
		k.Context("role", "admin")
		k.Context("plan", "pro")
	}); n != 0 {
		t.Errorf("Context: %v allocs", n)
	}
	s := testSite(t, allocVaryRoutes, WithCache(newFakeStore()), withLocalsCache())
	h := contextMiddleware(s)
	for _, tt := range []struct {
		path  string
		state string
		want  float64
	}{
		{"/vary", "HIT", 0},
		{"/", "HIT", 0}, // context set, the page has no VaryBy
	} {
		r := WithLocals(httptest.NewRequest("GET", tt.path, nil), new(cacheLocals))
		h.ServeHTTP(httptest.NewRecorder(), r) // MISS
		fw := newFakeResponseWriter()
		n := testing.AllocsPerRun(1000, func() {
			fw.reset()
			h.ServeHTTP(fw, r)
		})
		if fw.h.Get("X-Lyr-Cache") != tt.state {
			t.Fatalf("%s: %q", tt.path, fw.h.Get("X-Lyr-Cache"))
		}
		if n != tt.want {
			t.Errorf("%s %s: %v allocs, want %v", tt.path, tt.state, n, tt.want)
		}
	}
}

func BenchmarkCacheContext(b *testing.B) {
	b.Setenv("LYR_DEV", "")
	b.Setenv("LYR_STATIC", b.TempDir())
	b.Run("context-3-keys", func(b *testing.B) {
		var k Cache
		b.ReportAllocs()
		for b.Loop() {
			k = Cache{}
			k.Context("user", "u_123456")
			k.Context("role", "admin")
			k.Context("plan", "pro")
		}
	})
	serve := func(b *testing.B, h http.Handler, path string) {
		r := WithLocals(httptest.NewRequest("GET", path, nil), new(cacheLocals))
		h.ServeHTTP(httptest.NewRecorder(), r)
		fw := newFakeResponseWriter()
		b.ReportAllocs()
		for b.Loop() {
			fw.reset()
			h.ServeHTTP(fw, r)
		}
	}
	h := contextMiddleware(NewSite(allocVaryRoutes, nil, WithCache(newFakeStore()), withLocalsCache()))
	b.Run("hit", func(b *testing.B) { serve(b, h, "/") })
	b.Run("varyby-hit", func(b *testing.B) { serve(b, h, "/vary") })
	miss := contextMiddleware(NewSite(allocVaryRoutes, nil, WithCache(&missStore{}), withLocalsCache()))
	b.Run("miss-store", func(b *testing.B) { serve(b, miss, "/") })
	b.Run("varyby-miss-store", func(b *testing.B) { serve(b, miss, "/vary") })
}

// Sessions (PLAN.md §4i).

type allocSessData struct {
	UserID int64    `json:"userId,omitempty"`
	Cart   []string `json:"cart,omitempty"`
	Flash  string   `json:"flash,omitempty"`
}

type allocSessLocals struct {
	Session Session[allocSessData]
}

func allocSessionPage(change bool) func(c *Ctx, w *Writer) error {
	return func(c *Ctx, w *Writer) error {
		d, err := SessionOf[allocSessData](c).Data()
		if err != nil {
			return err
		}
		if change {
			d.Flash = d.Flash[:0] + "x"
			if len(d.Cart) > 0 {
				d.Cart[0] = d.Cart[0] + "1"
			}
		}
		return allocPage(c, w)
	}
}

func allocSessionSite(tb testing.TB, store SessionStore, opts ...Option) *Site {
	tb.Setenv("LYR_DEV", "")
	tb.Setenv("LYR_STATIC", tb.TempDir())
	tb.Setenv("LYR_PUBLIC", "")
	routes := []Route{
		{Pattern: "/", File: "src/pages/index.lyr", Render: allocPage},
		{Pattern: "/read", File: "src/pages/read.lyr", Render: allocSessionPage(false)},
		{Pattern: "/change", File: "src/pages/change.lyr", Render: allocSessionPage(true)},
		{Pattern: "/cached", File: "src/pages/cached.lyr", Render: func(c *Ctx, w *Writer) error {
			c.Cache().Set(CacheOptions{MaxAge: time.Hour})
			return allocSessionPage(false)(c, w)
		}},
	}
	opts = append([]Option{WithSessions(store, SessionOptions{TTL: time.Hour}),
		WithLocalsSession(func(l *allocSessLocals) *Session[allocSessData] { return &l.Session })}, opts...)
	return NewSite(routes, nil, opts...)
}

func TestSessionAllocs(t *testing.T) {
	if testing.Short() || raceEnabled {
		t.Skip("allocation counts (not under -short or -race)")
	}
	store := newFakeSessions()
	s := allocSessionSite(t, store, WithCache(newFakeStore()))
	h := s.Middleware(http.NotFoundHandler())
	allocs := func(name string, h http.Handler, r *http.Request, want float64) {
		t.Helper()
		fw := newFakeResponseWriter()
		h.ServeHTTP(fw, r) // warm-up (pools, a first MISS)
		n := testing.AllocsPerRun(1000, func() {
			fw.reset()
			h.ServeHTTP(fw, r)
		})
		if fw.status != 200 {
			t.Fatalf("%s: status %d", name, fw.status)
		}
		if n != want {
			t.Errorf("%s: %v allocs, want %v", name, n, want)
		}
	}
	// A page that doesn't use the session: Content-Length only.
	allocs("page without session", h, httptest.NewRequest("GET", "/", nil), 1)
	// Anonymous Data() with no change: no cookie scan result, zero D, the
	// pooled encoder.
	allocs("anonymous read", h, httptest.NewRequest("GET", "/read", nil), 1)
	// site.Sessions on a request that doesn't use the session, with
	// Locals attached earlier.
	r := WithLocals(httptest.NewRequest("GET", "/", nil), new(allocSessLocals))
	allocs("site.Sessions unused", s.Sessions(h), r, 1)
	// A cached page that reads the session, served from the cache, with or
	// without a session cookie: no cookie scan, no store call.
	allocs("cached HIT", h, httptest.NewRequest("GET", "/cached", nil), 0)
	cr := httptest.NewRequest("GET", "/cached", nil)
	cr.Header.Set("Cookie", "lyr-session="+sid1)
	allocs("cached HIT with a cookie", h, cr, 0)
	if store.calls() != [3]int32{} {
		t.Errorf("store calls %v", store.calls())
	}
}

func BenchmarkSession(b *testing.B) {
	serve := func(b *testing.B, h http.Handler, r *http.Request) {
		fw := newFakeResponseWriter()
		b.ReportAllocs()
		for b.Loop() {
			fw.reset()
			h.ServeHTTP(fw, r)
		}
	}
	withCookie := func(path, id string) *http.Request {
		r := httptest.NewRequest("GET", path, nil)
		r.Header.Set("Cookie", "theme=dark; lyr-session="+id)
		return r
	}
	store := newFakeSessions()
	stored := `{"userId":42,"cart":["sku-1","sku-2","sku-3"],"flash":"hello"}`
	store.put(sid1, stored)
	s := allocSessionSite(b, store)
	h := s.Middleware(http.NotFoundHandler())
	b.Run("page-no-session", func(b *testing.B) { serve(b, h, httptest.NewRequest("GET", "/", nil)) })
	b.Run("anonymous-read", func(b *testing.B) { serve(b, h, httptest.NewRequest("GET", "/read", nil)) })
	b.Run("stored-read", func(b *testing.B) { serve(b, h, withCookie("/read", sid1)) })
	b.Run("stored-change", func(b *testing.B) {
		store.onWrite = func(id string) { store.put(id, stored) } // keep the input fixed
		defer func() { store.onWrite = nil }()
		serve(b, h, withCookie("/change", sid1))
	})
	b.Run("new-session", func(b *testing.B) { serve(b, h, httptest.NewRequest("GET", "/change", nil)) })
	b.Run("sessions-unused", func(b *testing.B) {
		serve(b, s.Sessions(h), WithLocals(httptest.NewRequest("GET", "/", nil), new(allocSessLocals)))
	})
	b.Run("sessions-attach", func(b *testing.B) { serve(b, s.Sessions(h), httptest.NewRequest("GET", "/", nil)) })
	cs := allocSessionSite(b, store, WithCache(newFakeStore()))
	ch := cs.Middleware(http.NotFoundHandler())
	ch.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/cached", nil))
	b.Run("cached-hit", func(b *testing.B) { serve(b, ch, httptest.NewRequest("GET", "/cached", nil)) })
	b.Run("cached-hit-cookie", func(b *testing.B) { serve(b, ch, withCookie("/cached", sid1)) })
}

// TestCacheMiddlewareSetAllocs: middleware that calls Set before the
// lookup keeps a HIT allocation-free, with and without site.Rewrites (the
// copy of its options for a rewrite's target is pooled).
func TestCacheMiddlewareSetAllocs(t *testing.T) {
	if testing.Short() || raceEnabled {
		t.Skip("allocation counts (not under -short or -race)")
	}
	for _, marker := range []bool{false, true} {
		s := testSite(t, allocVaryRoutes, WithCache(newFakeStore()), withLocalsCache())
		var next http.Handler = s.Middleware(http.NotFoundHandler())
		mw := func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				l := LocalsFromRequest[cacheLocals](r)
				l.Cache = Cache{}
				l.Cache.Set(CacheOptions{MaxAge: time.Hour, Tags: []string{"mw"}})
				next.ServeHTTP(w, r)
			})
		}
		h := mw(next)
		if marker {
			h = mw(s.Rewrites(next))
		}
		r := WithLocals(httptest.NewRequest("GET", "/", nil), new(cacheLocals))
		h.ServeHTTP(httptest.NewRecorder(), r) // MISS
		fw := newFakeResponseWriter()
		n := testing.AllocsPerRun(1000, func() {
			fw.reset()
			h.ServeHTTP(fw, r)
		})
		if fw.h.Get("X-Lyr-Cache") != "HIT" {
			t.Fatalf("marker=%v: %q", marker, fw.h.Get("X-Lyr-Cache"))
		}
		if n != 0 {
			t.Errorf("marker=%v: middleware Set + HIT: %v allocs, want 0", marker, n)
		}
	}
}
