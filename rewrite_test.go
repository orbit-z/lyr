package lyr

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Rewrites (PLAN.md §4j).

type rwLocals struct {
	Route   RouteInfo
	Cache   Cache
	Session Session[sessData]
	Seen    []string // the paths the inner middleware saw
	User    string
}

func rwOptions() []Option {
	return []Option{
		WithLocalsRoute(func(l *rwLocals) *RouteInfo { return &l.Route }),
		WithLocalsCache(func(l *rwLocals) *Cache { return &l.Cache }),
	}
}

// rwTarget renders "tgt <path> q=<query> p=<param>" for /tgt/[...rest].
func rwTarget(c *Ctx, w *Writer) error {
	w.Raw("tgt " + c.URL.Path + " q=" + c.URL.RawQuery + " p=" + c.Param("rest") + " uri=" + c.Request.RequestURI)
	return nil
}

// rewriteTo is a page that returns ctx.Rewrite(to(c)), after setting a
// status, a header and a cookie that the rewrite must drop or keep.
func rewriteTo(to func(c *Ctx) string) func(*Ctx, *Writer) error {
	return func(c *Ctx, w *Writer) error {
		c.SetStatus(http.StatusTeapot)
		c.Header().Set("X-Src", "1")
		c.Header().Add("Set-Cookie", "src=1")
		w.Raw("source output")
		return c.Rewrite(to(c))
	}
}

func rwRoutes(extra ...Route) []Route {
	return append([]Route{
		{Pattern: "/tgt/[...rest]", File: "src/pages/tgt/[...rest].lyr", Render: rwTarget},
		{Pattern: "/created", File: "src/pages/created.lyr", Render: func(c *Ctx, w *Writer) error {
			c.SetStatus(http.StatusCreated)
			w.Raw("created")
			return nil
		}},
		{Pattern: "/es-cu/[...path]", File: "src/pages/es-cu/[...path].lyr", Render: rewriteTo(func(c *Ctx) string {
			return "/tgt/" + c.Param("path") + "?lang=es"
		})},
		{Pattern: "/to-created", File: "src/pages/to-created.lyr", Render: rewriteTo(func(*Ctx) string { return "/created" })},
		{Pattern: "/to-404", File: "src/pages/to-404.lyr", Render: rewriteTo(func(*Ctx) string { return "/404" })},
		{Pattern: "/to-nope", File: "src/pages/to-nope.lyr", Render: rewriteTo(func(*Ctx) string { return "/nope" })},
		{Pattern: "/to-slash", File: "src/pages/to-slash.lyr", Render: rewriteTo(func(*Ctx) string { return "/created/" })},
		{Pattern: "/to-bad", File: "src/pages/to-bad.lyr", Render: rewriteTo(func(*Ctx) string { return "https://evil.example/x" })},
		{Pattern: "/to-admin", File: "src/pages/to-admin.lyr", Render: rewriteTo(func(*Ctx) string { return "/admin/panel" })},
		{Pattern: "/admin/panel", File: "src/pages/admin/panel.lyr", Render: func(c *Ctx, w *Writer) error {
			w.Raw("admin")
			return nil
		}},
		{Pattern: "/loop-a", File: "src/pages/loop-a.lyr", Render: rewriteTo(func(*Ctx) string { return "/loop-b" })},
		{Pattern: "/loop-b", File: "src/pages/loop-b.lyr", Render: rewriteTo(func(*Ctx) string { return "/loop-a" })},
		{Pattern: "/chain-1", File: "src/pages/chain-1.lyr", Render: rewriteTo(func(*Ctx) string { return "/chain-2" })},
		{Pattern: "/chain-2", File: "src/pages/chain-2.lyr", Render: rewriteTo(func(*Ctx) string { return "/created" })},
		{Pattern: "/to-fail", File: "src/pages/to-fail.lyr", Render: rewriteTo(func(*Ctx) string { return "/fail" })},
		{Pattern: "/fail", File: "src/pages/fail.lyr", Render: func(c *Ctx, w *Writer) error {
			return errors.New("boom")
		}},
		{Pattern: "/concurrent", File: "src/pages/concurrent.lyr", Render: func(c *Ctx, w *Writer) error {
			return cComp(c, w, cProps{Kids: []cProps{{Name: "a"}, {Name: "b", Err: c.Rewrite("/created")}}, Mark: true}, Slots{})
		}},
		{Pattern: "404", File: "src/pages/404.lyr", Render: func(c *Ctx, w *Writer) error {
			w.Raw("not found " + c.URL.Path)
			return nil
		}},
	}, extra...)
}

// rwChain is the test chain: outer (counted, once per request), then
// site.Rewrites when marker is set, then inner (counted, records the path
// in the locals, and guards /admin), then site.Handler().
type rwChain struct {
	outer, inner atomic.Int32
	locals       []*rwLocals
}

func (ch *rwChain) handler(s *Site, marker bool) http.Handler {
	outer := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ch.outer.Add(1)
			next.ServeHTTP(w, r)
		})
	}
	inner := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ch.inner.Add(1)
			r, l := EnsureLocals[rwLocals](r)
			l.Seen = append(l.Seen, r.URL.Path)
			ch.locals = append(ch.locals, l)
			if strings.HasPrefix(r.URL.Path, "/admin") && r.Header.Get("X-User") == "" {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
	mws := []func(http.Handler) http.Handler{outer}
	if marker {
		mws = append(mws, s.Rewrites)
	}
	mws = append(mws, inner)
	return Sequence(mws...)(s.Handler())
}

func TestRewritePages(t *testing.T) {
	for _, marker := range []bool{true, false} {
		t.Run("marker="+strconv.FormatBool(marker), func(t *testing.T) {
			s := testSite(t, rwRoutes(), rwOptions()...)
			var ch rwChain
			h := ch.handler(s, marker)
			tests := []struct {
				target   string
				code     int
				body     string
				innerRun int32 // inner middleware runs with the marker
				route    string
			}{
				// Params and query come from the target; the request's own
				// query is dropped; RequestURI keeps the original.
				{"/es-cu/a/b?x=1", 200, "tgt /tgt/a/b q=lang=es p=a/b uri=/es-cu/a/b?x=1", 2, "/tgt/[...rest]"},
				// The status is the target's; the source's 418 is dropped.
				{"/to-created", 201, "created", 2, "/created"},
				{"/to-404", 404, "not found /404", 2, "/404"},
				// Without the marker, site.Handler's next (404.lyr) answers.
				{"/to-nope", 404, "not found /nope", 2, "/404"},
				// The canonical form, never a 308.
				{"/to-slash", 201, "created", 2, "/created"},
				// Two chained rewrites are fine.
				{"/chain-1", 201, "created", 3, "/created"},
				{"/concurrent", 201, "created", 2, "/created"},
			}
			for _, tt := range tests {
				ch.outer.Store(0)
				ch.inner.Store(0)
				ch.locals = nil
				rec := get(h, tt.target)
				if rec.Code != tt.code || rec.Body.String() != tt.body {
					t.Errorf("%s: %d %q, want %d %q", tt.target, rec.Code, rec.Body, tt.code, tt.body)
				}
				if rec.Header().Get("X-Src") != "" {
					t.Errorf("%s: the source's header was kept", tt.target)
				}
				if got := rec.Header().Values("Set-Cookie"); tt.target != "/concurrent" && (len(got) == 0 || got[0] != "src=1") {
					t.Errorf("%s: Set-Cookie %q, want the source's", tt.target, got)
				}
				want := tt.innerRun
				if !marker {
					want = 1
				}
				if ch.outer.Load() != 1 || ch.inner.Load() != want {
					t.Errorf("%s: outer ran %d, inner %d times, want 1, %d", tt.target, ch.outer.Load(), ch.inner.Load(), want)
				}
				l := ch.locals[0]
				for _, o := range ch.locals {
					if o != l {
						t.Errorf("%s: the re-run got other locals", tt.target)
					}
				}
				if l.Route.Pattern != tt.route {
					t.Errorf("%s: Locals.Route %q, want the target's %q", tt.target, l.Route.Pattern, tt.route)
				}
				if marker && len(l.Seen) > 1 && l.Seen[len(l.Seen)-1] == l.Seen[0] {
					t.Errorf("%s: the re-run saw %q", tt.target, l.Seen)
				}
				if got := s.Match(httptest.NewRequest("GET", tt.target, nil)); got.Kind != RoutePage {
					t.Errorf("%s: Match changed: %v", tt.target, got.Kind)
				}
			}
		})
	}
}

