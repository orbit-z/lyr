package orbit

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
	URL    *url.URL
	Params Params   // route params, e.g. Params["slug"] for blog/[slug].orbit
	Site   *url.URL // the "site" URL from orbit.json; nil when unset

	locals any         // nil, or the *L cached by LocalsOf
	status int         // 0 means unset (200)
	header http.Header // nil until Header is first called on this Ctx
	dev    bool        // running under orbit dev
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
// code must be a 3xx status; any other code panics, which Orbit reports as a
// server error pointing at the call.
func (c *Ctx) Redirect(to string, code int) error {
	if code < 300 || code > 399 {
		panic("orbit: Redirect to " + strconv.Quote(to) + " with status " + strconv.Itoa(code) +
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

// isDev reports whether the Ctx was acquired in dev mode (orbit dev).
func (c *Ctx) isDev() bool { return c.dev }

var ctxPool = sync.Pool{New: func() any { return new(Ctx) }}

// acquireCtx returns a pooled Ctx for r, with URL set to r.URL (nil if r is
// nil). Locals aren't read here: LocalsOf looks them up in r's context on
// first use. Pair every acquireCtx with one releaseCtx after the response has
// been written.
func acquireCtx(r *http.Request, params Params, site *url.URL, dev bool) *Ctx {
	c := ctxPool.Get().(*Ctx)
	c.Request = r
	if r != nil {
		c.URL = r.URL
	}
	c.Params = params
	c.Site = site
	c.dev = dev
	return c
}

// releaseCtx resets every field of c and returns it to the pool; c must not
// be used afterwards. The header map is emptied but kept, so a reused Ctx
// doesn't allocate it again.
func releaseCtx(c *Ctx) {
	h := c.header
	clear(h)
	*c = Ctx{header: h}
	ctxPool.Put(c)
}
