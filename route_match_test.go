package orbit

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// Tests of Site.Match, matching before serving (PLAN.md §4g).

func matchRoutes() []Route {
	return append(routeTestRoutes(false),
		Route{Pattern: "/", File: "src/pages/index.orbit", Render: page("home")},
		Route{Pattern: "/shadow", File: "src/pages/shadow.orbit", Render: page("page")},
		Route{Pattern: "/rss.xml", File: "src/pages/rss.xml.orbit", Render: page("rss")},
	)
}

// matchSite is a site with pages, islands and static files: robots.txt,
// an /_orbit/ asset and a public file named like the /shadow page.
func matchSite(t *testing.T, opts ...Option) *Site {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "robots.txt"), "robots")
	writeFile(t, filepath.Join(dir, "shadow"), "public shadow")
	writeFile(t, filepath.Join(dir, "_orbit", "app-1234.js"), "js")
	t.Setenv("ORBIT_STATIC", "")
	t.Setenv("ORBIT_PUBLIC", "")
	t.Setenv("ORBIT_KEY", "")
	return NewSite(matchRoutes(), islandTable, append([]Option{WithStaticDir(dir), withRoute}, opts...)...)
}

func sameRoute(a, b *RouteInfo) bool {
	return a.Kind == b.Kind && a.Pattern == b.Pattern && a.File == b.File && paramString(a) == paramString(b)
}

func TestSiteMatch(t *testing.T) {
	s := matchSite(t)
	build := s.keys.buildID
	island := routeWant{RouteIsland, islandPattern, "src/components/Comments.orbit", "buildID=" + build + ",id=" + commentsID}
	for _, tt := range []struct {
		method, target string
		want           routeWant
	}{
		{"GET", "/", routeWant{RoutePage, "/", "src/pages/index.orbit", ""}},
		{"GET", "/ok", routeWant{RoutePage, "/ok", "src/pages/ok.orbit", ""}},
		{"POST", "/ok", routeWant{RoutePage, "/ok", "src/pages/ok.orbit", ""}},
		{"GET", "/ok?x=1", routeWant{RoutePage, "/ok", "src/pages/ok.orbit", ""}},
		{"GET", "/blog/hello", routeWant{RoutePage, "/blog/[slug]", "src/pages/blog/[slug].orbit", "slug=hello"}},
		{"GET", "/blog/a%20b%2Fc", routeWant{RoutePage, "/blog/[slug]", "src/pages/blog/[slug].orbit", "slug=a b/c"}},
		{"GET", "/docs/a/b/c", routeWant{RoutePage, "/docs/[...path]", "src/pages/docs/[...path].orbit", "path=a/b/c"}},
		{"GET", "/p/1/2/3/4/5/6/7/8/9", routeWant{RoutePage, "/p/[a]/[b]/[c]/[d]/[e]/[f]/[g]/[h]/[i]", "src/pages/p.orbit", "a=1,b=2,c=3,d=4,e=5,f=6,g=7,h=8,i=9"}},
		{"GET", "/robots.txt", routeWant{RouteFile, "/robots.txt", "", ""}},
		{"HEAD", "/robots.txt", routeWant{RouteFile, "/robots.txt", "", ""}},
		{"POST", "/robots.txt", routeWant{}}, // files are GET and HEAD only; no page: next
		{"GET", "/_orbit/app-1234.js", routeWant{RouteFile, "/_orbit/app-1234.js", "", ""}},
		{"GET", "/_orbit/missing.js", routeWant{}},
		{"GET", "/nothing.zip", routeWant{}},
		{"GET", "/robots.txt/", routeWant{}},
		{"GET", "/shadow", routeWant{RouteFile, "/shadow", "", ""}},                        // the public file wins
		{"POST", "/shadow", routeWant{RoutePage, "/shadow", "src/pages/shadow.orbit", ""}}, // files are GET and HEAD only
		{"GET", "/ok/", routeWant{}},                                                       // the slash redirect
		{"GET", "/unknown", routeWant{}},                                                   // next, maybe NotFound
		{"GET", "/_orbit/island/" + build + "/" + commentsID + "?p=x", island},
		{"DELETE", "/_orbit/island/" + build + "/" + commentsID, island},
		{"GET", "/_orbit/island/b/Nope-00000000", routeWant{RouteIsland, islandPattern, "", "buildID=b,id=Nope-00000000"}},
	} {
		ri := s.Match(httptest.NewRequest(tt.method, tt.target, nil))
		checkRoute(t, tt.method+" "+tt.target, &ri, tt.want)
	}

	// The strings stay valid: a 9-param match doesn't share storage.
	a := s.Match(httptest.NewRequest("GET", "/p/1/2/3/4/5/6/7/8/9", nil))
	_ = s.Match(httptest.NewRequest("GET", "/p/a/b/c/d/e/f/g/h/i", nil))
	if a.Param("i") != "9" {
		t.Errorf("retained match: i=%q", a.Param("i"))
	}
}

