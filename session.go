package lyr

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"
)

// Sessions (PLAN.md §4i): per-visitor server-side state behind a random
// cookie, typed by the project's SessionData. The request flow (saving
// from pages and site.Sessions, the route-cache flag) is in
// session_serve.go; the stored format and the merge in session_json.go.

// Session is a request's session, typed by your SessionData. Pages and
// components get it as the implicit session; middleware and app handlers
// through a lyr.Session[SessionData] field in Locals, bound by
// site.Sessions:
//
//	type Locals struct {
//		Session lyr.Session[SessionData]
//		// …
//	}
//
// Its zero value is unbound: its methods return ErrSessionUnbound.
// Changes to the data are saved automatically at the end of the request.
type Session[D any] struct {
	st *sessionState // must stay the only field (SessionOf)
}

// SessionStore stores sessions: the bytes of each session's data under
// its ID. cache.NewSessionStore (github.com/orbit-z/lyr/cache) is an
// in-process one; a Redis store is a few lines (see the docs).
type SessionStore interface {
	// Get returns the stored bytes, or nil, nil when id is unknown or
	// expired. Lyr never modifies the returned slice.
	Get(ctx context.Context, id string) ([]byte, error)
	// Set stores data under id for ttl (0: no expiry). Lyr never modifies
	// data afterwards, so the store may keep it.
	Set(ctx context.Context, id string, data []byte, ttl time.Duration) error
	// Delete drops the session stored under id, if any.
	Delete(ctx context.Context, id string) error
}

// SessionOptions configure sessions; see WithSessions.
type SessionOptions struct {
	// TTL is a session's lifetime from its last save (passed to the
	// store's Set); 0 means no expiry. Reading a session doesn't extend
	// it.
	TTL    time.Duration
	Cookie SessionCookie
}

// SessionCookie configures the session cookie. HttpOnly is always set.
type SessionCookie struct {
	// Name defaults to "lyr-session"; letters, digits, '-' and '_' only.
	Name   string
	Domain string
	// Path defaults to "/".
	Path string
	// MaxAge defaults to 0: a browser-session cookie. When set, the
	// cookie is sent again, with a fresh Max-Age, on every save.
	MaxAge time.Duration
	// SameSite defaults to http.SameSiteLaxMode.
	SameSite http.SameSite
	// Insecure drops the Secure attribute in production. Under lyr dev
	// it's always dropped.
	Insecure bool
}

var (
	// ErrSessionsNotEnabled is returned by Session methods when the site
	// has no session store.
	ErrSessionsNotEnabled = errors.New("lyr: sessions are not enabled: configure a store with lyr.WithSessions in app.New()")
	// ErrSessionNotFound is returned by Session.Load for an ID with no
	// stored session.
	ErrSessionNotFound = errors.New("lyr: session not found")
	// ErrSessionUnbound is returned by the methods of a Session that no
	// request bound: a Locals field read by middleware that runs before
	// site.Sessions, or a component rendered by RenderComponent outside it.
	ErrSessionUnbound = errors.New("lyr: this lyr.Session isn't bound to a request: put site.Sessions before this middleware")
)

// SessionDecodeError is the error of a session whose stored bytes don't
// decode into SessionData, such as after a field changed kind. The stored
// session is left as it is; Destroy drops it.
type SessionDecodeError struct {
	ID  string // the session's ID (kept out of Error, so logs don't carry it)
	Err error
}

func (e *SessionDecodeError) Error() string {
	return "lyr: the stored session doesn't decode into SessionData: " + e.Err.Error()
}

func (e *SessionDecodeError) Unwrap() error { return e.Err }

// sessionStoreError wraps a store's error: "lyr: session store: …".
type sessionStoreError struct{ err error }

func (e *sessionStoreError) Error() string { return "lyr: session store: " + e.err.Error() }
func (e *sessionStoreError) Unwrap() error { return e.err }

func storeErr(err error) error { return &sessionStoreError{err} }

// sessionSaveError is a failed save at the end of a page or island
// request, which becomes the request's error.
type sessionSaveError struct{ err error }

func (e *sessionSaveError) Error() string { return "saving session: " + e.err.Error() }
func (e *sessionSaveError) Unwrap() error { return e.err }