func TestRewriteAuthOnTarget(t *testing.T) {
	s := testSite(t, rwRoutes(), rwOptions()...)
	var ch rwChain
	// With the marker, path-gated middleware sees the target.
	if rec := get(ch.handler(s, true), "/to-admin"); rec.Code != http.StatusForbidden {
		t.Errorf("marker: %d %q, want 403", rec.Code, rec.Body)
	}
	if rec := get(ch.handler(testSite(t, rwRoutes(), rwOptions()...), true), "/to-admin", "X-User", "ann"); rec.Code != 200 || rec.Body.String() != "admin" {
		t.Errorf("signed in: %d %q", rec.Code, rec.Body)
	}
}

func TestRewriteLoop(t *testing.T) {
	logs := captureLogSync(t)
	s := testSite(t, rwRoutes(), rwOptions()...)
	var ch rwChain
	for _, marker := range []bool{true, false} {
		if marker {
			s = testSite(t, rwRoutes(), rwOptions()...)
		}
		rec := get(ch.handler(s, marker), "/loop-a")
		if rec.Code != http.StatusLoopDetected || rec.Body.Len() != 0 {
			t.Errorf("marker %v: %d %q, want an empty 508", marker, rec.Code, rec.Body)
		}
	}
	if !strings.Contains(logs.String(), "lyr: GET /loop-a: rewrite loop: /loop-a → /loop-b → /loop-a → /loop-b") {
		t.Errorf("log %q", logs.String())
	}
	t.Setenv("LYR_DEV", "1")
	s = testSite(t, rwRoutes(), rwOptions()...)
	rec := get(s.Handler(), "/loop-a")
	if rec.Code != http.StatusLoopDetected || !strings.Contains(rec.Body.String(), "/loop-a → /loop-b → /loop-a → /loop-b") {
		t.Errorf("dev: %d %q", rec.Code, rec.Body)
	}
}

func TestRewriteErrors(t *testing.T) {
	logs := captureLogSync(t)
	var got []string
	s := testSite(t, rwRoutes(), append(rwOptions(), WithErrorHandler(func(w http.ResponseWriter, r *http.Request, err error) bool {
		if errors.As(err, new(*RewriteError)) {
			t.Errorf("a RewriteError reached the error handler")
		}
		got = append(got, r.URL.Path+" "+r.RequestURI+" "+err.Error())
		return false
	}))...)
	h := s.Handler()
	// A scheme and host are ignored, as in Astro: "https://evil.example/x"
	// is /x, which no page matches.
	if rec := get(h, "/to-bad"); rec.Code != 404 {
		t.Errorf("URL target: %d", rec.Code)
	}
	if rec := get(h, "/to-fail"); rec.Code != 500 {
		t.Errorf("failing target: %d", rec.Code)
	}
	want := []string{
		"/fail /to-fail boom",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("error handler got\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	_ = logs
	// A deferred island can't rewrite.
	var handled error
	s2 := testSite(t, rwRoutes(), WithErrorHandler(func(w http.ResponseWriter, r *http.Request, err error) bool {
		handled = err
		return false
	}))
	rec := httptest.NewRecorder()
	s2.islandFailed(rec, httptest.NewRequest("GET", "/_lyr/island/x", nil), ErrorAt("x.lyr:1:1", &RewriteError{Target: "/x"}), nil)
	if rec.Code != 500 || handled != errRewriteInIsland {
		t.Errorf("island: %d %v", rec.Code, handled)
	}
	// ServeError serves a rewrite's target.
	rec = httptest.NewRecorder()
	s2.ServeError(rec, httptest.NewRequest("GET", "/custom", nil), &RewriteError{Target: "/created"})
	if rec.Code != 201 || rec.Body.String() != "created" {
		t.Errorf("ServeError: %d %q", rec.Code, rec.Body)
	}
}

func TestRewriteTarget(t *testing.T) {
	tests := []struct {
		target                        string
		path, rawPath, query, escaped string
		force                         bool
	}{
		{"/plain", "/plain", "", "", "/plain", false},
		{"/a%2Fb?x=1&y=%20", "/a/b", "/a%2Fb", "x=1&y=%20", "/a%2Fb", false},
		{"/caf%C3%A9", "/café", "", "", "/caf%C3%A9", false},
		{"/x#frag", "/x", "", "", "/x", false},
		{"/x?q#frag", "/x", "", "q", "/x", false},
		{"/x?", "/x", "", "", "/x", true},
		{"/a b", "/a b", "", "", "/a%20b", false},
		// Never a panic (PLAN.md §4j): any target becomes a local path.
		{"https://x.example/y?q=1#f", "/y", "", "q=1", "/y", false},
		{"https://x.example", "/", "", "", "/", false},
		{"http://x.example?q", "/", "", "q", "/", false},
		{"mailto:a@b", "/a@b", "", "", "/a@b", false},
		{"//x.example/y", "//x.example/y", "", "", "//x.example/y", false},
		{"x", "/x", "", "", "/x", false},
		{"", "/orig", "", "keep=no", "/orig", false},
		// Relative targets resolve against the request's URL (/orig), as
		// Astro's new URL(target, url) does.
		{"?q=1", "/orig", "", "q=1", "/orig", false},
		{"./x?y", "/x", "", "y", "/x", false},
		{"/a/./b/../c/", "/a/c/", "", "", "/a/c/", false},
		{"/a/%2E%2e/admin", "/admin", "", "", "/admin", false},
		{"/../../x", "/x", "", "", "/x", false},
		{"/a\nb\x00", "/a\nb\x00", "", "", "/a%0Ab%00", false},
		{"/%zz", "/%zz", "", "", "/%25zz", false},
		{"/a%2", "/a%2", "", "", "/a%252", false},
	}
	r := httptest.NewRequest("POST", "/orig?keep=no", strings.NewReader("body"))
	r.Header.Set("X-Test", "1")
	for _, tt := range tests {
		r2 := RewriteRequest(r, tt.target)
		u := r2.URL
		if u.Path != tt.path || u.RawPath != tt.rawPath || u.RawQuery != tt.query || u.EscapedPath() != tt.escaped || u.ForceQuery != tt.force {
			t.Errorf("%q: path %q raw %q query %q escaped %q force %v", tt.target, u.Path, u.RawPath, u.RawQuery, u.EscapedPath(), u.ForceQuery)
		}
		if r2.RequestURI != "/orig?keep=no" || r2.Method != "POST" || r2.Header.Get("X-Test") != "1" || r2.Body != r.Body || r2.Context() != r.Context() {
			t.Errorf("%q: request fields not kept", tt.target)
		}
	}
	if r.URL.Path != "/orig" || r.URL.RawQuery != "keep=no" {
		t.Error("RewriteRequest changed the original")
	}
}

// TestRewriteRequestNoMatch: a lenient target no route matches gets the
// normal 404, through the chain.
func TestRewriteRequestNoMatch(t *testing.T) {
	s := testSite(t, rwRoutes(), rwOptions()...)
	for _, target := range []string{"/a\x00b", "/%zz", "//evil.example", "https://evil.example/nope"} {
		mw := func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				next.ServeHTTP(w, RewriteRequest(r, target))
			})
		}
		if rec := get(mw(s.Handler()), "/"); rec.Code != 404 {
			t.Errorf("RewriteRequest(%q): %d", target, rec.Code)
		}
	}
}

