package lyr

import (
	"bytes"
	"context"
	"errors"
	"log"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Route caching tests (PLAN.md §4h), with a fake store.

// fakeStore is a map-backed CacheStore.
type fakeStore struct {
	mu     sync.RWMutex
	m      map[string]*CachedResponse
	puts   atomic.Int32
	invals []InvalidateOptions
}

func newFakeStore() *fakeStore { return &fakeStore{m: map[string]*CachedResponse{}} }

func (f *fakeStore) Get(k CacheKey) *CachedResponse {
	f.mu.RLock()
	e := f.m[k.String()]
	f.mu.RUnlock()
	return e
}

func (f *fakeStore) Put(k CacheKey, e *CachedResponse) {
	f.mu.Lock()
	f.m[strings.Clone(k.String())] = e
	f.mu.Unlock()
	f.puts.Add(1)
}

func (f *fakeStore) Invalidate(_ context.Context, o InvalidateOptions) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.invals = append(f.invals, o)
	for k, e := range f.m {
		if (o.Path != "" && e.Path == o.Path) || slices.ContainsFunc(e.Tags, func(t string) bool { return slices.Contains(o.Tags, t) }) {
			delete(f.m, k)
		}
	}
	return nil
}

// keys returns the stored keys, sorted.
func (f *fakeStore) keys() []string {
	f.mu.RLock()
	defer f.mu.RUnlock()
	ks := make([]string, 0, len(f.m))
	for k := range f.m {
		ks = append(ks, k)
	}
	slices.Sort(ks)
	return ks
}

func (f *fakeStore) entry(key string) *CachedResponse {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.m[key]
}

// queryStore is a fakeStore with its own CacheQuery.
type queryStore struct {
	*fakeStore
	q CacheQuery
}

func (q queryStore) CacheQuery() CacheQuery { return q.q }

// headerProvider is a CacheHeaderWriter.
type headerProvider struct{ calls atomic.Int32 }

func (*headerProvider) Invalidate(context.Context, InvalidateOptions) error { return nil }

func (h *headerProvider) WriteCacheHeaders(hd http.Header, o *CacheOptions, r *http.Request) {
	h.calls.Add(1)
	hd.Set("Surrogate-Control", "max-age="+strconv.Itoa(int(o.MaxAge/time.Second)))
	hd.Set("Surrogate-Key", strings.Join(o.Tags, " "))
}

// plainProvider is neither a store nor a header writer: Astro's default
// headers.
type plainProvider struct{}

func (plainProvider) Invalidate(context.Context, InvalidateOptions) error { return nil }

// testClock replaces the cache clock until the test ends.
type testClock struct{ ns atomic.Int64 }

func useClock(t *testing.T) *testClock {
	t.Helper()
	c := &testClock{}
	c.ns.Store(time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC).UnixNano())
	old := cacheNow
	cacheNow = func() time.Time { return time.Unix(0, c.ns.Load()) }
	t.Cleanup(func() { cacheNow = old })
	return c
}

func (c *testClock) add(d time.Duration) { c.ns.Add(int64(d)) }

// counted returns a page that counts its renders, applies set (if any) to
// its cache handle, and writes body plus the render count.
func counted(n *atomic.Int32, set func(c *Ctx) error, body string) func(*Ctx, *Writer) error {
	return func(c *Ctx, w *Writer) error {
		i := n.Add(1)
		if set != nil {
			if err := set(c); err != nil {
				return err
			}
		}
		w.Raw(body + " #")
		w.Int(int64(i))
		return nil
	}
}

func setOpts(o CacheOptions) func(c *Ctx) error {
	return func(c *Ctx) error { c.Cache().Set(o); return nil }
}

var hour = CacheOptions{MaxAge: time.Hour, SWR: time.Hour, Tags: []string{"posts", "home"}}

func cacheSite(t *testing.T, routes []Route, opts ...Option) (*Site, *fakeStore) {
	t.Helper()
	st := newFakeStore()
	return testSite(t, routes, append([]Option{WithCache(st)}, opts...)...), st
}

