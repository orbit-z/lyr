package lyr

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Sessions and the route cache (PLAN.md §4i "Route caching"): caching is
// manual, like Astro's; only a response that sends the session cookie is
// never stored.

func TestSessionRouteCache(t *testing.T) {
	sessions := newFakeSessions()
	sessions.put(sid1, `{"userId":1}`)
	sessions.put(sid2, `{"userId":2}`)
	var n atomic.Int32
	page := func(pattern string, f func(c *Ctx, s *Session[sessData]) error) Route {
		return Route{Pattern: pattern, File: "src/pages" + pattern + ".lyr", Render: func(c *Ctx, w *Writer) error {
			i := n.Add(1)
			c.Cache().Set(CacheOptions{MaxAge: time.Hour, Tags: []string{"t"}})
			s := SessionOf[sessData](c)
			d, err := s.Data()
			if err != nil {
				return err
			}
			if f != nil {
				if err := f(c, s); err != nil {
					return err
				}
			}
			w.Int(d.UserID)
			w.Raw(" ")
			w.Int(int64(i))
			return nil
		}}
	}
	q := func(c *Ctx, name string) bool { return c.Request.URL.Query().Get(name) != "" }
	routes := []Route{
		page("/read", nil),
		page("/me", func(c *Ctx, s *Session[sessData]) error { c.Cache().Disable(); return nil }),
		page("/new", func(c *Ctx, s *Session[sessData]) error {
			if q(c, "c") {
				d, _ := s.Data()
				d.Flash = "x"
			}
			return nil
		}),
		page("/regen", func(c *Ctx, s *Session[sessData]) error { return s.Regenerate() }),
		page("/destroy", func(c *Ctx, s *Session[sessData]) error { s.Destroy(); return nil }),
		page("/change", func(c *Ctx, s *Session[sessData]) error {
			d, _ := s.Data()
			d.Flash = c.Request.URL.Query().Get("f")
			return nil
		}),
	}
	store := newFakeStore()
	s := sessSite(t, sessions, SessionOptions{}, routes, WithCache(store))
	h := s.Handler()
	state := func(r *httptest.ResponseRecorder) string { return r.Header().Get("X-Lyr-Cache") }

	// A page that reads the session is stored and served to everyone, a
	// cookie holder included: the body is the page's responsibility.
	if r := reqWith(h, "/read", ""); state(r) != "MISS" || r.Body.String() != "0 1" {
		t.Fatalf("read: %q %q", state(r), r.Body)
	}
	if m := store.entry("example.com/read"); m == nil || m.Vary != nil {
		t.Fatalf("entry %+v; keys %q", m, store.keys())
	}
	if r := reqWith(h, "/read", sid1); state(r) != "HIT" || r.Body.String() != "0 1" {
		t.Errorf("cookie holder: %q %q", state(r), r.Body)
	}
	// The first render's visitor decides the stored copy.
	store.Invalidate(t.Context(), InvalidateOptions{Tags: []string{"t"}})
	if r := reqWith(h, "/read", sid2); state(r) != "MISS" || r.Body.String() != "2 2" {
		t.Errorf("cookie holder miss: %q %q", state(r), r.Body)
	}
	if r := reqWith(h, "/read", ""); state(r) != "HIT" || r.Body.String() != "2 2" {
		t.Errorf("anonymous after a cookie holder: %q %q", state(r), r.Body)
	}

	// Disable keeps a personal page out.
	puts := store.puts.Load()
	for _, id := range []string{"", sid1, sid1} {
		if r := reqWith(h, "/me", id); state(r) != "" || r.Header().Get("Etag") != "" {
			t.Errorf("disabled (%q): %q %v", id, state(r), r.Header())
		}
	}
	if store.puts.Load() != puts {
		t.Error("disabled page stored")
	}

	// A response that sends the session cookie is never stored, and that
	// isn't warned about: a new session, Regenerate, Destroy.
	logs := captureLogSync(t)
	for _, c := range []struct{ target, id string }{
		{"/new?c=1", ""},
		{"/regen", sid1},
		{"/destroy", sid2},
	} {
		puts := store.puts.Load()
		r := reqWith(h, c.target, c.id)
		if len(r.Header().Values("Set-Cookie")) != 1 || state(r) != "" || store.puts.Load() != puts {
			t.Errorf("%s: %q %v, %d puts", c.target, state(r), r.Header(), store.puts.Load()-puts)
		}
	}
	if strings.Contains(logs.String(), "not cached") {
		t.Errorf("warned: %q", logs)
	}
	// The same page without a cookie to send is stored.
	if r := reqWith(h, "/new", ""); state(r) != "MISS" {
		t.Errorf("new without a change: %q", state(r))
	}

	// A save that changes the data but not the cookie (MaxAge 0) may be
	// stored.
	sessions.put(sid1, `{"userId":1}`)
	puts = store.puts.Load()
	r := reqWith(h, "/change?f=y", sid1)
	if r.Header().Get("Set-Cookie") != "" || state(r) != "MISS" || store.puts.Load() != puts+1 {
		t.Errorf("change: %q %v", state(r), r.Header())
	}
	if !strings.Contains(sessions.val(sid1), `"flash":"y"`) {
		t.Errorf("not saved: %s", sessions.val(sid1))
	}

	// Middleware that loads a stored session doesn't skip the lookup.
	sessions.put(sid2, `{"userId":2}`)
	mw := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, l := EnsureLocals[sessLocals](r)
			d, _ := l.Session.Data()
			l.User = d.UserID
			next.ServeHTTP(w, r)
		})
	}
	chain := s.Sessions(mw(h))
	if r := reqWith(chain, "/read", sid2); state(r) != "HIT" {
		t.Errorf("middleware signed in: %q", state(r))
	}
	// Middleware's Disable keeps a signed-in request out.
	off := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r, l := EnsureLocals[sessLocals](r)
			if d, _ := l.Session.Data(); d.UserID != 0 {
				l.Cache.Disable()
			}
			next.ServeHTTP(w, r)
		})
	}
	s2 := sessSite(t, sessions, SessionOptions{}, routes, WithCache(newFakeStore()), WithLocalsCache(func(l *sessLocals) *Cache { return &l.Cache }))
	chain2 := s2.Sessions(off(s2.Handler()))
	reqWith(chain2, "/read", "")
	if r := reqWith(chain2, "/read", sid2); state(r) != "" {
		t.Errorf("middleware Disable: %q", state(r))
	}
	if r := reqWith(chain2, "/read", ""); state(r) != "HIT" {
		t.Errorf("anonymous after middleware Disable: %q", state(r))
	}
}