func TestRewriteRequestMiddleware(t *testing.T) {
	s := testSite(t, rwRoutes(), rwOptions()...)
	var l *rwLocals
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r, l = EnsureLocals[rwLocals](r)
		if strings.HasPrefix(r.URL.Path, "/account") {
			r = RewriteRequest(r, "/tgt/login?next=account")
		}
		s.Handler().ServeHTTP(w, r)
	})
	rec := get(h, "/account/x")
	if rec.Code != 200 || rec.Body.String() != "tgt /tgt/login q=next=account p=login uri=/account/x" || l.Route.Pattern != "/tgt/[...rest]" {
		t.Errorf("%d %q %q", rec.Code, rec.Body, l.Route.Pattern)
	}
	// Middleware rewrites don't count toward the loop limit.
	mw := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/m" {
				r = RewriteRequest(r, "/chain-1")
			}
			next.ServeHTTP(w, r)
		})
	}
	s2 := testSite(t, rwRoutes(), rwOptions()...)
	if rec := get(Sequence(s2.Rewrites, mw)(s2.Handler()), "/m"); rec.Code != 201 {
		t.Errorf("middleware rewrite then two page rewrites: %d %q", rec.Code, rec.Body)
	}
}

func TestRewriteMarker(t *testing.T) {
	s := testSite(t, rwRoutes())
	h := http.Handler(new(notFoundHandler))
	if got, ok := s.Rewrites(h).(*rewritesMarker); !ok || got.next != h || s.rewrites != h {
		t.Error("Rewrites doesn't wrap next, or doesn't re-enter it")
	}
	defer func() {
		if recover() == nil {
			t.Error("a second Rewrites didn't panic")
		}
	}()
	s.Rewrites(h)
}

// TestRewriteTrailingSlashAlways: the target is put in the "always" form.
func TestRewriteTrailingSlashAlways(t *testing.T) {
	s := testSite(t, rwRoutes(), WithTrailingSlash(TrailingSlashAlways))
	if rec := get(s.Handler(), "/to-created/"); rec.Code != 201 {
		t.Errorf("%d %q", rec.Code, rec.Body)
	}
}

// ---- the route cache ----

func TestRewriteCache(t *testing.T) {
	var src, tgt atomic.Int32
	routes := []Route{
		{Pattern: "/src", File: "src.lyr", Render: func(c *Ctx, w *Writer) error {
			src.Add(1)
			c.Cache().Set(CacheOptions{MaxAge: time.Hour, Tags: []string{"src"}}) // dropped
			return c.Rewrite("/tgt?b=2")
		}},
		{Pattern: "/tgt", File: "tgt.lyr", Render: counted(&tgt, func(c *Ctx) error {
			if t := c.Cache().Options().Tags; len(t) > 0 {
				return errors.New("the source's options reached the target")
			}
			c.Cache().Set(CacheOptions{MaxAge: time.Hour})
			return nil
		}, "tgt")},
	}
	s, st := cacheSite(t, routes, rwOptions()...)
	var ch rwChain
	h := ch.handler(s, true)
	for i, want := range []string{"MISS", "HIT"} {
		rec := get(h, "/src?a=1")
		if rec.Code != 200 || rec.Body.String() != "tgt #1" || rec.Header().Get("X-Lyr-Cache") != want {
			t.Errorf("request %d: %d %q %q", i, rec.Code, rec.Body, rec.Header().Get("X-Lyr-Cache"))
		}
	}
	if keys := st.keys(); strings.Join(keys, " ") != "example.com/tgt?b=2" {
		t.Errorf("stored keys %q, want only the target's", keys)
	}
	if src.Load() != 2 || tgt.Load() != 1 {
		t.Errorf("source rendered %d, target %d times", src.Load(), tgt.Load())
	}
	// A direct request for the target is the same entry.
	if rec := get(h, "/tgt?b=2"); rec.Header().Get("X-Lyr-Cache") != "HIT" {
		t.Errorf("direct: %q", rec.Header().Get("X-Lyr-Cache"))
	}
}

func TestRewriteCacheDisable(t *testing.T) {
	var n atomic.Int32
	routes := []Route{
		{Pattern: "/src", File: "src.lyr", Render: func(c *Ctx, w *Writer) error {
			c.Cache().Set(CacheOptions{MaxAge: time.Hour}) // enables the handle again
			return c.Rewrite("/tgt")
		}},
		{Pattern: "/tgt", File: "tgt.lyr", Render: counted(&n, setOpts(CacheOptions{MaxAge: time.Hour}), "tgt")},
	}
	for _, marker := range []bool{true, false} {
		s, st := cacheSite(t, routes, rwOptions()...)
		disable := func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				r, l := EnsureLocals[rwLocals](r)
				if r.Header.Get("X-User") != "" {
					l.Cache.Disable()
				}
				next.ServeHTTP(w, r)
			})
		}
		// Outside the marker: Disable runs once, before the source's lookup.
		var mws []func(http.Handler) http.Handler
		mws = append(mws, disable)
		if marker {
			mws = append(mws, s.Rewrites)
		}
		h := Sequence(mws...)(s.Handler())
		for range 2 {
			rec := get(h, "/src", "X-User", "ann")
			if rec.Code != 200 || rec.Header().Get("X-Lyr-Cache") != "" {
				t.Errorf("marker %v: %d %q", marker, rec.Code, rec.Header().Get("X-Lyr-Cache"))
			}
		}
		if len(st.keys()) != 0 {
			t.Errorf("marker %v: stored %q although disabled", marker, st.keys())
		}
		// Anonymous: cached as usual.
		get(h, "/src")
		if rec := get(h, "/src"); rec.Header().Get("X-Lyr-Cache") != "HIT" {
			t.Errorf("marker %v: anonymous %q", marker, rec.Header().Get("X-Lyr-Cache"))
		}
	}
}

