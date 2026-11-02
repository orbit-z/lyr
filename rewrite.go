package lyr

import (
	"bytes"
	"context"
	"errors"
	"log"
	"net/http"
	"net/url"
	"strings"
)

// Rewrites (PLAN.md §4j): ctx.Rewrite renders another route's content
// without a redirect, site.Rewrites marks where a page rewrite re-enters
// the middleware chain, and RewriteRequest rewrites a request in
// middleware. Everything here runs on a page's error path only: a page
// that doesn't rewrite pays nothing.

// RewriteError is returned by Ctx.Rewrite. Returning it from a page's
// frontmatter (or from a component the page renders) serves the route for
// Target instead of the page, without a redirect.
type RewriteError struct {
	Target string // a path with an optional query, such as "/es/about?x=1"
}

func (e *RewriteError) Error() string { return "lyr: rewrite to " + e.Target }

// Rewrite returns a *RewriteError that renders the route for target
// instead of this page, without a redirect: the browser keeps its URL.
// Return it from the frontmatter: return ctx.Rewrite("/es/" + params.Path).
//
// target is a path with an optional query ("/search?q=go"); the
// request's own query isn't kept. A relative target is resolved against
// the request's URL, as Astro does ("x" from /a/b is /a/x, "../x", "?q=1"
// keeps the path). It never fails, as in Astro: a scheme and host are
// ignored ("https://example.com/a" is "/a"), "//x" is the path "//x", a
// malformed percent-escape is kept as written and a control character
// percent-encoded, so a target built from request data is safe. The response is
// the target's: its status, headers and body. The headers this page set
// with ctx.Header are dropped, except Set-Cookie. "/404" renders 404.lyr
// with status 404. The request runs again through the middleware inside
// site.Rewrites, with the same locals, or through site.Middleware alone
// when the chain has no site.Rewrites. ctx.Request.RequestURI keeps the
// original request target. The third chained rewrite of a request answers
// 508 Loop Detected.
func (c *Ctx) Rewrite(target string) error { return &RewriteError{Target: target} }

// Rewrites marks where a page rewrite (ctx.Rewrite) re-enters your
// middleware chain: the handlers inside it (next and below) run again for
// the rewrite target, with the same locals; those outside it run once.
// Cache options set by the middleware outside it hold for the target.
// It costs nothing per request without a cache provider:
//
//	h := lyr.Sequence(site.TrailingSlash, logging, site.Rewrites, site.Sessions, auth)(mux)
//
// Put middleware that wraps the ResponseWriter (gzip, access logs) outside
// it, and middleware that reads the path (auth, locals, sessions) inside
// it. Inside middleware should attach locals with EnsureLocals, so the
// second run finds them. Without site.Rewrites, a rewrite re-enters
// site.Middleware only. A second call panics.
func (s *Site) Rewrites(next http.Handler) http.Handler {
	if s.rewrites != nil {
		panic("lyr: site.Rewrites is called twice; a site has one re-entry point for rewrites")
	}
	if next == nil {
		panic("lyr: site.Rewrites(nil)")
	}
	s.rewrites = next
	return &rewritesMarker{site: s, next: next}
}

// rewritesMarker is site.Rewrites' handler. On a request's first pass, it
// copies the cache options the middleware outside it set (on the Locals'
// Cache field, pooled, so a HIT still allocates nothing), for a page
// rewrite to restore for its target; the middleware inside it re-runs and
// sets its own again. A rewrite re-enters next directly.
type rewritesMarker struct {
	site *Site
	next http.Handler
}

func (m *rewritesMarker) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if sc := m.site.cache; sc != nil && sc.field != nil {
		if _, k := sc.field.attached(r); k != nil && k.pre == nil && k.state&cacheSet != 0 {
			snap := snapshotOptions(k)
			k.pre = snap
			defer func() {
				if k.pre == snap {
					k.pre = nil
				}
				putSnapshot(snap)
			}()
		}
	}
	m.next.ServeHTTP(w, r)
}

// RewriteRequest returns a shallow copy of r for target, a path (usually
// starting with "/") and an optional query, parsed as ctx.Rewrite parses it:
// URL.Path, URL.RawPath and URL.RawQuery are set (the request's own query
// isn't kept), and RequestURI, the method, the headers and the body are
// kept. Pass it to next instead of r, like Astro's next("/path"):
//
//	if l.User == nil && strings.HasPrefix(r.URL.Path, "/account") {
//		r = lyr.RewriteRequest(r, "/login")
//	}
//	next.ServeHTTP(w, r)
//
// The handlers before this middleware don't run again, and a middleware
// rewrite doesn't count toward the rewrite loop limit.
//
// It never panics: any target becomes a local path, so one built from
// request data is safe. As in Astro, a relative target is resolved
// against r's URL ("x" from /a/b is /a/x, "../x" is /x, "?q=1" keeps the
// path), dot segments are removed, and a scheme and host are ignored
// ("https://example.com/a?b" is "/a?b"). "//x" is the path "//x". A
// malformed percent-escape is kept as written, and a control character is
// percent-encoded, as a browser would send it. The result is decoded and
// matched as a direct request for it would be; one no route matches gets
// the usual 404.
func RewriteRequest(r *http.Request, target string) *http.Request {
	x := new(struct {
		req http.Request
		u   url.URL
	})
	x.req = *r
	esc, query, force := parseRewriteTarget(r.URL, target)
	setRewriteURL(&x.u, r.URL, esc, query, force)
	x.req.URL = &x.u
	return &x.req
}