func TestSessionRouteCacheHeaders(t *testing.T) {
	sessions := newFakeSessions()
	sessions.put(sid1, `{"userId":1}`)
	var change atomic.Bool
	render := func(c *Ctx, w *Writer) error {
		c.Cache().Set(CacheOptions{MaxAge: time.Hour, Tags: []string{"t"}})
		d, _ := SessionOf[sessData](c).Data()
		if change.Load() {
			d.Flash = "x"
		}
		return nil
	}
	hp := &headerProvider{}
	s := sessSite(t, sessions, SessionOptions{}, []Route{{Pattern: "/", File: "src/pages/index.lyr", Render: render}}, WithCache(hp))
	h := s.Handler()
	for _, id := range []string{"", sid1} {
		if r := reqWith(h, "/", id); r.Header().Get("Surrogate-Control") == "" {
			t.Errorf("reads the session (%q): %v", id, r.Header())
		}
	}
	calls := hp.calls.Load()
	// A new session's cookie: no CDN headers.
	change.Store(true)
	if r := reqWith(h, "/", ""); setCookieID(t, r) == "" || r.Header().Get("Surrogate-Control") != "" || hp.calls.Load() != calls {
		t.Errorf("new session: %v, %d calls", r.Header(), hp.calls.Load()-calls)
	}
	// Default (plain) provider: Astro's CDN headers, except with a cookie.
	change.Store(false)
	s2 := sessSite(t, sessions, SessionOptions{}, []Route{{Pattern: "/", File: "src/pages/index.lyr", Render: render}}, WithCache(plainProvider{}))
	if r := reqWith(s2.Handler(), "/", sid1); r.Header().Get("Cdn-Cache-Control") == "" || r.Header().Get("Cache-Tag") == "" {
		t.Errorf("plain: %v", r.Header())
	}
	change.Store(true)
	if r := reqWith(s2.Handler(), "/", ""); r.Header().Get("Cdn-Cache-Control") != "" || r.Header().Get("Cache-Tag") != "" {
		t.Errorf("plain new session: %v", r.Header())
	}
}

