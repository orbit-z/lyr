package lyr

import (
	"context"
	"errors"
	"maps"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Session tests (PLAN.md §4i), with a fake store that counts calls.

type sessData struct {
	UserID int64    `json:"userId,omitempty"`
	Cart   []string `json:"cart,omitempty"`
	Flash  string   `json:"flash,omitempty"`
}

type sessLocals struct {
	Session Session[sessData]
	Cache   Cache
	User    int64
}

func withLocalsSess() Option {
	return WithLocalsSession(func(l *sessLocals) *Session[sessData] { return &l.Session })
}

// fakeSessions is a map-backed SessionStore counting its calls.
type fakeSessions struct {
	mu                 sync.Mutex
	m                  map[string][]byte
	ttls               map[string]time.Duration
	gets, sets, dels   atomic.Int32
	getErr, setErr     error
	delErr             error
	canceled           atomic.Int32 // Set or Delete calls whose context was done
	beforeGet, onWrite func(id string)
}

func newFakeSessions() *fakeSessions {
	return &fakeSessions{m: map[string][]byte{}, ttls: map[string]time.Duration{}}
}

func (f *fakeSessions) Get(ctx context.Context, id string) ([]byte, error) {
	f.gets.Add(1)
	if f.beforeGet != nil {
		f.beforeGet(id)
	}
	if f.getErr != nil {
		return nil, f.getErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.m[id], nil
}

func (f *fakeSessions) Set(ctx context.Context, id string, b []byte, ttl time.Duration) error {
	f.sets.Add(1)
	if ctx.Err() != nil {
		f.canceled.Add(1)
	}
	if f.setErr != nil {
		return f.setErr
	}
	f.mu.Lock()
	f.m[id], f.ttls[id] = b, ttl
	f.mu.Unlock()
	if f.onWrite != nil {
		f.onWrite(id)
	}
	return nil
}

func (f *fakeSessions) Delete(ctx context.Context, id string) error {
	f.dels.Add(1)
	if ctx.Err() != nil {
		f.canceled.Add(1)
	}
	if f.delErr != nil {
		return f.delErr
	}
	f.mu.Lock()
	delete(f.m, id)
	f.mu.Unlock()
	return nil
}

func (f *fakeSessions) put(id, data string) {
	f.mu.Lock()
	f.m[id] = []byte(data)
	f.mu.Unlock()
}

func (f *fakeSessions) val(id string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return string(f.m[id])
}

func (f *fakeSessions) ids() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Sorted(maps.Keys(f.m))
}

func (f *fakeSessions) calls() [3]int32 {
	return [3]int32{f.gets.Load(), f.sets.Load(), f.dels.Load()}
}

const (
	sid1 = "11111111-1111-4111-8111-111111111111"
	sid2 = "22222222-2222-4222-9222-222222222222"
)

// sessPage is a page that runs f with the implicit session.
func sessPage(f func(c *Ctx, s *Session[sessData]) error) Route {
	return Route{Pattern: "/", File: "src/pages/index.lyr", Render: func(c *Ctx, w *Writer) error {
		if err := f(c, SessionOf[sessData](c)); err != nil {
			return err
		}
		w.Raw("ok")
		return nil
	}}
}

func sessSite(t *testing.T, store SessionStore, o SessionOptions, routes []Route, opts ...Option) *Site {
	t.Helper()
	return testSite(t, routes, append([]Option{WithSessions(store, o), withLocalsSess()}, opts...)...)
}

// reqWith sends GET target with the session cookie id (none when "").
func reqWith(h http.Handler, target, id string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("GET", target, nil)
	if id != "" {
		r.Header.Set("Cookie", "theme=dark; lyr-session="+id)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

// cookieID returns the session ID set by rec, or "".
func setCookieID(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	vals := rec.Header().Values("Set-Cookie")
	if len(vals) > 1 {
		t.Errorf("%d Set-Cookie headers: %q", len(vals), vals)
	}
	for _, v := range vals {
		if rest, ok := strings.CutPrefix(v, "lyr-session="); ok {
			id, _, _ := strings.Cut(rest, ";")
			return id
		}
	}
	return ""
}

func TestSessionLazy(t *testing.T) {
	st := newFakeSessions()
	st.put(sid1, `{"userId":1}`)
	var used bool
	s := sessSite(t, st, SessionOptions{}, []Route{
		{Pattern: "/plain", File: "src/pages/plain.lyr", Render: func(c *Ctx, w *Writer) error { w.Raw("x"); return nil }},
		sessPage(func(c *Ctx, s *Session[sessData]) error {
			if used {
				_, err := s.Data()
				return err
			}
			return nil
		}),
	})
	h := s.Handler()
	reqWith(h, "/plain", sid1)
	reqWith(h, "/", sid1) // SessionOf without a method call
	if c := st.calls(); c != [3]int32{} {
		t.Fatalf("no use: store calls %v", c)
	}
	used = true
	if rec := reqWith(h, "/", ""); rec.Code != 200 || setCookieID(t, rec) != "" {
		t.Fatalf("anonymous: %d %v", rec.Code, rec.Header())
	}
	if c := st.calls(); c != [3]int32{} {
		t.Fatalf("anonymous read: store calls %v", c)
	}
	reqWith(h, "/", sid1)
	if c := st.calls(); c != [3]int32{1, 0, 0} {
		t.Fatalf("cookie read: store calls %v", c)
	}
}

func TestSessionChangeDetection(t *testing.T) {
	st := newFakeSessions()
	var change func(d *sessData)
	s := sessSite(t, st, SessionOptions{}, []Route{sessPage(func(c *Ctx, s *Session[sessData]) error {
		d, err := s.Data()
		if err != nil {
			return err
		}
		if change != nil {
			change(d)
		}
		return nil
	})})
	h := s.Handler()

	st.put(sid1, `{"userId":1,"cart":["a"]}`)
	if rec := reqWith(h, "/", sid1); setCookieID(t, rec) != "" || st.sets.Load() != 0 {
		t.Errorf("unchanged: cookie %v, %d sets", rec.Header(), st.sets.Load())
	}
	// Another key order, other whitespace and an unknown key: no field
	// differs.
	st.put(sid1, `{ "cart": ["a"], "old": true, "userId": 1 }`)
	reqWith(h, "/", sid1)
	if st.sets.Load() != 0 {
		t.Errorf("reordered: %d sets", st.sets.Load())
	}
	// Anonymous read, and a change set back to zero: no session.
	reqWith(h, "/", "")
	change = func(d *sessData) { d.Flash = "x"; d.Flash = "" }
	if rec := reqWith(h, "/", ""); setCookieID(t, rec) != "" || st.sets.Load() != 0 {
		t.Errorf("zero data: %v, %d sets", rec.Header(), st.sets.Load())
	}
	change = func(d *sessData) { d.Flash = "hi" }
	rec := reqWith(h, "/", "")
	id := setCookieID(t, rec)
	if !validSessionID(id) || st.val(id) != `{"flash":"hi"}` {
		t.Errorf("new session %q: %q", id, st.val(id))
	}
}

func TestSessionMerge(t *testing.T) {
	st := newFakeSessions()
	var change func(d *sessData)
	s := sessSite(t, st, SessionOptions{}, []Route{sessPage(func(c *Ctx, s *Session[sessData]) error {
		d, err := s.Data()
		if err == nil {
			change(d)
		}
		return err
	})})
	h := s.Handler()
	cases := []struct {
		name, stored string
		change       func(d *sessData)
		interleave   string // the store's value written between the load and the save
		want         string
	}{
		{"replace", `{"userId":1,"flash":"a"}`, func(d *sessData) { d.Flash = "b" }, "", `{"userId":1,"flash":"b"}`},
		{"omitempty removal", `{"userId":1,"flash":"a"}`, func(d *sessData) { d.Flash = "" }, "", `{"userId":1}`},
		{"unknown kept", `{"userId":1,"legacy":{"x":1}}`, func(d *sessData) { d.UserID = 2 }, "", `{"userId":2,"legacy":{"x":1}}`},
		{"nested whole", `{"cart":["a","b"]}`, func(d *sessData) { d.Cart = append(d.Cart[:1], "c") }, "", `{"cart":["a","c"]}`},
		{"interleaved writer", `{"userId":1}`, func(d *sessData) { d.Flash = "mine" }, `{"userId":1,"cart":["theirs"]}`, `{"userId":1,"flash":"mine","cart":["theirs"]}`},
		{"interleaved same field", `{"flash":"a"}`, func(d *sessData) { d.Flash = "mine" }, `{"flash":"theirs"}`, `{"flash":"mine"}`},
		{"unchanged keeps the store", `{"userId":1,"flash":"a"}`, func(d *sessData) { d.Cart = []string{"x"} }, `{"userId":9,"flash":"a"}`, `{"userId":9,"cart":["x"],"flash":"a"}`},
	}
	for _, tc := range cases {
		st.put(sid1, tc.stored)
		change = tc.change
		st.beforeGet = nil
		if tc.interleave != "" {
			n := 0
			st.beforeGet = func(string) {
				if n++; n == 2 { // the save's re-read
					st.put(sid1, tc.interleave)
				}
			}
		}
		rec := reqWith(h, "/", sid1)
		if got := st.val(sid1); got != tc.want || setCookieID(t, rec) != "" {
			t.Errorf("%s: stored %s, want %s (cookie %q)", tc.name, got, tc.want, rec.Header().Get("Set-Cookie"))
		}
	}
}

func TestSessionLifecycle(t *testing.T) {
	st := newFakeSessions()
	var run func(s *Session[sessData]) error
	s := sessSite(t, st, SessionOptions{}, []Route{sessPage(func(c *Ctx, s *Session[sessData]) error { return run(s) })})
	h := s.Handler()

	// Regenerate: same data under a new ID, the old one deleted.
	st.put(sid1, `{"userId":1}`)
	run = func(s *Session[sessData]) error {
		if s.ID() != sid1 {
			t.Errorf("ID %q", s.ID())
		}
		if err := s.Regenerate(); err != nil {
			return err
		}
		d, _ := s.Data()
		d.Flash = "welcome"
		return nil
	}
	rec := reqWith(h, "/", sid1)
	nid := setCookieID(t, rec)
	if nid == "" || nid == sid1 || st.val(nid) != `{"userId":1,"flash":"welcome"}` || st.val(sid1) != "" {
		t.Errorf("regenerate: %q %q, old %q", nid, st.val(nid), st.val(sid1))
	}
	// Regenerate without a change still moves the session.
	run = func(s *Session[sessData]) error { return s.Regenerate() }
	rec = reqWith(h, "/", nid)
	nid2 := setCookieID(t, rec)
	if nid2 == "" || nid2 == nid || st.val(nid2) != `{"userId":1,"flash":"welcome"}` || st.val(nid) != "" {
		t.Errorf("regenerate unchanged: %q %v", nid2, st.ids())
	}

	// Destroy: deleted, cookie expired.
	run = func(s *Session[sessData]) error { s.Destroy(); return nil }
	rec = reqWith(h, "/", nid2)
	if st.val(nid2) != "" || !strings.Contains(rec.Header().Get("Set-Cookie"), "lyr-session=; Path=/; Max-Age=0") {
		t.Errorf("destroy: %v %q", st.ids(), rec.Header().Get("Set-Cookie"))
	}
	// Destroy then change: a new session with a new ID, one Set-Cookie.
	st.put(sid1, `{"userId":1}`)
	run = func(s *Session[sessData]) error {
		s.Destroy()
		d, err := s.Data()
		if err != nil || d.UserID != 0 {
			t.Errorf("after Destroy: %+v %v", d, err)
		}
		d.Flash = "bye"
		return nil
	}
	rec = reqWith(h, "/", sid1)
	nid3 := setCookieID(t, rec)
	if nid3 == "" || nid3 == sid1 || st.val(nid3) != `{"flash":"bye"}` || st.val(sid1) != "" {
		t.Errorf("destroy then change: %q %v", nid3, st.ids())
	}

	// The stored session disappears between the load and the save: the
	// changed fields go to a new ID.
	st.put(sid1, `{"userId":1,"cart":["a"]}`)
	run = func(s *Session[sessData]) error {
		d, _ := s.Data()
		st.mu.Lock()
		delete(st.m, sid1)
		st.mu.Unlock()
		d.Flash = "x"
		return nil
	}
	rec = reqWith(h, "/", sid1)
	nid4 := setCookieID(t, rec)
	if nid4 == "" || nid4 == sid1 || st.val(nid4) != `{"flash":"x"}` {
		t.Errorf("gone: %q %q", nid4, st.val(nid4))
	}
}

func TestSessionLoadAndIDs(t *testing.T) {
	st := newFakeSessions()
	st.put(sid2, `{"userId":2}`)
	var run func(s *Session[sessData]) error
	s := sessSite(t, st, SessionOptions{}, []Route{sessPage(func(c *Ctx, s *Session[sessData]) error { return run(s) })})
	h := s.Handler()

	run = func(s *Session[sessData]) error {
		if err := s.Load("33333333-3333-4333-8333-333333333333"); !errors.Is(err, ErrSessionNotFound) {
			t.Errorf("unknown: %v", err)
		}
		if err := s.Load("not-an-id"); !errors.Is(err, ErrSessionNotFound) {
			t.Errorf("invalid: %v", err)
		}
		if s.ID() != "" {
			t.Errorf("unknown Load changed the ID: %q", s.ID())
		}
		if err := s.Load(sid2); err != nil {
			return err
		}
		d, _ := s.Data()
		if d.UserID != 2 || s.ID() != sid2 {
			t.Errorf("loaded %+v %q", d, s.ID())
		}
		return nil
	}
	rec := reqWith(h, "/", "")
	if setCookieID(t, rec) != sid2 || st.sets.Load() != 0 {
		t.Errorf("Load: cookie %q, %d sets", rec.Header().Get("Set-Cookie"), st.sets.Load())
	}

	// Invalid, unknown and planted IDs are never adopted.
	run = func(s *Session[sessData]) error {
		d, err := s.Data()
		if err == nil {
			d.Flash = "x"
		}
		return err
	}
	for _, id := range []string{"garbage", strings.ToUpper(sid1), "44444444-4444-4444-8444-444444444444"} {
		gets := st.gets.Load()
		rec := reqWith(h, "/", id)
		got := setCookieID(t, rec)
		if got == "" || got == id || st.val(got) != `{"flash":"x"}` {
			t.Errorf("cookie %q: new ID %q", id, got)
		}
		if validSessionID(id) != (st.gets.Load() > gets) {
			t.Errorf("cookie %q: store read %v", id, st.gets.Load() > gets)
		}
	}
}

func TestSessionCookieAttributes(t *testing.T) {
	cases := []struct {
		name string
		o    SessionOptions
		dev  bool
		want string
	}{
		{"defaults", SessionOptions{}, false, "lyr-session=ID; Path=/; HttpOnly; Secure; SameSite=Lax"},
		{"insecure", SessionOptions{Cookie: SessionCookie{Insecure: true}}, false, "lyr-session=ID; Path=/; HttpOnly; SameSite=Lax"},
		{"dev", SessionOptions{}, true, "lyr-session=ID; Path=/; HttpOnly; SameSite=Lax"},
		{"all", SessionOptions{Cookie: SessionCookie{Name: "sid", Domain: "example.com", Path: "/app", MaxAge: time.Hour, SameSite: http.SameSiteStrictMode}}, false,
			"sid=ID; Path=/app; Domain=example.com; Max-Age=3600; HttpOnly; Secure; SameSite=Strict"},
		{"none", SessionOptions{Cookie: SessionCookie{SameSite: http.SameSiteNoneMode}}, false, "lyr-session=ID; Path=/; HttpOnly; Secure; SameSite=None"},
	}
	for _, tc := range cases {
		dev := ""
		if tc.dev {
			dev = "1"
		}
		t.Setenv("LYR_DEV", dev)
		st := newFakeSessions()
		s := sessSite(t, st, tc.o, []Route{sessPage(func(c *Ctx, s *Session[sessData]) error {
			d, _ := s.Data()
			d.UserID = 7
			return nil
		})})
		rec := reqWith(s.Handler(), "/", "")
		got := rec.Header().Get("Set-Cookie")
		ids := st.ids()
		if len(ids) != 1 || got != strings.Replace(tc.want, "ID", ids[0], 1) {
			t.Errorf("%s: %q (want %q)", tc.name, got, tc.want)
		}
	}
	t.Setenv("LYR_DEV", "")
	for _, bad := range []SessionOptions{{Cookie: SessionCookie{Name: "a b"}}, {Cookie: SessionCookie{SameSite: http.SameSiteNoneMode, Insecure: true}}, {Cookie: SessionCookie{MaxAge: 500 * time.Millisecond}}} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("WithSessions(%+v) didn't panic", bad)
				}
			}()
			WithSessions(newFakeSessions(), bad)
		}()
	}
}