// maxRewrites is the number of chained page rewrites a request may make:
// the next one answers 508 Loop Detected (Astro's counter: the fourth
// pass through the middleware).
const maxRewrites = 2

// rewriteKey is the context key of a rewritten request's *rewriteState.
type rewriteKey struct{}

// rewriteState is the context of a request a page rewrote: it wraps the
// original request's context, so values (the locals) carry over.
type rewriteState struct {
	context.Context
	prev  *rewriteState // the previous rewrite of the request, or nil
	from  string        // the escaped path of the request that rewrote
	to    string        // the target's escaped path
	depth int           // 1 for the first rewrite
	// sess is the session state of the page that rewrote, when the page
	// owned it (no site.Sessions bound one): the target takes it, so the
	// request keeps one session (newPageSession).
	sess *sessionState
}

func (st *rewriteState) Value(key any) any {
	if _, ok := key.(rewriteKey); ok {
		return st
	}
	return st.Context.Value(key)
}

// rewriteOf returns the request's latest rewrite, or nil.
func rewriteOf(ctx context.Context) *rewriteState {
	st, _ := ctx.Value(rewriteKey{}).(*rewriteState)
	return st
}

// takeRewriteSession takes the session state a rewritten page handed to
// its target, or returns nil.
func takeRewriteSession(r *http.Request) *sessionState {
	if r == nil {
		return nil
	}
	for rs := rewriteOf(r.Context()); rs != nil; rs = rs.prev {
		if st := rs.sess; st != nil {
			rs.sess = nil
			return st
		}
	}
	return nil
}

// rewriteReq is one page rewrite's allocation: the request copy, its URL
// and its context.
type rewriteReq struct {
	req http.Request
	u   url.URL
	st  rewriteState
}

// asRewrite returns err's *RewriteError, or nil.
func asRewrite(err error) *RewriteError {
	if re, ok := err.(*RewriteError); ok {
		return re
	}
	var re *RewriteError
	if errors.As(err, &re) {
		return re
	}
	return nil
}

// rewrite serves re's target for the request r, whose page (rendered with
// c, nil from ServeError) returned re. k, off and pre are the route
// cache's handle, whether it was disabled before the lookup, and the
// options middleware set before it, from the cached page path; with k nil
// (the 404 page, ServeError, a site without a cache), the handle is the
// Locals' Cache field, if any (resetRewriteCache). The caller releases c
// afterwards.
func (s *Site) rewrite(w http.ResponseWriter, r *http.Request, c *Ctx, re *RewriteError, k *Cache, off bool, pre *Cache) {
	prev := rewriteOf(r.Context())
	esc, query, force := parseRewriteTarget(r.URL, re.Target)
	// The target is never redirected: it's put in its canonical
	// trailing-slash form first (Astro's normalizeRewritePathname).
	if t, ok := s.router.redirectTarget(esc, ""); ok {
		esc = t
	}
	depth := 1
	if prev != nil {
		depth = prev.depth + 1
	}
	if depth > maxRewrites {
		s.rewriteLoop(w, r, prev, esc)
		return
	}
	x := new(rewriteReq)
	setRewriteURL(&x.u, r.URL, esc, query, force)
	x.st = rewriteState{Context: r.Context(), prev: prev, from: r.URL.EscapedPath(), to: esc, depth: depth}
	if c != nil {
		// Cookies the page set are kept (Astro merges them); its other
		// headers and its status are the target's to decide.
		if v := c.responseHeader()["Set-Cookie"]; len(v) > 0 {
			h := w.Header()
			h["Set-Cookie"] = append(h["Set-Cookie"], v...)
		}
		if st, ok := c.session.(*sessionState); ok && st.ctx == c {
			// The page's own session (saved by endSession already) goes
			// to the target, which saves its changes again.
			st.ctx, c.session = nil, nil
			x.st.sess = st
		}
	}
	s.resetRewriteCache(r, k, off, pre)
	x.req = *r.WithContext(&x.st)
	x.req.URL = &x.u
	if s.route != nil {
		// The target's pass fills the RouteInfo again; a target no page
		// matches (an app route) has none, not the source page's.
		s.fillRouteOnly(&x.req, RouteNone, "", "", nil, nil)
	}
	h := s.rewrites
	if h == nil {
		if c != nil && c.mw != nil {
			h = c.mw
		} else {
			h = &middleware{site: s, next: notFoundHandler{s}}
		}
	}
	h.ServeHTTP(w, &x.req)
	if st := x.st.sess; st != nil {
		// The target didn't use the session.
		x.st.sess = nil
		releaseSession(st)
	}
}

