package orbit

import (
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// Tests of the RouteInfo field in Locals (PLAN.md §4g).

type routeLocals struct {
	ID    string
	Route RouteInfo
}

var withRoute = WithLocalsRoute(func(l *routeLocals) *RouteInfo { return &l.Route })

// routeWritten writes the RouteInfo the page sees through LocalsOf.
func routeWritten(c *Ctx, w *Writer) {
	ri := &LocalsOf[routeLocals](c).Route
	fmt.Fprintf(w.buf, "kind=%d pattern=%s file=%s params=%s", ri.Kind, ri.Pattern, ri.File, paramString(ri))
}

func paramString(ri *RouteInfo) string {
	var parts []string
	for name, v := range ri.Params() {
		parts = append(parts, name+"="+v)
	}
	return strings.Join(parts, ",")
}

func routeTestRoutes(notFound bool) []Route {
	page := func(c *Ctx, w *Writer) error {
		routeWritten(c, w)
		return nil
	}
	routes := []Route{
		{Pattern: "/ok", File: "src/pages/ok.orbit", Render: page},
		{Pattern: "/redirect", File: "src/pages/redirect.orbit", Render: func(c *Ctx, w *Writer) error { return c.Redirect("/ok", 302) }},
		{Pattern: "/error", File: "src/pages/error.orbit", Render: func(c *Ctx, w *Writer) error { return errors.New("db down") }},
		{Pattern: "/panic", File: "src/pages/panic.orbit", Render: func(c *Ctx, w *Writer) error { panic("boom") }},
		{Pattern: "/gone", File: "src/pages/gone.orbit", Render: func(c *Ctx, w *Writer) error { return ErrNotFound }},
		{Pattern: "/blog/[slug]", File: "src/pages/blog/[slug].orbit", Render: page},
		{Pattern: "/docs/[...path]", File: "src/pages/docs/[...path].orbit", Render: page},
		{Pattern: "/p/[a]/[b]/[c]/[d]/[e]/[f]/[g]/[h]/[i]", File: "src/pages/p.orbit", Render: page},
	}
	if notFound {
		routes = append(routes, Route{Pattern: "404", File: "src/pages/404.orbit", Render: func(c *Ctx, w *Writer) error {
			w.Raw("404: ")
			routeWritten(c, w)
			return nil
		}})
	}
	return routes
}

// attachLocals is the app's middleware: it attaches the locals and records
// them after next returns.
func attachLocals(next http.Handler, got **routeLocals) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r, l := EnsureLocals[routeLocals](r)
		*got = nil
		next.ServeHTTP(w, r)
		*got = l
	})
}

type routeWant struct {
	kind    RouteKind
	pattern string
	file    string
	params  string
}

func checkRoute(t *testing.T, name string, ri *RouteInfo, want routeWant) {
	t.Helper()
	if ri.Kind != want.kind || ri.Pattern != want.pattern || ri.File != want.file || paramString(ri) != want.params {
		t.Errorf("%s: route {%d %q %q %q}, want {%d %q %q %q}", name, ri.Kind, ri.Pattern, ri.File, paramString(ri),
			want.kind, want.pattern, want.file, want.params)
	}
}