func TestSessionMaxAgeAndTTL(t *testing.T) {
	st := newFakeSessions()
	st.put(sid1, `{"userId":1}`)
	var change bool
	s := sessSite(t, st, SessionOptions{TTL: 24 * time.Hour, Cookie: SessionCookie{MaxAge: time.Hour}}, []Route{sessPage(func(c *Ctx, s *Session[sessData]) error {
		d, err := s.Data()
		if change {
			d.Flash += "x"
		}
		return err
	})})
	h := s.Handler()
	// A read doesn't extend anything: no write, no cookie.
	if rec := reqWith(h, "/", sid1); st.sets.Load() != 0 || rec.Header().Get("Set-Cookie") != "" {
		t.Errorf("read: %d sets, %q", st.sets.Load(), rec.Header().Get("Set-Cookie"))
	}
	change = true
	for i := range 2 {
		rec := reqWith(h, "/", sid1)
		if !strings.Contains(rec.Header().Get("Set-Cookie"), "lyr-session="+sid1+"; Path=/; Max-Age=3600") {
			t.Errorf("save %d: cookie %q", i, rec.Header().Get("Set-Cookie"))
		}
		st.mu.Lock()
		ttl := st.ttls[sid1]
		st.mu.Unlock()
		if ttl != 24*time.Hour {
			t.Errorf("save %d: ttl %v", i, ttl)
		}
	}
	if st.sets.Load() != 2 {
		t.Errorf("%d sets", st.sets.Load())
	}
}