// resetRewriteCache resets the request's cache handle for a rewrite's
// target. Without k (the source was the 404 page, or ServeError), it's
// the Locals' Cache field, which the target's pass shares: disabled when
// it was before a lookup (cacheOff), or when no lookup ran and middleware
// disabled it; its pre is the outer middleware's options (site.Rewrites'
// copy, or the cached page's when the chain has none). Without
// site.Rewrites and before any lookup, every option on it is
// middleware's, so they all hold.
func (s *Site) resetRewriteCache(r *http.Request, k *Cache, off bool, pre *Cache) {
	if k == nil {
		if s.cache == nil || s.cache.field == nil {
			return
		}
		if _, k = s.cache.field.attached(r); k == nil {
			return
		}
		off = k.flags&cacheLooked == 0 && k.state&cacheDisabled != 0
	}
	off = off || k.flags&cacheOff != 0
	if pre == nil {
		pre = k.pre
	}
	if pre == nil && s.rewrites == nil && k.flags&cacheLooked == 0 && k.state&cacheSet != 0 {
		snap := snapshotOptions(k)
		defer putSnapshot(snap)
		pre = snap
	}
	resetCacheForRewrite(k, off, pre)
}

// resetCacheForRewrite prepares the request's cache handle, shared by the
// rewrite's passes when it's a Locals field, for the target's own lookup:
// the options the source page set are dropped, the ones middleware set
// before the source's lookup (pre) are restored, a Disable made before
// that lookup still holds (PLAN.md §4h: it holds for the whole request),
// and Context may set values again until the target's lookup. The
// context values are kept, and so is a late Context change: the target
// is then neither looked up nor stored, since the values it would be
// keyed by aren't the request's.
func resetCacheForRewrite(k *Cache, off bool, pre *Cache) {
	clearOptions(k)
	k.state = 0
	switch {
	case off:
		k.state = cacheDisabled
	case pre != nil:
		mergeOpts(k, pre.maxAge, pre.swr, pre.etag, pre.lastMod, tagsOf(pre), varyOf(pre))
	}
	k.flags &= cacheLate
}

// parseRewriteTarget resolves a rewrite target against base, the URL of
// the request that rewrites, as Astro does with new URL(target, url)
// (RFC 3986 §5.2), keeping only the path and query. It never fails: the
// fragment is dropped; control characters are percent-encoded; a scheme
// and its authority ("https://host") are dropped, and the rest is a path
// from the root; "?q" keeps base's path, "" base's path and query, and a
// relative path ("x", "../x") is merged with base's directory; dot
// segments ("." and "..", also percent-encoded) are removed. A target
// starting with "//" stays the path "//…" (Astro would read a host and
// keep its path; Lyr keeps it whole, so nothing is dropped silently).
// force is set for a query that's empty but present ("?").
func parseRewriteTarget(base *url.URL, target string) (esc, query string, force bool) {
	t, _, _ := strings.Cut(target, "#")
	t = escapeControls(t)
	if n := schemeLen(t); n > 0 {
		t = t[n+1:]
		if rest, ok := strings.CutPrefix(t, "//"); ok {
			// The authority runs to the path or the query.
			if i := strings.IndexAny(rest, "/?"); i >= 0 {
				t = rest[i:]
			} else {
				t = ""
			}
		}
		if !strings.HasPrefix(t, "/") {
			t = "/" + t
		}
	}
	p, query, hasQuery := strings.Cut(t, "?")
	force = hasQuery && query == ""
	basePath := "/"
	if base != nil {
		if bp := base.EscapedPath(); bp != "" {
			basePath = bp
		}
	}
	switch {
	case p == "":
		esc = basePath
		if !hasQuery && base != nil {
			query, force = base.RawQuery, base.ForceQuery
		}
	case p[0] == '/':
		esc = removeDotSegments(p)
	default:
		esc = removeDotSegments(basePath[:strings.LastIndexByte(basePath, '/')+1] + p)
	}
	return esc, query, force
}

