package lyr

import (
	"net/http"
	"net/url"
	"strconv"
	"sync"
)

// Ctx is the request context that every page and component receives as ctx.
//
// A Ctx is pooled and reused across requests: don't keep it, or anything it
// holds (such as the map returned by Header), after the request ends.
type Ctx struct {
	Request *http.Request
	// URL is the request URL. In a deferred island it's the island request's
	// URL; the page's URL is in Request.Referer().
	URL  *url.URL
	Site *url.URL // the "site" URL from lyr.json; nil when unset

	paramNames []string    // the matched route's param names (static)
	paramVals  []string    // values for paramNames, usually in paramBuf
	paramBuf   [8]string   // storage for paramVals, so matching doesn't allocate
	locals     any         // nil, or the *L cached by LocalsOf
	status     int         // 0 means unset (200)
	header     http.Header // nil until Header is first called on this Ctx
	dev        bool        // running under lyr dev
	keys       *keyring    // island key and build ID for Defer; nil: defaultKeyring
	route      routeRef    // the request's route, when the site fills a RouteInfo
	// w is the Writer rendering the request (set where the runtime pairs a
	// Ctx with a Writer), or nil; RenderHTML points it at its capture
	// writer while the capture runs.
	w *Writer
	// cache is the Ctx's own cache handle; cacheP, when set, is the
	// request's handle instead (the Locals field, a CacheHandler's), and
	// cacheField is WithLocalsCache's accessor, for LocalsOf (PLAN.md §4h).
	cache      Cache
	cacheP     *Cache
	cacheField any
	keyBuf     []byte // the cache key's storage, kept across requests
}

// Param returns the percent-decoded value of the route parameter name, such
// as "hello" for Param("slug") on /blog/hello with src/pages/blog/[slug].lyr.
// It returns "" if the matched route has no such parameter. A rest parameter
// ([...path]) holds the rest of the path, "a/b" for /docs/a/b.
//
// Pages normally use their generated, typed params instead. Param is for
// components, layouts and other code that doesn't know the page.
func (c *Ctx) Param(name string) string {
	for i, n := range c.paramNames {
		if n == name {
			return c.paramVals[i]
		}
	}
	return ""
}

// matchRoute matches path (r.URL.EscapedPath()) and stores the route's
// params on c. It returns nil if no page matches.
func (c *Ctx) matchRoute(rt *router, path string) *compiledRoute {
	cr, vals := rt.match(path, c.paramBuf[:0])
	if cr == nil {
		return nil
	}
	c.paramNames, c.paramVals = cr.names, vals
	return cr
}

// SetStatus sets the response status code (default 200).
func (c *Ctx) SetStatus(code int) { c.status = code }

// Header returns the response headers, to set or change before the page is
// sent.
func (c *Ctx) Header() http.Header {
	if c.header == nil {
		c.header = make(http.Header)
	}
	return c.header
}

// Redirect returns a *RedirectError that redirects to to with status code.
// Return it from the frontmatter: return ctx.Redirect("/login", 302).
//
// code must be a 3xx status; any other code panics, which Lyr reports as a
// server error pointing at the call.
func (c *Ctx) Redirect(to string, code int) error {
	if code < 300 || code > 399 {
		panic("lyr: Redirect to " + strconv.Quote(to) + " with status " + strconv.Itoa(code) +
			": the code must be a 3xx redirect status, such as 302 or 303")
	}
	return &RedirectError{Location: to, Code: code}
}

// responseStatus returns the status set with SetStatus, or 200 if none was.
// The Site uses it when it writes a successful render.
func (c *Ctx) responseStatus() int {
	if c.status == 0 {
		return http.StatusOK
	}
	return c.status
}

// responseHeader returns the headers set through Header, or nil if none were
// set for this request. The Site copies its entries into the ResponseWriter's
// headers; it must not keep the map after releaseCtx, because the map is
// cleared and reused by the next request.
func (c *Ctx) responseHeader() http.Header {
	if len(c.header) == 0 {
		return nil
	}
	return c.header
}

// cachedLocals returns the *L that LocalsOf cached for this request, or nil
// if LocalsOf hasn't run yet. Code that needs the typed value calls LocalsOf.
func (c *Ctx) cachedLocals() any { return c.locals }

// isDev reports whether the Ctx was acquired in dev mode (lyr dev).
func (c *Ctx) isDev() bool { return c.dev }

var ctxPool = sync.Pool{New: func() any { return new(Ctx) }}

// acquireCtx returns a pooled Ctx for r, with URL set to r.URL (nil if r is
// nil). Locals aren't read here: LocalsOf looks them up in r's context on
// first use. Pair every acquireCtx with one releaseCtx after the response has
// been written. Route params are set afterwards, with matchRoute.
func acquireCtx(r *http.Request, site *url.URL, dev bool) *Ctx {
	c := ctxPool.Get().(*Ctx)
	c.Request = r
	if r != nil {
		c.URL = r.URL
	}
	c.Site = site
	c.dev = dev
	return c
}

// releaseCtx resets every field of c and returns it to the pool; c must not
// be used afterwards. Param values are cleared too, so they don't leak into
// the next request. The header map is emptied but kept, so a reused Ctx
// doesn't allocate it again.
func releaseCtx(c *Ctx) {
	h := c.header
	clear(h)
	kb := c.keyBuf[:0]
	if cap(kb) > maxKeyBuf {
		kb = nil
	}
	releaseCache(&c.cache)
	more := c.cache.more
	*c = Ctx{header: h, keyBuf: kb}
	c.cache.more = more
	ctxPool.Put(c)
}