func TestSessionRouteCacheCoalescing(t *testing.T) {
	sessions := newFakeSessions()
	sessions.put(sid1, `{"userId":1}`)
	var n atomic.Int32
	release := make(chan struct{})
	started := make(chan struct{}, 4)
	s := sessSite(t, sessions, SessionOptions{}, []Route{{Pattern: "/", File: "src/pages/index.lyr", Render: func(c *Ctx, w *Writer) error {
		i := n.Add(1)
		c.Cache().Set(CacheOptions{MaxAge: time.Hour})
		d, _ := SessionOf[sessData](c).Data()
		if i == 1 {
			started <- struct{}{}
			<-release
		}
		w.Int(d.UserID)
		return nil
	}}}, WithCache(newFakeStore()))
	h := s.Handler()
	// Make the route known.
	cr, _ := s.router.find("/")
	cr.cache.stored.Store(true)
	waiting := make(chan struct{}, 1)
	testFlightWaitHook = func() { waiting <- struct{}{} }
	t.Cleanup(func() { testFlightWaitHook = nil })
	done := make(chan *httptest.ResponseRecorder, 2)
	go func() { done <- reqWith(h, "/", "") }()
	<-started
	go func() { done <- reqWith(h, "/", sid1) }()
	<-waiting // the cookie holder joined the anonymous render's flight
	close(release)
	r1, r2 := <-done, <-done
	bodies := []string{r1.Body.String(), r2.Body.String()}
	slices.Sort(bodies)
	if !slices.Equal(bodies, []string{"0", "0"}) || n.Load() != 1 {
		t.Errorf("bodies %q, %d renders", bodies, n.Load())
	}
}

// A background revalidation renders as an anonymous visitor and never
// saves, whoever triggered it.
func TestSessionRouteCacheRevalidation(t *testing.T) {
	clock := useClock(t)
	sessions := newFakeSessions()
	sessions.put(sid1, `{"userId":1}`)
	var n atomic.Int32
	s := sessSite(t, sessions, SessionOptions{}, []Route{{Pattern: "/", File: "src/pages/index.lyr", Render: func(c *Ctx, w *Writer) error {
		i := n.Add(1)
		c.Cache().Set(CacheOptions{MaxAge: 10 * time.Second, SWR: time.Minute})
		d, _ := SessionOf[sessData](c).Data()
		d.Flash = "x" // a change: never saved by a revalidation
		w.Int(d.UserID)
		w.Raw(" ")
		w.Int(int64(i))
		return nil
	}}}, WithCache(newFakeStore()))
	h := s.Handler()
	// The first render saves a new session, so it isn't stored; with a
	// cookie and no change it is.
	sessions.put(sid2, `{"flash":"x"}`)
	if r := reqWith(h, "/", sid2); r.Header().Get("X-Lyr-Cache") != "MISS" || r.Body.String() != "0 1" {
		t.Fatalf("first: %q %q", r.Header().Get("X-Lyr-Cache"), r.Body)
	}
	clock.add(20 * time.Second)
	sets := sessions.sets.Load()
	if r := reqWith(h, "/", sid1); r.Header().Get("X-Lyr-Cache") != "STALE" {
		t.Fatalf("stale: %q", r.Header().Get("X-Lyr-Cache"))
	}
	waitFlights(t, s)
	if r := reqWith(h, "/", ""); r.Header().Get("X-Lyr-Cache") != "HIT" || r.Body.String() != "0 2" {
		t.Errorf("revalidated: %q %q", r.Header().Get("X-Lyr-Cache"), r.Body)
	}
	if sessions.sets.Load() != sets {
		t.Errorf("revalidation saved: %d sets", sessions.sets.Load()-sets)
	}
}

func TestSessionRouteCacheIslands(t *testing.T) {
	sessions := newFakeSessions()
	sessions.put(sid1, `{"userId":1}`)
	var n atomic.Int32
	isl := []Island{NewIsland(cachedIslandID, "src/components/Cached.lyr", func(c *Ctx, w *Writer, p cachedIslandProps, s Slots) error {
		n.Add(1)
		c.Cache().Set(CacheOptions{MaxAge: time.Hour})
		d, err := SessionOf[sessData](c).Data()
		if err != nil {
			return err
		}
		w.Int(d.UserID)
		return nil
	})}
	t.Setenv("LYR_STATIC", "")
	t.Setenv("LYR_DEV", "")
	s := NewSite(nil, isl, WithStaticDir(t.TempDir()), WithCache(newFakeStore()), WithSessions(sessions, SessionOptions{}), withLocalsSess())
	h := s.Handler()
	u := islandPrefix + s.keys.buildID + "/" + cachedIslandID + "?p=" + sealerOf(s.keys).Seal(cachedIslandID, s.keys.buildID, []byte(`{"N":1}`))
	reqWith(h, u, "")
	for _, id := range []string{"", sid1} {
		if r := reqWith(h, u, id); r.Header().Get("X-Lyr-Cache") != "HIT" || r.Body.String() != "0" {
			t.Errorf("island (%q): %q %q", id, r.Header().Get("X-Lyr-Cache"), r.Body)
		}
	}
	if n.Load() != 1 {
		t.Errorf("%d renders", n.Load())
	}
}