// removeDotSegments removes the "." and ".." segments of the absolute
// path p (RFC 3986 §5.2.4), recognizing them percent-encoded too, as the
// WHATWG URL parser does. p is returned as is when it has none.
func removeDotSegments(p string) string {
	if !hasDotSegment(p) {
		return p
	}
	segs := strings.Split(p[1:], "/")
	out := make([]string, 0, len(segs))
	for i, seg := range segs {
		last := i == len(segs)-1
		switch {
		case isDot(seg):
			if last {
				out = append(out, "")
			}
		case isDotDot(seg):
			if len(out) > 0 {
				out = out[:len(out)-1]
			}
			if last {
				out = append(out, "")
			}
		default:
			out = append(out, seg)
		}
	}
	return "/" + strings.Join(out, "/")
}

func hasDotSegment(p string) bool {
	for seg := range strings.SplitSeq(p[1:], "/") {
		if isDot(seg) || isDotDot(seg) {
			return true
		}
	}
	return false
}

func isDot(seg string) bool { return seg == "." || strings.EqualFold(seg, "%2e") }

func isDotDot(seg string) bool {
	switch len(seg) {
	case 2:
		return seg == ".."
	case 4:
		return strings.EqualFold(seg, ".%2e") || strings.EqualFold(seg, "%2e.")
	case 6:
		return strings.EqualFold(seg, "%2e%2e")
	}
	return false
}

// schemeLen returns the length of t's URL scheme (RFC 3986: a letter,
// then letters, digits, '+', '-' or '.', ending at a ':' that comes
// before any '/', '?'), or 0.
func schemeLen(t string) int {
	for i := 0; i < len(t); i++ {
		c := t[i]
		switch {
		case 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z':
		case i > 0 && ('0' <= c && c <= '9' || c == '+' || c == '-' || c == '.'):
		case i > 0 && c == ':':
			return i
		default:
			return 0
		}
	}
	return 0
}

// escapeControls percent-encodes the ASCII control characters in t, as a
// browser does in a URL; t is returned as is when it has none.
func escapeControls(t string) string {
	n := 0
	for i := 0; i < len(t); i++ {
		if b := t[i]; b < 0x20 || b == 0x7f {
			n++
		}
	}
	if n == 0 {
		return t
	}
	const hex = "0123456789ABCDEF"
	b := make([]byte, 0, len(t)+2*n)
	for i := 0; i < len(t); i++ {
		if c := t[i]; c < 0x20 || c == 0x7f {
			b = append(b, '%', hex[c>>4], hex[c&0xf])
		} else {
			b = append(b, c)
		}
	}
	return string(b)
}

// setRewriteURL makes u a copy of base for the escaped path esc and the
// query, decoded as net/http decodes a request's: Path is the decoded path,
// and RawPath the escaped one when it isn't the default encoding. A
// malformed escape is kept as written (Path is esc), as the router keeps
// it.
func setRewriteURL(u, base *url.URL, esc, query string, force bool) {
	*u = *base
	u.Path, u.RawPath = esc, ""
	u.RawQuery, u.ForceQuery = query, force
	u.Fragment, u.RawFragment = "", ""
	if strings.IndexByte(esc, '%') < 0 {
		return
	}
	p, err := url.PathUnescape(esc)
	if err != nil {
		return
	}
	u.Path = p
	if u.EscapedPath() != esc {
		u.RawPath = esc
	}
}

// rewriteLoop answers the rewrite past maxRewrites: 508 Loop Detected,
// logged with the chain, and with an empty body (in dev, the chain).
func (s *Site) rewriteLoop(w http.ResponseWriter, r *http.Request, last *rewriteState, to string) {
	var chain []string
	for rs := last; rs != nil; rs = rs.prev {
		chain = append(chain, rs.to)
		if rs.prev == nil {
			chain = append(chain, rs.from)
		}
	}
	for i, j := 0, len(chain)-1; i < j; i, j = i+1, j-1 {
		chain[i], chain[j] = chain[j], chain[i]
	}
	chain = append(chain, to)
	msg := "rewrite loop: " + strings.Join(chain, " → ")
	path := r.URL.Path
	if len(chain) > 0 {
		path = chain[0]
	}
	log.Printf("lyr: %s %s: %s", r.Method, path, msg)
	h := w.Header()
	if !s.dev {
		h["Content-Length"] = zeroLength
		w.WriteHeader(http.StatusLoopDetected)
		return
	}
	var body bytes.Buffer
	body.WriteString(`<!DOCTYPE html><html><head><meta charset="utf-8"><title>508 Loop Detected</title></head><body><h1>508 Loop Detected</h1><pre>`)
	escapeString(&body, msg)
	body.WriteString("</pre>")
	body.WriteString(errorPageEnd)
	h["Content-Type"] = htmlContentType
	h["Content-Length"] = contentLength(body.Len())
	w.WriteHeader(http.StatusLoopDetected)
	if r.Method != http.MethodHead {
		w.Write(body.Bytes())
	}
}