func TestSessionErrors(t *testing.T) {
	st := newFakeSessions()
	st.put(sid1, `{"userId":"not a number"}`)
	var run func(s *Session[sessData]) error
	var handled []error
	s := sessSite(t, st, SessionOptions{}, []Route{sessPage(func(c *Ctx, s *Session[sessData]) error { return run(s) })},
		WithErrorHandler(func(w http.ResponseWriter, r *http.Request, err error) bool {
			handled = append(handled, err)
			if r.URL.Query().Get("handle") == "" {
				return false
			}
			w.WriteHeader(503)
			return true
		}))
	h := s.Handler()
	logs := captureLogSync(t)

	// A value that doesn't decode is an error, and stays stored.
	run = func(s *Session[sessData]) error {
		_, err := s.Data()
		var de *SessionDecodeError
		if !errors.As(err, &de) || de.ID != sid1 || strings.Contains(err.Error(), sid1) {
			t.Errorf("decode error: %v", err)
		}
		return nil
	}
	reqWith(h, "/", sid1)
	if st.val(sid1) != `{"userId":"not a number"}` || st.sets.Load() != 0 {
		t.Errorf("decode error: stored %q", st.val(sid1))
	}

	// Store errors are wrapped and returned.
	boom := errors.New("boom")
	st.getErr = boom
	run = func(s *Session[sessData]) error {
		_, err := s.Data()
		if !errors.Is(err, boom) || err.Error() != "lyr: session store: boom" {
			t.Errorf("get error: %v", err)
		}
		return nil
	}
	reqWith(h, "/", sid1)
	st.getErr = nil

	// A failed save is the page's error: the handler, then the 500.
	st.setErr = boom
	run = func(s *Session[sessData]) error {
		d, err := s.Data()
		d.Flash = "x"
		return err
	}
	if rec := reqWith(h, "/?handle=1", ""); rec.Code != 503 || len(handled) != 1 || !errors.Is(handled[0], boom) {
		t.Errorf("handled save error: %d %v", rec.Code, handled)
	}
	if rec := reqWith(h, "/", ""); rec.Code != 500 || rec.Header().Get("Set-Cookie") != "" || !strings.Contains(logs.String(), "saving session: lyr: session store: boom") {
		t.Errorf("save error: %d %q, log %q", rec.Code, rec.Header().Get("Set-Cookie"), logs)
	}
	st.setErr = nil
}

