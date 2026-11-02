package lyr

import (
	"encoding/binary"
	"fmt"
	"hash/maphash"
	"net/http"
	"path"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"
)

// The route cache's request flow (PLAN.md §4h): keys, lookup, store,
// stale-while-revalidate, miss coalescing and conditional requests. It runs
// inside site.Middleware, after the route match, only when the site has a
// provider and isn't in dev (Site.cache != nil); otherwise the serve path is
// the one in site.go.

// siteCache is a site's cache state, built by NewSite from WithCache.
type siteCache struct {
	site     *Site
	prov     CacheProvider
	store    CacheStore        // nil for a header-only provider
	hdr      CacheHeaderWriter // nil unless prov implements it (and no store)
	query    queryRules
	rules    *router        // the compiled route rules, or nil
	ruleOpts []CacheOptions // by compiledRoute.idx
	field    *localsCache   // WithLocalsCache, or nil

	mu      sync.Mutex
	flights map[string]*cacheFlight
}

// cacheNow is the route cache's clock; tests replace it.
var cacheNow = time.Now

// testFlightWaitHook, when set by a test, is called when a miss starts
// waiting for another request's render.
var testFlightWaitHook func()

// cacheState is a route's (an island's) cache state: the
// per-route bit that gates the lookup, and the refusal warnings given.
type cacheState struct {
	stored atomic.Bool   // a response of this route was stored once
	warned atomic.Uint32 // refusal reasons already logged (1 << reason)
}

// cacheFlight is one render that concurrent misses on its key wait for.
type cacheFlight struct {
	key  string // the key it was joined under (owned)
	done chan struct{}
	// Set before done is closed: the stored entry (nil if nothing was
	// stored), the key it was stored under and its Vary names.
	e    *CachedResponse
	ekey string
	vary []string
}

// cacheReq is one request's state in the flow; it lives on the stack.
type cacheReq struct {
	k       *Cache
	buf     *[]byte // key storage (the Ctx's)
	path    string  // the escaped request path
	state   *cacheState
	get     bool // a GET that may be looked up and stored
	keyed   bool
	primary CacheKey // host + path + query
	key     CacheKey // primary, or the variant under a Vary marker
	flight  *cacheFlight
	// storedVary is the Vary names of the entry this request stored.
	storedVary []string
	// off is set when the handle was disabled before the lookup: no store
	// and no cache headers for this request.
	off bool
	// pre is a pooled copy of the options middleware set before the
	// lookup, taken by serveCachedPage when the chain has no
	// site.Rewrites (all middleware runs once), or nil: a page rewrite
	// restores them for its target (PLAN.md §4j). With site.Rewrites, the
	// copy is the handle's pre, taken at the marker.
	pre *Cache
	// bg is set for a background revalidation: no lookup, and the handle
	// starts as snap (the triggering request's options).
	bg   bool
	snap *Cache
	// origin, for a revalidation: the page route (nil: an island).
	route *compiledRoute
	// marker is the Vary names of the live marker the lookup went through,
	// or nil: a store keeps its context names (put).
	marker []string
	// For a revalidation: locals is its shallow copy of the trigger's
	// Locals (cloneLocals), or nil; anon is set when the stale copy
	// doesn't vary by context, so it renders as an anonymous visitor.
	locals any
	anon   bool
	// sess is the session site.Sessions bound for the request, or nil;
	// sessCookie is set when the response sends the session's cookie (a
	// new, regenerated or destroyed session): it's never stored (PLAN.md
	// §4i "Route caching").
	sess       *sessionState
	sessCookie bool
}

func newSiteCache(s *Site, o *siteOptions, rules *router) *siteCache {
	sc := &siteCache{site: s, prov: o.cache, rules: rules, field: o.cacheField, flights: make(map[string]*cacheFlight)}
	for _, r := range o.rules {
		sc.ruleOpts = append(sc.ruleOpts, r.Options)
	}
	if st, ok := o.cache.(CacheStore); ok {
		sc.store = st
	} else if hw, ok := o.cache.(CacheHeaderWriter); ok {
		sc.hdr = hw
	}
	var q CacheQuery
	if qp, ok := o.cache.(interface{ CacheQuery() CacheQuery }); ok {
		q = qp.CacheQuery()
	}
	sc.query = newQueryRules(q)
	return sc
}