func get(h http.Handler, target string, hdr ...string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("GET", target, nil)
	for i := 0; i+1 < len(hdr); i += 2 {
		r.Header.Set(hdr[i], hdr[i+1])
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

// captureLogSync sends the standard logger's output to a buffer until the
// test ends.
func captureLogSync(t *testing.T) *syncBuffer {
	t.Helper()
	var b syncBuffer
	log.SetOutput(&b)
	flags := log.Flags()
	log.SetFlags(0)
	t.Cleanup(func() {
		log.SetOutput(logDefault)
		log.SetFlags(flags)
	})
	return &b
}

var logDefault = log.Writer()

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// waitFlights waits until no render runs for the site's cache (background
// revalidations included).
func waitFlights(t *testing.T, s *Site) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		s.cache.mu.Lock()
		n := len(s.cache.flights)
		s.cache.mu.Unlock()
		if n == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("renders still running after 5 s")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestCacheSetMerge(t *testing.T) {
	var k Cache
	if k.Enabled() || cacheActive(&k) {
		t.Fatal("zero handle is enabled or active")
	}
	t1 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	k.Set(CacheOptions{MaxAge: time.Minute, SWR: time.Hour, Tags: []string{"a", "b"}, ETag: `"1"`, LastModified: t1})
	k.Set(CacheOptions{MaxAge: 2 * time.Minute, Tags: []string{"b", "c"}, LastModified: t1.Add(-time.Hour)})
	k.Set(CacheOptions{ETag: `W/"2"`, LastModified: t1.Add(time.Hour)})
	o := k.Options()
	want := CacheOptions{MaxAge: 2 * time.Minute, SWR: time.Hour, Tags: []string{"a", "b", "c"}, ETag: `W/"2"`, LastModified: t1.Add(time.Hour)}
	if o.MaxAge != want.MaxAge || o.SWR != want.SWR || !slices.Equal(o.Tags, want.Tags) || o.ETag != want.ETag || !o.LastModified.Equal(want.LastModified) {
		t.Fatalf("merged %+v, want %+v", o, want)
	}
	o.Tags[0] = "changed"
	if k.Options().Tags[0] != "a" {
		t.Error("Options().Tags isn't a copy")
	}
	// More than 4 tags move to the overflow slice, in order, deduplicated.
	k.Set(CacheOptions{Tags: []string{"d", "e", "a", "f"}})
	if got := k.Options().Tags; !slices.Equal(got, []string{"a", "b", "c", "d", "e", "f"}) {
		t.Errorf("tags %q", got)
	}
	k.Disable()
	if o := k.Options(); o.MaxAge != 0 || o.Tags != nil || o.ETag != "" || !o.LastModified.IsZero() || cacheActive(&k) || cacheStorable(&k) {
		t.Errorf("after Disable: %+v", o)
	}
	k.Set(CacheOptions{Tags: []string{"x"}})
	if !cacheActive(&k) || cacheStorable(&k) || !slices.Equal(k.Options().Tags, []string{"x"}) {
		t.Errorf("Set after Disable: %+v", k.Options())
	}
	k.Set(CacheOptions{MaxAge: time.Second})
	if !cacheStorable(&k) {
		t.Error("MaxAge > 0 isn't storable")
	}
	var c Cache
	copyCache(&c, &k)
	if !slices.Equal(c.Options().Tags, []string{"x"}) || c.maxAge != int64(time.Second) {
		t.Errorf("copyFrom: %+v", c.Options())
	}
	// release keeps the overflow slice and clears it.
	k.Set(CacheOptions{Tags: []string{"1", "2", "3", "4", "5"}})
	releaseCache(&k)
	if len(k.Options().Tags) != 0 || cap(k.more) == 0 {
		t.Errorf("release: tags %q, cap %d", k.Options().Tags, cap(k.more))
	}
}

func TestCacheMissThenHit(t *testing.T) {
	var n atomic.Int32
	s, st := cacheSite(t, []Route{
		{Pattern: "/", File: "src/pages/index.lyr", Render: counted(&n, setOpts(hour), "home")},
		{Pattern: "/plain", File: "src/pages/plain.lyr", Render: counted(&n, nil, "plain")},
	})
	h := s.Handler()
	r1 := get(h, "/")
	r2 := get(h, "/")
	if r1.Code != 200 || r1.Header().Get("X-Lyr-Cache") != "MISS" || r1.Body.String() != "home #1" {
		t.Fatalf("first: %d %q %q", r1.Code, r1.Header().Get("X-Lyr-Cache"), r1.Body)
	}
	if r2.Header().Get("X-Lyr-Cache") != "HIT" || r2.Body.String() != "home #1" || n.Load() != 1 {
		t.Fatalf("second: %q %q, %d renders", r2.Header().Get("X-Lyr-Cache"), r2.Body, n.Load())
	}
	for _, name := range []string{"Content-Type", "Content-Length", "Etag"} {
		if r1.Header().Get(name) == "" || r1.Header().Get(name) != r2.Header().Get(name) {
			t.Errorf("%s: MISS %q, HIT %q", name, r1.Header().Get(name), r2.Header().Get(name))
		}
	}
	// Astro: no CDN headers with a store, and no Cache-Control from MaxAge.
	for _, name := range []string{"Cdn-Cache-Control", "Cache-Tag", "Cache-Control", "Age"} {
		if v := r2.Header().Get(name); v != "" {
			t.Errorf("%s: %q", name, v)
		}
	}
	e := st.entry("example.com/")
	if e == nil || e.MaxAge != time.Hour || e.SWR != time.Hour || !slices.Equal(e.Tags, []string{"posts", "home"}) || e.Path != "/" {
		t.Fatalf("entry %+v (keys %q)", e, st.keys())
	}
	// A page that doesn't set options: rendered each time, no header.
	p1, p2 := get(h, "/plain"), get(h, "/plain")
	if p1.Header().Get("X-Lyr-Cache") != "" || p2.Body.String() != "plain #3" {
		t.Errorf("plain: %q %q", p1.Header().Get("X-Lyr-Cache"), p2.Body)
	}
	if st.puts.Load() != 1 {
		t.Errorf("%d puts, want 1", st.puts.Load())
	}
}

func TestCacheKey(t *testing.T) {
	var n atomic.Int32
	routes := []Route{
		{Pattern: "/", File: "src/pages/index.lyr", Render: counted(&n, setOpts(hour), "home")},
		{Pattern: "/blog/[slug]", File: "src/pages/blog/[slug].lyr", Render: counted(&n, setOpts(hour), "post")},
	}
	s, st := cacheSite(t, routes)
	h := s.Handler()
	get(h, "/?b=2&a=1&utm_source=x&fbclid=y&a=0&REF=z&UTM_Medium=q")
	if r := get(h, "/?a=1&a=0&b=2"); r.Header().Get("X-Lyr-Cache") != "HIT" {
		t.Errorf("normalized query: %q (keys %q)", r.Header().Get("X-Lyr-Cache"), st.keys())
	}
	get(h, "/blog/hello%20world")
	r := httptest.NewRequest("GET", "/", nil)
	r.Host = "other.example"
	h.ServeHTTP(httptest.NewRecorder(), r)
	want := []string{"example.com/?a=1&a=0&b=2", "example.com/blog/hello%20world", "other.example/"}
	if got := st.keys(); !slices.Equal(got, want) {
		t.Errorf("keys %q, want %q", got, want)
	}

	for _, tt := range []struct {
		name   string
		q      CacheQuery
		target string
		want   string
	}{
		{"include", CacheQuery{Include: []string{"page", "Q*"}}, "/?sort=x&page=2&utm_source=y&query=z", "example.com/?page=2&query=z"},
		{"exclude", CacheQuery{Exclude: []string{"sort*"}}, "/?SortBy=1&page=2&utm_source=y", "example.com/?page=2&utm_source=y"},
		{"nosort", CacheQuery{NoSort: true}, "/?b=1&a=2", "example.com/?b=1&a=2"},
		{"empty exclude", CacheQuery{Exclude: []string{}}, "/?ref=1", "example.com/?ref=1"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			st := newFakeStore()
			s := testSite(t, routes, WithCache(queryStore{st, tt.q}))
			get(s.Handler(), tt.target)
			if got := st.keys(); len(got) != 1 || got[0] != tt.want {
				t.Errorf("keys %q, want %q", got, tt.want)
			}
		})
	}
	if msg := panicMessage(func() {
		testSite(t, routes, WithCache(queryStore{newFakeStore(), CacheQuery{Include: []string{"a"}, Exclude: []string{"b"}}}))
	}); !strings.Contains(msg, "Include and Exclude") {
		t.Errorf("Include with Exclude: %q", msg)
	}
}

func TestCacheGETOnly(t *testing.T) {
	var n atomic.Int32
	s, st := cacheSite(t, []Route{{Pattern: "/", File: "src/pages/index.lyr", Render: counted(&n, setOpts(hour), "home")}})
	h := s.Handler()
	for _, m := range []string{"HEAD", "POST", "PUT"} {
		rec := do(h, m, "/")
		if rec.Code != 200 || rec.Header().Get("X-Lyr-Cache") != "" {
			t.Errorf("%s: %d %q", m, rec.Code, rec.Header().Get("X-Lyr-Cache"))
		}
	}
	if st.puts.Load() != 0 {
		t.Fatalf("stored a non-GET response")
	}
	get(h, "/")
	if rec := do(h, "HEAD", "/"); rec.Header().Get("X-Lyr-Cache") != "" || n.Load() != 5 {
		t.Errorf("HEAD after a store: %q, %d renders", rec.Header().Get("X-Lyr-Cache"), n.Load())
	}
}

func TestCacheNeverStored(t *testing.T) {
	logs := captureLogSync(t)
	set := func(f func(c *Ctx) error) func(c *Ctx) error {
		return func(c *Ctx) error { c.Cache().Set(hour); return f(c) }
	}
	var n atomic.Int32
	routes := []Route{
		{Pattern: "/error", File: "e.lyr", Render: counted(&n, set(func(*Ctx) error { return errors.New("db down") }), "")},
		{Pattern: "/panic", File: "p.lyr", Render: counted(&n, set(func(*Ctx) error { panic("boom") }), "")},
		{Pattern: "/handled", File: "h.lyr", Render: counted(&n, set(func(*Ctx) error { return errHandled }), "")},
		{Pattern: "/missing", File: "m.lyr", Render: counted(&n, set(func(*Ctx) error { return ErrNotFound }), "")},
		{Pattern: "/cookie", File: "c.lyr", Render: counted(&n, set(func(c *Ctx) error { c.Header().Set("Set-Cookie", "a=1"); return nil }), "")},
		{Pattern: "/mwcookie", File: "mc.lyr", Render: counted(&n, set(func(*Ctx) error { return nil }), "")},
		{Pattern: "/varycookie", File: "vc.lyr", Render: counted(&n, set(func(c *Ctx) error { c.Header().Set("Vary", "Accept, cookie"); return nil }), "")},
		{Pattern: "/varystar", File: "vs.lyr", Render: counted(&n, set(func(c *Ctx) error { c.Header().Set("Vary", "*"); return nil }), "")},
		{Pattern: notFoundPattern, File: "src/pages/404.lyr", Render: counted(&n, setOpts(hour), "404")},
	}
	s, st := cacheSite(t, routes, WithErrorHandler(func(w http.ResponseWriter, r *http.Request, err error) bool {
		if errors.Is(err, errHandled) {
			w.WriteHeader(http.StatusTeapot)
			return true
		}
		return false
	}))
	mw := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/mwcookie" {
			w.Header().Set("Set-Cookie", "session=1")
		}
		s.Handler().ServeHTTP(w, r)
	})
	codes := map[string]int{"/error": 500, "/panic": 500, "/handled": 418, "/missing": 404, "/cookie": 200, "/mwcookie": 200, "/varycookie": 200, "/varystar": 200, "/nope": 404}
	for target, code := range codes {
		for range 2 {
			if rec := get(mw, target); rec.Code != code || rec.Header().Get("X-Lyr-Cache") != "" {
				t.Errorf("%s: %d %q, want %d and no X-Lyr-Cache", target, rec.Code, rec.Header().Get("X-Lyr-Cache"), code)
			}
		}
	}
	if st.puts.Load() != 0 {
		t.Errorf("stored %q", st.keys())
	}
	out := logs.String()
	for _, want := range []string{"GET /cookie: not cached because it sets a cookie", "GET /mwcookie: not cached because it sets a cookie",
		"GET /varycookie: not cached because it varies on Cookie", "GET /varystar: not cached because it has Vary: *"} {
		if strings.Count(out, want) != 1 {
			t.Errorf("log has %d × %q:\n%s", strings.Count(out, want), want, out)
		}
	}
}