func TestSessionSaveOnRedirectAndError(t *testing.T) {
	st := newFakeSessions()
	var fail error
	s := sessSite(t, st, SessionOptions{}, []Route{sessPage(func(c *Ctx, s *Session[sessData]) error {
		d, err := s.Data()
		if err != nil {
			return err
		}
		d.Flash = "saved"
		if fail != nil {
			return fail
		}
		return c.Redirect("/next", 303)
	})})
	h := s.Handler()
	rec := reqWith(h, "/", "")
	if id := setCookieID(t, rec); rec.Code != 303 || id == "" || st.val(id) != `{"flash":"saved"}` {
		t.Errorf("redirect: %d %q", rec.Code, rec.Header())
	}
	fail = errors.New("render failed")
	captureLogSync(t)
	rec = reqWith(h, "/", "")
	if id := setCookieID(t, rec); rec.Code != 500 || id == "" || st.val(id) != `{"flash":"saved"}` {
		t.Errorf("render error: %d %q", rec.Code, rec.Header())
	}
}

func TestSessionSaveWithoutCancel(t *testing.T) {
	st := newFakeSessions()
	s := sessSite(t, st, SessionOptions{}, []Route{sessPage(func(c *Ctx, s *Session[sessData]) error {
		d, err := s.Data()
		d.Flash = "x"
		return err
	})})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := httptest.NewRequest("GET", "/", nil).WithContext(ctx)
	s.Handler().ServeHTTP(httptest.NewRecorder(), r)
	if st.sets.Load() != 1 || st.canceled.Load() != 0 {
		t.Errorf("%d sets, %d with a canceled context", st.sets.Load(), st.canceled.Load())
	}
}