// TestSiteMatchAgrees: for every path × method × slash mode, in prod and dev,
// Match returns what site.Middleware fills.
// matchCorpus is the request paths of TestSiteMatchAgrees and
// TestTrailingSlashAgrees, for matchSite; BUILD stands for the build ID.
var matchCorpus = []string{
	"/", "/ok", "/ok/", "//ok", "/redirect", "/gone", "/blog/hello", "/blog/hello/", "/blog/a%20b%2Fc",
	"/blog/a%2F", "/blog/a%2F/", "/blog/hello/?q=go", "/blog/hello?q=go",
	"/blog", "/docs", "/docs/", "/docs/a/b/c", "/p/1/2/3/4/5/6/7/8/9", "/p/1/2", "/rss.xml", "/rss.xml/",
	"/robots.txt", "/robots.txt/", "/_orbit/app-1234.js", "/_orbit/missing.js", "/_orbit/", "/shadow", "/shadow/",
	"/unknown", "/unknown/", "/nothing.zip", "/_orbit/island/", "/_orbit/island/b", "/_orbit/island/b/Nope-00000000",
	"/_orbit/island/BUILD/" + commentsID, "/_orbit/island/BUILD/" + commentsID + "/extra",
}

var matchMethods = []string{"GET", "HEAD", "POST", "DELETE"}

var modeName = map[TrailingSlash]string{TrailingSlashNever: "never", TrailingSlashAlways: "always"}

func TestSiteMatchAgrees(t *testing.T) {
	captureLog(t)
	kinds := map[RouteKind]int{}
	for _, dev := range []string{"", "1"} {
		for _, mode := range []TrailingSlash{TrailingSlashNever, TrailingSlashAlways} {
			t.Setenv("ORBIT_DEV", dev)
			s := matchSite(t, WithTrailingSlash(mode))
			var got *routeLocals
			h := attachLocals(s.Middleware(http.NotFoundHandler()), &got)
			for _, p := range matchCorpus {
				p = strings.Replace(p, "BUILD", s.keys.buildID, 1)
				for _, method := range matchMethods {
					name := method + " " + p + " dev=" + dev + " mode=" + modeName[mode]
					ri := s.Match(httptest.NewRequest(method, p, nil))
					h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(method, p, nil))
					kinds[ri.Kind]++
					if !sameRoute(&ri, &got.Route) {
						t.Errorf("%s: Match {%d %q %q %q}, Middleware filled {%d %q %q %q}", name,
							ri.Kind, ri.Pattern, ri.File, paramString(&ri),
							got.Route.Kind, got.Route.Pattern, got.Route.File, paramString(&got.Route))
					}
				}
			}
		}
	}
	if kinds[RoutePage] == 0 || kinds[RouteIsland] == 0 || kinds[RouteFile] == 0 || kinds[RouteNone] == 0 {
		t.Errorf("corpus kinds %v: want pages, islands, files and none", kinds)
	}
}

// TestSiteMatchThenServe: an early match in Locals is overwritten by Orbit's
// own fill whenever the request reaches Orbit.
func TestSiteMatchThenServe(t *testing.T) {
	s := matchSite(t)
	var got *routeLocals
	early := func(rewrite string, next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r, l := EnsureLocals[routeLocals](r)
			l.Route = s.Match(r)
			if rewrite != "" {
				r.URL.Path = rewrite
			}
			next.ServeHTTP(w, r)
			got = l
		})
	}
	// Same route: Orbit fills the same value.
	early("", s.Handler()).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/blog/hello", nil))
	checkRoute(t, "same", &got.Route, routeWant{RoutePage, "/blog/[slug]", "src/pages/blog/[slug].orbit", "slug=hello"})
	// Rewritten after Match: Orbit's value wins.
	early("/ok", s.Handler()).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/blog/hello", nil))
	checkRoute(t, "rewritten", &got.Route, routeWant{RoutePage, "/ok", "src/pages/ok.orbit", ""})
	// No page: RouteNone, then Handler's NotFound records /404.
	early("", s.Handler()).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/unknown", nil))
	checkRoute(t, "unknown", &got.Route, routeWant{RouteNotFound, "/404", "", ""})
	// Answered before Orbit (a gate): the early match stays.
	gate := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/signin", http.StatusSeeOther) })
	early("", gate).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/blog/hello", nil))
	checkRoute(t, "gate", &got.Route, routeWant{RoutePage, "/blog/[slug]", "src/pages/blog/[slug].orbit", "slug=hello"})
}

// TestSiteMatchFileDeleted: the documented production deviation. A file in
// the static index counts for Match without being opened; if it's gone,
// site.Middleware falls back to the pages (here /shadow). Dev opens it in
// both.
func TestSiteMatchFileDeleted(t *testing.T) {
	for _, dev := range []string{"", "1"} {
		t.Setenv("ORBIT_DEV", dev)
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, "shadow"), "public shadow")
		t.Setenv("ORBIT_STATIC", "")
		t.Setenv("ORBIT_PUBLIC", "")
		t.Setenv("ORBIT_KEY", "")
		s := NewSite(matchRoutes(), islandTable, WithStaticDir(dir), withRoute)
		if err := os.Remove(filepath.Join(dir, "shadow")); err != nil {
			t.Fatal(err)
		}
		ri := s.Match(httptest.NewRequest("GET", "/shadow", nil))
		var got *routeLocals
		attachLocals(s.Middleware(http.NotFoundHandler()), &got).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/shadow", nil))
		page := routeWant{RoutePage, "/shadow", "src/pages/shadow.orbit", ""}
		if dev == "" {
			checkRoute(t, "prod Match", &ri, routeWant{RouteFile, "/shadow", "", ""})
		} else {
			checkRoute(t, "dev Match", &ri, page)
		}
		checkRoute(t, "Middleware dev="+dev, &got.Route, page)
	}
}

