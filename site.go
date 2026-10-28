package orbit

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"unsafe"
)

// Site is a whole Orbit site: its pages, router, static files and options.
// It's created by the generated orbit_gen/app.New(), and is safe for
// concurrent use.
type Site struct {
	router *router
	site   *url.URL // ctx.Site; nil when unset
	dev    bool     // ORBIT_DEV=1
	assets fileSet  // /_orbit/* files
	public fileSet  // public/ files
	// devAssets, in dev with ORBIT_STATIC set, gives the page assets from
	// $ORBIT_STATIC/manifest.json; nil in production.
	devAssets *devManifest
	keys      *keyring           // island key and build ID
	islands   map[string]*Island // the island table, by component ID
	// heads holds the precomputed head tags (headTags: the loader and the
	// asset tags) of the routes that have any; unused in dev with a dev
	// manifest.
	heads map[*Route]string
	// onError is WithErrorHandler's handler, or nil.
	onError errorHandler
	// limit is the server:concurrent limit (WithConcurrencyLimit).
	limit int32
	// route is WithLocalsRoute's accessor, or nil: then no RouteInfo is
	// filled and the serve path is today's (PLAN.md §4g).
	route *localsRoute
	// cache is the route cache (PLAN.md §4h): nil without a provider and
	// in dev, and then the serve path is the one without a cache.
	cache *siteCache
	// cacheProv is WithCache's provider, also in dev (InvalidateCache).
	cacheProv CacheProvider
}

// Option configures a Site. Pass options to app.New().
type Option func(*siteOptions)

type siteOptions struct {
	site       *url.URL
	staticDir  string
	staticFS   fs.FS
	key        []byte // WithKey
	onError    errorHandler
	slash      TrailingSlash
	concLimit  int32 // WithConcurrencyLimit, when concSet
	concSet    bool
	route      *localsRoute  // WithLocalsRoute
	cache      CacheProvider // WithCache
	rules      []RouteRule   // WithRouteRules
	cacheField *localsCache  // WithLocalsCache
}

// NewSite is called by the generated orbit_gen/app.New(). It compiles the
// routes and panics if they're invalid; orbit build reports the same errors
// at build time. It reads the environment once: ORBIT_DEV, ORBIT_STATIC,
// ORBIT_KEY and, in dev, ORBIT_PUBLIC. In dev, page assets come from
// $ORBIT_STATIC/manifest.json when it exists (orbit dev writes it).
//
// The island key is WithKey's, else $ORBIT_KEY (standard base64 of 32 bytes;
// anything else panics), else the fixed test key under go test, else the key
// embedded by orbit build, else a random key for the process (with a
// warning on stderr when the site has islands). A duplicate island ID
// panics.
func NewSite(routes []Route, islands []Island, opts ...Option) *Site {
	rt, err := newRouter(routes)
	if err != nil {
		panic(err)
	}
	var o siteOptions
	for _, opt := range opts {
		opt(&o)
	}
	rt.always = o.slash == TrailingSlashAlways
	s := &Site{
		router: rt,
		site:   o.site,
		dev:    os.Getenv("ORBIT_DEV") == "1",
		keys:   resolveKeyring(o.key),

		onError: o.onError,
		limit:   o.concurrencyLimit(),
		route:   o.route,
	}
	rules := compileRules(o.rules, rt.always)
	if o.cache != nil {
		s.cacheProv = o.cache
		if !s.dev {
			s.cache = newSiteCache(s, &o, rules)
		}
	}
	s.keys.warnIfRandom(len(islands))
	islands = slices.Clone(islands) // the Site owns its entries (their cache state)
	s.islands = make(map[string]*Island, len(islands))
	for i := range islands {
		isl := &islands[i]
		if prev, ok := s.islands[isl.id]; ok {
			panic(fmt.Sprintf("orbit: island %s is registered twice (%s and %s)", isl.id, prev.file, isl.file))
		}
		isl.cache = new(cacheState)
		s.islands[isl.id] = isl
	}
	for i := range routes {
		r := &routes[i]
		if head := headTags(loaderHead(s.dev, r.Islands, r.Assets), r.Assets); head != "" {
			if s.heads == nil {
				s.heads = make(map[*Route]string)
			}
			s.heads[r] = head
		}
	}
	s.assets = resolveStatic(o, s.dev)
	s.public = s.assets
	if s.dev {
		if dir := os.Getenv("ORBIT_PUBLIC"); dir != "" {
			s.public = newDirFiles(dir, true)
		}
		if dir := os.Getenv("ORBIT_STATIC"); dir != "" {
			s.devAssets = newDevManifest(dir)
		}
	}
	return s
}