// compileRules validates the route rules and compiles them into a router,
// or returns nil without rules. An invalid pattern panics.
func compileRules(rules []RouteRule, always bool) *router {
	if len(rules) == 0 {
		return nil
	}
	routes := make([]Route, len(rules))
	for i, r := range rules {
		if !strings.HasPrefix(r.Pattern, "/") {
			panic(fmt.Sprintf("lyr: WithRouteRules: pattern %q must start with /", r.Pattern))
		}
		routes[i] = Route{Pattern: r.Pattern, File: "lyr.json routeRules"}
	}
	rt, err := newRouter(routes)
	if err != nil {
		panic(err)
	}
	rt.always = always
	return rt
}

// ---- keys ----

// trackingParams are the query parameters Astro's memoryCache leaves out
// of keys by default.
var trackingParams = []string{"utm_*", "fbclid", "gclid", "gbraid", "wbraid", "dclid", "msclkid", "twclid",
	"li_fat_id", "mc_cid", "mc_eid", "_ga", "_gl", "_hsenc", "_hsmi", "_ke", "oly_anon_id", "oly_enc_id",
	"rb_clickid", "s_cid", "vero_id", "wickedid", "yclid", "__s", "ref"}

type queryRules struct {
	include, exclude *nameMatcher
	sort             bool
}

func newQueryRules(q CacheQuery) queryRules {
	if len(q.Include) > 0 && len(q.Exclude) > 0 {
		panic("lyr: CacheQuery: Include and Exclude can't both be set")
	}
	r := queryRules{sort: !q.NoSort}
	switch {
	case len(q.Include) > 0:
		r.include = newNameMatcher(q.Include)
	case q.Exclude != nil:
		r.exclude = newNameMatcher(q.Exclude)
	default:
		r.exclude = newNameMatcher(trackingParams)
	}
	return r
}

// nameMatcher matches query parameter names against path.Match patterns,
// ignoring ASCII case.
type nameMatcher struct {
	exact    map[string]bool
	prefixes []string // "utm_*" patterns, as "utm_"
	globs    []string // any other pattern
}