func TestRewriteCacheContext(t *testing.T) {
	logs := captureLogSync(t)
	warnedLateContext.Store(false)
	t.Cleanup(func() { warnedLateContext.Store(false) })
	var n atomic.Int32
	routes := []Route{
		{Pattern: "/src", File: "src.lyr", Render: func(c *Ctx, w *Writer) error { return c.Rewrite("/tgt") }},
		{Pattern: "/tgt", File: "tgt.lyr", Render: userPage(&n, CacheOptions{MaxAge: time.Hour, VaryBy: []string{"user", "pass"}})},
	}
	s, st := cacheSite(t, routes, WithLocalsCache(func(l *cacheLocals) *Cache { return &l.Cache }))
	// Inside the marker: Context runs again for the target, with a new
	// "pass" value, before the target's lookup.
	ctxMW := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r, l := EnsureLocals[cacheLocals](r)
			pass := "1"
			if rewriteOf(r.Context()) != nil {
				pass = "2"
			}
			l.User = r.Header.Get("X-User")
			l.Cache.Context("user", l.User)
			l.Cache.Context("pass", pass)
			next.ServeHTTP(w, r)
		})
	}
	h := Sequence(s.Rewrites, ctxMW)(s.Handler())
	for i, want := range []string{"MISS", "HIT"} {
		rec := get(h, "/src", "X-User", "ann")
		if rec.Body.String() != "hi ann #1" || rec.Header().Get("X-Lyr-Cache") != want {
			t.Errorf("ann %d: %q %q", i, rec.Body, rec.Header().Get("X-Lyr-Cache"))
		}
	}
	if rec := get(h, "/src", "X-User", "bob"); rec.Body.String() != "hi bob #2" {
		t.Errorf("bob: %q", rec.Body)
	}
	var variant string
	for _, k := range st.keys() {
		if strings.Contains(k, "ann") {
			variant = k
		}
	}
	if !strings.Contains(variant, "\x04pass\x012") {
		t.Errorf("the ann variant %q isn't keyed by the re-run's pass value", variant)
	}
	if strings.Contains(logs.String(), "after the cache lookup") {
		t.Errorf("the re-run's Context was taken as late: %q", logs.String())
	}
	// A page's late Context still keeps its response out of the cache.
	routes2 := []Route{{Pattern: "/late", File: "late.lyr", Render: counted(&n, func(c *Ctx) error {
		c.Cache().Set(CacheOptions{MaxAge: time.Hour})
		c.Cache().Context("user", "changed")
		return nil
	}, "late")}}
	s2, st2 := cacheSite(t, routes2, WithLocalsCache(func(l *cacheLocals) *Cache { return &l.Cache }))
	get(Sequence(s2.Rewrites, ctxMW)(s2.Handler()), "/late", "X-User", "ann")
	if len(st2.keys()) != 0 {
		t.Errorf("late Context stored: %q", st2.keys())
	}
}

// ---- sessions ----

func TestRewriteSessions(t *testing.T) {
	src := Route{Pattern: "/src", File: "src.lyr", Render: func(c *Ctx, w *Writer) error {
		d, err := SessionOf[sessData](c).Data()
		if err != nil {
			return err
		}
		d.Cart = append(d.Cart, "src")
		return c.Rewrite("/tgt")
	}}
	tgtRoute := func(add bool) Route {
		return Route{Pattern: "/tgt", File: "tgt.lyr", Render: func(c *Ctx, w *Writer) error {
			LocalsOf[sessLocals](c) // binds the Locals' Session field first
			d, err := SessionOf[sessData](c).Data()
			if err != nil {
				return err
			}
			if add {
				d.Cart = append(d.Cart, "tgt")
			}
			w.Raw("cart " + strings.Join(d.Cart, ","))
			return nil
		}}
	}
	for _, tt := range []struct {
		name      string
		sessions  bool // site.Sessions inside the marker
		add       bool // the target changes the session too
		wantCart  string
		wantStore string
		wantSets  int32
	}{
		{"page-only", false, false, "cart src", `{"cart":["src"]}`, 1},
		{"page-only, target changes", false, true, "cart src,tgt", `{"cart":["src","tgt"]}`, 2},
		{"site.Sessions", true, false, "cart src", `{"cart":["src"]}`, 1},
		{"site.Sessions, target changes", true, true, "cart src,tgt", `{"cart":["src","tgt"]}`, 2},
	} {
		t.Run(tt.name, func(t *testing.T) {
			store := newFakeSessions()
			s := sessSite(t, store, SessionOptions{}, []Route{src, tgtRoute(tt.add)})
			mws := []func(http.Handler) http.Handler{s.Rewrites}
			if tt.sessions {
				mws = append(mws, s.Sessions)
			}
			h := Sequence(mws...)(s.Handler())
			rec := reqWith(h, "/src", "")
			if rec.Code != 200 || rec.Body.String() != tt.wantCart {
				t.Fatalf("%d %q", rec.Code, rec.Body)
			}
			id := setCookieID(t, rec)
			if id == "" || len(store.ids()) != 1 || store.val(id) != tt.wantStore {
				t.Errorf("cookie %q, store %q = %q", id, store.ids(), store.val(id))
			}
			if c := store.calls(); c[1] != tt.wantSets {
				t.Errorf("%d store writes, want %d", c[1], tt.wantSets)
			}
			// The next request sees it.
			rec = reqWith(h, "/tgt", id)
			if !strings.HasPrefix(rec.Body.String(), tt.wantCart) {
				t.Errorf("next request: %q", rec.Body)
			}
		})
	}
	// Without site.Sessions or the marker, with an existing session: the
	// source's change is saved once and the target reads it.
	store := newFakeSessions()
	store.put(sid1, `{"cart":["old"]}`)
	s := sessSite(t, store, SessionOptions{}, []Route{src, tgtRoute(false)})
	rec := reqWith(s.Handler(), "/src", sid1)
	if rec.Body.String() != "cart old,src" || store.val(sid1) != `{"cart":["old","src"]}` || store.calls()[1] != 1 {
		t.Errorf("%q %q %v", rec.Body, store.val(sid1), store.calls())
	}
}

// TestRewriteSessionCacheHit: the source's session change is saved when the
// target is served from the route cache.
func TestRewriteSessionCacheHit(t *testing.T) {
	store := newFakeSessions()
	store.put(sid1, `{"userId":1}`)
	var n atomic.Int32
	routes := []Route{
		{Pattern: "/src", File: "src.lyr", Render: func(c *Ctx, w *Writer) error {
			d, err := SessionOf[sessData](c).Data()
			if err != nil {
				return err
			}
			d.Flash = "seen"
			return c.Rewrite("/tgt")
		}},
		{Pattern: "/tgt", File: "tgt.lyr", Render: counted(&n, setOpts(CacheOptions{MaxAge: time.Hour}), "tgt")},
	}
	cs := newFakeStore()
	s := sessSite(t, store, SessionOptions{}, routes, WithCache(cs))
	h := s.Handler()
	get(h, "/tgt")
	rec := reqWith(h, "/src", sid1)
	if rec.Header().Get("X-Lyr-Cache") != "HIT" || store.val(sid1) != `{"userId":1,"flash":"seen"}` {
		t.Errorf("%q %q", rec.Header().Get("X-Lyr-Cache"), store.val(sid1))
	}
}

