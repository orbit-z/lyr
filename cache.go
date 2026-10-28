package orbit

import (
	"context"
	"errors"
	"hash/maphash"
	"log"
	"net/http"
	"slices"
	"strings"
	"sync/atomic"
	"time"
)

// Route caching (PLAN.md §4h): the request's cache handle, the options a
// page sets, the provider interfaces and the site options. The request flow
// (lookup, store, stale-while-revalidate, 304s) is in cache_serve.go.

// CacheOptions are a response's cache settings, set with ctx.Cache().Set or
// a route rule in orbit.json (Astro's CacheOptions).
type CacheOptions struct {
	// MaxAge is how long a stored response stays fresh. A response is
	// stored only when it's positive. Durations are whole seconds on the
	// wire (CDN headers): sub-second values are truncated.
	MaxAge time.Duration
	// SWR (stale-while-revalidate) is how long after MaxAge a stale copy is
	// still served while one background render refreshes it.
	SWR time.Duration
	// Tags label the response for InvalidateOptions.Tags.
	Tags []string
	// ETag is a quoted entity tag, `"v1"` or `W/"v1"`, sent as the ETag
	// header. Without one, a stored response gets an automatic one.
	ETag string
	// LastModified is sent as the Last-Modified header.
	LastModified time.Time
}

// InvalidateOptions select the stored responses to drop.
type InvalidateOptions struct {
	// Tags drops every response carrying any of them.
	Tags []string
	// Path is an escaped request path, such as /blog/hello: it drops every
	// stored variant of that path (any query, any host).
	Path string
}

// ErrCacheNotEnabled is returned by Invalidate when the site has no cache
// provider (orbit.WithCache).
var ErrCacheNotEnabled = errors.New("orbit: caching is not enabled; configure a provider with orbit.WithCache")

// Cache is a request's cache handle: ctx.Cache() in pages and components,
// or a field of this type in your Locals for middleware:
//
//	type Locals struct {
//		Cache orbit.Cache
//		// …
//	}
//
// Set merges options, Disable keeps the request out of the cache. The zero
// value is ready to use. A Cache belongs to one request: don't share it
// between goroutines.
type Cache struct {
	maxAge, swr int64 // nanoseconds; 0: unset
	lastMod     int64 // Unix nanoseconds; 0: unset
	etag        string
	n           uint8     // tags in tagBuf, when more is nil
	state       uint8     // cacheSet, cacheDisabled
	mode        uint8     // cacheModeOff, cacheModeOn, cacheModeDev
	tagBuf      [4]string // the tags, when there are at most 4
	more        []string  // the tags, when there are more than 4
	// site is the *siteCache serving the request (cacheModeOn), kept as
	// any so the provider types stay out of every generated package's type
	// graph (PLAN.md §4b).
	site any
}

const (
	cacheSet      = 1 << iota // Set was called (and no Disable since)
	cacheDisabled             // Disable was called (and no Set since)
)

const (
	cacheModeOff = iota // no provider, or Orbit hasn't seen the request yet
	cacheModeOn         // a provider is configured, not in dev
	cacheModeDev        // orbit dev: caching is off
)

// Set merges o into the request's options, with Astro's rules: MaxAge, SWR
// and ETag take the last non-zero value, LastModified the latest time, and
// Tags accumulate (duplicates are dropped). It also cancels an earlier
// Disable. A route rule from orbit.json is applied before the page runs,
// so the page's Set merges over it.
//
// Set records the options even when the site has no provider; Orbit then
// logs a warning once.
func (k *Cache) Set(o CacheOptions) {
	var lm int64
	if !o.LastModified.IsZero() {
		lm = o.LastModified.UnixNano()
	}
	// Separate parameters: storing a field of o would make the caller's
	// Tags literal escape (escape analysis is field-insensitive).
	mergeOpts(k, int64(o.MaxAge), int64(o.SWR), o.ETag, lm, o.Tags)
}

func mergeOpts(k *Cache, maxAge, swr int64, etag string, lastMod int64, tags []string) {
	k.state = cacheSet
	if maxAge != 0 {
		k.maxAge = maxAge
	}
	if swr != 0 {
		k.swr = swr
	}
	if etag != "" && etag != k.etag {
		// A copy: keeping the parameter would make Set's o, and so the
		// caller's Tags literal, escape.
		k.etag = strings.Clone(etag)
	}
	if lastMod > k.lastMod {
		k.lastMod = lastMod
	}
	for _, t := range tags {
		addTag(k, t)
	}
}