// WithSite sets the absolute site URL exposed as ctx.Site. The generated
// app.New() passes orbit.json's "site" first, so a caller's option wins. A
// value that isn't an absolute URL (with a scheme and a host) panics.
func WithSite(rawURL string) Option {
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		panic(fmt.Sprintf("orbit: WithSite(%q): the site must be an absolute URL, such as \"https://example.com\"", rawURL))
	}
	return func(o *siteOptions) { o.site = u }
}

// TrailingSlash is a site's trailing-slash mode: whether page URLs end
// with "/". Set it in orbit.json ("trailingSlash"); see WithTrailingSlash.
type TrailingSlash uint8

const (
	// TrailingSlashNever is the default: page URLs have no trailing slash
	// (/blog/hello), and /blog/hello/ gets a 308 to /blog/hello.
	TrailingSlashNever TrailingSlash = iota
	// TrailingSlashAlways makes page URLs end with "/" (/blog/hello/), and
	// /blog/hello gets a 308 to /blog/hello/. "/" and extension routes
	// (/rss.xml) keep their URL, and paths with a file extension are never
	// redirected.
	TrailingSlashAlways
)

// WithTrailingSlash sets the trailing-slash mode. The generated app.New()
// passes orbit.json's "trailingSlash" first (only for "always"), so a
// caller's option wins. Set the mode in orbit.json rather than here:
// orbit generate writes Markdown's Post.URL and rewritten .md links in the
// orbit.json mode, so overriding it makes each of those links redirect once.
func WithTrailingSlash(mode TrailingSlash) Option {
	return func(o *siteOptions) { o.slash = mode }
}

// WithStaticDir sets the directory of built static files (the /_orbit/
// assets and public/ files), replacing the default: client/ in the server
// binary's directory. $ORBIT_STATIC still wins when it's set.
func WithStaticDir(dir string) Option {
	return func(o *siteOptions) { o.staticDir = dir }
}

// WithStaticFS serves the static files from fsys, laid out like dist/client.
// orbit build --embed uses it with the embedded files. $ORBIT_STATIC and
// WithStaticDir win over it.
func WithStaticFS(fsys fs.FS) Option {
	return func(o *siteOptions) { o.staticFS = fsys }
}

// errorHandler answers a render error; see WithErrorHandler.
type errorHandler = func(w http.ResponseWriter, r *http.Request, err error) bool

// WithErrorHandler sets h to answer the render errors that would otherwise
// become the built-in 500: errors returned by a page or any component it
// renders (the 404 page and deferred islands included) and recovered
// panics. ErrNotFound and redirects never reach h; they keep their usual
// answers. h returns true when it wrote the response, false to let Orbit
// send the 500 and log the error.
//
// Nothing has been written when h runs: the page's output and the headers
// it set with ctx.Header are dropped, and h can send any status, headers and
// body. err keeps the user's error for errors.Is and errors.As, and its text
// starts with the .orbit position of the failing return. For an island
// request, r is the island request (the page is r.Referer()), and
// Cache-Control: private, no-store and X-Robots-Tag: noindex are already set
// on w. Orbit doesn't log an error h answers, and doesn't recover a panic in
// h. With several WithErrorHandler options, the last one wins.
func WithErrorHandler(h func(w http.ResponseWriter, r *http.Request, err error) bool) Option {
	return func(o *siteOptions) { o.onError = h }
}

// ServeError answers err as a page's render error would be answered: a
// *RedirectError redirects, ErrNotFound renders 404.orbit, and any other
// error goes to the WithErrorHandler handler, then to the built-in 500 with
// its log line. Use it in custom handlers:
//
//	if err := orbit.RenderComponent(w, r, card.Render, props); err != nil {
//		site.ServeError(w, r, err)
//	}
//
// A nil err writes nothing.
func (s *Site) ServeError(w http.ResponseWriter, r *http.Request, err error) {
	if err == nil {
		return
	}
	s.renderFailed(w, r, err, nil, nil, false)
}

// Middleware serves the requests Orbit owns and passes the rest to next,
// untouched. In order: island requests (/_orbit/island/…, GET, HEAD and
// POST; other methods get a 405), static files (/_orbit/* assets and public/ files,
// GET and HEAD only), then pages (all methods), including the 308 redirect
// of a path whose other form is a page URL: /x/ to /x by default, /x to /x/
// with TrailingSlashAlways.
func (s *Site) Middleware(next http.Handler) http.Handler {
	return &middleware{site: s, next: next}
}