// ---- cost ----

func TestRewriteAllocs(t *testing.T) {
	if testing.Short() || raceEnabled {
		t.Skip("allocation counts (not under -short or -race)")
	}
	t.Setenv("LYR_DEV", "")
	s := testSite(t, rwRoutes(Route{Pattern: "/r", File: "r.lyr", Render: func(c *Ctx, w *Writer) error {
		return c.Rewrite("/created")
	}}))
	h := s.Handler()
	fw := newFakeResponseWriter()
	r := httptest.NewRequest("GET", "/r", nil)
	allocs := testing.AllocsPerRun(1000, func() {
		fw.reset()
		h.ServeHTTP(fw, r)
	})
	if fw.status != 201 {
		t.Fatalf("status %d", fw.status)
	}
	// The RewriteError, the request copy (with its URL and context), and
	// the target's Content-Length.
	if allocs > 3 {
		t.Errorf("a rewrite: %v allocs, want ≤ 3", allocs)
	}
	t.Logf("a rewrite: %v allocs", allocs)
}

func BenchmarkRewrite(b *testing.B) {
	b.Setenv("LYR_DEV", "")
	s := NewSite(rwRoutes(Route{Pattern: "/r", File: "r.lyr", Render: func(c *Ctx, w *Writer) error {
		return c.Rewrite("/created")
	}}), nil, WithStaticDir(b.TempDir()))
	h := s.Handler()
	for _, target := range []string{"/created", "/r"} {
		b.Run(strings.TrimPrefix(target, "/"), func(b *testing.B) {
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

var _ = bytes.MinRead

// ---- review fixes ----

// TestRewriteCookiesKept (R1): a page's own Set-Cookie is appended to the
// cookies already on the response, never replacing them: the session's,
// a rewritten source page's, and middleware's.
func TestRewriteCookiesKept(t *testing.T) {
	store := newFakeSessions()
	s := sessSite(t, store, SessionOptions{}, []Route{
		{Pattern: "/plain", File: "plain.lyr", Render: func(c *Ctx, w *Writer) error {
			d, err := SessionOf[sessData](c).Data()
			if err != nil {
				return err
			}
			d.Cart = append(d.Cart, "p")
			c.Header().Add("Set-Cookie", "mine=1")
			w.Raw("p")
			return nil
		}},
		{Pattern: "/src", File: "src.lyr", Render: func(c *Ctx, w *Writer) error {
			c.Header().Add("Set-Cookie", "src=1")
			return c.Rewrite("/tgt")
		}},
		{Pattern: "/tgt", File: "tgt.lyr", Render: func(c *Ctx, w *Writer) error {
			c.Header().Add("Set-Cookie", "tgt=1")
			w.Raw("t")
			return nil
		}},
	})
	has := func(rec *httptest.ResponseRecorder, prefixes ...string) bool {
		got := rec.Header()["Set-Cookie"]
		for _, p := range prefixes {
			found := false
			for _, v := range got {
				found = found || strings.HasPrefix(v, p)
			}
			if !found {
				return false
			}
		}
		return len(got) == len(prefixes)
	}
	if rec := reqWith(s.Handler(), "/plain", ""); !has(rec, "lyr-session=", "mine=1") {
		t.Errorf("session + page cookie: %q", rec.Header()["Set-Cookie"])
	}
	if rec := get(s.Handler(), "/src"); !has(rec, "src=1", "tgt=1") {
		t.Errorf("rewrite: %q", rec.Header()["Set-Cookie"])
	}
	mw := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Set-Cookie", "mw=1")
		s.Handler().ServeHTTP(w, r)
	})
	if rec := get(mw, "/tgt"); !has(rec, "mw=1", "tgt=1") {
		t.Errorf("middleware + page cookie: %q", rec.Header()["Set-Cookie"])
	}
	// The cached path too (the response isn't stored, but is written).
	sc, _ := cacheSite(t, []Route{{Pattern: "/tgt", File: "tgt.lyr", Render: func(c *Ctx, w *Writer) error {
		c.Cache().Set(CacheOptions{MaxAge: time.Hour})
		c.Header().Add("Set-Cookie", "tgt=1")
		return nil
	}}})
	mw2 := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Set-Cookie", "mw=1")
		sc.Handler().ServeHTTP(w, r)
	})
	if rec := get(mw2, "/tgt"); !has(rec, "mw=1", "tgt=1") {
		t.Errorf("cached path: %q", rec.Header()["Set-Cookie"])
	}
}

// TestRewriteRouteReset (R2): a rewrite to a path no page matches leaves
// the Locals' RouteInfo empty, not the source page's.
func TestRewriteRouteReset(t *testing.T) {
	s := testSite(t, rwRoutes(Route{Pattern: "/to-app", File: "to-app.lyr", Render: rewriteTo(func(*Ctx) string { return "/app" })}), rwOptions()...)
	mux := http.NewServeMux()
	var seen RouteInfo
	mux.HandleFunc("/app", func(w http.ResponseWriter, r *http.Request) {
		seen = LocalsFromRequest[rwLocals](r).Route
		w.Write([]byte("app"))
	})
	mux.Handle("/", s.Handler())
	attach := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r, _ = EnsureLocals[rwLocals](r)
			next.ServeHTTP(w, r)
		})
	}
	rec := get(Sequence(attach, s.Rewrites)(mux), "/to-app")
	if rec.Body.String() != "app" || seen.Kind != RouteNone || seen.Pattern != "" {
		t.Errorf("%q, route %v %q", rec.Body, seen.Kind, seen.Pattern)
	}
}

// TestRewriteFromNotFoundPage (R3): a 404 page rendered for a page's
// ErrNotFound, or for ctx.Rewrite("/404"), rewrites through the user's
// next (site.Middleware's), like any page.
func TestRewriteFromNotFoundPage(t *testing.T) {
	routes := rwRoutes(
		Route{Pattern: "/missing", File: "missing.lyr", Render: func(c *Ctx, w *Writer) error { return ErrNotFound }},
		Route{Pattern: "/to-404-page", File: "to-404-page.lyr", Render: func(c *Ctx, w *Writer) error { return c.Rewrite("/404") }},
	)
	for i := range routes {
		if routes[i].Pattern == "404" {
			routes[i].Render = func(c *Ctx, w *Writer) error {
				if rewriteOf(c.Request.Context()) == nil || c.URL.Path == "/404" {
					return c.Rewrite("/app")
				}
				w.Raw("not found " + c.URL.Path)
				return nil
			}
		}
	}
	s := testSite(t, routes, rwOptions()...)
	mux := http.NewServeMux()
	mux.HandleFunc("/app", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("app")) })
	mux.Handle("/", s.NotFound())
	h := s.Middleware(mux)
	for _, path := range []string{"/missing", "/to-404-page"} {
		if rec := get(h, path); rec.Body.String() != "app" {
			t.Errorf("%s: %d %q", path, rec.Code, rec.Body)
		}
	}
}

