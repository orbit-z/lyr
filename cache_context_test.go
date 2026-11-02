package lyr

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Caching by context (PLAN.md §4h "Caching by context (VaryBy)"):
// middleware sets Cache.Context values, pages name the ones they depend on
// in CacheOptions.VaryBy.

func TestCacheContextHandle(t *testing.T) {
	var k Cache
	k.Context("user", "ann")
	k.Context("role", "admin")
	k.Context("user", "bob") // replaces
	if got := contextOf(&k); !slices.Equal(got, []cacheCtxValue{{"user", "bob"}, {"role", "admin"}}) {
		t.Fatalf("context %v", got)
	}
	// More than 4 keys move to the overflow slice, in order.
	for _, key := range []string{"a", "b", "c", "d"} {
		k.Context(key, key+"1")
	}
	if len(contextOf(&k)) != 6 || contextValue(&k, "d") != "d1" || contextValue(&k, "user") != "bob" || contextValue(&k, "none") != "" {
		t.Fatalf("overflow: %v", contextOf(&k))
	}
	k.Set(CacheOptions{MaxAge: time.Minute, VaryBy: []string{"user"}})
	k.Set(CacheOptions{VaryBy: []string{"role", "user", "x"}})
	if got := k.Options().VaryBy; !slices.Equal(got, []string{"user", "role", "x"}) {
		t.Errorf("VaryBy %q", got)
	}
	var c Cache
	copyCache(&c, &k)
	if !slices.Equal(contextOf(&c), contextOf(&k)) || !slices.Equal(c.Options().VaryBy, []string{"user", "role", "x"}) {
		t.Errorf("copy: %v %q", contextOf(&c), c.Options().VaryBy)
	}
	// Disable drops VaryBy (an option) but keeps the context.
	k.Disable()
	if k.Options().VaryBy != nil || contextValue(&k, "role") != "admin" {
		t.Errorf("after Disable: %q %v", k.Options().VaryBy, contextOf(&k))
	}
	// After the lookup: the same value is fine, a change makes it late.
	logs := captureLogSync(t)
	warnedLateContext.Store(false)
	t.Cleanup(func() { warnedLateContext.Store(false) })
	var l Cache
	l.Set(CacheOptions{MaxAge: time.Minute})
	l.Context("user", "ann")
	l.flags |= cacheLooked
	l.Context("user", "ann")
	if !cacheStorable(&l) {
		t.Fatal("same value after the lookup made the handle late")
	}
	l.Context("user", "bob")
	if cacheStorable(&l) || cacheActive(&l) || contextValue(&l, "user") != "ann" {
		t.Errorf("late change: storable %v, value %q", cacheStorable(&l), contextValue(&l, "user"))
	}
	if !strings.Contains(logs.String(), `lyr: Cache.Context("user", …) was called after the cache lookup`) {
		t.Errorf("log %q", logs.String())
	}
	var m Cache
	m.flags |= cacheLooked
	m.Context("new", "1")
	if m.flags&cacheLate == 0 || len(contextOf(&m)) != 0 {
		t.Error("new key after the lookup isn't late")
	}
	releaseCache(&l)
	if l.flags != 0 || len(contextOf(&l)) != 0 {
		t.Error("release keeps the context")
	}
}

// contextSite serves pages behind a middleware that sets the "user" and
// "role" context values from the X-User and X-Role headers (when present).
func contextSite(t *testing.T, routes []Route, opts ...Option) (http.Handler, *Site, *fakeStore) {
	t.Helper()
	s, st := cacheSite(t, routes, append([]Option{withLocalsCache()}, opts...)...)
	mw := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r, l := EnsureLocals[cacheLocals](r)
		if u, ok := r.Header["X-User"]; ok {
			l.User = u[0]
			l.Cache.Context("user", u[0])
		}
		if role := r.Header.Get("X-Role"); role != "" {
			l.Cache.Context("role", role)
		}
		s.Handler().ServeHTTP(w, r)
	})
	return mw, s, st
}