func TestSessionNotEnabledAndUnbound(t *testing.T) {
	warnedNoSessions.Store(false)
	logs := captureLogSync(t)
	var errs []error
	s := testSite(t, []Route{sessPage(func(c *Ctx, s *Session[sessData]) error {
		_, err := s.Data()
		errs = append(errs, err, s.Regenerate(), s.Save())
		return nil
	})})
	do(s.Handler(), "GET", "/")
	do(s.Handler(), "GET", "/")
	for _, err := range errs {
		if !errors.Is(err, ErrSessionsNotEnabled) {
			t.Errorf("not enabled: %v", err)
		}
	}
	if n := strings.Count(logs.String(), "sessions are not enabled"); n != 1 {
		t.Errorf("logged %d times: %q", n, logs)
	}
	var zero Session[sessData]
	if _, err := zero.Data(); !errors.Is(err, ErrSessionUnbound) {
		t.Errorf("zero Session: %v", err)
	}
	// RenderComponent outside site.Sessions.
	err := RenderComponent(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil), func(c *Ctx, w *Writer, _ struct{}, _ Slots) error {
		_, err := SessionOf[sessData](c).Data()
		return err
	}, struct{}{})
	if !errors.Is(err, ErrSessionUnbound) {
		t.Errorf("RenderComponent: %v", err)
	}
}