func newNameMatcher(patterns []string) *nameMatcher {
	m := &nameMatcher{exact: map[string]bool{}}
	for _, p := range patterns {
		p = strings.ToLower(p)
		if _, err := path.Match(p, ""); err != nil {
			panic(fmt.Sprintf("lyr: CacheQuery: bad pattern %q: %v", p, err))
		}
		if pre, ok := strings.CutSuffix(p, "*"); ok && !strings.ContainsAny(pre, `*?[\`) {
			m.prefixes = append(m.prefixes, pre)
		} else if strings.ContainsAny(p, `*?[\`) {
			m.globs = append(m.globs, p)
		} else {
			m.exact[p] = true
		}
	}
	return m
}

func (m *nameMatcher) match(name string) bool {
	if m == nil {
		return false
	}
	var buf [64]byte
	lower := name
	if name != "" && len(name) <= len(buf) {
		b := buf[:len(name)]
		for i := 0; i < len(name); i++ {
			c := name[i]
			if 'A' <= c && c <= 'Z' {
				c += 'a' - 'A'
			}
			b[i] = c
		}
		lower = unsafe.String(&b[0], len(b))
	} else if name != "" {
		lower = strings.ToLower(name)
	}
	if m.exact[lower] {
		return true
	}
	for _, pre := range m.prefixes {
		if strings.HasPrefix(lower, pre) && !strings.Contains(lower[len(pre):], "/") {
			return true
		}
	}
	for _, g := range m.globs {
		if ok, _ := path.Match(g, lower); ok {
			return true
		}
	}
	return false
}

func (q *queryRules) keep(name string) bool {
	if q.include != nil {
		return q.include.match(name)
	}
	return !q.exclude.match(name)
}

// appendQuery appends the normalized query ("?a=1&b=2", or nothing) to b.
// Up to 16 parameters are sorted on the stack.
func (q *queryRules) appendQuery(b []byte, raw string) []byte {
	if raw == "" {
		return b
	}
	var arr [16]string
	parts := arr[:0]
	for raw != "" {
		var p string
		p, raw, _ = strings.Cut(raw, "&")
		if p == "" {
			continue
		}
		name, _, _ := strings.Cut(p, "=")
		if q.keep(name) {
			parts = append(parts, p)
		}
	}
	if q.sort && len(parts) > 1 {
		slices.SortStableFunc(parts, compareParamNames)
	}
	for i, p := range parts {
		if i == 0 {
			b = append(b, '?')
		} else {
			b = append(b, '&')
		}
		b = append(b, p...)
	}
	return b
}

func compareParamNames(a, b string) int {
	a, _, _ = strings.Cut(a, "=")
	b, _, _ = strings.Cut(b, "=")
	return strings.Compare(a, b)
}

// maxKeyBuf is the largest key buffer a pooled Ctx keeps.
const maxKeyBuf = 4 << 10

// primaryKey builds p's primary key in p.buf.
func (sc *siteCache) primaryKey(r *http.Request, p *cacheReq) CacheKey {
	b := (*p.buf)[:0]
	b = append(b, r.Host...)
	b = append(b, p.path...)
	b = sc.query.appendQuery(b, r.URL.RawQuery)
	*p.buf = b
	return sc.keyOf(b)
}

func (sc *siteCache) keyOf(b []byte) CacheKey {
	if len(b) == 0 {
		return CacheKey{hash: maphash.String(cacheSeed, "")}
	}
	s := unsafe.String(&b[0], len(b))
	return CacheKey{hash: maphash.String(cacheSeed, s), s: s}
}

// ctxVaryPrefix marks a context key among a Vary marker's names
// ("@user" for VaryBy "user"): '@' can't start a header name, and
// varyNames drops such names from a response's Vary header.
const ctxVaryPrefix = "@"

// varyKey builds the variant key of p's primary key for the Vary names,
// after the primary key in p.buf (the primary key's bytes stay as they are).
// A header name adds "\x00Name:values"; a context key (ctxVaryPrefix)
// adds "\x00@", then the key and the request's value for it, each
// length-prefixed so no value can forge another combination.
func (sc *siteCache) varyKey(r *http.Request, p *cacheReq, names []string) CacheKey {
	b := (*p.buf)[:len(p.primary.s)]
	for _, name := range names {
		b = append(b, 0)
		if key, ok := strings.CutPrefix(name, ctxVaryPrefix); ok {
			v := contextValue(p.k, key)
			b = append(b, ctxVaryPrefix...)
			b = binary.AppendUvarint(b, uint64(len(key)))
			b = append(b, key...)
			b = binary.AppendUvarint(b, uint64(len(v)))
			b = append(b, v...)
			continue
		}
		b = append(b, name...)
		b = append(b, ':')
		for i, v := range r.Header[name] {
			if i > 0 {
				b = append(b, ',')
			}
			b = append(b, v...)
		}
	}
	*p.buf = b
	return sc.keyOf(b)
}

// ensureKey computes p's primary key if the lookup didn't.
func (sc *siteCache) ensureKey(r *http.Request, p *cacheReq) {
	if !p.keyed {
		p.primary = sc.primaryKey(r, p)
		p.key = p.primary
		p.keyed = true
	}
}

// ---- the handle ----

// attach makes the request's handle: the Locals field when middleware
// attached locals (WithLocalsCache), else the Ctx's own; for a background
// revalidation, the field of its Locals copy (or the Ctx's own) seeded
// with the trigger's options.
func (sc *siteCache) attach(r *http.Request, c *Ctx, p *cacheReq) *Cache {
	if p.bg {
		if l := p.locals; l != nil {
			c.locals = l
			if sc.field != nil {
				c.cacheField = sc.field.field
				c.cacheP = sc.field.of(l)
			}
		}
		copyCache(c.Cache(), p.snap)
	} else if sc.field != nil {
		c.cacheField = sc.field.field
		var lk *Cache
		if c.locals != nil {
			lk = sc.field.of(c.locals)
		} else if l, f := sc.field.attached(r); f != nil {
			lk, c.locals = f, l
		}
		if lk != nil {
			c.cacheP = lk
		}
	}
	k := c.Cache()
	k.mode, k.site = cacheModeOn, sc
	return k
}

// begin starts the flow for a GET page or island request: it applies a
// route rule and, on a route known to cache, looks the request up. It
// reports whether the response was served.
func (sc *siteCache) begin(w http.ResponseWriter, r *http.Request, p *cacheReq, rules bool) bool {
	// From here on, the context values are the key's (Cache.Context).
	p.k.flags |= cacheLooked
	if p.bg {
		p.get = true
		return false
	}
	if p.k.state&cacheDisabled != 0 {
		// Disabled before the lookup (by middleware): the request stays
		// out of the cache even if the page calls Set again.
		p.off = true
		p.k.flags |= cacheOff // for a rewrite from the 404 page (PLAN.md §4j)
		return false
	}
	if p.k.flags&cacheLate != 0 {
		// A rewrite's target after a late Context change: the values the
		// key would use aren't the request's, so no lookup (and no store).
		return false
	}
	if r.Method != http.MethodGet {
		return false
	}
	p.get = true
	known := p.state.stored.Load()
	if rules && sc.rules != nil {
		if cr, _ := sc.rules.find(p.path); cr != nil {
			applyRule(p.k, &sc.ruleOpts[cr.idx])
			known = true
		}
	}
	if !known || sc.store == nil {
		return false
	}
	return sc.lookup(w, r, p)
}

// lookup serves p from the store: a fresh entry as a HIT, a stale one as
// STALE (starting its revalidation), or, on a miss, the result of a render
// already running for the key. It reports whether it served the request;
// otherwise p.flight may be set: this request renders for the waiters.
func (sc *siteCache) lookup(w http.ResponseWriter, r *http.Request, p *cacheReq) bool {
	sc.ensureKey(r, p)
	now := cacheNow()
	e := sc.store.Get(p.primary)
	if e != nil && e.Vary != nil {
		if e.state(now) == entryExpired {
			e = nil
		} else {
			p.marker = e.Vary
			p.key = sc.varyKey(r, p, e.Vary)
			e = sc.store.Get(p.key)
		}
	}
	if e != nil && e.Vary == nil {
		switch e.state(now) {
		case entryFresh:
			serveEntry(w, r, e, cacheHit)
			return true
		case entryStale:
			if e.revalidating.CompareAndSwap(false, true) {
				sc.startRevalidation(r, p, e)
			}
			serveEntry(w, r, e, cacheStale)
			return true
		}
	}
	f, leader := sc.joinFlight(p.key.s)
	if leader {
		p.flight = f
		return false
	}
	if testFlightWaitHook != nil {
		testFlightWaitHook()
	}
	select {
	case <-f.done:
	case <-r.Context().Done():
		emptyStatus(w, http.StatusServiceUnavailable)
		return true
	}
	if f.e == nil {
		return false
	}
	key := p.primary
	if f.vary != nil {
		key = sc.varyKey(r, p, f.vary)
	}
	if key.s != f.ekey {
		return false
	}
	serveEntry(w, r, f.e, cacheHit)
	return true
}

// beginSession sets up p's session for the flow: a revalidation gets a
// session of its own, never saved (anonymous unless the stale copy
// varies by context), which its Locals copy's Session field shares; other
// requests note the session site.Sessions bound.
func (sc *siteCache) beginSession(r *http.Request, c *Ctx, p *cacheReq) {
	s := sc.site
	if s.sess == nil {
		return
	}
	if p.bg {
		st := newBGSession(c, s.sess, r, p.anon)
		s.bindBGLocals(p.locals, st)
		return
	}
	p.sess = s.boundSession(r, c)
}

// endSession saves a page or island session before the store decision
// (not for a revalidation) and records on p whether the response sends
// the session cookie.
func (sc *siteCache) endSession(w http.ResponseWriter, c *Ctx, p *cacheReq, err error) error {
	if sc.site.sess == nil {
		return err
	}
	st, _ := c.session.(*sessionState)
	if st == nil {
		st = p.sess
	}
	if st == nil {
		return err
	}
	if !p.bg {
		err = saveSession(w, st, err)
		p.sessCookie = sentCookie(st)
	}
	return err
}

func (sc *siteCache) joinFlight(key string) (*cacheFlight, bool) {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	if f := sc.flights[key]; f != nil {
		return f, false
	}
	f := &cacheFlight{key: strings.Clone(key), done: make(chan struct{})}
	sc.flights[f.key] = f
	return f, true
}

// finishFlight ends p's flight, if it leads one, with the stored entry e
// (nil: nothing stored, the waiters render their own).
func (sc *siteCache) finishFlight(p *cacheReq, e *CachedResponse) {
	f := p.flight
	if f == nil {
		return
	}
	p.flight = nil
	if e != nil {
		f.e, f.ekey, f.vary = e, strings.Clone(p.key.s), p.storedVary
	}
	sc.mu.Lock()
	delete(sc.flights, f.key)
	sc.mu.Unlock()
	close(f.done)
}