var errHandled = errors.New("handled")

func TestCacheRedirectStored(t *testing.T) {
	var n atomic.Int32
	s, st := cacheSite(t, []Route{{Pattern: "/old", File: "src/pages/old.lyr", Render: counted(&n, func(c *Ctx) error {
		c.Cache().Set(hour)
		c.Header().Set("X-Page", "1")
		return c.Redirect("/new", http.StatusMovedPermanently)
	}, "")}})
	h := s.Handler()
	r1, r2 := get(h, "/old"), get(h, "/old")
	for i, r := range []*httptest.ResponseRecorder{r1, r2} {
		if r.Code != 301 || r.Header().Get("Location") != "/new" || r.Header().Get("X-Page") != "1" {
			t.Errorf("response %d: %d %v", i, r.Code, r.Header())
		}
	}
	if r1.Header().Get("X-Lyr-Cache") != "MISS" || r2.Header().Get("X-Lyr-Cache") != "HIT" || n.Load() != 1 || st.puts.Load() != 1 {
		t.Errorf("%q %q, %d renders", r1.Header().Get("X-Lyr-Cache"), r2.Header().Get("X-Lyr-Cache"), n.Load())
	}
	if r2.Body.String() != r1.Body.String() || !strings.Contains(r1.Body.String(), "/new") {
		t.Errorf("bodies %q %q", r1.Body, r2.Body)
	}
}

func TestCacheVary(t *testing.T) {
	var n atomic.Int32
	s, st := cacheSite(t, []Route{{Pattern: "/", File: "src/pages/index.lyr", Render: func(c *Ctx, w *Writer) error {
		n.Add(1)
		c.Cache().Set(hour)
		c.Header().Set("Vary", "accept-language, Set-Cookie")
		w.String("lang=" + c.Request.Header.Get("Accept-Language"))
		return nil
	}}})
	h := s.Handler()
	for i, tt := range []struct{ lang, state, body string }{
		{"en", "MISS", "lang=en"}, {"fr", "MISS", "lang=fr"}, {"en", "HIT", "lang=en"}, {"fr", "HIT", "lang=fr"}, {"", "MISS", "lang="},
	} {
		r := get(h, "/", "Accept-Language", tt.lang)
		if r.Header().Get("X-Lyr-Cache") != tt.state || r.Body.String() != tt.body {
			t.Errorf("request %d (%q): %q %q, want %q %q", i, tt.lang, r.Header().Get("X-Lyr-Cache"), r.Body, tt.state, tt.body)
		}
	}
	if n.Load() != 3 {
		t.Errorf("%d renders, want 3", n.Load())
	}
	m := st.entry("example.com/")
	if m == nil || !slices.Equal(m.Vary, []string{"Accept-Language"}) || m.Body != nil {
		t.Errorf("marker %+v", m)
	}
}

func TestCacheStatusAndHeaders(t *testing.T) {
	var n atomic.Int32
	s, _ := cacheSite(t, []Route{
		{Pattern: "/gone", File: "g.lyr", Render: counted(&n, func(c *Ctx) error {
			c.Cache().Set(hour)
			c.SetStatus(http.StatusGone)
			c.Header().Set("Cache-Control", "public, max-age=60")
			c.Header().Set("Content-Type", "text/plain")
			return nil
		}, "gone")},
	})
	h := s.Handler()
	mw := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Request-Id", "r1")
		h.ServeHTTP(w, r)
	})
	get(mw, "/gone")
	r := get(mw, "/gone")
	if r.Code != 410 || r.Header().Get("X-Lyr-Cache") != "HIT" || r.Header().Get("Cache-Control") != "public, max-age=60" ||
		r.Header().Get("Content-Type") != "text/plain" || r.Header().Get("X-Request-Id") != "r1" {
		t.Errorf("%d %v", r.Code, r.Header())
	}
	// A 410 isn't 2xx: no automatic ETag, no 304.
	if r.Header().Get("Etag") != "" {
		t.Errorf("ETag on a 410: %q", r.Header().Get("Etag"))
	}
}