// WithSessions turns sessions on with store and o. Pass it to app.New()
// in cmd/server/main.go:
//
//	store := cache.NewSessionStore(cache.SessionStoreOptions{MaxEntries: 100_000})
//	site := app.New(lyr.WithSessions(store, lyr.SessionOptions{TTL: 30 * 24 * time.Hour}))
//
// It panics when store is nil, when the cookie name has a character other
// than a letter, a digit, '-' or '_', for SameSite=None with Insecure
// (browsers drop such cookies), and for a cookie MaxAge between 0 and one
// second (Max-Age is whole seconds, and 0 deletes the cookie). With several WithSessions options, the
// last one wins.
func WithSessions(store SessionStore, o SessionOptions) Option {
	if store == nil {
		panic("lyr: WithSessions: the store is nil")
	}
	c := o.Cookie
	if c.Name == "" {
		c.Name = "lyr-session"
	}
	for i := 0; i < len(c.Name); i++ {
		b := c.Name[i]
		if !('a' <= b && b <= 'z' || 'A' <= b && b <= 'Z' || '0' <= b && b <= '9' || b == '-' || b == '_') {
			panic(fmt.Sprintf("lyr: WithSessions: cookie name %q: use letters, digits, '-' and '_' only", c.Name))
		}
	}
	if c.Path == "" {
		c.Path = "/"
	}
	if c.SameSite == 0 || c.SameSite == http.SameSiteDefaultMode {
		c.SameSite = http.SameSiteLaxMode
	}
	if c.SameSite == http.SameSiteNoneMode && c.Insecure {
		panic("lyr: WithSessions: SameSite=None needs Secure: browsers drop the cookie without it; drop Insecure or use SameSite Lax")
	}
	if o.TTL < 0 {
		panic("lyr: WithSessions: TTL must not be negative")
	}
	if c.MaxAge > 0 && c.MaxAge < time.Second {
		// Max-Age is whole seconds: it would be written as 0, which
		// deletes the cookie.
		panic(fmt.Sprintf("lyr: WithSessions: cookie MaxAge %v is under a second; Max-Age is whole seconds, and 0 deletes the cookie", c.MaxAge))
	}
	cfg := &sessionConfig{store: store, ttl: o.TTL, cookie: c}
	return func(so *siteOptions) { so.sessions = cfg }
}

// For generated code. WithLocalsSession tells the site where the
// lyr.Session field of your Locals is. The generated app.New() passes it,
// before the caller's options, when src/locals declares one:
//
//	lyr.WithLocalsSession(func(l *locals.Locals) *lyr.Session[locals.SessionData] { return &l.Session })
func WithLocalsSession[L, D any](field func(*L) *Session[D]) Option {
	ls := &localsSession{
		attached: func(r *http.Request) **sessionState {
			if l := LocalsFromRequest[L](r); l != nil {
				return &field(l).st
			}
			return nil
		},
		ensure: func(r *http.Request) (*http.Request, **sessionState) {
			r, l := EnsureLocals[L](r)
			return r, &field(l).st
		},
		of: func(l any) **sessionState {
			if l, ok := l.(*L); ok {
				return &field(l).st
			}
			return nil
		},
		clone: cloneLocals[L],
	}
	return func(o *siteOptions) { o.sessField = ls }
}

// localsSession is WithLocalsSession's accessor.
type localsSession struct {
	// attached returns the Session field of the request's attached *L, or
	// nil when no locals are attached.
	attached func(r *http.Request) **sessionState
	// ensure attaches a *L if none is (EnsureLocals) and returns its field.
	ensure func(r *http.Request) (*http.Request, **sessionState)
	// of returns the field of l when l is an *L, else nil.
	of func(l any) **sessionState
	// clone attaches a shallow copy of the request's *L (cloneLocals).
	clone func(r *http.Request) (*http.Request, any)
}

// sessionConfig is a site's session configuration (WithSessions), with
// the Locals accessor and the dev mode filled in by NewSite.
type sessionConfig struct {
	store  SessionStore
	ttl    time.Duration
	cookie SessionCookie
	secure bool
	field  *localsSession
}

// newSiteSessions returns the site's configuration: a copy of
// WithSessions' with the accessor and the Secure decision.
func newSiteSessions(o *siteOptions, dev bool) *sessionConfig {
	if o.sessions == nil {
		return nil
	}
	cfg := *o.sessions
	cfg.field = o.sessField
	cfg.secure = !cfg.cookie.Insecure && !dev
	return &cfg
}