// userPage renders the user from Locals, with VaryBy names.
func userPage(n *atomic.Int32, o CacheOptions) func(*Ctx, *Writer) error {
	return func(c *Ctx, w *Writer) error {
		i := n.Add(1)
		c.Cache().Set(o)
		w.Raw("hi " + LocalsOf[cacheLocals](c).User + " #")
		w.Int(int64(i))
		return nil
	}
}

func TestCacheVaryBy(t *testing.T) {
	var n atomic.Int32
	h, _, st := contextSite(t, []Route{
		{Pattern: "/dashboard", File: "d.lyr", Render: userPage(&n, CacheOptions{MaxAge: time.Hour, VaryBy: []string{"user"}})},
		{Pattern: "/shared", File: "s.lyr", Render: counted(&n, setOpts(CacheOptions{MaxAge: time.Hour}), "shared")},
		{Pattern: "/both", File: "b.lyr", Render: userPage(&n, CacheOptions{MaxAge: time.Hour, VaryBy: []string{"user", "role"}})},
		{Pattern: "/lang", File: "l.lyr", Render: counted(&n, func(c *Ctx) error {
			c.Cache().Set(CacheOptions{MaxAge: time.Hour, VaryBy: []string{"role"}})
			c.Header().Set("Vary", "Accept-Language, @role")
			return nil
		}, "lang")},
	})
	type step struct {
		path, user, role, lang string
		state, body            string
	}
	for i, s := range []step{
		{path: "/dashboard", user: "ann", state: "MISS", body: "hi ann #1"},
		{path: "/dashboard", user: "bob", state: "MISS", body: "hi bob #2"},
		{path: "/dashboard", user: "ann", state: "HIT", body: "hi ann #1"},
		{path: "/dashboard", user: "bob", state: "HIT", body: "hi bob #2"},
		// No value is its own variant; an empty one is the same variant.
		{path: "/dashboard", state: "MISS", body: "hi  #3"},
		{path: "/dashboard", state: "HIT", body: "hi  #3"},
		{path: "/dashboard", user: "-", state: "HIT", body: "hi  #3"}, // X-User: "" sets ""
		// Other context keys don't split the copies.
		{path: "/dashboard", user: "ann", role: "admin", state: "HIT", body: "hi ann #1"},
		// A page without VaryBy stays one copy, whatever the context.
		{path: "/shared", user: "ann", state: "MISS", body: "shared #4"},
		{path: "/shared", user: "bob", role: "admin", state: "HIT", body: "shared #4"},
		{path: "/shared", state: "HIT", body: "shared #4"},
		// Several keys: one copy per combination.
		{path: "/both", user: "ann", role: "admin", state: "MISS", body: "hi ann #5"},
		{path: "/both", user: "ann", role: "member", state: "MISS", body: "hi ann #6"},
		{path: "/both", user: "ann", role: "admin", state: "HIT", body: "hi ann #5"},
		// A header Vary and VaryBy together; the page's "@role" Vary name
		// is dropped (not a header name).
		{path: "/lang", role: "admin", lang: "fr", state: "MISS", body: "lang #7"},
		{path: "/lang", role: "admin", lang: "de", state: "MISS", body: "lang #8"},
		{path: "/lang", role: "member", lang: "fr", state: "MISS", body: "lang #9"},
		{path: "/lang", role: "admin", lang: "fr", state: "HIT", body: "lang #7"},
	} {
		r := httptest.NewRequest("GET", s.path, nil)
		switch s.user {
		case "":
		case "-":
			r.Header["X-User"] = []string{""}
		default:
			r.Header["X-User"] = []string{s.user}
		}
		if s.role != "" {
			r.Header.Set("X-Role", s.role)
		}
		if s.lang != "" {
			r.Header.Set("Accept-Language", s.lang)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		if got := rec.Header().Get("X-Lyr-Cache"); got != s.state || rec.Body.String() != s.body {
			t.Errorf("step %d %s user=%q role=%q: %q %q, want %q %q", i, s.path, s.user, s.role, got, rec.Body, s.state, s.body)
		}
	}
	for key, want := range map[string][]string{
		"example.com/dashboard": {"@user"},
		"example.com/both":      {"@user", "@role"},
		"example.com/lang":      {"Accept-Language", "@role"},
	} {
		if m := st.entry(key); m == nil || m.Body != nil || !slices.Equal(m.Vary, want) {
			t.Errorf("%s marker %+v, want Vary %q", key, m, want)
		}
	}
	if e := st.entry("example.com/shared"); e == nil || e.Vary != nil {
		t.Errorf("/shared isn't a plain entry: %+v", e)
	}
}

// TestCacheVaryByKeys checks that length prefixes keep crafted values from
// forging another combination's key.
func TestCacheVaryByKeys(t *testing.T) {
	sc := &siteCache{}
	key := func(vals ...string) string {
		var k Cache
		for i := 0; i+1 < len(vals); i += 2 {
			k.Context(vals[i], vals[i+1])
		}
		buf := []byte("h/p")
		p := &cacheReq{k: &k, buf: &buf}
		p.primary = sc.keyOf(buf)
		return strings.Clone(sc.varyKey(httptest.NewRequest("GET", "/p", nil), p, []string{"@a", "@b"}).s)
	}
	seen := map[string][]string{}
	for _, vals := range [][]string{
		{"a", "1\x00@\x01b\x012", "b", "3"},
		{"a", "1", "b", "2\x00@\x01b\x013"},
		{"a", "1", "b", "2"},
		{"a", "1,2"},
		{"a", "1", "b", ",2"},
		{"b", "1"},
		{"a", "1"},
		{},
	} {
		k := key(vals...)
		if prev, ok := seen[k]; ok {
			t.Errorf("%q and %q share the key %q", prev, vals, k)
		}
		seen[k] = vals
	}
	if key("a", "") != key() {
		t.Error("an empty value isn't the same variant as no value")
	}
}

func TestCacheVaryByNoCDNHeaders(t *testing.T) {
	page := func(vary bool) func(*Ctx, *Writer) error {
		return func(c *Ctx, w *Writer) error {
			o := CacheOptions{MaxAge: time.Minute, Tags: []string{"t"}, ETag: `"v1"`}
			if vary {
				o.VaryBy = []string{"user"}
			}
			c.Cache().Set(o)
			w.Raw("x")
			return nil
		}
	}
	routes := []Route{{Pattern: "/", File: "i.lyr", Render: page(true)}, {Pattern: "/shared", File: "s.lyr", Render: page(false)}}
	hp := &headerProvider{}
	for _, p := range []CacheProvider{plainProvider{}, hp} {
		s := testSite(t, routes, WithCache(p))
		r := get(s.Handler(), "/")
		h := r.Header()
		if h.Get("Cdn-Cache-Control") != "" || h.Get("Cache-Tag") != "" || h.Get("Surrogate-Control") != "" || h.Get("Etag") != `"v1"` || h.Get("Cache-Control") != "no-cache" {
			t.Errorf("%T: %v", p, h)
		}
		if r := get(s.Handler(), "/shared"); r.Header().Get("Cdn-Cache-Control") == "" && r.Header().Get("Surrogate-Control") == "" {
			t.Errorf("%T without VaryBy: no CDN headers: %v", p, r.Header())
		}
	}
	if hp.calls.Load() != 1 {
		t.Errorf("WriteCacheHeaders called %d times, want 1 (the page without VaryBy)", hp.calls.Load())
	}
	// With a store: the stored entry has no CDN headers either.
	var n atomic.Int32
	h, _, st := contextSite(t, []Route{{Pattern: "/", File: "i.lyr", Render: userPage(&n, CacheOptions{MaxAge: time.Hour, VaryBy: []string{"user"}, Tags: []string{"t"}})}})
	if r := get(h, "/", "X-User", "ann"); r.Header().Get("X-Lyr-Cache") != "MISS" || r.Header().Get("Cache-Tag") != "" {
		t.Errorf("store: %v (keys %q)", r.Header(), st.keys())
	}
}

func TestCacheVaryByRouteRules(t *testing.T) {
	var n atomic.Int32
	h, _, st := contextSite(t, []Route{{Pattern: "/account/[tab]", File: "a.lyr", Render: userPage(&n, CacheOptions{})}},
		WithRouteRules(RouteRule{Pattern: "/account/[...rest]", Options: CacheOptions{MaxAge: time.Hour, VaryBy: []string{"user"}}}))
	for i, s := range []struct{ user, state, body string }{
		{"ann", "MISS", "hi ann #1"},
		{"bob", "MISS", "hi bob #2"},
		{"ann", "HIT", "hi ann #1"},
	} {
		if r := get(h, "/account/home", "X-User", s.user); r.Header().Get("X-Lyr-Cache") != s.state || r.Body.String() != s.body {
			t.Errorf("step %d: %q %q", i, r.Header().Get("X-Lyr-Cache"), r.Body)
		}
	}
	if m := st.entry("example.com/account/home"); m == nil || !slices.Equal(m.Vary, []string{"@user"}) {
		t.Errorf("marker %+v", m)
	}
}

func TestCacheVaryByIslands(t *testing.T) {
	var n atomic.Int32
	isl := []Island{NewIsland(cachedIslandID, "src/components/Cached.lyr", func(c *Ctx, w *Writer, p cachedIslandProps, s Slots) error {
		i := n.Add(1)
		c.Cache().Set(CacheOptions{MaxAge: time.Hour, VaryBy: []string{"user"}})
		w.Raw(LocalsOf[cacheLocals](c).User + " #")
		w.Int(int64(i))
		return nil
	})}
	st := newFakeStore()
	t.Setenv("LYR_STATIC", "")
	t.Setenv("LYR_DEV", "")
	s := NewSite(nil, isl, WithStaticDir(t.TempDir()), WithCache(st), withLocalsCache())
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r, l := EnsureLocals[cacheLocals](r)
		l.User = r.Header.Get("X-User")
		l.Cache.Context("user", l.User)
		s.Handler().ServeHTTP(w, r)
	})
	u := islandPrefix + s.keys.buildID + "/" + cachedIslandID + "?p=" + sealerOf(s.keys).Seal(cachedIslandID, s.keys.buildID, []byte(`{"N":1}`))
	for i, step := range []struct{ user, state, body string }{
		{"ann", "MISS", "ann #1"},
		{"bob", "MISS", "bob #2"},
		{"ann", "HIT", "ann #1"},
		{"bob", "HIT", "bob #2"},
	} {
		r := get(h, u, "X-User", step.user)
		if r.Header().Get("X-Lyr-Cache") != step.state || r.Body.String() != step.body || r.Header().Get("Cache-Control") != "private, no-store" {
			t.Errorf("step %d: %q %q %v", i, r.Header().Get("X-Lyr-Cache"), r.Body, r.Header())
		}
	}
}