func TestRouteInfoPages(t *testing.T) {
	captureLog(t)
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "robots.txt"), "robots")
	writeFile(t, filepath.Join(dir, "_orbit", "app-1234.js"), "js")
	for _, with404 := range []bool{true, false} {
		t.Run(fmt.Sprintf("404.orbit=%v", with404), func(t *testing.T) {
			s := testSite(t, routeTestRoutes(with404), withRoute, WithStaticDir(dir))
			var got *routeLocals
			passed := false
			next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				passed = true
				s.NotFound().ServeHTTP(w, r)
			})
			viaNext := attachLocals(s.Middleware(next), &got)
			mw := attachLocals(s.Middleware(http.NotFoundHandler()), &got)
			nf404 := routeWant{RouteNotFound, "/404", "", ""}
			if with404 {
				nf404.file = "src/pages/404.orbit"
			}
			gone := routeWant{RoutePage, "/gone", "src/pages/gone.orbit", ""}
			for _, tt := range []struct {
				target string
				code   int
				want   routeWant
			}{
				{"/ok", 200, routeWant{RoutePage, "/ok", "src/pages/ok.orbit", ""}},
				{"/redirect", 302, routeWant{RoutePage, "/redirect", "src/pages/redirect.orbit", ""}},
				{"/error", 500, routeWant{RoutePage, "/error", "src/pages/error.orbit", ""}},
				{"/panic", 500, routeWant{RoutePage, "/panic", "src/pages/panic.orbit", ""}},
				{"/gone", 404, gone}, // ErrNotFound from a page stays RoutePage
				{"/blog/hello", 200, routeWant{RoutePage, "/blog/[slug]", "src/pages/blog/[slug].orbit", "slug=hello"}},
				{"/blog/a%20b%2Fc", 200, routeWant{RoutePage, "/blog/[slug]", "src/pages/blog/[slug].orbit", "slug=a b/c"}},
				{"/docs/a/b/c", 200, routeWant{RoutePage, "/docs/[...path]", "src/pages/docs/[...path].orbit", "path=a/b/c"}},
				{"/p/1/2/3/4/5/6/7/8/9", 200, routeWant{RoutePage, "/p/[a]/[b]/[c]/[d]/[e]/[f]/[g]/[h]/[i]", "src/pages/p.orbit", "a=1,b=2,c=3,d=4,e=5,f=6,g=7,h=8,i=9"}},
				{"/robots.txt", 200, routeWant{RouteFile, "/robots.txt", "", ""}},
				{"/_orbit/app-1234.js", 200, routeWant{RouteFile, "/_orbit/app-1234.js", "", ""}},
				{"/ok/", 308, routeWant{}},
				{"/unknown", 404, routeWant{}}, // passed to next
			} {
				rec := httptest.NewRecorder()
				mw.ServeHTTP(rec, httptest.NewRequest("GET", tt.target, nil))
				if rec.Code != tt.code {
					t.Errorf("%s: status %d, want %d", tt.target, rec.Code, tt.code)
				}
				checkRoute(t, tt.target, &got.Route, tt.want)
			}

			// A request through Middleware, then NotFound.
			passed = false
			rec := httptest.NewRecorder()
			viaNext.ServeHTTP(rec, httptest.NewRequest("GET", "/unknown", nil))
			if !passed || rec.Code != 404 {
				t.Fatalf("/unknown via next: passed %v, %d", passed, rec.Code)
			}
			checkRoute(t, "Middleware then NotFound", &got.Route, nf404)
			if with404 && rec.Body.String() != "404: kind=2 pattern=/404 file=src/pages/404.orbit params=" {
				t.Errorf("404 page sees %q", rec.Body)
			}
			rec = httptest.NewRecorder()
			attachLocals(s.Handler(), &got).ServeHTTP(rec, httptest.NewRequest("GET", "/unknown", nil))
			checkRoute(t, "Handler", &got.Route, nf404)

			// The 404 page rendered for a page's ErrNotFound sees the page.
			if with404 {
				rec = httptest.NewRecorder()
				mw.ServeHTTP(rec, httptest.NewRequest("GET", "/gone", nil))
				if want := "404: kind=1 pattern=/gone file=src/pages/gone.orbit params="; rec.Body.String() != want {
					t.Errorf("404 page for /gone sees %q, want %q", rec.Body, want)
				}
			}
		})
	}
}

func TestRouteInfoServeError(t *testing.T) {
	s := testSite(t, routeTestRoutes(false), withRoute)
	var got *routeLocals
	h := attachLocals(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.ServeError(w, r, ErrNotFound)
	}), &got)
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/x", nil))
	checkRoute(t, "ServeError(ErrNotFound)", &got.Route, routeWant{RouteNotFound, "/404", "", ""})
}