// unboundSessions is the Ctx.session of a RenderComponent outside
// site.Sessions: its session is unbound.
var unboundSessions = new(sessionConfig)

// ---- per-type information ----

// sessionType is what Lyr knows about a SessionData type: its top-level
// JSON names, the encoding of its zero value and a pool of values.
type sessionType struct {
	typ    reflect.Type
	fields []string // top-level JSON names, as encoding/json gives them
	zero   []byte   // the encoding of a zero D
	pool   sync.Pool
}

var sessionTypes sync.Map // reflect.Type → *sessionType

// sessionTypeFor returns the information on t, computed once per type.
func sessionTypeFor(t reflect.Type) *sessionType {
	if v, ok := sessionTypes.Load(t); ok {
		return v.(*sessionType)
	}
	ti := &sessionType{typ: t, fields: jsonFieldNames(t)}
	ti.pool.New = func() any { return reflect.New(t).Interface() }
	zero, err := json.Marshal(reflect.New(t).Interface())
	if err != nil {
		panic(fmt.Sprintf("lyr: %v can't be a session's data: %v", t, err))
	}
	ti.zero = zero
	v, _ := sessionTypes.LoadOrStore(t, ti)
	return v.(*sessionType)
}

func (ti *sessionType) get() any { return ti.pool.Get() }

// reset zeroes the *D p.
func (ti *sessionType) reset(p any) { reflect.ValueOf(p).Elem().SetZero() }

func (ti *sessionType) put(p any) {
	ti.reset(p)
	ti.pool.Put(p)
}

// fieldIndex returns the index of the JSON name in ti.fields, or -1.
func (ti *sessionType) fieldIndex(name string) int {
	for i, f := range ti.fields {
		if f == name {
			return i
		}
	}
	return -1
}

// jsonFieldNames lists the top-level JSON object names encoding/json gives
// a struct type: the tag's name, else the field's; embedded structs
// without a tag name flattened; json:"-" skipped. Duplicates are kept once.
// A non-struct type has none.
func jsonFieldNames(t reflect.Type) []string {
	var names []string
	var walk func(t reflect.Type, depth int)
	walk = func(t reflect.Type, depth int) {
		if depth > 8 {
			return
		}
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			tag := f.Tag.Get("json")
			if tag == "-" {
				continue
			}
			name, _, _ := strings.Cut(tag, ",")
			ft := f.Type
			if ft.Kind() == reflect.Pointer {
				ft = ft.Elem()
			}
			if f.Anonymous && name == "" && ft.Kind() == reflect.Struct {
				walk(ft, depth+1)
				continue
			}
			if !f.IsExported() {
				continue
			}
			if name == "" {
				name = f.Name
			}
			if !containsString(names, name) {
				names = append(names, name)
			}
		}
	}
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() == reflect.Struct {
		walk(t, 0)
	}
	return names
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// ---- the per-request state ----

// Session state flags.
const (
	sessLoaded  = 1 << iota // the data is loaded (or reset by Destroy)
	sessBacked              // the session has stored data (the cookie's, or Load's)
	sessRegen               // Regenerate: write under id, delete oldID
	sessDestroy             // Destroy: delete delID and expire the cookie
	sessCookie              // Load: send the cookie even without a write
	sessSaved               // a store write or delete happened
	sessBG                  // a background revalidation: never saves
	sessSeeded              // lyrtest.WithSession: never pooled
	sessAnon                // a revalidation as an anonymous visitor: no cookie read
)

// Cookie actions pending on a state.
const (
	cookieNone = iota
	cookieSet
	cookieExpire
)

// sessionState is one request's session, pooled. Its methods run under mu.
type sessionState struct {
	self *sessionState // SessionOf's *Session[D] points here
	mu   sync.Mutex
	cfg  *sessionConfig // nil: sessions aren't enabled
	r    *http.Request
	ti   *sessionType
	// ctx is the Ctx that owns the state (and releases it), or nil when
	// site.Sessions does.
	ctx    *Ctx
	fl     uint16
	cookie uint8       // the pending cookie action
	out    bool        // a Set-Cookie was written to h
	h      http.Header // where Save writes the cookie, or nil
	id     string      // the session's ID, "" when it has none
	oldID  string      // Regenerate: the stored ID, deleted at save
	delID  string      // Destroy: the stored ID, deleted at save
	data   any         // *D, once loaded
	snap   []byte      // the stored bytes, or the zero encoding
	err    error       // the load's decode error, kept
}

