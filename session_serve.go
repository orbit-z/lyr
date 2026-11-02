package lyr

import (
	"errors"
	"log"
	"net/http"
	"sync"
)

// The session request flow (PLAN.md §4i): site.Sessions and its
// ResponseWriter, the save at the end of page and island requests, and
// the route cache's session handling.

// Sessions binds the Session field of your Locals for next: middleware
// and app handlers inside it reach the request's session through it,
// and it saves their changes before the response's headers go out (or
// after next returns, when nothing was written). Put it before the
// middleware that uses the session:
//
//	lyr.ListenAndServe(lyr.Sequence(site.TrailingSlash, site.Sessions, auth, site.Middleware)(mux))
//
// It attaches Locals when no earlier middleware did, and reads no cookie
// until the session is used. Pages don't need it: their implicit session
// works without it. Without lyr.WithSessions, it binds the field to a
// session whose methods return ErrSessionsNotEnabled. It panics when your
// Locals has no lyr.Session[SessionData] field.
func (s *Site) Sessions(next http.Handler) http.Handler {
	if s.sessField == nil {
		panic("lyr: site.Sessions needs a field of type lyr.Session[SessionData] in your Locals (src/locals), " +
			"such as Session lyr.Session[SessionData]; then run lyr generate")
	}
	if s.sess == nil {
		return &sessionsOffMiddleware{field: s.sessField, next: next}
	}
	return &sessionMiddleware{cfg: s.sess, next: next}
}

// sessionsOffMiddleware is site.Sessions without WithSessions: the field
// is bound to sessionsOff, which is never written, so the methods say
// sessions aren't enabled instead of unbound.
type sessionsOffMiddleware struct {
	field *localsSession
	next  http.Handler
}

func (m *sessionsOffMiddleware) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	r, p := m.field.ensure(r)
	if *p == nil {
		*p = sessionsOff
	}
	m.next.ServeHTTP(w, r)
}

type sessionMiddleware struct {
	cfg  *sessionConfig
	next http.Handler
}

func (m *sessionMiddleware) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	r, p := m.cfg.field.ensure(r)
	if *p != nil {
		// An outer site.Sessions already bound it.
		m.next.ServeHTTP(w, r)
		return
	}
	st := acquireSession(m.cfg, r)
	st.h = w.Header()
	*p = st
	sw := sessionWriterPool.Get().(*sessionWriter)
	sw.ResponseWriter, sw.st = w, st
	m.next.ServeHTTP(sw, r)
	sw.finish()
	*sw = sessionWriter{}
	sessionWriterPool.Put(sw)
	// Unbound, so a Locals kept past the request can't reach the next
	// request's state. (A revalidation never uses it: it has a Locals
	// copy and a state of its own.)
	*p = nil
	releaseSession(st)
}

// sessionWriter is site.Sessions' ResponseWriter: it saves the session on
// the first WriteHeader or Write, so the cookie goes out with the headers.
type sessionWriter struct {
	http.ResponseWriter
	st    *sessionState
	wrote bool
}

var sessionWriterPool = sync.Pool{New: func() any { return new(sessionWriter) }}

func (sw *sessionWriter) WriteHeader(code int) {
	sw.before()
	sw.ResponseWriter.WriteHeader(code)
}

func (sw *sessionWriter) Write(b []byte) (int, error) {
	sw.before()
	return sw.ResponseWriter.Write(b)
}

// Unwrap returns the wrapped ResponseWriter, for http.ResponseController.
func (sw *sessionWriter) Unwrap() http.ResponseWriter { return sw.ResponseWriter }

// Flush saves the session, then flushes the wrapped writer if it can.
func (sw *sessionWriter) Flush() {
	sw.before()
	http.NewResponseController(sw.ResponseWriter).Flush()
}

func (sw *sessionWriter) lyrSession() *sessionState { return sw.st }

// before saves the session before the response starts.
func (sw *sessionWriter) before() {
	if sw.wrote {
		return
	}
	sw.wrote = true
	saveTo(sw.ResponseWriter.Header(), sw.st)
}

// saveTo saves st and writes its cookie to h; an error is logged.
func saveTo(h http.Header, st *sessionState) {
	st.mu.Lock()
	err := st.save(false)
	if err == nil {
		st.flushCookie(h)
	} else {
		st.cookie = cookieNone
	}
	st.mu.Unlock()
	if err != nil {
		log.Printf("lyr: saving session: %v", err)
	}
}