func TestCacheVaryByRevalidation(t *testing.T) {
	clock := useClock(t)
	var n atomic.Int32
	h, s, st := contextSite(t, []Route{{Pattern: "/", File: "i.lyr", Render: userPage(&n, CacheOptions{MaxAge: 10 * time.Second, SWR: time.Minute, VaryBy: []string{"user"}})}})
	get(h, "/", "X-User", "ann")
	get(h, "/", "X-User", "bob")
	clock.add(20 * time.Second)
	if r := get(h, "/", "X-User", "ann"); r.Header().Get("X-Lyr-Cache") != "STALE" || r.Body.String() != "hi ann #1" {
		t.Fatalf("stale: %q %q", r.Header().Get("X-Lyr-Cache"), r.Body)
	}
	waitFlights(t, s)
	// The background render stored ann's page under ann's variant only.
	if r := get(h, "/", "X-User", "ann"); r.Header().Get("X-Lyr-Cache") != "HIT" || r.Body.String() != "hi ann #3" {
		t.Errorf("ann after revalidation: %q %q (keys %q)", r.Header().Get("X-Lyr-Cache"), r.Body, st.keys())
	}
	if r := get(h, "/", "X-User", "bob"); r.Header().Get("X-Lyr-Cache") != "STALE" || r.Body.String() != "hi bob #2" {
		t.Errorf("bob: %q %q", r.Header().Get("X-Lyr-Cache"), r.Body)
	}
	waitFlights(t, s)
	if r := get(h, "/", "X-User", "bob"); r.Body.String() != "hi bob #4" {
		t.Errorf("bob after revalidation: %q", r.Body)
	}
	if len(st.keys()) != 3 {
		t.Errorf("keys %q, want the marker and two variants", st.keys())
	}
}