func TestRouteInfoTrailingSlashAlways(t *testing.T) {
	s := testSite(t, routeTestRoutes(false), withRoute, WithTrailingSlash(TrailingSlashAlways))
	var got *routeLocals
	h := attachLocals(s.Handler(), &got)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/ok", nil))
	if rec.Code != 308 {
		t.Fatalf("/ok: %d", rec.Code)
	}
	checkRoute(t, "/ok (redirect)", &got.Route, routeWant{})
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/ok/", nil))
	checkRoute(t, "/ok/", &got.Route, routeWant{RoutePage, "/ok", "src/pages/ok.orbit", ""})
}

// TestRouteInfoLazy: without middleware attaching locals, pages see the
// route in the Locals that LocalsOf allocates.
func TestRouteInfoLazy(t *testing.T) {
	s := testSite(t, routeTestRoutes(true), withRoute)
	h := s.Handler()
	for _, tt := range []struct{ target, want string }{
		{"/blog/hello", "kind=1 pattern=/blog/[slug] file=src/pages/blog/[slug].orbit params=slug=hello"},
		{"/p/1/2/3/4/5/6/7/8/9", "kind=1 pattern=/p/[a]/[b]/[c]/[d]/[e]/[f]/[g]/[h]/[i] file=src/pages/p.orbit params=a=1,b=2,c=3,d=4,e=5,f=6,g=7,h=8,i=9"},
		{"/gone", "404: kind=1 pattern=/gone file=src/pages/gone.orbit params="},
		{"/unknown", "404: kind=2 pattern=/404 file=src/pages/404.orbit params="},
	} {
		if rec := do(h, "GET", tt.target); rec.Body.String() != tt.want {
			t.Errorf("%s: %q, want %q", tt.target, rec.Body, tt.want)
		}
	}
	// Without WithLocalsRoute nothing is filled.
	plain := testSite(t, routeTestRoutes(false))
	if rec := do(plain.Handler(), "GET", "/blog/hello"); rec.Body.String() != "kind=0 pattern= file= params=" {
		t.Errorf("without the option: %q", rec.Body)
	}
}

func TestRouteInfoErrorHandler(t *testing.T) {
	captureLog(t)
	var seen string
	s := testSite(t, routeTestRoutes(false), withRoute, WithErrorHandler(func(w http.ResponseWriter, r *http.Request, err error) bool {
		l := LocalsFromRequest[routeLocals](r)
		seen = fmt.Sprintf("%d %s %s", l.Route.Kind, l.Route.Pattern, l.Route.File)
		w.WriteHeader(503)
		return true
	}))
	var got *routeLocals
	h := attachLocals(s.Handler(), &got)
	for _, target := range []string{"/error", "/panic"} {
		seen = ""
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", target, nil))
		if want := "1 " + target + " src/pages" + target + ".orbit"; rec.Code != 503 || seen != want {
			t.Errorf("%s: %d, the handler saw %q, want %q", target, rec.Code, seen, want)
		}
	}
}

// TestRouteInfoShared: two middlewares share one value, and what they keep
// stays valid after the Ctx is reused.
func TestRouteInfoShared(t *testing.T) {
	s := testSite(t, routeTestRoutes(false), withRoute)
	var outer, inner *routeLocals
	h := attachLocals(attachLocals(s.Handler(), &inner), &outer)
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/blog/caf%C3%A9", nil))
	if outer != inner {
		t.Fatal("the middlewares got different locals")
	}
	kept := outer.Route // a copy
	keptParams := maps.Collect(outer.Route.Params())
	first := outer
	for i := range 20 {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", fmt.Sprintf("/blog/other-%d", i), nil))
	}
	want := routeWant{RoutePage, "/blog/[slug]", "src/pages/blog/[slug].orbit", "slug=café"}
	checkRoute(t, "kept copy", &kept, want)
	checkRoute(t, "kept locals", &first.Route, want)
	if keptParams["slug"] != "café" || kept.Param("slug") != "café" || kept.Param("nope") != "" {
		t.Errorf("params %v, Param %q", keptParams, kept.Param("slug"))
	}
	// Params stops when the loop breaks.
	var names []string
	for name := range first.Route.Params() {
		names = append(names, name)
		break
	}
	if !slices.Equal(names, []string{"slug"}) {
		t.Errorf("names %v", names)
	}
}