func TestCacheConditional(t *testing.T) {
	var n atomic.Int32
	lm := time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC)
	s, _ := cacheSite(t, []Route{
		{Pattern: "/", File: "i.lyr", Render: counted(&n, setOpts(hour), "home")},
		{Pattern: "/etag", File: "e.lyr", Render: counted(&n, setOpts(CacheOptions{MaxAge: time.Hour, ETag: `"v1"`, LastModified: lm}), "etag")},
		{Pattern: "/manual", File: "m.lyr", Render: counted(&n, func(c *Ctx) error {
			c.Header().Set("ETag", `"m1"`)
			c.Header().Set("Last-Modified", lm.Format(http.TimeFormat))
			return nil
		}, "manual")},
		{Pattern: "/own", File: "o.lyr", Render: counted(&n, func(c *Ctx) error {
			c.Header().Set("ETag", `"o1"`)
			c.Header().Set("Cache-Control", "max-age=10")
			return nil
		}, "own")},
	})
	h := s.Handler()
	first := get(h, "/")
	etag := first.Header().Get("Etag")
	if len(etag) != 13 || etag[0] != '"' || first.Header().Get("Cache-Control") != "" {
		t.Fatalf("automatic ETag %q, Cache-Control %q", etag, first.Header().Get("Cache-Control"))
	}
	if autoETag([]byte("home #1")) != etag || autoETag([]byte("x")) == etag {
		t.Error("automatic ETag isn't a function of the body")
	}
	for _, tt := range []struct {
		target, header, value string
		want                  int
	}{
		{"/", "If-None-Match", etag, 304},
		{"/", "If-None-Match", "W/" + etag, 304},
		{"/", "If-None-Match", `"nope", ` + etag, 304},
		{"/", "If-None-Match", "*", 304},
		{"/", "If-None-Match", `"nope"`, 200},
		{"/etag", "If-None-Match", `W/"v1"`, 304},
		{"/etag", "If-Modified-Since", lm.Format(http.TimeFormat), 304},
		{"/etag", "If-Modified-Since", lm.Add(-time.Second).Format(http.TimeFormat), 200},
		{"/etag", "If-Modified-Since", "garbage", 200},
		{"/manual", "If-None-Match", `"m1"`, 304},
		{"/manual", "If-Modified-Since", lm.Add(time.Hour).Format(http.TimeFormat), 304},
		{"/manual", "If-None-Match", `"m2"`, 200},
		{"/own", "If-None-Match", `"o1"`, 304},
	} {
		r := get(h, tt.target, tt.header, tt.value)
		if r.Code != tt.want {
			t.Errorf("%s %s: %s: %d, want %d", tt.target, tt.header, tt.value, r.Code, tt.want)
		}
		if r.Code == 304 && (r.Body.Len() != 0 || r.Header().Get("Content-Length") != "") {
			t.Errorf("%s: 304 with body %q, Content-Length %q", tt.target, r.Body, r.Header().Get("Content-Length"))
		}
	}
	e := get(h, "/etag")
	if e.Header().Get("Etag") != `"v1"` || e.Header().Get("Last-Modified") != lm.Format(http.TimeFormat) || e.Header().Get("Cache-Control") != "no-cache" {
		t.Errorf("/etag headers %v", e.Header())
	}
	if m := get(h, "/manual"); m.Header().Get("Cache-Control") != "no-cache" || m.Header().Get("X-Lyr-Cache") != "" {
		t.Errorf("/manual headers %v", m.Header())
	}
	if o := get(h, "/own"); o.Header().Get("Cache-Control") != "max-age=10" {
		t.Errorf("/own Cache-Control %q", o.Header().Get("Cache-Control"))
	}
	// The ETag is stable: the same body stored again gets the same one.
	if err := s.InvalidateCache(context.Background(), InvalidateOptions{Path: "/"}); err != nil {
		t.Fatal(err)
	}
	s2, _ := cacheSite(t, []Route{{Pattern: "/", File: "i.lyr", Render: func(c *Ctx, w *Writer) error { c.Cache().Set(hour); w.Raw("home #1"); return nil }}})
	if got := get(s2.Handler(), "/").Header().Get("Etag"); got != etag {
		t.Errorf("same body on another site: ETag %q, want %q", got, etag)
	}
}

func TestCacheProviders(t *testing.T) {
	page := func(c *Ctx, w *Writer) error {
		c.Cache().Set(CacheOptions{MaxAge: 90 * time.Second, SWR: 1500 * time.Millisecond, Tags: []string{"a", "b"}})
		w.Raw("x")
		return nil
	}
	routes := []Route{{Pattern: "/", File: "i.lyr", Render: page}}
	s := testSite(t, routes, WithCache(plainProvider{}))
	r := get(s.Handler(), "/")
	if r.Header().Get("Cdn-Cache-Control") != "max-age=90, stale-while-revalidate=1" || r.Header().Get("Cache-Tag") != "a, b" || r.Header().Get("X-Lyr-Cache") != "" {
		t.Errorf("default headers: %v", r.Header())
	}
	hp := &headerProvider{}
	s = testSite(t, routes, WithCache(hp))
	r = get(s.Handler(), "/")
	if r.Header().Get("Surrogate-Control") != "max-age=90" || r.Header().Get("Surrogate-Key") != "a b" || r.Header().Get("Cdn-Cache-Control") != "" {
		t.Errorf("header provider: %v", r.Header())
	}
	// Disable before the lookup holds even when the page calls Set; a
	// cookie keeps CDN headers off too.
	cookie := func(c *Ctx, w *Writer) error {
		c.Header().Set("Set-Cookie", "a=1")
		return page(c, w)
	}
	s = testSite(t, []Route{{Pattern: "/", File: "i.lyr", Render: page}, {Pattern: "/cookie", File: "c.lyr", Render: cookie}},
		WithCache(plainProvider{}), withLocalsCache())
	mw := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r, l := EnsureLocals[cacheLocals](r)
		if r.Header.Get("X-User") != "" {
			l.Cache.Disable()
		}
		s.Handler().ServeHTTP(w, r)
	})
	for _, r := range []*httptest.ResponseRecorder{get(mw, "/", "X-User", "ann"), get(mw, "/cookie")} {
		if r.Header().Get("Cdn-Cache-Control") != "" || r.Header().Get("Cache-Tag") != "" {
			t.Errorf("CDN headers on a per-user response: %v", r.Header())
		}
	}
	if r := get(mw, "/"); r.Header().Get("Cdn-Cache-Control") == "" {
		t.Errorf("anonymous: %v", r.Header())
	}
	// Nothing active: no headers.
	s = testSite(t, []Route{{Pattern: "/", File: "i.lyr", Render: func(c *Ctx, w *Writer) error { return nil }}}, WithCache(hp))
	get(s.Handler(), "/")
	if hp.calls.Load() != 1 {
		t.Errorf("WriteCacheHeaders called %d times, want 1", hp.calls.Load())
	}
}