// TestRewriteCacheMiddlewareOptions: options middleware outside
// site.Rewrites set before the source's lookup hold for the target; the
// source page's own options are dropped.
func TestRewriteCacheMiddlewareOptions(t *testing.T) {
	var n atomic.Int32
	routes := []Route{
		{Pattern: "/src", File: "src.lyr", Render: func(c *Ctx, w *Writer) error {
			c.Cache().Set(CacheOptions{Tags: []string{"src"}})
			return c.Rewrite("/tgt")
		}},
		{Pattern: "/tgt", File: "tgt.lyr", Render: counted(&n, func(c *Ctx) error {
			if o := c.Cache().Options(); o.MaxAge != time.Hour || len(o.Tags) != 1 || o.Tags[0] != "mw" {
				return errors.New("target options: " + o.MaxAge.String() + " " + strings.Join(o.Tags, ","))
			}
			return nil
		}, "tgt")},
	}
	s, st := cacheSite(t, routes, rwOptions()...)
	mw := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r, l := EnsureLocals[rwLocals](r)
			l.Cache.Set(CacheOptions{MaxAge: time.Hour, Tags: []string{"mw"}})
			next.ServeHTTP(w, r)
		})
	}
	h := Sequence(mw, s.Rewrites)(s.Handler())
	for i, want := range []string{"MISS", "HIT"} {
		if rec := get(h, "/src"); rec.Code != 200 || rec.Header().Get("X-Lyr-Cache") != want {
			t.Errorf("request %d: %d %q %q", i, rec.Code, rec.Header().Get("X-Lyr-Cache"), rec.Body)
		}
	}
	if e := st.entry("example.com/tgt"); e == nil || len(e.Tags) != 1 || e.Tags[0] != "mw" {
		t.Errorf("stored %+v", e)
	}
}

// TestRewriteCacheLateKept (C5): a source page's late Context change
// holds for the target: it's neither looked up nor stored.
func TestRewriteCacheLateKept(t *testing.T) {
	captureLogSync(t)
	warnedLateContext.Store(false)
	t.Cleanup(func() { warnedLateContext.Store(false) })
	var n atomic.Int32
	routes := []Route{
		{Pattern: "/src", File: "src.lyr", Render: func(c *Ctx, w *Writer) error {
			c.Cache().Context("user", "changed")
			return c.Rewrite("/tgt")
		}},
		{Pattern: "/tgt", File: "tgt.lyr", Render: counted(&n, setOpts(CacheOptions{MaxAge: time.Hour, VaryBy: []string{"user"}}), "tgt")},
	}
	s, st := cacheSite(t, routes, rwOptions()...)
	mw := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r, l := EnsureLocals[rwLocals](r)
			l.Cache.Context("user", "ann")
			next.ServeHTTP(w, r)
		})
	}
	h := Sequence(mw, s.Rewrites)(s.Handler())
	get(h, "/tgt") // ann's copy of /tgt
	if rec := get(h, "/src"); rec.Header().Get("X-Lyr-Cache") != "" || rec.Body.String() != "tgt #2" {
		t.Errorf("late source: %q %q", rec.Header().Get("X-Lyr-Cache"), rec.Body)
	}
	if len(st.keys()) != 2 {
		t.Errorf("keys %q", st.keys())
	}
}

// TestNotFoundPageKeepsSession (S6): the 404 page rendered for a page's
// ErrNotFound shares the page's session: one session, one cookie.
func TestNotFoundPageKeepsSession(t *testing.T) {
	store := newFakeSessions()
	s := sessSite(t, store, SessionOptions{}, []Route{
		{Pattern: "/missing", File: "missing.lyr", Render: func(c *Ctx, w *Writer) error {
			d, err := SessionOf[sessData](c).Data()
			if err != nil {
				return err
			}
			d.Cart = append(d.Cart, "page")
			return ErrNotFound
		}},
		{Pattern: "404", File: "404.lyr", Render: func(c *Ctx, w *Writer) error {
			d, err := SessionOf[sessData](c).Data()
			if err != nil {
				return err
			}
			d.Cart = append(d.Cart, "404")
			w.Raw(strings.Join(d.Cart, ","))
			return nil
		}},
	})
	rec := reqWith(s.Handler(), "/missing", "")
	if rec.Code != 404 || rec.Body.String() != "page,404" || len(rec.Header()["Set-Cookie"]) != 1 {
		t.Fatalf("%d %q, cookies %q", rec.Code, rec.Body, rec.Header()["Set-Cookie"])
	}
	id := setCookieID(t, rec)
	if got := store.val(id); got != `{"cart":["page","404"]}` || len(store.ids()) != 1 {
		t.Errorf("stored %s, %d sessions", got, len(store.ids()))
	}
}

// TestRewriteCacheInnerOptionsDropped: options set by middleware inside
// site.Rewrites (which re-runs for the target) aren't restored for the
// target: a personal target without Set isn't stored because the
// middleware set options for the source's path. Without site.Rewrites,
// every middleware ran once, outside the re-entry point, so its options
// hold for the target.
func TestRewriteCacheInnerOptionsDropped(t *testing.T) {
	for _, marker := range []bool{true, false} {
		var n atomic.Int32
		routes := []Route{
			{Pattern: "/src", File: "src.lyr", Render: func(c *Ctx, w *Writer) error { return c.Rewrite("/tgt") }},
			{Pattern: "/tgt", File: "tgt.lyr", Render: counted(&n, nil, "personal")},
		}
		s, st := cacheSite(t, routes, rwOptions()...)
		mw := func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				r, l := EnsureLocals[rwLocals](r)
				if r.URL.Path == "/src" {
					l.Cache.Set(CacheOptions{MaxAge: time.Hour})
				}
				next.ServeHTTP(w, r)
			})
		}
		var h http.Handler
		if marker {
			h = Sequence(s.Rewrites, mw)(s.Handler())
		} else {
			h = Sequence(mw)(s.Handler())
		}
		get(h, "/src")
		stored := st.entry("example.com/tgt") != nil
		if stored != !marker {
			t.Errorf("marker=%v: target stored %v, keys %q", marker, stored, st.keys())
		}
		if marker {
			if rec := get(h, "/tgt"); rec.Header().Get("X-Lyr-Cache") == "HIT" {
				t.Errorf("direct GET /tgt is a HIT of the source's options")
			}
		}
	}
}