func TestCacheVaryByInvalidate(t *testing.T) {
	var n atomic.Int32
	h, s, st := contextSite(t, []Route{{Pattern: "/d", File: "d.lyr", Render: userPage(&n, CacheOptions{MaxAge: time.Hour, Tags: []string{"dash"}, VaryBy: []string{"user"}})}})
	for _, opt := range []InvalidateOptions{{Tags: []string{"dash"}}, {Path: "/d"}} {
		get(h, "/d", "X-User", "ann")
		get(h, "/d", "X-User", "bob")
		if len(st.keys()) != 3 {
			t.Fatalf("keys %q", st.keys())
		}
		if err := s.InvalidateCache(t.Context(), opt); err != nil {
			t.Fatal(err)
		}
		if len(st.keys()) != 0 {
			t.Errorf("%+v left %q", opt, st.keys())
		}
		if r := get(h, "/d", "X-User", "ann"); r.Header().Get("X-Lyr-Cache") != "MISS" {
			t.Errorf("%+v: ann %q", opt, r.Header().Get("X-Lyr-Cache"))
		}
		s.InvalidateCache(t.Context(), opt)
	}
}

func TestCacheContextLate(t *testing.T) {
	logs := captureLogSync(t)
	warnedLateContext.Store(false)
	t.Cleanup(func() { warnedLateContext.Store(false) })
	var n atomic.Int32
	h, _, st := contextSite(t, []Route{
		// Sets a value the middleware didn't: the lookup used another key.
		{Pattern: "/late", File: "l.lyr", Render: counted(&n, func(c *Ctx) error {
			c.Cache().Set(CacheOptions{MaxAge: time.Hour, VaryBy: []string{"user"}})
			c.Cache().Context("user", "page")
			return nil
		}, "late")},
		// Repeats the middleware's value: fine.
		{Pattern: "/same", File: "s.lyr", Render: counted(&n, func(c *Ctx) error {
			c.Cache().Set(CacheOptions{MaxAge: time.Hour, VaryBy: []string{"user"}})
			c.Cache().Context("user", "ann")
			return nil
		}, "same")},
	})
	for range 2 {
		if r := get(h, "/late", "X-User", "ann"); r.Header().Get("X-Lyr-Cache") != "" {
			t.Errorf("late: %q", r.Header().Get("X-Lyr-Cache"))
		}
	}
	get(h, "/same", "X-User", "ann")
	if r := get(h, "/same", "X-User", "ann"); r.Header().Get("X-Lyr-Cache") != "HIT" {
		t.Errorf("same value: %q", r.Header().Get("X-Lyr-Cache"))
	}
	if st.entry("example.com/late") != nil || strings.Count(logs.String(), "after the cache lookup") != 1 {
		t.Errorf("keys %q, log %q", st.keys(), logs.String())
	}
}