func TestRouteInfoIslands(t *testing.T) {
	t.Setenv("ORBIT_KEY", "")
	captureLog(t)
	s := islandSite(t, withRoute)
	var got *routeLocals
	h := attachLocals(s.Handler(), &got)
	src, tok := placeholder(t, s.Handler(), "")
	errSrc, errTok := placeholder(t, s.Handler(), "error")
	build := strings.Split(src, "/")[3]
	known := routeWant{RouteIsland, "/_orbit/island/[buildID]/[id]", "src/components/Comments.orbit", "buildID=" + build + ",id=" + commentsID}
	for _, tt := range []struct {
		method, target string
		code           int
		want           routeWant
	}{
		{"GET", src + "?p=" + tok, 200, known},
		{"GET", src + "?p=bad", 400, known},
		{"GET", errSrc + "?p=" + errTok, 500, known},
		{"DELETE", src, 405, known},
		{"GET", "/_orbit/island/" + build + "/Nope-00000000?p=" + tok, 404, routeWant{RouteIsland, "/_orbit/island/[buildID]/[id]", "", "buildID=" + build + ",id=Nope-00000000"}},
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(tt.method, tt.target, nil))
		if rec.Code != tt.code {
			t.Errorf("%s %s: %d, want %d", tt.method, tt.target, rec.Code, tt.code)
		}
		checkRoute(t, tt.method+" "+tt.target, &got.Route, tt.want)
	}

	// An island component reading the route through LocalsOf, without
	// middleware.
	isl := []Island{NewIsland("Route-00000000", "src/components/Route.orbit", func(c *Ctx, w *Writer, p struct{}, s Slots) error {
		routeWritten(c, w)
		fmt.Fprintf(w.buf, " ctx.Param=%q", c.Param("id"))
		return nil
	})}
	s2 := NewSite(nil, isl, WithStaticDir(t.TempDir()), withRoute)
	tok2 := sealerOf(s2.keys).Seal("Route-00000000", s2.keys.buildID, []byte(`{}`))
	rec := do(s2.Handler(), "GET", "/_orbit/island/"+s2.keys.buildID+"/Route-00000000?p="+tok2)
	want := "kind=3 pattern=/_orbit/island/[buildID]/[id] file=src/components/Route.orbit params=buildID=" + s2.keys.buildID + ",id=Route-00000000 ctx.Param=\"\""
	if rec.Body.String() != want {
		t.Errorf("island sees %q, want %q", rec.Body, want)
	}
}

func TestRouteInfoZero(t *testing.T) {
	var ri RouteInfo
	if ri.Param("x") != "" || paramString(&ri) != "" || ri.Kind != RouteNone {
		t.Error("zero RouteInfo isn't empty")
	}
	// Refilling with fewer params clears the old ones.
	ri.set(&routeRef{kind: RoutePage, names: []string{"a", "b"}, vals: []string{"1", "2"}})
	ri.set(&routeRef{kind: RoutePage, names: []string{"a"}, vals: []string{"x"}})
	if paramString(&ri) != "a=x" || ri.buf[1] != "" {
		t.Errorf("refilled: %q %q", paramString(&ri), ri.buf[1])
	}
}