// TestRewriteRelative: relative targets resolve against the current URL
// (Astro's new URL(target, url)), for RewriteRequest and ctx.Rewrite, and
// the result is matched (and seen by the middleware inside site.Rewrites)
// exactly as a direct request for it.
func TestRewriteRelative(t *testing.T) {
	base := httptest.NewRequest("GET", "/a/b?keep=1", nil)
	for _, tt := range []struct{ target, path, query string }{
		{"x", "/a/x", ""},
		{"../x", "/x", ""},
		{"../../../x", "/x", ""},
		{"?q", "/a/b", "q"},
		{"", "/a/b", "keep=1"},
		{"c/./d/..", "/a/c/", ""},
	} {
		u := RewriteRequest(base, tt.target).URL
		if u.Path != tt.path || u.RawQuery != tt.query {
			t.Errorf("RewriteRequest(/a/b, %q): %q ? %q, want %q ? %q", tt.target, u.Path, u.RawQuery, tt.path, tt.query)
		}
	}
	s := testSite(t, rwRoutes(
		Route{Pattern: "/dir/[name]", File: "src/pages/dir/[name].lyr", Render: func(c *Ctx, w *Writer) error {
			switch c.Param("name") {
			case "rel":
				return c.Rewrite("../admin/panel")
			case "url":
				return c.Rewrite("//evil.example/admin/panel")
			}
			return c.Rewrite("../tgt/x?q=1")
		}},
	), rwOptions()...)
	var ch rwChain
	h := ch.handler(s, true)
	if rec := get(h, "/dir/go"); rec.Code != 200 || !strings.HasPrefix(rec.Body.String(), "tgt /tgt/x q=q=1 p=x") {
		t.Errorf("relative ctx.Rewrite: %d %q", rec.Code, rec.Body)
	}
	// The inner middleware sees the resolved path: the /admin guard runs.
	if rec := get(h, "/dir/rel"); rec.Code != http.StatusForbidden {
		t.Errorf("relative rewrite into /admin: %d %q (guard bypassed)", rec.Code, rec.Body)
	}
	if rec := get(h, "/dir/rel", "X-User", "ann"); rec.Body.String() != "admin" {
		t.Errorf("relative rewrite into /admin, signed in: %d %q", rec.Code, rec.Body)
	}
	// ctx.Rewrite never fails on its target, as in Astro: "//x" is the
	// path "//x" (a 404 here), and request data such as "/" + a param
	// starting with "/", or a slug with a ':', never makes a 500.
	if rec := get(h, "/dir/url"); rec.Code != 404 || rec.Body.String() != "not found //evil.example/admin/panel" {
		t.Errorf(`ctx.Rewrite("//…"): %d %q`, rec.Code, rec.Body)
	}
	s2 := testSite(t, rwRoutes(
		Route{Pattern: "/r/[...path]", File: "src/pages/r/[...path].lyr", Render: func(c *Ctx, w *Writer) error {
			return c.Rewrite("/" + c.Param("path"))
		}},
		Route{Pattern: "/s/[slug]", File: "src/pages/s/[slug].lyr", Render: func(c *Ctx, w *Writer) error {
			return c.Rewrite(c.Param("slug"))
		}},
	), rwOptions()...)
	var ch2 rwChain
	h2 := ch2.handler(s2, true)
	for _, path := range []string{"/r/%2Fevil.com", "/s/a:b", "/s/.%2F%2Fevil.com"} {
		if rec := get(h2, path); rec.Code != 404 {
			t.Errorf("%s: %d %q", path, rec.Code, rec.Body)
		}
	}
}

// TestRewriteFromNotFoundPageCache: a rewrite from the 404 page resets
// the Locals' Cache handle like a page's: the options of a page that
// returned ErrNotFound, and of middleware inside site.Rewrites, are
// dropped; a Disable before the lookup holds; without site.Rewrites,
// middleware options set before any lookup hold.
func TestRewriteFromNotFoundPageCache(t *testing.T) {
	routes := func(n *atomic.Int32) []Route {
		return []Route{
			{Pattern: "/x", File: "x.lyr", Render: func(c *Ctx, w *Writer) error {
				c.Cache().Set(CacheOptions{MaxAge: time.Hour, Tags: []string{"x"}})
				return ErrNotFound
			}},
			{Pattern: "/tgt", File: "tgt.lyr", Render: counted(n, nil, "personal")},
			{Pattern: "404", File: "404.lyr", Render: func(c *Ctx, w *Writer) error { return c.Rewrite("/tgt") }},
		}
	}
	for _, tc := range []struct {
		name   string
		marker bool
		path   string
		set    string // the path middleware calls Set for
		off    bool   // middleware calls Disable
		stored bool
	}{
		{"page ErrNotFound, marker", true, "/x", "", false, false},
		{"page ErrNotFound, no marker", false, "/x", "", false, false},
		{"inner Set, no match", true, "/nope", "/nope", false, false},
		{"Set, no match, no marker", false, "/nope", "/nope", false, true},
		{"Disable, no match, no marker", false, "/nope", "/nope", true, false},
		{"Disable before the page's lookup", false, "/x", "/x", true, false},
	} {
		var n atomic.Int32
		s, st := cacheSite(t, routes(&n), rwOptions()...)
		mw := func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				r, l := EnsureLocals[rwLocals](r)
				if r.URL.Path == tc.set {
					l.Cache.Set(CacheOptions{MaxAge: time.Hour})
					if tc.off {
						l.Cache.Disable()
					}
				}
				next.ServeHTTP(w, r)
			})
		}
		h := Sequence(mw)(s.Handler())
		if tc.marker {
			h = Sequence(s.Rewrites, mw)(s.Handler())
		}
		rec := get(h, tc.path)
		if rec.Body.String() != "personal #1" {
			t.Errorf("%s: %d %q", tc.name, rec.Code, rec.Body)
		}
		if stored := st.entry("example.com/tgt") != nil; stored != tc.stored {
			t.Errorf("%s: target stored %v, want %v (keys %q)", tc.name, stored, tc.stored, st.keys())
		}
	}
}

// appRewriteSite is a site whose /api app handler rewrites to /tset with
// ServeError; /tset is a page that sets its own options, and outer
// middleware sets options too when the Locals have a Cache field.
func appRewriteSite(t *testing.T, marker, fields bool, handled, rendered *atomic.Int32) (http.Handler, *fakeStore) {
	t.Helper()
	routes := []Route{{Pattern: "/tset", File: "tset.lyr", Render: counted(rendered, setOpts(CacheOptions{MaxAge: time.Hour, Tags: []string{"t"}}), "tset")}}
	var opts []Option
	if fields {
		opts = rwOptions()
	}
	s, st := cacheSite(t, routes, opts...)
	mux := http.NewServeMux()
	mux.HandleFunc("/api", func(w http.ResponseWriter, r *http.Request) {
		handled.Add(1)
		s.ServeError(w, r, &RewriteError{Target: "/tset"})
	})
	outer := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if fields {
				var l *rwLocals
				r, l = EnsureLocals[rwLocals](r)
				l.Cache.Set(CacheOptions{MaxAge: time.Hour, Tags: []string{"mw"}})
			}
			next.ServeHTTP(w, r)
		})
	}
	if marker {
		return Sequence(outer, s.Rewrites)(s.Middleware(mux)), st
	}
	return Sequence(outer)(s.Middleware(mux)), st
}