var sessionPool = sync.Pool{New: func() any {
	st := new(sessionState)
	st.self = st
	return st
}}

func acquireSession(cfg *sessionConfig, r *http.Request) *sessionState {
	st := sessionPool.Get().(*sessionState)
	st.cfg, st.r = cfg, r
	return st
}

func releaseSession(st *sessionState) {
	if st.fl&sessSeeded != 0 {
		return // lyrtest.WithSession's: never pooled (the test's Locals keep it)
	}
	if st.data != nil && st.ti != nil {
		st.ti.put(st.data)
	}
	*st = sessionState{}
	st.self = st
	sessionPool.Put(st)
}

// sessionsOff is the state of every session on a site without
// WithSessions: its methods return ErrSessionsNotEnabled and it's never
// written.
var sessionsOff = func() *sessionState {
	st := new(sessionState)
	st.self = st
	return st
}()

// unboundSlot is what an unbound handle points to.
var unboundSlot *sessionState

// For generated code. SessionOf is the implicit session of a page or
// component: the Locals field bound by site.Sessions when it ran, else a
// session of the request's own. Every component of a request shares it.
func SessionOf[D any](c *Ctx) *Session[D] {
	return (*Session[D])(unsafe.Pointer(sessionSlot(c)))
}

// sessionSlot returns the *sessionState slot SessionOf's handle points to,
// resolving the request's state on first use.
func sessionSlot(c *Ctx) **sessionState {
	switch v := c.session.(type) {
	case *sessionState:
		return &v.self
	case *sessionConfig:
		if v == unboundSessions {
			return &unboundSlot
		}
		return &resolveSession(c, v).self
	}
	return &sessionsOff.self
}

// resolveSession makes c's session state: the Locals field's when
// site.Sessions bound it, else a pooled state of c's own. The state of
// c's own is never put in a Locals attached to the request: that Locals
// outlives c, and the pooled state would reach the next request through
// it. (A Locals that LocalsOf allocates is bound by bindLocalsSession.)
func resolveSession(c *Ctx, cfg *sessionConfig) *sessionState {
	if f := cfg.field; f != nil {
		var p **sessionState
		if c.locals != nil {
			p = f.of(c.locals)
		} else if c.Request != nil {
			p = f.attached(c.Request)
		}
		if p != nil && *p != nil {
			c.session = *p
			return *p
		}
	}
	return newPageSession(c, cfg)
}

// newPageSession makes a session state that c owns: the one a page that
// rewrote to this request handed over (PLAN.md §4j), else a pooled one.
func newPageSession(c *Ctx, cfg *sessionConfig) *sessionState {
	st := takeRewriteSession(c.Request)
	if st == nil {
		st = acquireSession(cfg, c.Request)
	}
	st.ctx = c
	c.session = st
	return st
}

// bindLocalsSession binds the Session field of a Locals that LocalsOf just
// allocated to the request's session.
func bindLocalsSession(c *Ctx, l any) {
	cfg, ok := c.session.(*sessionConfig)
	var st *sessionState
	switch {
	case ok && cfg != unboundSessions && cfg.field != nil:
		st = newPageSession(c, cfg)
	case !ok:
		st, _ = c.session.(*sessionState)
	}
	if st != nil && st.fl&sessSeeded != 0 {
		bindSeededSession(st, l) // lyrtest.WithSession: no accessor
		return
	}
	if st == nil || st.cfg == nil || st.cfg.field == nil {
		return
	}
	if p := st.cfg.field.of(l); p != nil && *p == nil {
		*p = st
	}
}

// releaseCtxSession releases c's session state if c owns it.
func releaseCtxSession(c *Ctx) {
	if st, ok := c.session.(*sessionState); ok && st.ctx == c {
		releaseSession(st)
	}
}

// sessionForFork resolves c's session before a server:concurrent child
// copies it, so the children share one state.
func sessionForFork(c *Ctx) any {
	if cfg, ok := c.session.(*sessionConfig); ok && cfg != unboundSessions {
		return resolveSession(c, cfg)
	}
	return c.session
}