// Handler is Middleware(NotFound()): Orbit's pages, with 404.orbit for
// everything else.
func (s *Site) Handler() http.Handler { return s.Middleware(s.NotFound()) }

// NotFound renders 404.orbit with a 404 status, or sends an empty 404 if the
// site has no 404.orbit. Use it as the catch-all of your own mux when Orbit
// comes first.
func (s *Site) NotFound() http.Handler { return notFoundHandler{s} }

type middleware struct {
	site *Site
	next http.Handler
}

func (m *middleware) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s := m.site
	switch d, rest := s.classify(w, r); d {
	case dispatchIsland:
		s.serveIsland(w, r, rest, nil)
		return
	case dispatchFile:
		if s.route != nil {
			s.fillRouteOnly(r, RouteFile, r.URL.Path, "", nil, nil)
		}
		return
	}
	path := r.URL.EscapedPath()
	c := s.acquire(r)
	if cr := c.matchRoute(s.router, path); cr != nil {
		if s.cache != nil {
			s.serveCachedPage(w, r, c, cr, path, nil)
			return
		}
		s.servePage(w, r, c, cr.route, false)
		return
	}
	releaseCtx(c)
	if target, ok := s.router.redirectTarget(path, r.URL.RawQuery); ok {
		writeSlashRedirect(w, target)
		return
	}
	m.next.ServeHTTP(w, r)
}

// TrailingSlash redirects the non-canonical form of a page URL (per
// orbit.json's trailingSlash: /x/ to /x by default, /x to /x/ with
// TrailingSlashAlways) with a 308 before calling next, so middleware inside
// it never sees the other form: Astro's order. Put it outermost, inside an
// access logger if you want the redirects logged:
//
//	orbit.ListenAndServe(site.TrailingSlash(logging(auth(mux))))
//
// It covers Orbit's pages only, with exactly site.Middleware's rule: a path
// is redirected only when its other form is a page URL; island URLs,
// /_orbit/ assets, public/ files, "//…" paths and file-extension paths
// never are. Your own routes keep their own redirects. Anything else
// reaches next untouched, and a canonical path costs one byte check.
// site.Middleware keeps its own redirect, so a request this one passed
// never gets a second one.
func (s *Site) TrailingSlash(next http.Handler) http.Handler {
	return &slashMiddleware{site: s, next: next}
}

type slashMiddleware struct {
	site *Site
	next http.Handler
}

func (m *slashMiddleware) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !m.site.canonicalSlash(r.URL) {
		if target, ok := m.site.slashRedirect(r); ok {
			writeSlashRedirect(w, target)
			return
		}
	}
	m.next.ServeHTTP(w, r)
}

// canonicalSlash reports, from the path's last byte, that u is in its
// canonical trailing-slash form, which site.Middleware never redirects.
// A path with a RawPath (an escaped "/", say) is left to slashRedirect,
// which reads the escaped path as site.Middleware does.
func (s *Site) canonicalSlash(u *url.URL) bool {
	p := u.Path
	if len(p) < 2 {
		return true
	}
	if u.RawPath != "" {
		return false
	}
	return (p[len(p)-1] == '/') == s.router.always
}

// slashRedirect is site.Middleware's trailing-slash decision for r, without
// serving anything: classify's order (islands, then files), then
// redirectTarget for a page request. It needs no match: a path
// redirectTarget accepts never matches a page (PLAN.md §4e).
func (s *Site) slashRedirect(r *http.Request) (string, bool) {
	if d, _ := s.classify(nil, r); d != dispatchPage {
		return "", false
	}
	return s.router.redirectTarget(r.URL.EscapedPath(), r.URL.RawQuery)
}

// writeSlashRedirect sends the trailing-slash 308, with an empty body.
func writeSlashRedirect(w http.ResponseWriter, target string) {
	w.Header()["Location"] = []string{target}
	w.WriteHeader(http.StatusPermanentRedirect)
}

// dispatch is what site.Middleware does with a request, decided by
// classify.
type dispatch uint8

const (
	// dispatchPage: match the page routes; without a match, the
	// trailing-slash redirect, else next.
	dispatchPage   dispatch = iota
	dispatchIsland          // an island request
	dispatchFile            // a static file (served, or found by Match)
)