func TestCacheDev(t *testing.T) {
	t.Setenv("LYR_DEV", "1")
	var enabled atomic.Bool
	var invErr atomic.Value
	hp := &headerProvider{}
	st := newFakeStore()
	for _, p := range []CacheProvider{st, hp} {
		var n atomic.Int32
		s := testSite(t, []Route{{Pattern: "/", File: "i.lyr", Render: counted(&n, func(c *Ctx) error {
			c.Cache().Set(hour)
			enabled.Store(c.Cache().Enabled())
			invErr.Store(errBox{c.Cache().Invalidate(context.Background(), InvalidateOptions{Tags: []string{"x"}})})
			return nil
		}, "x")}}, WithCache(p))
		for range 2 {
			if r := get(s.Handler(), "/"); r.Header().Get("X-Lyr-Cache") != "" || r.Header().Get("Surrogate-Control") != "" || r.Header().Get("Etag") != "" {
				t.Errorf("dev headers %v", r.Header())
			}
		}
		if n.Load() != 2 || enabled.Load() || invErr.Load().(errBox).err != nil {
			t.Errorf("dev: %d renders, Enabled %v, Invalidate %v", n.Load(), enabled.Load(), invErr.Load())
		}
		if err := s.InvalidateCache(context.Background(), InvalidateOptions{}); err != nil {
			t.Errorf("site.InvalidateCache in dev: %v", err)
		}
	}
	if st.puts.Load() != 0 || hp.calls.Load() != 0 {
		t.Error("the provider was used in dev")
	}
}

type errBox struct{ err error }

func TestCacheNoProvider(t *testing.T) {
	logs := captureLogSync(t)
	warnedNoProvider.Store(false)
	t.Cleanup(func() { warnedNoProvider.Store(false) })
	var invErr atomic.Value
	var n atomic.Int32
	s := testSite(t, []Route{
		{Pattern: "/", File: "i.lyr", Render: counted(&n, func(c *Ctx) error {
			c.Cache().Set(hour)
			invErr.Store(errBox{c.Cache().Invalidate(context.Background(), InvalidateOptions{})})
			if c.Cache().Enabled() {
				return errors.New("enabled without a provider")
			}
			return nil
		}, "x")},
		{Pattern: "/off", File: "o.lyr", Render: counted(&n, func(c *Ctx) error { c.Cache().Disable(); return nil }, "x")},
	})
	for range 3 {
		if r := get(s.Handler(), "/"); r.Code != 200 || r.Header().Get("X-Lyr-Cache") != "" {
			t.Errorf("%d %v", r.Code, r.Header())
		}
	}
	if out := logs.String(); strings.Count(out, "caching is not enabled") != 1 {
		t.Errorf("warnings:\n%s", out)
	}
	if err := invErr.Load().(errBox).err; !errors.Is(err, ErrCacheNotEnabled) {
		t.Errorf("ctx.Cache().Invalidate: %v", err)
	}
	if err := s.InvalidateCache(context.Background(), InvalidateOptions{}); !errors.Is(err, ErrCacheNotEnabled) {
		t.Errorf("site.InvalidateCache: %v", err)
	}
	// Disable alone doesn't warn.
	warnedNoProvider.Store(false)
	get(s.Handler(), "/off")
	if warnedNoProvider.Load() {
		t.Error("Disable warned")
	}
}

// Locals with the cache field, for the middleware tests.
type cacheLocals struct {
	Cache Cache
	Route RouteInfo
	User  string
}

func withLocalsCache() Option {
	return WithLocalsCache(func(l *cacheLocals) *Cache { return &l.Cache })
}

func TestCacheMiddleware(t *testing.T) {
	var n, mwRuns atomic.Int32
	s, _ := cacheSite(t, []Route{{Pattern: "/", File: "i.lyr", Render: counted(&n, func(c *Ctx) error {
		c.Cache().Set(CacheOptions{MaxAge: time.Hour})
		if LocalsOf[cacheLocals](c).Cache.Options().MaxAge != time.Hour {
			return errors.New("ctx.Cache() isn't the Locals field")
		}
		return nil
	}, "x")}}, withLocalsCache())
	var lastEnabled atomic.Bool
	var lastOpts atomic.Value
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mwRuns.Add(1)
		r, l := EnsureLocals[cacheLocals](r)
		if l.User = r.Header.Get("X-User"); l.User != "" {
			l.Cache.Disable()
		} else {
			l.Cache.Set(CacheOptions{Tags: []string{"mw"}})
		}
		s.Handler().ServeHTTP(w, r)
		lastEnabled.Store(l.Cache.Enabled())
		lastOpts.Store(l.Cache.Options())
	})
	if r := get(h, "/"); r.Header().Get("X-Lyr-Cache") != "MISS" {
		t.Fatalf("first: %q", r.Header().Get("X-Lyr-Cache"))
	}
	if o := lastOpts.Load().(CacheOptions); !lastEnabled.Load() || !slices.Equal(o.Tags, []string{"mw"}) || o.MaxAge != time.Hour {
		t.Errorf("middleware's view after next: enabled %v, %+v", lastEnabled.Load(), o)
	}
	if r := get(h, "/"); r.Header().Get("X-Lyr-Cache") != "HIT" || mwRuns.Load() != 2 {
		t.Errorf("HIT: %q, middleware ran %d times", r.Header().Get("X-Lyr-Cache"), mwRuns.Load())
	}
	// Disable before the lookup: a fresh render, no header, nothing stored.
	r := get(h, "/", "X-User", "ann")
	if r.Header().Get("X-Lyr-Cache") != "" || n.Load() != 2 || r.Body.String() != "x #2" {
		t.Errorf("signed in: %q %q, %d renders", r.Header().Get("X-Lyr-Cache"), r.Body, n.Load())
	}
	if r := get(h, "/"); r.Body.String() != "x #1" {
		t.Errorf("anonymous after signed in: %q", r.Body)
	}
}