func TestSessionFieldNames(t *testing.T) {
	type Inner struct {
		A string `json:"a"`
		B int
	}
	type tagged struct {
		Inner
		*sessData `json:"-"`
		Name      string `json:"name,omitempty"`
		Skip      int    `json:"-"`
		Dash      int    `json:"-,"`
		hidden    int
		Plain     bool
		Named     Inner `json:"named"`
	}
	_ = tagged{}.hidden
	got := jsonFieldNames(reflect.TypeFor[tagged]())
	want := []string{"a", "B", "name", "-", "Plain", "named"}
	if !slices.Equal(got, want) {
		t.Errorf("fields %q, want %q", got, want)
	}
}

func TestSessionsMiddleware(t *testing.T) {
	st := newFakeSessions()
	st.put(sid1, `{"userId":1}`)
	page := sessPage(func(c *Ctx, s *Session[sessData]) error {
		d, err := s.Data()
		if err == nil && c.Request.URL.Query().Get("page") != "" {
			d.Flash = "page"
		}
		return err
	})
	s := sessSite(t, st, SessionOptions{}, []Route{page})
	auth := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r, l := EnsureLocals[sessLocals](r)
			d, err := l.Session.Data()
			if err != nil {
				s.ServeError(w, r, err)
				return
			}
			l.User = d.UserID
			next.ServeHTTP(w, r)
		})
	}
	mux := http.NewServeMux()
	var late bool
	mux.HandleFunc("/login", func(w http.ResponseWriter, r *http.Request) {
		l := LocalsFromRequest[sessLocals](r)
		if err := l.Session.Regenerate(); err != nil {
			t.Error(err)
		}
		d, _ := l.Session.Data()
		d.UserID = 42
		switch r.URL.Query().Get("how") {
		case "header":
			w.WriteHeader(204)
		case "write":
			w.Write([]byte("hi"))
		case "save":
			if err := l.Session.Save(); err != nil {
				t.Error(err)
			}
			w.Write([]byte("saved"))
			d.Flash = "after"
		}
		// "none": nothing written.
	})
	mux.HandleFunc("/late", func(w http.ResponseWriter, r *http.Request) {
		l := LocalsFromRequest[sessLocals](r)
		d, _ := l.Session.Data()
		w.WriteHeader(200)
		d.Cart = []string{"late"}
		late = true
		if rc := http.NewResponseController(w); rc.Flush() != nil {
			t.Error("ResponseController can't flush through the wrapper")
		}
	})
	mux.HandleFunc("/who", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(LocalsFromRequest[sessLocals](r).Session.ID()))
	})
	chain := s.Sessions(auth(s.Middleware(mux)))

	for _, how := range []string{"header", "write", "none", "save"} {
		st.put(sid1, `{"userId":1}`)
		rec := reqWith(chain, "/login?how="+how, sid1)
		id := setCookieID(t, rec)
		want := `{"userId":42}`
		if how == "save" {
			want = `{"userId":42,"flash":"after"}` // the late change is saved without a new cookie
		}
		if id == "" || id == sid1 || st.val(id) != want || st.val(sid1) != "" {
			t.Errorf("login %s: cookie %q, stored %q, old %q", how, id, st.val(id), st.val(sid1))
		}
	}
	// A late change that needs no cookie is saved.
	st.put(sid1, `{"userId":1}`)
	reqWith(chain, "/late", sid1)
	if !late || st.val(sid1) != `{"userId":1,"cart":["late"]}` {
		t.Errorf("late: %q", st.val(sid1))
	}
	// A late change that needs a cookie is logged and dropped.
	logs := captureLogSync(t)
	rec := reqWith(chain, "/late", "")
	if setCookieID(t, rec) != "" || !strings.Contains(logs.String(), "session changed after the response was written") {
		t.Errorf("late new session: %q, log %q", rec.Header().Get("Set-Cookie"), logs)
	}
	// A page under site.Sessions saves through the page path, on the same
	// state middleware loaded.
	st.put(sid1, `{"userId":1}`)
	gets := st.gets.Load()
	reqWith(chain, "/?page=1", sid1)
	if st.val(sid1) != `{"userId":1,"flash":"page"}` || st.gets.Load()-gets != 2 { // load, then the save's re-read
		t.Errorf("page under Sessions: %q, %d gets", st.val(sid1), st.gets.Load()-gets)
	}
	// site.Sessions with Locals attached earlier, and nested twice.
	early := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r, _ = EnsureLocals[sessLocals](r)
			next.ServeHTTP(w, r)
		})
	}
	if rec := reqWith(early(s.Sessions(s.Sessions(mux))), "/who", sid1); rec.Body.String() != sid1 {
		t.Errorf("early locals: %q", rec.Body)
	}
	// Unwrap.
	sw := &sessionWriter{ResponseWriter: httptest.NewRecorder()}
	if sw.Unwrap() == nil {
		t.Error("Unwrap")
	}

	// Without WithSessions: the field says sessions aren't enabled.
	// Without the field: a panic.
	plain := testSite(t, nil, withLocalsSess())
	var offErr error
	plain.Sessions(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, offErr = LocalsFromRequest[sessLocals](r).Session.Data()
	})).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
	if !errors.Is(offErr, ErrSessionsNotEnabled) {
		t.Errorf("no WithSessions: the field's Data returned %v, want ErrSessionsNotEnabled", offErr)
	}
	func() {
		defer func() {
			if p := recover(); p == nil || !strings.Contains(p.(string), "lyr.Session[SessionData]") {
				t.Errorf("no field: %v", p)
			}
		}()
		testSite(t, nil, WithSessions(st, SessionOptions{})).Sessions(mux)
	}()
}