// warnedNoSessions is set once ErrSessionsNotEnabled is logged.
var warnedNoSessions atomic.Bool

func sessionsNotEnabled() error {
	if !warnedNoSessions.Load() && warnedNoSessions.CompareAndSwap(false, true) {
		log.Print(ErrSessionsNotEnabled.Error())
	}
	return ErrSessionsNotEnabled
}

// beginSession checks st for a method call of a Session[D] (t is D) and
// locks it; the caller unlocks when err is nil. The methods are thin
// generic wrappers over these functions, so little generic code is
// instantiated in user packages.
func beginSession(st *sessionState, t reflect.Type) (*sessionState, error) {
	if st == nil {
		return nil, ErrSessionUnbound
	}
	if st.cfg == nil {
		return nil, sessionsNotEnabled()
	}
	st.mu.Lock()
	if st.ti == nil {
		st.ti = sessionTypeFor(t)
	} else if st.ti.typ != t {
		st.mu.Unlock()
		panic(fmt.Sprintf("lyr: this request's session holds a %v, not a %v; the code uses two SessionData types (run lyr generate)", st.ti.typ, t))
	}
	return st, nil
}

// Data returns the session's data, loading it on the first call of the
// request: a zero SessionData when the visitor has no session. Change the
// struct in place; Lyr saves the changes at the end of the request. The
// pointer is valid until the end of the request (don't keep it), and every
// call of the request returns the same one.
//
// A store error is returned as "lyr: session store: …", and stored bytes
// that don't decode as a *SessionDecodeError.
func (s *Session[D]) Data() (*D, error) {
	p, err := sessionData(s.st, reflect.TypeFor[D]())
	if err != nil {
		return nil, err
	}
	return p.(*D), nil
}

func sessionData(st *sessionState, t reflect.Type) (any, error) {
	st, err := beginSession(st, t)
	if err != nil {
		return nil, err
	}
	defer st.mu.Unlock()
	if err := st.load(); err != nil {
		return nil, err
	}
	return st.data, nil
}

// ID returns the session's ID, or "" when the visitor has no session
// (yet: a new session gets its ID when it's saved). Like Data, it loads
// the session; it returns "" when the store fails, and the stored
// session's ID when its data doesn't decode.
func (s *Session[D]) ID() string { return sessionID(s.st, reflect.TypeFor[D]()) }

func sessionID(st *sessionState, t reflect.Type) string {
	st, err := beginSession(st, t)
	if err != nil {
		return ""
	}
	defer st.mu.Unlock()
	st.load()
	return st.id
}

// Regenerate gives the session a new ID and keeps its data: at the end of
// the request, the data is saved under the new ID, the old ID is deleted
// and the new cookie sent. Call it when the visitor signs in, against
// session fixation.
func (s *Session[D]) Regenerate() error { return regenerateSession(s.st, reflect.TypeFor[D]()) }

func regenerateSession(st *sessionState, t reflect.Type) error {
	st, err := beginSession(st, t)
	if err != nil {
		return err
	}
	defer st.mu.Unlock()
	if err := st.load(); err != nil {
		return err
	}
	if st.fl&sessRegen == 0 && st.fl&sessBacked != 0 {
		st.oldID = st.id
	}
	st.id = newSessionID()
	st.fl |= sessRegen
	return nil
}

// Destroy ends the session: at the end of the request, its stored data is
// deleted and the cookie expired. Data then returns a zero SessionData;
// changing it starts a new session, with a new ID. It also drops a
// session whose data doesn't decode.
func (s *Session[D]) Destroy() { destroySession(s.st, reflect.TypeFor[D]()) }

func destroySession(st *sessionState, t reflect.Type) {
	st, err := beginSession(st, t)
	if err != nil {
		return
	}
	defer st.mu.Unlock()
	switch {
	case st.fl&sessRegen != 0:
		st.delID = st.oldID
	case st.fl&sessBacked != 0:
		st.delID = st.id
	case st.fl&sessLoaded == 0:
		st.delID = st.cookieID()
	}
	if st.data != nil {
		st.ti.reset(st.data)
	} else {
		st.data = st.ti.get()
	}
	st.fl = st.fl&^(sessBacked|sessRegen|sessCookie) | sessDestroy | sessLoaded
	st.id, st.oldID, st.snap, st.err = "", "", st.ti.zero, nil
}