func TestCacheLocalsOfMovesHandle(t *testing.T) {
	var got atomic.Value
	s, st := cacheSite(t, []Route{{Pattern: "/", File: "i.lyr", Render: func(c *Ctx, w *Writer) error {
		c.Cache().Set(CacheOptions{MaxAge: time.Hour, Tags: []string{"before"}})
		l := LocalsOf[cacheLocals](c) // no middleware: allocated here
		if c.Cache() != &l.Cache {
			return errors.New("ctx.Cache() isn't the new Locals field")
		}
		l.Cache.Set(CacheOptions{Tags: []string{"after"}})
		got.Store(l.Cache.Options())
		return nil
	}}}, withLocalsCache())
	get(s.Handler(), "/")
	if o := got.Load().(CacheOptions); !slices.Equal(o.Tags, []string{"before", "after"}) {
		t.Errorf("options %+v", o)
	}
	if e := st.entry("example.com/"); e == nil || !slices.Equal(e.Tags, []string{"before", "after"}) {
		t.Errorf("entry %+v", e)
	}
}

func TestCacheRouteRules(t *testing.T) {
	var n atomic.Int32
	routes := []Route{
		{Pattern: "/blog/[slug]", File: "s.lyr", Render: counted(&n, setOpts(CacheOptions{MaxAge: time.Hour, Tags: []string{"post"}}), "post")},
		{Pattern: "/blog/off", File: "o.lyr", Render: counted(&n, func(c *Ctx) error { c.Cache().Disable(); return nil }, "off")},
		{Pattern: "/blog/plain", File: "p.lyr", Render: counted(&n, nil, "plain")},
		{Pattern: "/about", File: "a.lyr", Render: counted(&n, nil, "about")},
	}
	rules := WithRouteRules(RouteRule{Pattern: "/blog/[...path]", Options: CacheOptions{MaxAge: 300 * time.Second, SWR: time.Hour, Tags: []string{"blog"}}},
		RouteRule{Pattern: "/blog/plain", Options: CacheOptions{MaxAge: 5 * time.Second}})
	s, st := cacheSite(t, routes, rules, withLocalsCache())
	mw := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r, l := EnsureLocals[cacheLocals](r)
		if r.URL.Query().Has("mw") {
			l.Cache.Set(CacheOptions{MaxAge: 10 * time.Second})
		}
		s.Handler().ServeHTTP(w, r)
	})
	for _, target := range []string{"/blog/hello", "/blog/off", "/blog/plain", "/about", "/blog/plain?mw"} {
		get(mw, target)
		if r := get(mw, target); (r.Header().Get("X-Lyr-Cache") == "HIT") != (target != "/blog/off" && target != "/about") {
			t.Errorf("%s: second request %q", target, r.Header().Get("X-Lyr-Cache"))
		}
	}
	for key, want := range map[string]CacheOptions{
		"example.com/blog/hello":    {MaxAge: time.Hour, SWR: time.Hour, Tags: []string{"blog", "post"}},
		"example.com/blog/plain":    {MaxAge: 5 * time.Second},
		"example.com/blog/plain?mw": {MaxAge: 10 * time.Second}, // the middleware's Set wins over the rule
		"example.com/blog/off":      {},
		"example.com/about":         {},
	} {
		e := st.entry(key)
		if want.MaxAge == 0 {
			if e != nil {
				t.Errorf("%s stored", key)
			}
			continue
		}
		if e == nil || e.MaxAge != want.MaxAge || e.SWR != want.SWR || !slices.Equal(e.Tags, want.Tags) {
			t.Errorf("%s: %+v, want %+v", key, e, want)
		}
	}
	if msg := panicMessage(func() { testSite(t, routes, WithRouteRules(RouteRule{Pattern: "/blog/[slug"})) }); !strings.Contains(msg, "lyr.json routeRules") {
		t.Errorf("bad pattern: %q", msg)
	}
	if msg := panicMessage(func() { testSite(t, routes, WithRouteRules(RouteRule{Pattern: "blog"})) }); !strings.Contains(msg, "must start with /") {
		t.Errorf("relative pattern: %q", msg)
	}
	// Inert without a provider.
	s = testSite(t, routes, rules)
	if r := get(s.Handler(), "/blog/plain"); r.Header().Get("X-Lyr-Cache") != "" {
		t.Errorf("rule without a provider: %v", r.Header())
	}
}

func TestCacheRouteRulesTrailingSlash(t *testing.T) {
	var n atomic.Int32
	s, st := cacheSite(t, []Route{{Pattern: "/blog/[slug]", File: "s.lyr", Render: counted(&n, nil, "post")}},
		WithTrailingSlash(TrailingSlashAlways), WithRouteRules(RouteRule{Pattern: "/blog/[slug]", Options: CacheOptions{MaxAge: time.Hour}}))
	get(s.Handler(), "/blog/a/")
	if r := get(s.Handler(), "/blog/a/"); r.Header().Get("X-Lyr-Cache") != "HIT" {
		t.Errorf("always mode: %q (keys %q)", r.Header().Get("X-Lyr-Cache"), st.keys())
	}
}

const cachedIslandID = "Cached-12345678"

type cachedIslandProps struct{ N int }

func TestCacheIslands(t *testing.T) {
	var n atomic.Int32
	isl := []Island{NewIsland(cachedIslandID, "src/components/Cached.lyr", func(c *Ctx, w *Writer, p cachedIslandProps, s Slots) error {
		n.Add(1)
		if p.N > 0 {
			c.Cache().Set(CacheOptions{MaxAge: time.Hour, Tags: []string{"island"}})
		}
		w.Int(int64(p.N))
		return nil
	})}
	st := newFakeStore()
	t.Setenv("LYR_STATIC", "")
	t.Setenv("LYR_DEV", "")
	// The rule would match every path, but rules never apply to islands.
	s := NewSite(nil, isl, WithStaticDir(t.TempDir()), WithCache(st),
		WithRouteRules(RouteRule{Pattern: "/[...rest]", Options: CacheOptions{MaxAge: time.Hour}}))
	h := s.Handler()
	url := func(props string) string {
		return islandPrefix + s.keys.buildID + "/" + cachedIslandID + "?p=" + sealerOf(s.keys).Seal(cachedIslandID, s.keys.buildID, []byte(props))
	}
	u := url(`{"N":1}`)
	r1, r2 := get(h, u), get(h, u)
	if r1.Header().Get("X-Lyr-Cache") != "MISS" || r2.Header().Get("X-Lyr-Cache") != "HIT" || r2.Body.String() != "1" || n.Load() != 1 {
		t.Fatalf("island: %q %q %q, %d renders", r1.Header().Get("X-Lyr-Cache"), r2.Header().Get("X-Lyr-Cache"), r2.Body, n.Load())
	}
	if r2.Header().Get("Cache-Control") != "private, no-store" || r2.Header().Get("X-Robots-Tag") != "noindex" || r2.Header().Get("Etag") == "" {
		t.Errorf("island headers %v", r2.Header())
	}
	if r := get(h, u, "If-None-Match", r1.Header().Get("Etag")); r.Code != 304 {
		t.Errorf("island 304: %d", r.Code)
	}
	// Props that don't Set: rules don't apply to /_lyr/, nothing stored.
	u0 := url(`{"N":0}`)
	get(h, u0)
	if r := get(h, u0); r.Header().Get("X-Lyr-Cache") != "" || n.Load() != 3 {
		t.Errorf("uncached island: %q, %d renders", r.Header().Get("X-Lyr-Cache"), n.Load())
	}
	// POST islands always render.
	tok := strings.SplitN(u, "?p=", 2)[1]
	r := httptest.NewRequest("POST", strings.SplitN(u, "?", 2)[0], strings.NewReader(tok))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	if rec.Code != 200 || rec.Header().Get("X-Lyr-Cache") != "" || n.Load() != 4 {
		t.Errorf("POST: %d %q, %d renders", rec.Code, rec.Header().Get("X-Lyr-Cache"), n.Load())
	}
	if st.puts.Load() != 1 {
		t.Errorf("%d puts", st.puts.Load())
	}
}