func addTag(k *Cache, t string) {
	if slices.Contains(tagsOf(k), t) {
		return
	}
	switch {
	case len(k.more) > 0:
		k.more = append(k.more, t)
	case int(k.n) < len(k.tagBuf):
		k.tagBuf[k.n] = t
		k.n++
	default:
		k.more = append(append(k.more[:0], k.tagBuf[:]...), t)
		clear(k.tagBuf[:])
		k.n = 0
	}
}

// tags returns the handle's tags; the slice is the handle's storage.
func tagsOf(k *Cache) []string {
	if len(k.more) > 0 {
		return k.more
	}
	return k.tagBuf[:k.n]
}

// Disable keeps the request out of the cache (Astro's set(false)): it
// clears the options and tags, and Orbit neither looks the request up nor
// stores its response. Call it in middleware for per-user requests (a
// signed-in visitor), before Orbit's lookup. A later Set enables the
// handle again, but a request disabled before the lookup stays out of the
// cache: it isn't stored and gets no cache headers.
func (k *Cache) Disable() {
	clearOptions(k)
	k.state = cacheDisabled
}

func clearOptions(k *Cache) {
	k.maxAge, k.swr, k.lastMod, k.etag = 0, 0, 0, ""
	clear(k.tagBuf[:k.n])
	k.n = 0
	clear(k.more)
	k.more = k.more[:0]
}

// Enabled reports whether this request can be cached: a provider is
// configured, the site isn't running under orbit dev, and Disable wasn't
// called. A Locals field reports false in middleware until Orbit has seen
// the request (after next is called).
func (k *Cache) Enabled() bool {
	return k.mode == cacheModeOn && k.state&cacheDisabled == 0
}

// Options returns a snapshot of the merged options; Tags is a copy.
func (k *Cache) Options() CacheOptions {
	o := CacheOptions{MaxAge: time.Duration(k.maxAge), SWR: time.Duration(k.swr), ETag: k.etag}
	if t := tagsOf(k); len(t) > 0 {
		o.Tags = slices.Clone(t)
	}
	if k.lastMod != 0 {
		o.LastModified = time.Unix(0, k.lastMod)
	}
	return o
}

// Invalidate drops stored responses through the site's provider, like
// Site.InvalidateCache. It returns ErrCacheNotEnabled without a provider,
// and nil under orbit dev. A Locals field reaches the provider only once
// Orbit has seen the request; in middleware, use Site.InvalidateCache.
func (k *Cache) Invalidate(ctx context.Context, o InvalidateOptions) error {
	switch k.mode {
	case cacheModeDev:
		return nil
	case cacheModeOn:
		return k.site.(*siteCache).prov.Invalidate(ctx, o)
	}
	return ErrCacheNotEnabled
}

// active reports whether the handle produces cache headers (Astro: not
// disabled, and a MaxAge or tags).
func cacheActive(k *Cache) bool {
	return k.state&cacheDisabled == 0 && (k.maxAge > 0 || len(tagsOf(k)) > 0)
}

// storable reports whether the options ask for the response to be stored.
func cacheStorable(k *Cache) bool {
	return k.state&cacheDisabled == 0 && k.maxAge > 0
}

// applyRule applies a route rule under the options already set (by
// middleware): unset scalars take the rule's, tags accumulate. A disabled
// handle stays disabled.
func applyRule(k *Cache, o *CacheOptions) {
	if k.state&cacheDisabled != 0 {
		return
	}
	k.state = cacheSet
	if k.maxAge == 0 {
		k.maxAge = int64(o.MaxAge)
	}
	if k.swr == 0 {
		k.swr = int64(o.SWR)
	}
	for _, t := range o.Tags {
		addTag(k, t)
	}
}

// mergeFrom applies a server:concurrent child's options to k, at the join.
func mergeCache(k *Cache, c *Cache) {
	if c.state&cacheDisabled != 0 {
		k.Disable()
	}
	if c.state&cacheSet != 0 {
		mergeOpts(k, c.maxAge, c.swr, c.etag, c.lastMod, tagsOf(c))
	}
}

// copyFrom makes k a copy of c's options and state, with its own tag
// storage; k's mode and site are kept.
func copyCache(k *Cache, c *Cache) {
	if k == c {
		return
	}
	clearOptions(k)
	k.state = 0
	if c.state&cacheDisabled != 0 {
		k.state = cacheDisabled
	}
	if c.state&cacheSet != 0 {
		mergeOpts(k, c.maxAge, c.swr, c.etag, c.lastMod, tagsOf(c))
	}
}