// classify is site.Middleware's order, shared with Match (PLAN.md §4g) so
// the two can't drift: island requests (any method), then static files
// (GET and HEAD), then pages. With w set, a static file is served; with w
// nil (Match), only looked up. rest is the island path after islandPrefix.
func (s *Site) classify(w http.ResponseWriter, r *http.Request) (d dispatch, rest string) {
	if rest, ok := strings.CutPrefix(r.URL.Path, islandPrefix); ok {
		return dispatchIsland, rest
	}
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		if w != nil {
			if s.serveFile(w, r) {
				return dispatchFile, ""
			}
		} else if s.hasFile(r) {
			return dispatchFile, ""
		}
	}
	return dispatchPage, ""
}

type notFoundHandler struct{ site *Site }

func (h notFoundHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.site.serveNotFound(w, r)
}

// serveNotFound renders 404.orbit, or writes an empty 404.
func (s *Site) serveNotFound(w http.ResponseWriter, r *http.Request) {
	route := s.router.notFound
	if route == nil {
		if s.route != nil {
			s.fillRouteOnly(r, RouteNotFound, notFoundRoutePattern, "", nil, nil)
		}
		emptyNotFound(w)
		return
	}
	s.servePage(w, r, s.acquire(r), route, true)
}

// acquire returns a pooled Ctx for a request to s.
func (s *Site) acquire(r *http.Request) *Ctx {
	c := acquireCtx(r, s.site, s.dev)
	c.keys = s.keys
	return c
}

func emptyNotFound(w http.ResponseWriter) {
	w.Header()["Content-Length"] = zeroLength
	w.WriteHeader(http.StatusNotFound)
}

// Shared, read-only header values. Each has cap == len, so an append by
// later middleware copies instead of writing into the shared array.
var (
	htmlContentType = []string{"text/html; charset=utf-8"}
	zeroLength      = []string{"0"}
)

var writerPool = sync.Pool{New: func() any { return &Writer{head: -1} }}

// servePage renders route with c, writes the response and releases c.
// notFound is set when route is the 404 page.
//
// When the site fills a RouteInfo (WithLocalsRoute), c.route is set from
// route unless the caller set it already (the 404 page rendered for a
// page's ErrNotFound keeps the page's route).
func (s *Site) servePage(w http.ResponseWriter, r *http.Request, c *Ctx, route *Route, notFound bool) {
	if s.route != nil {
		if c.route.kind == RouteNone {
			c.route = routeRef{kind: RoutePage, pattern: route.Pattern, file: route.File, names: c.paramNames, vals: c.paramVals}
			if notFound {
				c.route.kind, c.route.pattern = RouteNotFound, notFoundRoutePattern
			}
		}
		s.fillRoute(r, c)
	}
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
		// Never send a partial page: the buffer is dropped.
		wr.reset(nil)
		writerPool.Put(wr)
		putBuffer(buf)
		s.renderFailed(w, r, err, c.responseHeader(), &c.route, notFound)
		releaseCtx(c)
		return
	}
	wr.finishHead()
	wr.reset(nil)
	writerPool.Put(wr)

	h := w.Header()
	for k, v := range c.responseHeader() {
		h[k] = v
	}
	if _, ok := h["Content-Type"]; !ok {
		h["Content-Type"] = htmlContentType
	}
	h["Content-Length"] = contentLength(buf.Len())
	status := c.status
	if status == 0 {
		status = http.StatusOK
		if notFound {
			status = http.StatusNotFound
		}
	}
	w.WriteHeader(status)
	if r.Method != http.MethodHead {
		w.Write(buf.Bytes())
	}
	if c.cache.state&cacheSet != 0 && s.cacheProv == nil && !s.dev {
		warnNoProvider()
	}
	putBuffer(buf)
	releaseCtx(c)
}

// lengthValue holds a Content-Length header value and the bytes of its
// string, so the pair costs one allocation instead of two.
type lengthValue struct {
	v [1]string
	b [20]byte
}

// contentLength returns a new []string holding n in decimal. The value is
// never reused: net/http may read it after the handler returns.
func contentLength(n int) []string {
	lv := new(lengthValue)
	b := strconv.AppendInt(lv.b[:0], int64(n), 10)
	lv.v[0] = unsafe.String(&b[0], len(b))
	return lv.v[:]
}

// panicError is a panic recovered while rendering.
type panicError struct {
	value any
	pos   string // "file.orbit:line" of the innermost .orbit frame, or ""
}