func TestSessionsHandlerSaveError(t *testing.T) {
	st := newFakeSessions()
	s := sessSite(t, st, SessionOptions{}, nil)
	logs := captureLogSync(t)
	h := s.Sessions(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		d, _ := LocalsFromRequest[sessLocals](r).Session.Data()
		d.UserID = 1
		w.Write([]byte("ok"))
	}))
	st.setErr = errors.New("down")
	rec := reqWith(h, "/", "")
	if rec.Code != 200 || rec.Header().Get("Set-Cookie") != "" || !strings.Contains(logs.String(), "lyr: saving session: lyr: session store: down") {
		t.Errorf("%d %q, log %q", rec.Code, rec.Header().Get("Set-Cookie"), logs)
	}
}

func TestSessionLocalsOfShares(t *testing.T) {
	st := newFakeSessions()
	st.put(sid1, `{"userId":5}`)
	s := sessSite(t, st, SessionOptions{}, []Route{sessPage(func(c *Ctx, s *Session[sessData]) error {
		l := LocalsOf[sessLocals](c)
		d1, err := l.Session.Data()
		if err != nil {
			return err
		}
		d2, _ := s.Data()
		if d1 != d2 || d1.UserID != 5 {
			t.Errorf("Locals field and implicit session differ: %p %p", d1, d2)
		}
		return nil
	})})
	reqWith(s.Handler(), "/", sid1)
	if st.gets.Load() != 1 {
		t.Errorf("%d gets", st.gets.Load())
	}
}

// TestSessionConcurrent: server:concurrent children and lyr.All loads
// share one handle (run with -race).
func TestSessionConcurrent(t *testing.T) {
	st := newFakeSessions()
	st.put(sid1, `{"userId":3}`)
	child := func(c *Ctx, w *Writer, _ struct{}, _ Slots) error {
		d, err := SessionOf[sessData](c).Data()
		if err != nil {
			return err
		}
		w.Int(d.UserID)
		return nil
	}
	s := sessSite(t, st, SessionOptions{}, []Route{{Pattern: "/", File: "src/pages/index.lyr", Render: func(c *Ctx, w *Writer) error {
		for range 8 {
			if err := Concurrent(c, w, child, struct{}{}, Slots{}); err != nil {
				return err
			}
		}
		sess := SessionOf[sessData](c)
		var ptrs [4]*sessData
		loads := make([]func(context.Context) error, 4)
		for i := range loads {
			loads[i] = func(context.Context) error {
				d, err := sess.Data()
				ptrs[i] = d
				return err
			}
		}
		if err := All(c, loads...); err != nil {
			return err
		}
		for _, p := range ptrs {
			if p != ptrs[0] {
				t.Error("lyr.All loads got different values")
			}
		}
		return nil
	}}})
	rec := reqWith(s.Handler(), "/", sid1)
	if rec.Body.String() != "33333333" || st.gets.Load() != 1 {
		t.Errorf("%q, %d gets", rec.Body, st.gets.Load())
	}
}