// release resets a pooled Ctx's handle, keeping the overflow tag slice.
func releaseCache(k *Cache) {
	m := k.more
	clear(m)
	*k = Cache{more: m[:0]}
}

// Cache returns the request's cache handle. When your Locals has an
// orbit.Cache field, it's that field (so middleware's Set and Disable apply);
// otherwise the Ctx's own. Every component of a request shares it.
func (c *Ctx) Cache() *Cache {
	if c.cacheP != nil {
		return c.cacheP
	}
	if c.dev {
		c.cache.mode = cacheModeDev
	}
	return &c.cache
}

// warnedNoProvider is set once the missing-provider warning is logged.
var warnedNoProvider atomic.Bool

// warnNoProvider logs, once per process, that a request set cache options
// on a site without a provider (Astro warns once too).
func warnNoProvider() {
	if !warnedNoProvider.Load() && warnedNoProvider.CompareAndSwap(false, true) {
		log.Print("orbit: ctx.Cache().Set was called, but caching is not enabled: configure a provider with orbit.WithCache in app.New()")
	}
}

// CacheProvider is a route-cache provider, configured with WithCache. A
// provider is a store (CacheStore, Astro's runtime providers such as
// memoryCache), a header writer (CacheHeaderWriter, for CDNs), or neither:
// then Orbit writes Astro's default headers (CDN-Cache-Control, Cache-Tag,
// Last-Modified, ETag) and the CDN does the caching. Orbit runs the
// request flow (keys, freshness, stale-while-revalidate, Vary, the
// Set-Cookie refusal, 304s, X-Orbit-Cache) for every provider.
type CacheProvider interface {
	// Invalidate drops the responses o selects.
	Invalidate(ctx context.Context, o InvalidateOptions) error
}

// CacheHeaderWriter is a provider that turns the options into its CDN's
// headers (Astro's setHeaders). Orbit calls WriteCacheHeaders on a
// response whose options are active (a MaxAge or tags, not disabled), after
// the page's own headers are set; o is read-only.
type CacheHeaderWriter interface {
	CacheProvider
	WriteCacheHeaders(h http.Header, o *CacheOptions, r *http.Request)
}

// CacheStore is a provider that stores responses (Astro's onRequest
// providers): Orbit looks each cacheable GET request up with Get, and
// stores a response with Put. With a store, Orbit never writes
// CDN-Cache-Control or Cache-Tag.
//
// A store may also implement CacheQuery() CacheQuery to choose which query
// parameters are part of the key.
type CacheStore interface {
	CacheProvider
	// Get returns the response stored under key, or nil. Orbit checks
	// its freshness. key is valid only during the call.
	Get(key CacheKey) *CachedResponse
	// Put stores e under key, replacing any earlier response. key is
	// valid only during the call (copy key.String() to keep it); e is
	// immutable from then on.
	Put(key CacheKey, e *CachedResponse)
}

// CacheQuery says which query parameters are part of a cache key (Astro's
// memoryCache query option). Include and Exclude are path.Match patterns,
// matched case-insensitively against parameter names; giving both is an
// error. Without Include, Exclude defaults to Astro's tracking parameters
// (utm_*, fbclid, gclid, ref and others). Parameters are sorted by name
// unless NoSort is set.
type CacheQuery struct {
	Include, Exclude []string
	NoSort           bool
}

// CacheKey is a cache key: the request's host, escaped path and normalized
// query, plus the values of the stored response's Vary headers.
type CacheKey struct {
	hash uint64
	s    string
}

// cacheSeed hashes every cache key of the process.
var cacheSeed = maphash.MakeSeed()

// NewCacheKey returns the key for s, with the same hash Orbit gives it. A
// store uses it in its own tests; Orbit builds the keys it passes.
func NewCacheKey(s string) CacheKey {
	return CacheKey{hash: maphash.String(cacheSeed, s), s: s}
}

// Hash returns a hash of the key, for sharding and map lookups. It's
// stable within a process only.
func (k CacheKey) Hash() uint64 { return k.hash }

// String returns the key. The string is valid only during the call it was
// passed to; copy it (strings.Clone) to keep it.
func (k CacheKey) String() string { return k.s }