// An app handler that rewrites with ServeError: the target is stored
// under its own key, never under the source's, and the next request runs
// the handler again.
func TestServeErrorRewriteNotStored(t *testing.T) {
	for _, tc := range []struct {
		name           string
		marker, fields bool
	}{
		{"locals", false, true},
		{"locals+marker", true, true},
		{"no field", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var handled, rendered atomic.Int32
			h, st := appRewriteSite(t, tc.marker, tc.fields, &handled, &rendered)
			for i := 1; i <= 2; i++ {
				rec := get(h, "/api")
				if rec.Code != http.StatusOK || !strings.HasPrefix(rec.Body.String(), "tset") {
					t.Fatalf("GET %d: %d %q", i, rec.Code, rec.Body)
				}
				if got := handled.Load(); got != int32(i) {
					t.Fatalf("GET %d: the handler ran %d times (a stored /api copy was served)", i, got)
				}
			}
			if st.entry("example.com/api") != nil {
				t.Errorf("the target's response is stored under the source's key: %q", st.keys())
			}
			if st.entry("example.com/tset") == nil {
				t.Errorf("the target isn't stored under its own key: %q", st.keys())
			}
			if got := rendered.Load(); got != 1 {
				t.Errorf("the target rendered %d times, want 1 (the second is a HIT on its key)", got)
			}
		})
	}
}

// The options outer middleware set hold for the target of an app
// handler's ServeError rewrite, with or without site.Rewrites.
func TestServeErrorRewriteKeepsMiddlewareOptions(t *testing.T) {
	for _, marker := range []bool{false, true} {
		var handled, rendered atomic.Int32
		h, st := appRewriteSite(t, marker, true, &handled, &rendered)
		get(h, "/api")
		e := st.entry("example.com/tset")
		if e == nil {
			t.Fatalf("marker=%v: target not stored: %q", marker, st.keys())
		}
		if got := strings.Join(e.Tags, " "); got != "mw t" {
			t.Errorf("marker=%v: target tags %q, want %q", marker, got, "mw t")
		}
	}
}

// wrapWriter wraps a ResponseWriter the way gzip or logging middleware
// does, with Unwrap.
type wrapWriter struct{ http.ResponseWriter }

func (w wrapWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// within runs f, failing the test if it takes longer than two seconds
// (a deadlock).
func within(t *testing.T, f func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		f()
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("deadlock: the request didn't finish")
	}
}

// An app handler's ServeError rewrite through a wrapped writer: the
// target is stored under its own key, nothing under the source's.
func TestServeErrorRewriteWrappedWriter(t *testing.T) {
	var rendered atomic.Int32
	routes := []Route{{Pattern: "/tset", File: "tset.lyr", Render: counted(&rendered, setOpts(CacheOptions{MaxAge: time.Hour, Tags: []string{"t"}}), "tset")}}
	s, st := cacheSite(t, routes)
	mux := http.NewServeMux()
	mux.HandleFunc("/api", func(w http.ResponseWriter, r *http.Request) {
		s.ServeError(wrapWriter{w}, r, &RewriteError{Target: "/tset"})
	})
	h := s.Middleware(mux)
	if rec := get(h, "/api"); !strings.HasPrefix(rec.Body.String(), "tset") {
		t.Errorf("got %d %q", rec.Code, rec.Body)
	}
	if st.entry("example.com/api") != nil || st.entry("example.com/tset") == nil {
		t.Errorf("keys %q, want the target's only", st.keys())
	}
}

// An app handler that rewrites to its own URL (or through a loop of
// them) on a caching site: the second pass answers, a loop gets the 508,
// and nothing is stored.
func TestServeErrorSelfRewriteCache(t *testing.T) {
	t.Run("self", func(t *testing.T) {
		s, st := cacheSite(t, nil)
		var calls atomic.Int32
		mux := http.NewServeMux()
		mux.HandleFunc("/api", func(w http.ResponseWriter, r *http.Request) {
			if calls.Add(1) == 1 {
				s.ServeError(w, r, &RewriteError{Target: "/api"})
				return
			}
			w.Write([]byte("ok"))
		})
		h := s.Rewrites(s.Middleware(mux))
		var rec *httptest.ResponseRecorder
		within(t, func() { rec = get(h, "/api") })
		if rec.Code != http.StatusOK || rec.Body.String() != "ok" {
			t.Errorf("got %d %q, want the second pass's 200 \"ok\"", rec.Code, rec.Body)
		}
		if keys := st.keys(); len(keys) != 0 {
			t.Errorf("an app route is stored: %q", keys)
		}
	})
	t.Run("loop", func(t *testing.T) {
		s, st := cacheSite(t, nil)
		mux := http.NewServeMux()
		mux.HandleFunc("/a", func(w http.ResponseWriter, r *http.Request) {
			s.ServeError(w, r, &RewriteError{Target: "/b"})
		})
		mux.HandleFunc("/b", func(w http.ResponseWriter, r *http.Request) {
			s.ServeError(w, r, &RewriteError{Target: "/a"})
		})
		h := s.Rewrites(s.Middleware(mux))
		captureLogSync(t)
		var rec *httptest.ResponseRecorder
		within(t, func() { rec = get(h, "/a") })
		if rec.Code != http.StatusLoopDetected {
			t.Errorf("got %d, want 508", rec.Code)
		}
		if keys := st.keys(); len(keys) != 0 {
			t.Errorf("the loop's 508 is stored: %q", keys)
		}
		within(t, func() { rec = get(h, "/b") })
		if rec.Code != http.StatusLoopDetected || rec.Header().Get("X-Lyr-Cache") == "HIT" {
			t.Errorf("GET /b: %d %q, want a fresh 508", rec.Code, rec.Header().Get("X-Lyr-Cache"))
		}
	})
}

// A HEAD for an app URL that rewrites to a cached page with ServeError
// gets the target's Content-Length and no body.
func TestServeErrorRewriteHeadLength(t *testing.T) {
	var rendered atomic.Int32
	routes := []Route{{Pattern: "/tset", File: "tset.lyr", Render: counted(&rendered, setOpts(CacheOptions{MaxAge: time.Hour, Tags: []string{"t"}}), "tset")}}
	s, _ := cacheSite(t, routes)
	mux := http.NewServeMux()
	mux.HandleFunc("/api", func(w http.ResponseWriter, r *http.Request) {
		s.ServeError(w, r, &RewriteError{Target: "/tset"})
	})
	h := s.Middleware(mux)
	want := strconv.Itoa(get(h, "/api").Body.Len())
	for i := range 2 { // a HEAD is never looked up: both render the target
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodHead, "/api", nil))
		if got := rec.Header().Get("Content-Length"); got != want || rec.Body.Len() != 0 {
			t.Errorf("HEAD %d: Content-Length %q body %q, want %q and no body", i, got, rec.Body, want)
		}
	}
}

// A page that set Content-Length and then redirects gets the redirect's
// own length on the cached path.
func TestCachedRedirectContentLength(t *testing.T) {
	s, _ := cacheSite(t, []Route{{Pattern: "/r", File: "r.lyr", Render: func(c *Ctx, w *Writer) error {
		c.Cache().Set(CacheOptions{MaxAge: time.Hour})
		c.Header().Set("Content-Length", "999")
		c.Header().Add("Set-Cookie", "a=1") // refused by the store: not stored
		return c.Redirect("/x", http.StatusFound)
	}}})
	captureLogSync(t)
	rec := get(s.Handler(), "/r")
	if got, want := rec.Header().Get("Content-Length"), strconv.Itoa(rec.Body.Len()); got != want {
		t.Errorf("Content-Length %q, want %q", got, want)
	}
}