// TestServeRouteInfoAllocs: Orbit's handler adds no allocation for the
// RouteInfo field, with locals attached or not; a route with more than 8
// params adds one.
func TestServeRouteInfoAllocs(t *testing.T) {
	if testing.Short() || raceEnabled {
		t.Skip("allocation counts (not under -short or -race)")
	}
	t.Setenv("ORBIT_DEV", "")
	routes := append(slices.Clone(allocRoutes), Route{Pattern: "/p/[a]/[b]/[c]/[d]/[e]/[f]/[g]/[h]/[i]", File: "src/pages/p.orbit", Render: allocPage})
	count := func(s *Site, r *http.Request) float64 {
		h := s.Middleware(http.NotFoundHandler())
		fw := newFakeResponseWriter()
		allocs := testing.AllocsPerRun(1000, func() {
			fw.reset()
			h.ServeHTTP(fw, r)
		})
		if fw.status != 200 {
			t.Fatalf("%s: %d", r.URL, fw.status)
		}
		return allocs
	}
	plain := testSite(t, routes, WithSite("https://example.com"))
	s := testSite(t, routes, WithSite("https://example.com"), withRoute)
	for _, tt := range []struct {
		target string
		extra  float64
	}{
		{"/", 0},
		{"/blog/hello-world", 0},
		{"/p/1/2/3/4/5/6/7/8/9", 1}, // a copy of the 9 values
	} {
		for _, attached := range []bool{true, false} {
			r := httptest.NewRequest("GET", tt.target, nil)
			var l *routeLocals
			if attached {
				r, l = EnsureLocals[routeLocals](r)
			}
			base, got := count(plain, r), count(s, r)
			want := base
			if attached {
				want += tt.extra // nothing reads the route without locals
			}
			if got != want {
				t.Errorf("serving %s (locals attached: %v): %v allocs, want %v (%v without the field)", tt.target, attached, got, want, base)
			}
			if tt.target != "/p/1/2/3/4/5/6/7/8/9" && base != 1 {
				t.Errorf("serving %s without the field: %v allocs, want 1", tt.target, base)
			}
			if attached && l.Route.Kind != RoutePage {
				t.Errorf("%s: route not filled", tt.target)
			}
		}
	}

	// Islands: the same count with and without the option.
	t.Setenv("ORBIT_KEY", "")
	islandCount := func(opts ...Option) float64 {
		s := islandSite(t, opts...)
		src, tok := placeholder(t, s.Handler(), "")
		r, _ := EnsureLocals[routeLocals](httptest.NewRequest("GET", src+"?p="+tok, nil))
		fw := newFakeResponseWriter()
		h := s.Handler()
		return testing.AllocsPerRun(1000, func() {
			fw.reset()
			h.ServeHTTP(fw, r)
		})
	}
	if without, with := islandCount(), islandCount(withRoute); with != without {
		t.Errorf("island request: %v allocs with the RouteInfo field, %v without", with, without)
	}
}

func BenchmarkServeRouteInfo(b *testing.B) {
	b.Setenv("ORBIT_DEV", "")
	b.Setenv("ORBIT_STATIC", b.TempDir())
	for _, tt := range []struct {
		name     string
		opts     []Option
		attached bool
	}{
		{"no field", nil, false},
		{"no field, locals attached", nil, true},
		{"field, locals attached", []Option{withRoute}, true},
		{"field, no locals", []Option{withRoute}, false},
	} {
		b.Run(tt.name, func(b *testing.B) {
			h := NewSite(allocRoutes, nil, tt.opts...).Middleware(http.NotFoundHandler())
			r := httptest.NewRequest("GET", "/blog/hello-world", nil)
			if tt.attached {
				r, _ = EnsureLocals[routeLocals](r)
			}
			fw := newFakeResponseWriter()
			b.ReportAllocs()
			for b.Loop() {
				fw.reset()
				h.ServeHTTP(fw, r)
			}
		})
	}
}