func TestSessionJSONMembers(t *testing.T) {
	ms, ok := jsonMembers([]byte(` { "a\"b" : [1,{"x":"]"}] , "c":null,"d":"s\\"} `), nil)
	if !ok || len(ms) != 3 || ms[0].key != `a"b` || string(ms[0].val) != `[1,{"x":"]"}]` || string(ms[1].val) != "null" || string(ms[2].val) != `"s\\"` {
		t.Errorf("members %+v %v", ms, ok)
	}
	for _, bad := range []string{``, `[]`, `{"a"}`, `{"a":1,}`, `{"a":"x`} {
		if _, ok := jsonMembers([]byte(bad), nil); ok {
			t.Errorf("%q read", bad)
		}
	}
}

// TestSessionDecodeRecovery: Destroy drops a session that no longer
// decodes, and Data then gives a fresh one.
func TestSessionDecodeRecovery(t *testing.T) {
	st := newFakeSessions()
	st.put(sid1, `{"cart":"not a list"}`)
	s := sessSite(t, st, SessionOptions{}, []Route{sessPage(func(c *Ctx, s *Session[sessData]) error {
		_, err := s.Data()
		var de *SessionDecodeError
		if !errors.As(err, &de) {
			t.Fatalf("Data: %v", err)
		}
		if s.ID() != sid1 {
			t.Errorf("ID after a decode error: %q", s.ID())
		}
		s.Destroy()
		d, err := s.Data()
		if err != nil || d.UserID != 0 || s.ID() != "" {
			t.Errorf("after Destroy: %+v %v %q", d, err, s.ID())
		}
		d.Flash = "fresh"
		return nil
	})})
	rec := reqWith(s.Handler(), "/", sid1)
	id := setCookieID(t, rec)
	if st.val(sid1) != "" || id == "" || st.val(id) != `{"flash":"fresh"}` {
		t.Errorf("recovery: %v, cookie %q", st.ids(), id)
	}
}

// TestSessionAttachedLocalsNotBound: a page's own session state is never
// put in a Locals that middleware attached (without site.Sessions): that
// Locals outlives the request, and the pooled state would reach the next
// visitor through it. Both ways in: LocalsOf then the implicit session,
// and a server:concurrent child resolving the session for the fork.
func TestSessionAttachedLocalsNotBound(t *testing.T) {
	for _, viaFork := range []bool{false, true} {
		st := newFakeSessions()
		st.put(sid1, `{"userId":1}`)
		st.put(sid2, `{"userId":2}`)
		var held []*sessLocals
		child := func(c *Ctx, w *Writer, _ struct{}, _ Slots) error {
			d, err := SessionOf[sessData](c).Data()
			if err == nil {
				w.Int(d.UserID)
			}
			return err
		}
		s := sessSite(t, st, SessionOptions{}, []Route{{Pattern: "/", File: "src/pages/index.lyr", Render: func(c *Ctx, w *Writer) error {
			held = append(held, LocalsOf[sessLocals](c))
			if viaFork {
				return Concurrent(c, w, child, struct{}{}, Slots{})
			}
			d, err := SessionOf[sessData](c).Data()
			if err == nil {
				w.Int(d.UserID)
			}
			return err
		}}})
		attach := func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				r, _ = EnsureLocals[sessLocals](r)
				next.ServeHTTP(w, r)
			})
		}
		h := attach(s.Handler())
		if rec := reqWith(h, "/", sid1); rec.Body.String() != "1" {
			t.Errorf("fork=%v: first request %q", viaFork, rec.Body)
		}
		if rec := reqWith(h, "/", sid2); rec.Body.String() != "2" {
			t.Errorf("fork=%v: second request %q", viaFork, rec.Body)
		}
		for i, l := range held {
			if l.Session.st != nil {
				t.Errorf("fork=%v: request %d's attached Locals is bound to a Ctx-owned state (ID %q)", viaFork, i+1, l.Session.ID())
			}
		}
	}
}