// CachedResponse is a stored response. Orbit builds it; a store keeps it
// as given and must not change it.
type CachedResponse struct {
	Status int
	// Header holds the response's own headers (the ones the page set, with
	// Content-Type, Content-Length and ETag); immutable, and its values are
	// shared with the responses served from it.
	Header http.Header
	Body   []byte // immutable
	Stored time.Time
	MaxAge time.Duration
	SWR    time.Duration
	Tags   []string
	// Path is the escaped request path, for InvalidateOptions.Path.
	Path string

	// Vary is set on a Vary marker: an entry stored under a URL's own key
	// when its response has a Vary header, holding the lowercased header
	// names (Cookie excluded) whose request values key the variants. A
	// marker has no body and is never served; a store keeps it like any
	// entry.
	Vary []string
	// revalidating is set while a background render refreshes the entry.
	revalidating atomic.Bool
}

// state reports the entry's state at now: fresh, stale (inside SWR) or
// expired.
func (e *CachedResponse) state(now time.Time) int {
	age := now.Sub(e.Stored)
	switch {
	case age < e.MaxAge:
		return entryFresh
	case age < e.MaxAge+e.SWR:
		return entryStale
	}
	return entryExpired
}

const (
	entryFresh = iota
	entryStale
	entryExpired
)

// For generated code. RouteRule is one orbit.json "routeRules" entry:
// default cache options for the requests whose path matches Pattern
// (Orbit's route syntax). The generated app.New() passes them with
// WithRouteRules.
type RouteRule struct {
	Pattern string
	Options CacheOptions
}

// WithCache configures the site's cache provider: a store such as
// cache.NewMemory (github.com/orbit-z/orbit/cache), a CDN header writer, or a
// plain provider for Astro's default headers. Pass it to app.New() in
// cmd/server/main.go. Under orbit dev, caching is off. With several
// WithCache options, the last one wins.
func WithCache(p CacheProvider) Option {
	return func(o *siteOptions) { o.cache = p }
}

// For generated code. WithRouteRules sets default cache options per route
// pattern (orbit.json "routeRules"); the generated app.New() passes them. Patterns use Orbit's
// route syntax and priority, and match the request path, whichever page
// serves it; they never match /_orbit/ paths. The first matching rule is
// applied before the page renders, as a first Set. Rules do nothing
// without a provider. An invalid pattern panics in NewSite.
func WithRouteRules(rules ...RouteRule) Option {
	return func(o *siteOptions) { o.rules = append(o.rules, rules...) }
}

// localsCache is WithLocalsCache's accessor.
type localsCache struct {
	// attached returns the request's attached *L (as any) and its Cache
	// field, or nil, nil.
	attached func(r *http.Request) (any, *Cache)
	// of returns the Cache field of l when l is an *L, else nil.
	of    func(l any) *Cache
	field any // func(*L) *Cache
}

// For generated code. WithLocalsCache tells the site where the
// orbit.Cache field of your Locals is. The generated app.New() passes it,
// before the caller's options, when src/locals declares one:
//
//	orbit.WithLocalsCache(func(l *locals.Locals) *orbit.Cache { return &l.Cache })
func WithLocalsCache[L any](field func(*L) *Cache) Option {
	lc := &localsCache{
		attached: func(r *http.Request) (any, *Cache) {
			if l := LocalsFromRequest[L](r); l != nil {
				return l, field(l)
			}
			return nil, nil
		},
		of: func(l any) *Cache {
			if l, ok := l.(*L); ok {
				return field(l)
			}
			return nil
		},
		field: field,
	}
	return func(o *siteOptions) { o.cacheField = lc }
}

// moveCacheToLocals is called by LocalsOf when it allocates the request's
// Locals: the field becomes the request's handle, with the options set so
// far.
func moveCacheToLocals[L any](c *Ctx, l *L) {
	f, ok := c.cacheField.(func(*L) *Cache)
	if !ok {
		return
	}
	lk := f(l)
	old := c.Cache()
	copyCache(lk, old)
	lk.mode, lk.site = old.mode, old.site
	c.cacheP = lk
}

// InvalidateCache drops stored responses through the provider: every
// response carrying one of o.Tags, and every variant of o.Path. It returns
// ErrCacheNotEnabled without a provider, and nil under orbit dev. Use it in
// admin handlers and webhooks:
//
//	err := site.InvalidateCache(r.Context(), orbit.InvalidateOptions{Tags: []string{"post:" + id}})
func (s *Site) InvalidateCache(ctx context.Context, o InvalidateOptions) error {
	switch {
	case s.dev:
		return nil
	case s.cacheProv == nil:
		return ErrCacheNotEnabled
	}
	return s.cacheProv.Invalidate(ctx, o)
}