// TestSessionRevalidationIdentity (PLAN.md §4i "Route caching"): a
// background refresh renders with a session state of its own, never
// saved, which the implicit session and its Locals' Session field share:
// an anonymous visitor's for a copy that doesn't vary by context, the
// triggering visitor's (read-only) for one that does. It never reaches
// the trigger's state or Locals.
func TestSessionRevalidationIdentity(t *testing.T) {
	for _, vary := range []bool{false, true} {
		clock := useClock(t)
		sessions := newFakeSessions()
		sessions.put(sid1, `{"userId":1}`)
		var n atomic.Int32
		s := sessSite(t, sessions, SessionOptions{}, []Route{{Pattern: "/", File: "src/pages/index.lyr", Render: func(c *Ctx, w *Writer) error {
			i := n.Add(1)
			o := CacheOptions{MaxAge: 10 * time.Second, SWR: time.Minute}
			if vary {
				o.VaryBy = []string{"user"}
			}
			c.Cache().Set(o)
			dl, err := LocalsOf[sessLocals](c).Session.Data()
			if err != nil {
				return err
			}
			d, err := SessionOf[sessData](c).Data()
			if err != nil {
				return err
			}
			if dl != d {
				t.Errorf("vary=%v: locals.Session and session differ in render %d", vary, i)
			}
			if i > 1 {
				d.Flash = strconv.Itoa(int(i)) // never saved by a refresh
			}
			w.Int(dl.UserID)
			w.Raw(" #")
			w.Int(int64(i))
			return nil
		}}}, WithCache(newFakeStore()), WithLocalsCache(func(l *sessLocals) *Cache { return &l.Cache }))
		mw := func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				l := LocalsFromRequest[sessLocals](r)
				d, _ := l.Session.Data()
				l.Cache.Context("user", strconv.FormatInt(d.UserID, 10))
				next.ServeHTTP(w, r)
			})
		}
		h := s.Sessions(mw(s.Handler()))
		if r := reqWith(h, "/", sid1); r.Header().Get("X-Lyr-Cache") != "MISS" || r.Body.String() != "1 #1" {
			t.Fatalf("vary=%v first: %q %q", vary, r.Header().Get("X-Lyr-Cache"), r.Body)
		}
		clock.add(20 * time.Second)
		sets := sessions.sets.Load()
		var trigger *sessLocals
		capture := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r, trigger = EnsureLocals[sessLocals](r)
			h.ServeHTTP(w, r)
		})
		if r := reqWith(capture, "/", sid1); r.Header().Get("X-Lyr-Cache") != "STALE" {
			t.Fatalf("vary=%v stale: %q", vary, r.Header().Get("X-Lyr-Cache"))
		}
		waitFlights(t, s)
		// The refresh rendered on a copy of the trigger's Locals.
		if o := trigger.Cache.Options(); o.MaxAge != 0 || o.VaryBy != nil {
			t.Errorf("vary=%v: the refresh wrote the trigger's Locals: %+v", vary, o)
		}
		if trigger.Session.st != nil {
			t.Errorf("vary=%v: the trigger's Session is bound after its request", vary)
		}
		want := "0 #2" // anonymous
		if vary {
			want = "1 #2" // the trigger's session, under its variant
		}
		if r := reqWith(h, "/", sid1); r.Header().Get("X-Lyr-Cache") != "HIT" || r.Body.String() != want {
			t.Errorf("vary=%v after refresh: %q %q, want %q", vary, r.Header().Get("X-Lyr-Cache"), r.Body, want)
		}
		if sessions.sets.Load() != sets || sessions.val(sid1) != `{"userId":1}` {
			t.Errorf("vary=%v: the refresh saved: %d sets, %s", vary, sessions.sets.Load()-sets, sessions.val(sid1))
		}
	}
}