// Load replaces the request's session with the stored session id, and
// sends its cookie at the end of the request; unsaved changes to the
// current one are dropped. An unknown id returns ErrSessionNotFound and
// changes nothing.
func (s *Session[D]) Load(id string) error { return loadSession(s.st, reflect.TypeFor[D](), id) }

func loadSession(st *sessionState, t reflect.Type, id string) error {
	st, err := beginSession(st, t)
	if err != nil {
		return err
	}
	defer st.mu.Unlock()
	if !validSessionID(id) {
		return ErrSessionNotFound
	}
	b, err := st.cfg.store.Get(st.r.Context(), id)
	if err != nil {
		return storeErr(err)
	}
	if b == nil {
		return ErrSessionNotFound
	}
	d := reflect.New(t).Interface()
	if err := json.Unmarshal(b, d); err != nil {
		return &SessionDecodeError{ID: id, Err: err}
	}
	// The old value isn't pooled again: the caller may still hold it.
	st.data, st.snap, st.id, st.oldID, st.err = d, b, id, "", nil
	st.fl = st.fl&^sessRegen | sessLoaded | sessBacked | sessCookie
	return nil
}

// Save saves the session now, and sets its cookie on the response when it
// can (in middleware and handlers under site.Sessions; a page's cookie is
// set when the page is written). Saving is otherwise automatic at the end
// of the request; call Save to handle its error, or before a response
// that site.Sessions can't delay. A later change is saved at the end of
// the request as usual.
func (s *Session[D]) Save() error { return saveSessionNow(s.st, reflect.TypeFor[D]()) }

func saveSessionNow(st *sessionState, t reflect.Type) error {
	st, err := beginSession(st, t)
	if err != nil {
		return err
	}
	defer st.mu.Unlock()
	if err := st.save(false); err != nil {
		return err
	}
	if st.h != nil {
		st.flushCookie(st.h)
	}
	return nil
}

// load loads the data on the first call; st is locked.
func (st *sessionState) load() error {
	if st.fl&sessLoaded != 0 {
		return st.err
	}
	id := st.cookieID()
	snap := st.ti.zero
	d := st.ti.get()
	if id != "" {
		b, err := st.cfg.store.Get(st.r.Context(), id)
		if err != nil {
			st.ti.pool.Put(d)
			return storeErr(err)
		}
		if b != nil {
			st.id = id
			st.fl |= sessBacked
			if err := json.Unmarshal(b, d); err != nil {
				st.ti.put(d)
				st.fl |= sessLoaded
				st.err = &SessionDecodeError{ID: id, Err: err}
				return st.err
			}
			snap = b
		}
	}
	st.data, st.snap = d, snap
	st.fl |= sessLoaded
	return nil
}

// cookieID returns the request's session cookie when it holds a valid
// session ID, else "". An anonymous revalidation has none.
func (st *sessionState) cookieID() string {
	if st.r == nil || st.fl&sessAnon != 0 {
		return ""
	}
	if id := cookieValue(st.r, st.cfg.cookie.Name); validSessionID(id) {
		return id
	}
	return ""
}

// saveCtx is the context of a save's store calls: the request's, without
// its cancellation (a visitor who hangs up doesn't lose a sign-in).
func (st *sessionState) saveCtx() context.Context {
	if st.r == nil {
		return context.Background()
	}
	return context.WithoutCancel(st.r.Context())
}