// finish saves after next returned: as before when nothing was written
// (net/http sends the headers afterwards), else the late changes.
func (sw *sessionWriter) finish() {
	if !sw.wrote {
		sw.before()
		return
	}
	st := sw.st
	st.mu.Lock()
	err := st.save(true)
	st.cookie = cookieNone
	st.mu.Unlock()
	if err != nil {
		log.Printf("lyr: saving session: %v", err)
	}
}

// sessionHolder is implemented by the ResponseWriters that carry a
// request's session (site.Sessions'), for
// RenderComponent.
type sessionHolder interface{ lyrSession() *sessionState }

// sessionFromWriter finds the bound session through w and the writers it
// wraps, or returns nil.
func sessionFromWriter(w http.ResponseWriter) *sessionState {
	for range 16 {
		if h, ok := w.(sessionHolder); ok {
			return h.lyrSession()
		}
		u, ok := w.(interface{ Unwrap() http.ResponseWriter })
		if !ok {
			return nil
		}
		w = u.Unwrap()
	}
	return nil
}

// endSession saves the session of a page or island request that resolved
// one, before the response is written, and puts its cookie on w. err is
// the render's; a failed save becomes the request's error unless the
// render already failed with another error.
func endSession(w http.ResponseWriter, c *Ctx, err error) error {
	st, ok := c.session.(*sessionState)
	if !ok {
		return err
	}
	return saveSession(w, st, err)
}

func saveSession(w http.ResponseWriter, st *sessionState, err error) error {
	st.mu.Lock()
	serr := st.save(false)
	if serr == nil {
		st.flushCookie(w.Header())
	} else {
		st.cookie = cookieNone
	}
	st.mu.Unlock()
	if serr == nil {
		return err
	}
	var re *RedirectError
	var rw *RewriteError
	if err == nil || errors.As(err, &re) || errors.As(err, &rw) || errors.Is(err, ErrNotFound) {
		return &sessionSaveError{serr}
	}
	log.Printf("lyr: saving session: %v", serr)
	return err
}

// ---- the route cache (PLAN.md §4i "Route caching") ----

// boundSession returns the session site.Sessions bound for r, or nil.
func (s *Site) boundSession(r *http.Request, c *Ctx) *sessionState {
	cfg := s.sess
	if cfg == nil || cfg.field == nil {
		return nil
	}
	var p **sessionState
	if c.locals != nil {
		p = cfg.field.of(c.locals)
	} else {
		p = cfg.field.attached(r)
	}
	if p == nil {
		return nil
	}
	return *p
}

// sentCookie reports whether st's cookie was written to the response.
func sentCookie(st *sessionState) bool {
	st.mu.Lock()
	out := st.out
	st.mu.Unlock()
	return out
}

// newBGSession is the session of a background revalidation (PLAN.md §4i
// "Route caching"): a state of its own, never saved. anon makes it an
// anonymous visitor's (no cookie read); otherwise it reads the
// triggering visitor's session, for a copy that varies by context. c
// owns it and releases it.
func newBGSession(c *Ctx, cfg *sessionConfig, r *http.Request, anon bool) *sessionState {
	st := acquireSession(cfg, r)
	st.ctx = c
	st.fl = sessBG
	if anon {
		st.fl |= sessAnon
	}
	c.session = st
	return st
}

// bindBGLocals binds the Session field of a revalidation's Locals copy
// (nil: none) to st, or unbinds it when st is nil.
func (s *Site) bindBGLocals(l any, st *sessionState) {
	if l == nil || s.sess == nil || s.sess.field == nil {
		return
	}
	if p := s.sess.field.of(l); p != nil {
		*p = st
	}
}

// cloneLocals gives a background revalidation's request r a shallow copy
// of the trigger's Locals (PLAN.md §4h, §4i), so the refresh never writes
// the trigger's: its Cache field is reset (the refresh seeds it from the
// snapshot) and its Session field unbound (the refresh binds its own
// state). It returns r and nil when no Locals with such a field is
// attached.
func (s *Site) cloneLocals(r *http.Request) (*http.Request, any) {
	var clone func(*http.Request) (*http.Request, any)
	switch {
	case s.cache != nil && s.cache.field != nil:
		clone = s.cache.field.clone
	case s.sessField != nil:
		clone = s.sessField.clone
	default:
		return r, nil
	}
	r, l := clone(r)
	if l == nil {
		return r, nil
	}
	if s.cache != nil && s.cache.field != nil {
		if k := s.cache.field.of(l); k != nil {
			*k = Cache{}
		}
	}
	if s.sess != nil && s.sessField != nil {
		if p := s.sessField.of(l); p != nil {
			*p = nil
		}
	}
	return r, l
}