type fragmentProps struct{ ID string }

func fragment(c *Ctx, w *Writer, p fragmentProps, s Slots) error {
	c.Cache().Set(CacheOptions{MaxAge: time.Hour, Tags: []string{"frag:" + p.ID}})
	w.Raw("<p>")
	w.String(p.ID)
	w.Raw("</p>")
	return nil
}

func TestCacheHandler(t *testing.T) {
	var n atomic.Int32
	s, st := cacheSite(t, nil, withLocalsCache())
	frag := s.CacheHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		if err := RenderComponent(w, r, fragment, fragmentProps{ID: r.URL.Query().Get("id")}); err != nil {
			t.Error(err)
		}
	}))
	api := s.CacheHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		if l := LocalsFromRequest[cacheLocals](r); l != nil {
			l.Cache.Set(CacheOptions{MaxAge: time.Minute})
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"ok":true}`))
	}))
	withLocals := func(h http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r, _ = EnsureLocals[cacheLocals](r)
			h.ServeHTTP(w, r)
		})
	}
	get(frag, "/frag?id=a")
	r := get(frag, "/frag?id=a")
	if r.Header().Get("X-Lyr-Cache") != "HIT" || r.Body.String() != "<p>a</p>" || r.Header().Get("Content-Type") != "text/html; charset=utf-8" || n.Load() != 1 {
		t.Errorf("fragment: %q %q %v, %d calls", r.Header().Get("X-Lyr-Cache"), r.Body, r.Header(), n.Load())
	}
	if e := st.entry("example.com/frag?id=a"); e == nil || !slices.Equal(e.Tags, []string{"frag:a"}) {
		t.Errorf("fragment entry %+v", e)
	}
	h := withLocals(api)
	get(h, "/api")
	r = get(h, "/api")
	if r.Code != 201 || r.Header().Get("X-Lyr-Cache") != "HIT" || r.Body.String() != `{"ok":true}` || r.Header().Get("Content-Type") != "application/json" || n.Load() != 2 {
		t.Errorf("api: %d %q %q, %d calls", r.Code, r.Header().Get("X-Lyr-Cache"), r.Body, n.Load())
	}
	// Without the locals, the handler can't set options: rendered each time.
	get(api, "/api2")
	if r := get(api, "/api2"); r.Header().Get("X-Lyr-Cache") != "" || n.Load() != 4 {
		t.Errorf("no locals: %q, %d calls", r.Header().Get("X-Lyr-Cache"), n.Load())
	}
	// Without a provider it's the handler itself.
	plain := testSite(t, nil)
	inner := http.NotFoundHandler()
	if got := plain.CacheHandler(inner); got.(*cacheHandler).h == nil {
		t.Error("CacheHandler")
	}
	if rec := get(plain.CacheHandler(inner), "/x"); rec.Code != 404 {
		t.Errorf("no provider: %d", rec.Code)
	}
}

func TestCacheConcurrentChildren(t *testing.T) {
	s, st := cacheSite(t, []Route{{Pattern: "/", File: "i.lyr", Render: func(c *Ctx, w *Writer) error {
		c.Cache().Set(CacheOptions{MaxAge: time.Minute, Tags: []string{"page"}})
		for i := range 3 {
			if err := Concurrent(c, w, func(c *Ctx, w *Writer, i int, s Slots) error {
				c.Cache().Set(CacheOptions{Tags: []string{"child" + strconv.Itoa(i)}})
				if i == 1 {
					c.Cache().Set(CacheOptions{MaxAge: time.Hour})
				}
				w.Int(int64(i))
				return nil
			}, i, Slots{}); err != nil {
				return err
			}
		}
		return nil
	}}})
	if r := get(s.Handler(), "/"); r.Body.String() != "012" || r.Header().Get("X-Lyr-Cache") != "MISS" {
		t.Fatalf("%q %q", r.Body, r.Header().Get("X-Lyr-Cache"))
	}
	e := st.entry("example.com/")
	if e == nil || e.MaxAge != time.Hour || !slices.Equal(e.Tags, []string{"page", "child0", "child1", "child2"}) {
		t.Errorf("entry %+v", e)
	}
}

func TestCacheInvalidate(t *testing.T) {
	var n atomic.Int32
	s, st := cacheSite(t, []Route{
		{Pattern: "/blog/[slug]", File: "s.lyr", Render: counted(&n, func(c *Ctx) error {
			c.Cache().Set(CacheOptions{MaxAge: time.Hour, Tags: []string{"post:" + c.Param("slug")}})
			return nil
		}, "post")},
		{Pattern: "/purge", File: "p.lyr", Render: func(c *Ctx, w *Writer) error {
			return c.Cache().Invalidate(c.Request.Context(), InvalidateOptions{Tags: []string{"post:a"}})
		}},
	})
	h := s.Handler()
	for _, u := range []string{"/blog/a", "/blog/a?x=1", "/blog/b"} {
		get(h, u)
	}
	if r := get(h, "/purge"); r.Code != 200 {
		t.Fatalf("purge: %d", r.Code)
	}
	if got := st.keys(); !slices.Equal(got, []string{"example.com/blog/b"}) {
		t.Errorf("after the tag purge: %q", got)
	}
	if err := s.InvalidateCache(context.Background(), InvalidateOptions{Path: "/blog/b"}); err != nil || len(st.keys()) != 0 {
		t.Errorf("path purge: %v, %q", err, st.keys())
	}
	if r := get(h, "/blog/a"); r.Header().Get("X-Lyr-Cache") != "MISS" {
		t.Errorf("after the purge: %q", r.Header().Get("X-Lyr-Cache"))
	}
}

func TestCacheStaleWhileRevalidate(t *testing.T) {
	clock := useClock(t)
	logs := captureLogSync(t)
	var n atomic.Int32
	var fail atomic.Value // "", "error", "panic"
	fail.Store("")
	release := make(chan struct{})
	var block atomic.Bool
	s, st := cacheSite(t, []Route{{Pattern: "/", File: "i.lyr", Render: counted(&n, func(c *Ctx) error {
		if block.Load() {
			<-release
		}
		switch fail.Load() {
		case "error":
			return errors.New("api down")
		case "panic":
			panic("kaboom")
		}
		c.Cache().Set(CacheOptions{MaxAge: 10 * time.Second, SWR: time.Minute})
		return nil
	}, "v")}})
	h := s.Handler()
	if r := get(h, "/"); r.Header().Get("X-Lyr-Cache") != "MISS" {
		t.Fatal(r.Header())
	}
	clock.add(20 * time.Second)
	block.Store(true)
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			if r := get(h, "/"); r.Header().Get("X-Lyr-Cache") != "STALE" || r.Body.String() != "v #1" {
				t.Errorf("stale: %q %q", r.Header().Get("X-Lyr-Cache"), r.Body)
			}
		})
	}
	wg.Wait()
	block.Store(false)
	close(release)
	waitFlights(t, s)
	if n.Load() != 2 || st.puts.Load() != 2 {
		t.Fatalf("%d renders, %d puts; want exactly one background render", n.Load(), st.puts.Load())
	}
	if r := get(h, "/"); r.Header().Get("X-Lyr-Cache") != "HIT" || r.Body.String() != "v #2" {
		t.Errorf("after revalidation: %q %q", r.Header().Get("X-Lyr-Cache"), r.Body)
	}
	// Errors and panics keep the old copy and are logged.
	for i, mode := range []string{"error", "panic"} {
		fail.Store(mode)
		clock.add(20 * time.Second)
		if r := get(h, "/"); r.Header().Get("X-Lyr-Cache") != "STALE" || r.Body.String() != "v #2" {
			t.Errorf("%s: %q %q", mode, r.Header().Get("X-Lyr-Cache"), r.Body)
		}
		waitFlights(t, s)
		if st.puts.Load() != 2 || n.Load() != int32(3+i) {
			t.Errorf("%s: %d puts, %d renders", mode, st.puts.Load(), n.Load())
		}
	}
	out := logs.String()
	for _, want := range []string{"lyr: revalidating GET /: api down; the stale copy is kept", "lyr: revalidating GET /: panic: kaboom; the stale copy is kept"} {
		if !strings.Contains(out, want) {
			t.Errorf("log lacks %q:\n%s", want, out)
		}
	}
	// Past MaxAge + SWR: a miss.
	fail.Store("")
	clock.add(2 * time.Minute)
	if r := get(h, "/"); r.Header().Get("X-Lyr-Cache") != "MISS" || r.Body.String() != "v #5" {
		t.Errorf("expired: %q %q", r.Header().Get("X-Lyr-Cache"), r.Body)
	}
}

func TestCacheCoalescing(t *testing.T) {
	var n atomic.Int32
	var cookie, block atomic.Bool
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	s, _ := cacheSite(t, []Route{{Pattern: "/", File: "i.lyr", Render: counted(&n, func(c *Ctx) error {
		if block.Load() {
			entered <- struct{}{}
			<-release
		}
		c.Cache().Set(CacheOptions{MaxAge: time.Hour, Tags: []string{"t"}})
		if cookie.Load() {
			c.Header().Set("Set-Cookie", "a=1")
		}
		return nil
	}, "v")}})
	h := s.Handler()
	var waiting atomic.Int32
	testFlightWaitHook = func() { waiting.Add(1) }
	t.Cleanup(func() { testFlightWaitHook = nil })

	run := func(storable bool) (renders int32, states []string) {
		get(h, "/") // the route is known
		if err := s.InvalidateCache(context.Background(), InvalidateOptions{Tags: []string{"t"}}); err != nil {
			t.Fatal(err)
		}
		cookie.Store(!storable)
		start := n.Load()
		waiting.Store(0)
		release = make(chan struct{})
		block.Store(true)
		var mu sync.Mutex
		var wg sync.WaitGroup
		wg.Go(func() {
			r := get(h, "/")
			mu.Lock()
			states = append(states, r.Header().Get("X-Lyr-Cache"))
			mu.Unlock()
		})
		<-entered
		block.Store(false)
		const waiters = 8
		for range waiters {
			wg.Go(func() {
				r := get(h, "/")
				mu.Lock()
				states = append(states, r.Header().Get("X-Lyr-Cache"))
				mu.Unlock()
			})
		}
		for waiting.Load() < waiters {
			time.Sleep(time.Millisecond)
		}
		close(release)
		wg.Wait()
		slices.Sort(states)
		return n.Load() - start, states
	}
	renders, states := run(true)
	want := append(slices.Repeat([]string{"HIT"}, 8), "MISS")
	if renders != 1 || !slices.Equal(states, want) {
		t.Errorf("storable: %d renders, states %q", renders, states)
	}
	renders, states = run(false)
	if renders != 9 || !slices.Equal(states, slices.Repeat([]string{""}, 9)) {
		t.Errorf("not storable: %d renders, states %q", renders, states)
	}
}

// TestCacheRace runs HITs, MISSes, STALEs and invalidations on one key
// concurrently; run it with -race.
func TestCacheRace(t *testing.T) {
	clock := useClock(t)
	var n atomic.Int32
	s, _ := cacheSite(t, []Route{{Pattern: "/", File: "i.lyr", Render: counted(&n, setOpts(CacheOptions{MaxAge: time.Second, SWR: time.Second, Tags: []string{"t"}}), "v")}},
		withLocalsCache(), WithLocalsRoute(func(l *cacheLocals) *RouteInfo { return &l.Route }))
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r, l := EnsureLocals[cacheLocals](r)
		l.Cache.Set(CacheOptions{Tags: []string{"mw"}})
		s.Handler().ServeHTTP(w, r)
		// After next, the middleware reads what Lyr filled, while a
		// background revalidation may run with the same locals.
		_ = l.Cache.Options()
		_ = l.Route.Pattern
		for range l.Route.Params() {
		}
	})
	var wg sync.WaitGroup
	for g := range 8 {
		wg.Go(func() {
			for i := range 200 {
				switch {
				case g == 0 && i%10 == 0:
					_ = s.InvalidateCache(context.Background(), InvalidateOptions{Tags: []string{"t"}})
				case g == 1 && i%7 == 0:
					clock.add(700 * time.Millisecond)
				default:
					if r := get(h, "/", "If-None-Match", `"x"`); r.Code != 200 || !strings.HasPrefix(r.Body.String(), "v #") {
						t.Errorf("%d %q", r.Code, r.Body)
						return
					}
				}
			}
		})
	}
	wg.Wait()
	waitFlights(t, s)
}