// TestSiteMatchCacheHit: a route-cache HIT fills the same route as Match.
func TestSiteMatchCacheHit(t *testing.T) {
	s, _ := cacheSite(t, []Route{{Pattern: "/blog/[slug]", File: "src/pages/blog/[slug].orbit", Render: func(c *Ctx, w *Writer) error {
		c.Cache().Set(hour)
		w.Raw("post")
		return nil
	}}}, withRoute)
	var got *routeLocals
	h := attachLocals(s.Handler(), &got)
	for i, want := range []string{"MISS", "HIT"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", "/blog/hello", nil))
		if rec.Header().Get("X-Orbit-Cache") != want {
			t.Fatalf("request %d: %q, want %q", i, rec.Header().Get("X-Orbit-Cache"), want)
		}
		ri := s.Match(httptest.NewRequest("GET", "/blog/hello", nil))
		if !sameRoute(&ri, &got.Route) || ri.Kind != RoutePage {
			t.Errorf("%s: filled {%d %q}, Match {%d %q}", want, got.Route.Kind, got.Route.Pattern, ri.Kind, ri.Pattern)
		}
	}
}

func TestSiteMatchConcurrent(t *testing.T) {
	s := matchSite(t)
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 100 {
				if ri := s.Match(httptest.NewRequest("GET", "/blog/hello", nil)); ri.Param("slug") != "hello" {
					t.Error("concurrent match:", ri.Param("slug"))
				}
			}
		})
	}
	wg.Wait()
}

func TestSiteMatchAllocs(t *testing.T) {
	if testing.Short() || raceEnabled {
		t.Skip("allocation counts (not under -short or -race)")
	}
	t.Setenv("ORBIT_DEV", "")
	s := matchSite(t)
	for _, tt := range []struct {
		method, target string
		want           float64
	}{
		{"GET", "/", 0},
		{"GET", "/blog/hello", 0},
		{"GET", "/docs/a/b/c", 0},
		{"POST", "/ok", 0},
		{"GET", "/robots.txt", 0},
		{"HEAD", "/robots.txt", 0},
		{"GET", "/_orbit/app-1234.js", 0},
		{"GET", "/nothing.zip", 0},
		{"GET", "/ok/", 0},
		{"GET", "/unknown", 0},
		{"GET", "/_orbit/island/" + s.keys.buildID + "/" + commentsID, 0},
		{"GET", "/p/1/2/3/4/5/6/7/8/9", 1}, // the 9 values
	} {
		r := httptest.NewRequest(tt.method, tt.target, nil)
		var sink RouteInfo
		got := testing.AllocsPerRun(1000, func() { sink = s.Match(r) })
		if got != tt.want {
			t.Errorf("Match(%s %s): %v allocs, want %v", tt.method, tt.target, got, tt.want)
		}
		_ = sink
	}
	// In middleware, after EnsureLocals: nothing more.
	r, l := EnsureLocals[routeLocals](httptest.NewRequest("GET", "/blog/hello", nil))
	if got := testing.AllocsPerRun(1000, func() { l.Route = s.Match(r) }); got != 0 {
		t.Errorf("l.Route = Match(r): %v allocs, want 0", got)
	}
	// Reading the params, from a variable or straight off Match: nothing.
	var sink string
	if got := testing.AllocsPerRun(1000, func() { sink = s.Match(r).Param("slug") }); got != 0 {
		t.Errorf("Match(r).Param: %v allocs, want 0", got)
	}
	if got := testing.AllocsPerRun(1000, func() {
		for _, v := range l.Route.Params() {
			sink = v
		}
	}); got != 0 {
		t.Errorf("l.Route.Params(): %v allocs, want 0", got)
	}
	if got := testing.AllocsPerRun(1000, func() {
		for _, v := range s.Match(r).Params() {
			sink = v
		}
	}); got != 0 {
		t.Errorf("Match(r).Params(): %v allocs, want 0", got)
	}
	_ = sink
}

func BenchmarkSiteMatch(b *testing.B) {
	b.Setenv("ORBIT_STATIC", "")
	b.Setenv("ORBIT_DEV", "")
	s := NewSite(matchRoutes(), islandTable, WithStaticDir(b.TempDir()))
	for _, target := range []string{"/blog/hello", "/unknown"} {
		r := httptest.NewRequest("GET", target, nil)
		b.Run(target, func(b *testing.B) {
			b.ReportAllocs()
			var sink RouteInfo
			for b.Loop() {
				sink = s.Match(r)
			}
			_ = sink
		})
	}
}
