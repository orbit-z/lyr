package lyr

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"hash/crc64"
	"log"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
)

// The route cache's responses (PLAN.md §4h): storing, serving entries,
// conditional requests, background revalidation and the page flow.

// Response kinds, for the default headers.
const (
	kindPage   = iota // Content-Type text/html unless set
	kindIsland        // also Cache-Control: private, no-store and X-Robots-Tag: noindex
	kindRaw           // a redirect: headers as given
)

var (
	cacheHit   = []string{"HIT"}
	cacheMiss  = []string{"MISS"}
	cacheStale = []string{"STALE"}
	noCache    = []string{"no-cache"}
)

// Refusal reasons: a response asking to be stored that isn't.
const (
	refuseSetCookie = iota + 1
	refuseVaryCookie
	refuseVaryStar
)

// respond writes a rendered response (headers ph, the status and body) for
// request p: stored and served as a MISS when it can be, else written
// with the cache headers of its options.
func (sc *siteCache) respond(w http.ResponseWriter, r *http.Request, ph http.Header, p *cacheReq, status int, body []byte, kind int) {
	k := p.k
	wh := w.Header()
	active := cacheActive(k) && !p.off
	if active && sc.store == nil {
		// A header provider's CDN would store it: the same refusals as a
		// store's.
		if p.sessCookie {
			active = false
		} else if reason := refusal(ph, wh); reason != 0 {
			warnRefused(r, p, reason)
			active = false
		}
	}
	// A response that sends the session cookie is never stored, and that
	// isn't warned about (PLAN.md §4i "Route caching").
	if p.get && sc.store != nil && cacheStorable(k) && !p.sessCookie {
		if reason := refusal(ph, wh); reason != 0 {
			warnRefused(r, p, reason)
		} else {
			e := sc.newEntry(r, ph, p, status, body, kind)
			sc.put(r, p, e)
			p.state.stored.Store(true)
			sc.finishFlight(p, e)
			serveEntry(w, r, e, cacheMiss)
			return
		}
	}
	sc.finishFlight(p, nil)
	copyHeader(wh, ph)
	setDefaults(wh, kind)
	// Validators come from the page's headers or the options; without
	// either, the response has none to add Cache-Control for or compare.
	validators := len(ph) > 0
	if active {
		sc.writeOptionHeaders(wh, k, r)
		validators = true
	}
	if validators {
		addNoCache(wh)
	}
	wh["Content-Length"] = contentLength(len(body))
	if validators && status/100 == 2 && notModified(r, wh) {
		delete(wh, "Content-Length")
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.WriteHeader(status)
	if r.Method != http.MethodHead {
		w.Write(body)
	}
}

func setDefaults(h http.Header, kind int) {
	if kind == kindRaw {
		return
	}
	if _, ok := h["Content-Type"]; !ok {
		h["Content-Type"] = htmlContentType
	}
	if kind == kindIsland {
		if _, ok := h["Cache-Control"]; !ok {
			h["Cache-Control"] = islandCacheControl
		}
		if _, ok := h["X-Robots-Tag"]; !ok {
			h["X-Robots-Tag"] = islandRobots
		}
	}
}

// addNoCache is Astro's rule: a response with ETag or Last-Modified but no
// Cache-Control or Expires gets Cache-Control: no-cache.
func addNoCache(h http.Header) {
	_, et := h["Etag"]
	_, lm := h["Last-Modified"]
	if !et && !lm {
		return
	}
	_, cc := h["Cache-Control"]
	_, ex := h["Expires"]
	if !cc && !ex {
		h["Cache-Control"] = noCache
	}
}

// writeOptionHeaders writes the headers of an active handle's options: the
// header provider's, or ETag and Last-Modified and, without a store,
// Astro's default CDN headers.
func (sc *siteCache) writeOptionHeaders(h http.Header, k *Cache, r *http.Request) {
	// A CDN can't see the context values: a VaryBy response gets the
	// validators only.
	vary := len(varyOf(k)) > 0
	if sc.hdr != nil && !vary {
		o := k.Options()
		sc.hdr.WriteCacheHeaders(h, &o, r)
		return
	}
	if k.etag != "" {
		h["Etag"] = []string{k.etag}
	}
	if k.lastMod != 0 {
		h["Last-Modified"] = []string{time.Unix(0, k.lastMod).UTC().Format(http.TimeFormat)}
	}
	if sc.store != nil || vary {
		return
	}
	if k.maxAge > 0 || k.swr > 0 {
		var b []byte
		if k.maxAge > 0 {
			b = append(b, "max-age="...)
			b = strconv.AppendInt(b, k.maxAge/int64(time.Second), 10)
		}
		if k.swr > 0 {
			if len(b) > 0 {
				b = append(b, ", "...)
			}
			b = append(b, "stale-while-revalidate="...)
			b = strconv.AppendInt(b, k.swr/int64(time.Second), 10)
		}
		h["Cdn-Cache-Control"] = []string{string(b)}
	}
	if t := tagsOf(k); len(t) > 0 {
		h["Cache-Tag"] = []string{strings.Join(t, ", ")}
	}
}

// refusal returns why a response with headers ph (the page's) and wh (the
// middleware's) can't be stored, or 0.
func refusal(ph, wh http.Header) int {
	if _, ok := ph["Set-Cookie"]; ok {
		return refuseSetCookie
	}
	if _, ok := wh["Set-Cookie"]; ok {
		return refuseSetCookie
	}
	for _, h := range [2]http.Header{ph, wh} {
		for _, v := range h["Vary"] {
			for name := range strings.SplitSeq(v, ",") {
				switch name = strings.TrimSpace(name); {
				case name == "*":
					return refuseVaryStar
				case strings.EqualFold(name, "Cookie"):
					return refuseVaryCookie
				}
			}
		}
	}
	return 0
}

// warnRefused logs, once per route and reason, that a response asking to
// be stored wasn't.
func warnRefused(r *http.Request, p *cacheReq, reason int) {
	bit := uint32(1) << reason
	old := p.state.warned.Load()
	if old&bit != 0 || !p.state.warned.CompareAndSwap(old, old|bit) {
		return
	}
	why := "it sets a cookie (Set-Cookie)"
	switch reason {
	case refuseVaryCookie:
		why = "it varies on Cookie (Vary: Cookie)"
	case refuseVaryStar:
		why = "it has Vary: *"
	}
	log.Printf("lyr: %s %s: not cached because %s; later refusals for this route aren't logged", r.Method, r.URL.Path, why)
}

// newEntry builds the stored entry of a rendered response.
func (sc *siteCache) newEntry(r *http.Request, ph http.Header, p *cacheReq, status int, body []byte, kind int) *CachedResponse {
	k := p.k
	h := make(http.Header, len(ph)+4)
	for name, v := range ph {
		if name == "X-Lyr-Cache" {
			continue
		}
		h[name] = slices.Clip(slices.Clone(v))
	}
	setDefaults(h, kind)
	sc.writeOptionHeaders(h, k, r)
	addNoCache(h)
	if _, ok := h["Etag"]; !ok && status/100 == 2 {
		h["Etag"] = []string{autoETag(body)}
	}
	h["Content-Length"] = contentLength(len(body))
	e := &CachedResponse{
		Status: status,
		Header: h,
		Body:   slices.Clone(body),
		Stored: cacheNow(),
		MaxAge: time.Duration(k.maxAge),
		SWR:    time.Duration(k.swr),
		Path:   strings.Clone(p.path),
	}
	if t := tagsOf(k); len(t) > 0 {
		e.Tags = slices.Clone(t)
	}
	return e
}

// put stores e under p's key. A response with a Vary header or VaryBy
// names is stored under its variant key, with a Vary marker under the
// primary key (VaryBy names follow the header names, as "@name"). The
// context names of the URL's live marker are kept (a union, a missing
// value counting as ""): a response without them never replaces the
// per-user copies with one for everyone, and a refresh never drops a
// dimension.
func (sc *siteCache) put(r *http.Request, p *cacheReq, e *CachedResponse) {
	sc.ensureKey(r, p)
	vary := varyNames(e.Header)
	for _, name := range varyOf(p.k) {
		vary = appendName(vary, ctxVaryPrefix+name)
	}
	vary = appendCtxNames(vary, p.marker)
	// Best effort: the marker is read right before the Put, but a marker
	// another request stores in between can still be overwritten (a store
	// has no compare-and-swap); the next per-user render stores it again.
	if old := sc.store.Get(p.primary); old != nil && old.Vary != nil && old.state(cacheNow()) != entryExpired {
		vary = appendCtxNames(vary, old.Vary)
	}
	p.storedVary = vary
	if vary == nil {
		p.key = p.primary
		sc.store.Put(p.primary, e)
		return
	}
	sc.store.Put(p.primary, &CachedResponse{Stored: e.Stored, MaxAge: e.MaxAge, SWR: e.SWR, Tags: e.Tags, Path: e.Path, Vary: vary})
	p.key = sc.varyKey(r, p, vary)
	sc.store.Put(p.key, e)
}

// appendName appends name to names unless it's there.
func appendName(names []string, name string) []string {
	if slices.Contains(names, name) {
		return names
	}
	return append(names, name)
}

// appendCtxNames appends the context names ("@name") of a marker's Vary
// to names, skipping the ones it has.
func appendCtxNames(names, marker []string) []string {
	for _, name := range marker {
		if strings.HasPrefix(name, ctxVaryPrefix) {
			names = appendName(names, name)
		}
	}
	return names
}

// varyNames returns the canonical header names of h's Vary, without
// Set-Cookie and names that start with ctxVaryPrefix (not header names; a
// marker uses them for VaryBy), or nil.
func varyNames(h http.Header) []string {
	var names []string
	for _, v := range h["Vary"] {
		for name := range strings.SplitSeq(v, ",") {
			name = http.CanonicalHeaderKey(strings.TrimSpace(name))
			if name != "" && name != "Set-Cookie" && !strings.HasPrefix(name, ctxVaryPrefix) && !slices.Contains(names, name) {
				names = append(names, name)
			}
		}
	}
	return names
}

var etagTable = crc64.MakeTable(crc64.ECMA)

// autoETag is a stored body's strong ETag: the base64 of its CRC-64, so
// every process gives the same one.
func autoETag(body []byte) string {
	var sum [8]byte
	binary.BigEndian.PutUint64(sum[:], crc64.Checksum(body, etagTable))
	var b [13]byte
	b[0] = '"'
	base64.RawURLEncoding.Encode(b[1:12], sum[:])
	b[12] = '"'
	return string(b[:])
}

// serveEntry writes a stored entry with X-Lyr-Cache: state, or a 304
// when the request's validators match. The entry's header values are
// shared, not copied.
func serveEntry(w http.ResponseWriter, r *http.Request, e *CachedResponse, state []string) {
	h := w.Header()
	for name, v := range e.Header {
		h[name] = v
	}
	h["X-Lyr-Cache"] = state
	if e.Status/100 == 2 && notModified(r, h) {
		delete(h, "Content-Length")
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.WriteHeader(e.Status)
	if r.Method != http.MethodHead {
		w.Write(e.Body)
	}
}

// notModified evaluates a GET or HEAD request's If-None-Match (weak
// comparison), or else its If-Modified-Since, against the response
// headers h (RFC 9110 §13.2.2).
func notModified(r *http.Request, h http.Header) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	if inm := r.Header["If-None-Match"]; len(inm) > 0 {
		et := h["Etag"]
		return len(et) > 0 && etagListMatch(inm, et[0])
	}
	ims := r.Header["If-Modified-Since"]
	lm := h["Last-Modified"]
	if len(ims) == 0 || len(lm) == 0 {
		return false
	}
	t, err := http.ParseTime(ims[0])
	if err != nil {
		return false
	}
	m, err := http.ParseTime(lm[0])
	return err == nil && !m.After(t)
}

// etagListMatch reports whether an If-None-Match header (values of
// comma-separated entity tags, or *) matches etag, comparing weakly.
func etagListMatch(list []string, etag string) bool {
	etag = strings.TrimPrefix(etag, "W/")
	for _, v := range list {
		for t := range strings.SplitSeq(v, ",") {
			t = strings.TrimSpace(t)
			if t == "*" || strings.TrimPrefix(t, "W/") == etag {
				return true
			}
		}
	}
	return false
}

// ---- stale-while-revalidate ----

// startRevalidation starts one background render of the stale entry e,
// unless a render for its key is already running.
func (sc *siteCache) startRevalidation(r *http.Request, p *cacheReq, e *CachedResponse) {
	f, leader := sc.joinFlight(p.key.s)
	if !leader {
		e.revalidating.Store(false)
		return
	}
	snap := new(Cache)
	copyCache(snap, p.k)
	bg := &cacheReq{bg: true, snap: snap, flight: f, route: p.route, marker: p.marker}
	// The refresh renders as an anonymous visitor, unless the stale copy
	// varies by context: then with the trigger's session, read-only
	// (PLAN.md §4i "Route caching").
	bg.anon = len(varyOf(snap)) == 0 && !hasCtxNames(p.marker)
	r2 := r.WithContext(context.WithoutCancel(r.Context()))
	// A Locals copy of its own: its writes never reach the trigger's.
	r2, bg.locals = sc.site.cloneLocals(r2)
	if testRevalidateHook != nil {
		testRevalidateHook(r2)
	}
	go sc.revalidate(r2, bg, e)
}

// hasCtxNames reports whether a marker's Vary has context names.
func hasCtxNames(vary []string) bool {
	for _, name := range vary {
		if strings.HasPrefix(name, ctxVaryPrefix) {
			return true
		}
	}
	return false
}

// testRevalidateHook, when set by a test, is called before a background
// revalidation starts.
var testRevalidateHook func(*http.Request)

// revalidate renders bg's request again and stores the result; errors and
// panics are logged and the old entry is kept.
func (sc *siteCache) revalidate(r *http.Request, bg *cacheReq, old *CachedResponse) {
	defer old.revalidating.Store(false)
	defer sc.finishFlight(bg, nil)
	defer sc.site.bindBGLocals(bg.locals, nil) // its state is released with its Ctx
	defer func() {
		if p := recover(); p != nil {
			log.Printf("lyr: revalidating %s %s: panic: %v; the stale copy is kept", r.Method, r.URL.Path, p)
		}
	}()
	w := &discardWriter{h: make(http.Header)}
	s := sc.site
	switch {
	case bg.route != nil:
		c := s.acquire(r)
		path := r.URL.EscapedPath()
		c.matchRoute(s.router, path)
		s.serveCachedPage(w, r, c, bg.route, path, bg)
	default:
		s.serveIsland(w, r, strings.TrimPrefix(r.URL.Path, islandPrefix), bg)
	}
}

// discardWriter is the ResponseWriter of a background revalidation, and
// records a redirect's status and headers.
type discardWriter struct {
	h      http.Header
	status int
}

func (d *discardWriter) Header() http.Header         { return d.h }
func (d *discardWriter) WriteHeader(code int)        { d.status = code }
func (d *discardWriter) Write(b []byte) (int, error) { return len(b), nil }

// logRevalidation logs a background render's error.
func logRevalidation(r *http.Request, err error) {
	log.Printf("lyr: revalidating %s %s: %v; the stale copy is kept", r.Method, r.URL.Path, err)
}

// ---- pages ----

// serveCachedPage is servePage with a cache provider, for a matched page
// route cr (never the 404 page). bg is set for a background revalidation.
func (s *Site) serveCachedPage(w http.ResponseWriter, r *http.Request, c *Ctx, cr *compiledRoute, path string, bg *cacheReq) {
	route := cr.route
	if s.route != nil && bg == nil { // a revalidation leaves the trigger's RouteInfo alone
		if c.route.kind == RouteNone {
			c.route = routeRef{kind: RoutePage, pattern: route.Pattern, file: route.File, names: c.paramNames, vals: c.paramVals}
		}
		s.fillRoute(r, c)
	}
	sc := s.cache
	var local cacheReq
	p := &local
	if bg != nil {
		p = bg
	}
	p.buf, p.path, p.state, p.route = &c.keyBuf, path, &cr.cache, cr
	p.k = sc.attach(r, c, p)
	if bg == nil && s.rewrites == nil && p.k.pre == nil && p.k.state&cacheSet != 0 {
		// No site.Rewrites: every middleware ran once, before this
		// lookup, so its options hold for a rewrite's target. On the
		// Locals field, the copy is the handle's pre, so a rewrite from
		// the 404 page shown for this page's ErrNotFound finds it too.
		snap := snapshotOptions(p.k)
		if k := p.k; k != &c.cache {
			k.pre = snap
			defer func() {
				if k.pre == snap {
					k.pre = nil
				}
				putSnapshot(snap)
			}()
		} else {
			p.pre = snap
			defer releaseSnapshot(p)
		}
	}
	sc.beginSession(r, c, p)
	if sc.begin(w, r, p, true) {
		releaseCtx(c)
		return
	}
	if p.flight != nil {
		defer sc.finishFlight(p, nil)
	}
	buf, err := s.render(c, route)
	p.k = c.Cache() // LocalsOf may have moved it into the Locals
	err = sc.endSession(w, c, p, err)
	if err != nil {
		if buf != nil {
			putBuffer(buf)
		}
		s.cachedPageFailed(w, r, c, p, err)
		releaseCtx(c)
		return
	}
	sc.respond(w, r, c.responseHeader(), p, c.responseStatus(), buf.Bytes(), kindPage)
	putBuffer(buf)
	releaseCtx(c)
}

// cachedPageFailed answers a failed render on the cache path: a rewrite
// serves its target; a redirect whose options ask for it is stored; a
// revalidation logs the error.
func (s *Site) cachedPageFailed(w http.ResponseWriter, r *http.Request, c *Ctx, p *cacheReq, err error) {
	sc := s.cache
	if rw := asRewrite(err); rw != nil {
		// Never stored: the target is looked up and stored under its own
		// key (PLAN.md §4j). A revalidation keeps the stale copy.
		if p.bg {
			return
		}
		sc.finishFlight(p, nil)
		s.rewrite(w, r, c, rw, p.k, p.off, p.pre)
		return
	}
	var re *RedirectError
	if errors.As(err, &re) && p.get && sc.store != nil && cacheStorable(p.k) {
		rec := &redirectRecorder{discardWriter: discardWriter{h: make(http.Header)}}
		for name, v := range c.responseHeader() {
			rec.h[name] = v
		}
		delete(rec.h, "Content-Length") // the redirect's own, set by respond
		http.Redirect(rec, r, re.Location, re.Code)
		sc.respond(w, r, rec.h, p, rec.status, rec.body.Bytes(), kindRaw)
		return
	}
	if p.bg {
		logRevalidation(r, err)
		return
	}
	sc.finishFlight(p, nil)
	s.renderFailed(w, r, err, c, false)
}

// redirectRecorder records http.Redirect's response.
type redirectRecorder struct {
	discardWriter
	body bytes.Buffer
}

func (rr *redirectRecorder) Write(b []byte) (int, error) { return rr.body.Write(b) }

// render renders route with c into a pooled buffer, joining
// server:concurrent children; the head tags are inserted on success.
func (s *Site) render(c *Ctx, route *Route) (*bytes.Buffer, error) {
	buf := getBuffer()
	wr := writerPool.Get().(*Writer)
	wr.reset(buf)
	if s.devAssets != nil {
		assets := s.devAssets.assets(route)
		wr.tags = headTags(loaderHead(s.dev, route.Islands, assets), assets)
	} else if s.heads != nil {
		wr.tags = s.heads[route]
	}
	c.w = wr
	wr.ctx, wr.limit = c, s.limit
	err := callRender(route.Render, c, wr)
	if err == nil {
		err = wr.err
	}
	if wr.grp != nil {
		err = join(wr, err)
		buf = wr.buf
	}
	if err != nil {
		wr.reset(nil)
		writerPool.Put(wr)
		putBuffer(buf)
		return nil, err
	}
	wr.finishHead()
	wr.reset(nil)
	writerPool.Put(wr)
	return buf, nil
}