// save writes the session's changes to the store and records the cookie
// to send; st is locked. late is set after the response started: a save
// that needs a new cookie is then skipped and logged.
func (st *sessionState) save(late bool) error {
	cfg := st.cfg
	if cfg == nil || st.fl&(sessBG|sessSeeded) != 0 {
		return nil
	}
	var ctx context.Context
	if st.fl&sessDestroy != 0 {
		if st.delID != "" {
			ctx = st.saveCtx()
			if err := cfg.store.Delete(ctx, st.delID); err != nil {
				return storeErr(err)
			}
			st.delID = ""
			st.fl |= sessSaved
		}
		st.fl &^= sessDestroy
		if st.r != nil && cookieValue(st.r, cfg.cookie.Name) != "" {
			st.cookie = cookieExpire
		}
	}
	if st.fl&sessLoaded == 0 || st.err != nil || st.data == nil {
		return nil
	}
	enc, n, err := encodeSession(st.data)
	if err != nil {
		return err
	}
	defer encPool.Put(enc)
	var cbuf [32]bool
	changed := cbuf[:0]
	dirty := !bytes.Equal(n, st.snap)
	if dirty {
		changed, dirty = changedFields(st.ti, n, st.snap, cbuf[:0])
	}
	regen := st.fl&sessRegen != 0
	if !dirty && !regen {
		if st.fl&sessCookie != 0 {
			st.fl &^= sessCookie
			st.cookie = cookieSet
		}
		return nil
	}
	if ctx == nil {
		ctx = st.saveCtx()
	}
	switch {
	case st.fl&sessBacked == 0:
		// A new session (also a regenerated anonymous one): written whole,
		// unless it's still the zero value.
		if !dirty {
			return nil
		}
		if late {
			logLateSession()
			return nil
		}
		id := st.id
		if !regen || id == "" {
			id = newSessionID()
		}
		data := bytes.Clone(n)
		if err := cfg.store.Set(ctx, id, data, cfg.ttl); err != nil {
			return storeErr(err)
		}
		st.id, st.snap = id, data
		st.fl = st.fl&^sessRegen | sessBacked | sessSaved
		st.cookie = cookieSet
		return nil
	case regen:
		s, err := cfg.store.Get(ctx, st.oldID)
		if err != nil {
			return storeErr(err)
		}
		if s == nil && !dirty {
			// The old session is gone and nothing changed: no session.
			st.fl &^= sessBacked | sessRegen
			st.id, st.oldID = "", ""
			return nil
		}
		if late {
			logLateSession()
			return nil
		}
		out := mergeSession(st.ti, s, n, changed)
		if err := cfg.store.Set(ctx, st.id, out, cfg.ttl); err != nil {
			return storeErr(err)
		}
		if st.oldID != "" {
			if err := cfg.store.Delete(ctx, st.oldID); err != nil {
				return storeErr(err)
			}
		}
		st.oldID, st.snap = "", bytes.Clone(n)
		st.fl = st.fl&^(sessRegen|sessCookie) | sessSaved
		st.cookie = cookieSet
		return nil
	}
	s, err := cfg.store.Get(ctx, st.id)
	if err != nil {
		return storeErr(err)
	}
	if s == nil {
		// Gone since the load (expired, or deleted by another request):
		// the changed fields start a new session under a new ID.
		if late {
			logLateSession()
			return nil
		}
		id := newSessionID()
		out := mergeSession(st.ti, nil, n, changed)
		if err := cfg.store.Set(ctx, id, out, cfg.ttl); err != nil {
			return storeErr(err)
		}
		st.id, st.snap = id, bytes.Clone(n)
		st.fl = st.fl&^sessCookie | sessSaved
		st.cookie = cookieSet
		return nil
	}
	out := mergeSession(st.ti, s, n, changed)
	if err := cfg.store.Set(ctx, st.id, out, cfg.ttl); err != nil {
		return storeErr(err)
	}
	st.snap = bytes.Clone(n)
	st.fl |= sessSaved
	if cfg.cookie.MaxAge > 0 || st.fl&sessCookie != 0 {
		st.fl &^= sessCookie
		st.cookie = cookieSet
	}
	return nil
}

// dirty reports whether a save would write anything: for the route cache,
// where the request's session isn't saved before the store decision.
func (st *sessionState) dirty() bool {
	if st.fl&(sessDestroy|sessRegen|sessCookie) != 0 || st.cookie != cookieNone {
		return true
	}
	if st.fl&sessLoaded == 0 || st.err != nil || st.data == nil {
		return false
	}
	enc, n, err := encodeSession(st.data)
	if err != nil {
		return true
	}
	defer encPool.Put(enc)
	if bytes.Equal(n, st.snap) {
		return false
	}
	var cbuf [32]bool
	_, d := changedFields(st.ti, n, st.snap, cbuf[:0])
	return d
}

func logLateSession() {
	log.Print("lyr: session changed after the response was written; it needs a new cookie, so it isn't saved (call Save before writing)")
}