// Unwrap returns the panic value when it's an error, so errors.Is and
// errors.As see a panic(err).
func (e *panicError) Unwrap() error {
	err, _ := e.value.(error)
	return err
}

func (e *panicError) Error() string {
	if e.pos != "" {
		return fmt.Sprintf("%s: panic: %v", e.pos, e.value)
	}
	return fmt.Sprintf("panic: %v", e.value)
}

// callRender calls render, turning a panic into a *panicError (see
// recoveredPanic). A panic with http.ErrAbortHandler is re-panicked, as
// net/http expects.
func callRender(render func(*Ctx, *Writer) error, c *Ctx, w *Writer) (err error) {
	defer func() {
		if p := recover(); p != nil {
			if p == http.ErrAbortHandler {
				panic(p)
			}
			err = recoveredPanic(p)
		}
	}()
	return render(c, w)
}

// recoveredPanic returns the *panicError for a recovered panic value p,
// with the innermost .orbit frame of the panicking goroutine. It's called
// from a deferred recover. A *panicError is kept as is: All re-panics a
// load's panic with the load's own position (PLAN.md §4f).
func recoveredPanic(p any) *panicError {
	if pe, ok := p.(*panicError); ok {
		return pe
	}
	return &panicError{value: p, pos: orbitFrame()}
}

// orbitFrame returns "file:line" of the innermost stack frame in a .orbit
// file (generated code carries line directives), or "". It's called from a
// deferred recover, while the panicking frames are still on the stack.
func orbitFrame() string {
	var pcs [64]uintptr
	n := runtime.Callers(3, pcs[:])
	frames := runtime.CallersFrames(pcs[:n])
	for {
		f, more := frames.Next()
		if strings.HasSuffix(f.File, ".orbit") {
			return relativeFile(f.File) + ":" + strconv.Itoa(f.Line)
		}
		if !more {
			return ""
		}
	}
}

// relativeFile shortens an absolute frame path (the compiler resolves
// relative line directives) to a slash path relative to the working
// directory, such as src/pages/blog/[slug].orbit, when it's inside it.
func relativeFile(file string) string {
	wd, err := os.Getwd()
	if err != nil || !filepath.IsAbs(file) {
		return file
	}
	rel, err := filepath.Rel(wd, file)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return file
	}
	return filepath.ToSlash(rel)
}

// renderFailed answers a render that returned err. head holds the headers
// the page set, kept for a redirect; prev is the failed render's route (nil
// from ServeError), which the 404 page rendered for ErrNotFound keeps;
// notFound is set when the 404 page itself failed. Any other error goes to
// the error handler first, then the 500.
func (s *Site) renderFailed(w http.ResponseWriter, r *http.Request, err error, head http.Header, prev *routeRef, notFound bool) {
	var re *RedirectError
	switch {
	case errors.As(err, &re):
		h := w.Header()
		for k, v := range head {
			h[k] = v
		}
		http.Redirect(w, r, re.Location, re.Code)
	case errors.Is(err, ErrNotFound):
		if notFound || s.router.notFound == nil {
			if prev == nil && s.route != nil {
				s.fillRouteOnly(r, RouteNotFound, notFoundRoutePattern, "", nil, nil)
			}
			emptyNotFound(w)
			return
		}
		c := s.acquire(r)
		if prev != nil && s.route != nil {
			c.route = *prev
		}
		s.servePage(w, r, c, s.router.notFound, true)
	default:
		if s.onError != nil && s.onError(w, r, err) {
			return
		}
		s.serverError(w, r, err)
	}
}

const (
	errorPageStart = `<!DOCTYPE html><html><head><meta charset="utf-8"><title>500 Internal Server Error</title></head><body><h1>500 Internal Server Error</h1>`
	errorPageEnd   = "</body></html>\n"
)

// serverError logs err and writes the built-in 500 page. Only in dev does
// the page show the error.
func (s *Site) serverError(w http.ResponseWriter, r *http.Request, err error) {
	log.Printf("orbit: %s %s: %v", r.Method, r.URL.Path, err)
	var body bytes.Buffer
	body.WriteString(errorPageStart)
	if s.dev {
		body.WriteString("<pre>")
		escapeString(&body, err.Error())
		body.WriteString("</pre>")
	}
	body.WriteString(errorPageEnd)
	h := w.Header()
	h["Content-Type"] = htmlContentType
	h["Content-Length"] = contentLength(body.Len())
	w.WriteHeader(http.StatusInternalServerError)
	if r.Method != http.MethodHead {
		w.Write(body.Bytes())
	}
}