// TestCacheVaryByConditional: a response without VaryBy for a URL whose
// live marker has context names is stored under them too (a missing value
// counts as ""), so it never replaces the per-user copies with one for
// everyone.
func TestCacheVaryByConditional(t *testing.T) {
	var n atomic.Int32
	h, _, st := contextSite(t, []Route{{Pattern: "/", File: "i.lyr", Render: func(c *Ctx, w *Writer) error {
		i := n.Add(1)
		u := LocalsOf[cacheLocals](c).User
		o := CacheOptions{MaxAge: time.Minute}
		if u != "" {
			o.VaryBy = []string{"user"} // only for signed-in users
		}
		c.Cache().Set(o)
		w.Raw("hi " + u + " #")
		w.Int(int64(i))
		return nil
	}}})
	steps := []struct{ user, state, body string }{
		{"ann", "MISS", "hi ann #1"},
		{"", "MISS", "hi  #2"},
		{"ann", "HIT", "hi ann #1"},
		{"", "HIT", "hi  #2"},
		{"bob", "MISS", "hi bob #3"},
	}
	for i, step := range steps {
		var hdr []string
		if step.user != "" {
			hdr = []string{"X-User", step.user}
		}
		r := get(h, "/", hdr...)
		if r.Header().Get("X-Lyr-Cache") != step.state || r.Body.String() != step.body {
			t.Errorf("step %d (%q): %q %q, keys %q", i, step.user, r.Header().Get("X-Lyr-Cache"), r.Body, st.keys())
		}
	}
	if m := st.entry("example.com/"); m == nil || !slices.Equal(m.Vary, []string{"@user"}) {
		t.Errorf("marker %+v", m)
	}
}