// flushCookie writes the pending cookie to h: one Set-Cookie for the
// session per response, replacing one written earlier.
func (st *sessionState) flushCookie(h http.Header) {
	if st.cookie == cookieNone {
		return
	}
	line := st.cookieLine(st.cookie == cookieExpire)
	st.cookie = cookieNone
	prefix := st.cfg.cookie.Name + "="
	vals := h["Set-Cookie"]
	if st.out {
		for i, v := range vals {
			if strings.HasPrefix(v, prefix) {
				vals[i] = line
				return
			}
		}
	}
	h["Set-Cookie"] = append(vals, line)
	st.out = true
}

// cookieLine is the Set-Cookie value for st.id, or the expired cookie.
func (st *sessionState) cookieLine(expire bool) string {
	c := &st.cfg.cookie
	b := make([]byte, 0, 128)
	b = append(b, c.Name...)
	b = append(b, '=')
	if !expire {
		b = append(b, st.id...)
	}
	b = append(b, "; Path="...)
	b = append(b, c.Path...)
	if c.Domain != "" {
		b = append(b, "; Domain="...)
		b = append(b, c.Domain...)
	}
	switch {
	case expire:
		b = append(b, "; Max-Age=0"...)
	case c.MaxAge > 0:
		b = append(b, "; Max-Age="...)
		b = strconv.AppendInt(b, int64(c.MaxAge/time.Second), 10)
	}
	b = append(b, "; HttpOnly"...)
	if st.cfg.secure {
		b = append(b, "; Secure"...)
	}
	switch c.SameSite {
	case http.SameSiteStrictMode:
		b = append(b, "; SameSite=Strict"...)
	case http.SameSiteNoneMode:
		b = append(b, "; SameSite=None"...)
	default:
		b = append(b, "; SameSite=Lax"...)
	}
	return string(b)
}

// ---- IDs and cookies ----

// newSessionID returns a random UUID v4 (122 bits from crypto/rand).
func newSessionID() string {
	var b [16]byte
	rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	const hex = "0123456789abcdef"
	var out [36]byte
	j := 0
	for i, c := range b {
		if i == 4 || i == 6 || i == 8 || i == 10 {
			out[j] = '-'
			j++
		}
		out[j], out[j+1] = hex[c>>4], hex[c&0xf]
		j += 2
	}
	return string(out[:])
}

// validSessionID reports whether s is a lowercase UUID v4.
func validSessionID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i := 0; i < 36; i++ {
		c := s[i]
		switch i {
		case 8, 13, 18, 23:
			if c != '-' {
				return false
			}
		case 14:
			if c != '4' {
				return false
			}
		case 19:
			if c != '8' && c != '9' && c != 'a' && c != 'b' {
				return false
			}
		default:
			if !('0' <= c && c <= '9' || 'a' <= c && c <= 'f') {
				return false
			}
		}
	}
	return true
}

// cookieValue returns the value of the first cookie named name in r's
// Cookie headers, or "". It doesn't allocate.
func cookieValue(r *http.Request, name string) string {
	for _, line := range r.Header["Cookie"] {
		for line != "" {
			var part string
			part, line, _ = strings.Cut(line, ";")
			part = strings.TrimLeft(part, " \t")
			if len(part) > len(name) && part[len(name)] == '=' && part[:len(name)] == name {
				v := strings.TrimRight(part[len(name)+1:], " \t")
				if len(v) > 1 && v[0] == '"' && v[len(v)-1] == '"' {
					v = v[1 : len(v)-1]
				}
				return v
			}
		}
	}
	return ""
}

// ---- encoding ----

// sessionEnc is a pooled encoder: encoding a session at the end of a
// request doesn't allocate for common types.
type sessionEnc struct {
	buf bytes.Buffer
	enc *json.Encoder
}

var encPool = sync.Pool{New: func() any {
	e := new(sessionEnc)
	e.enc = json.NewEncoder(&e.buf)
	return e
}}

// encodeSession encodes v like json.Marshal; the bytes are e's until e
// goes back to encPool.
func encodeSession(v any) (*sessionEnc, []byte, error) {
	e := encPool.Get().(*sessionEnc)
	e.buf.Reset()
	if err := e.enc.Encode(v); err != nil {
		encPool.Put(e)
		return nil, nil, fmt.Errorf("lyr: encoding the session: %w", err)
	}
	b := e.buf.Bytes()
	if n := len(b); n > 0 && b[n-1] == '\n' {
		b = b[:n-1]
	}
	return e, b, nil
}