// TestCacheContextConcurrentChild: a server:concurrent child repeating
// the request's value, or setting "" for a key never set, isn't late.
func TestCacheContextConcurrentChild(t *testing.T) {
	var n atomic.Int32
	h, _, _ := contextSite(t, []Route{{Pattern: "/", File: "i.lyr", Render: func(c *Ctx, w *Writer) error {
		n.Add(1)
		c.Cache().Set(CacheOptions{MaxAge: time.Minute, VaryBy: []string{"user"}})
		c.Cache().Context("user", "ann")
		c.Cache().Context("never", "")
		return Concurrent(c, w, func(c *Ctx, w *Writer, i int, s Slots) error {
			c.Cache().Context("user", "ann")
			c.Cache().Context("never", "")
			w.Int(int64(i))
			return nil
		}, 0, Slots{})
	}}})
	get(h, "/", "X-User", "ann")
	if r := get(h, "/", "X-User", "ann"); r.Header().Get("X-Lyr-Cache") != "HIT" || n.Load() != 1 {
		t.Errorf("second: %q, %d renders", r.Header().Get("X-Lyr-Cache"), n.Load())
	}
	var k Cache
	k.flags |= cacheLooked
	k.Context("absent", "")
	if k.flags&cacheLate != 0 {
		t.Error(`Context(key, "") for a key never set is late`)
	}
}

// TestCacheVaryByPageRevalidationLocals: a page refresh that sets its
// options on its locals' Cache field writes the refresh's own handle.
func TestCacheVaryByPageRevalidationLocals(t *testing.T) {
	clock := useClock(t)
	var n atomic.Int32
	h, s, st := contextSite(t, []Route{{Pattern: "/", File: "i.lyr", Render: func(c *Ctx, w *Writer) error {
		i := n.Add(1)
		l := LocalsOf[cacheLocals](c)
		l.Cache.Set(CacheOptions{MaxAge: 10 * time.Second, SWR: time.Minute, VaryBy: []string{"user"}})
		w.Raw("hi " + l.User + " #")
		w.Int(int64(i))
		return nil
	}}})
	get(h, "/", "X-User", "ann")
	get(h, "/", "X-User", "bob")
	clock.add(20 * time.Second)
	// The trigger's Locals, captured by a wrapping middleware: the refresh
	// renders on a copy, so they never get its options.
	var trigger *cacheLocals
	capture := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r, trigger = EnsureLocals[cacheLocals](r)
		h.ServeHTTP(w, r)
	})
	if r := get(capture, "/", "X-User", "ann"); r.Header().Get("X-Lyr-Cache") != "STALE" {
		t.Fatalf("trigger: %q", r.Header().Get("X-Lyr-Cache"))
	}
	waitFlights(t, s)
	if o := trigger.Cache.Options(); o.MaxAge != 0 || o.VaryBy != nil {
		t.Errorf("the refresh wrote the trigger's Locals: %+v", o)
	}
	if r := get(h, "/", "X-User", "bob"); r.Body.String() != "hi bob #2" {
		t.Errorf("bob after ann's refresh: %q %q (keys %q)", r.Header().Get("X-Lyr-Cache"), r.Body, st.keys())
	}
	waitFlights(t, s)
	if r := get(h, "/", "X-User", "ann"); r.Body.String() != "hi ann #3" {
		t.Errorf("ann after refresh: %q", r.Body)
	}
}
