package lyr

import (
	"fmt"
	"maps"
	"net/url"
	"strings"
	"testing"
)

// routesFor builds a route table from patterns; File is derived so error
// messages can be checked.
func routesFor(patterns ...string) []Route {
	rs := make([]Route, len(patterns))
	for i, p := range patterns {
		rs[i] = Route{Pattern: p, File: fileFor(p)}
	}
	return rs
}

func fileFor(p string) string {
	switch p {
	case notFoundPattern:
		return "src/pages/404.lyr"
	case "/":
		return "src/pages/index.lyr"
	}
	return "src/pages" + p + ".lyr"
}

func mustRouter(t testing.TB, patterns ...string) *router {
	t.Helper()
	rt, err := newRouter(routesFor(patterns...))
	if err != nil {
		t.Fatal(err)
	}
	return rt
}

// params is a test-only view of a match's params, by name.
type params map[string]string

// matchParams calls match and returns the params as a map (nil when the
// route has none), checking that names and values line up.
func matchParams(t testing.TB, rt *router, path string) (*Route, params, bool) {
	t.Helper()
	var buf [8]string
	cr, vals := rt.match(path, buf[:0])
	if cr == nil {
		if len(vals) != 0 {
			t.Fatalf("match(%q): no match but %d values", path, len(vals))
		}
		return nil, nil, false
	}
	if len(vals) != len(cr.names) {
		t.Fatalf("match(%q): %d values for names %q", path, len(vals), cr.names)
	}
	if cr.names == nil {
		return cr.route, nil, true
	}
	m := make(params, len(vals))
	for i, n := range cr.names {
		m[n] = vals[i]
	}
	return cr.route, m, true
}

func TestMatch(t *testing.T) {
	rt := mustRouter(t,
		"/",
		"/about",
		"/blog",
		"/blog/new",
		"/blog/[slug]",
		"/blog/[...rest]",
		"/blog/new/[id]",
		"/blog/[slug]/edit",
		"/blog/[slug]/comments/[n]",
		"/docs/[...path]",
		"/shop/[cat]/[item]",
		"/[...rest]",
		notFoundPattern,
	)
	tests := []struct {
		path    string
		pattern string // "" means no match
		params  params
	}{
		// root and statics
		{"/", "/", nil},
		{"/about", "/about", nil},
		{"/blog", "/blog", nil}, // blog/index.lyr
		// priority: static > param > rest
		{"/blog/new", "/blog/new", nil},
		{"/blog/hello", "/blog/[slug]", params{"slug": "hello"}},
		{"/blog/2026/10/hello", "/blog/[...rest]", params{"rest": "2026/10/hello"}},
		{"/about/x", "/[...rest]", params{"rest": "about/x"}},
		{"/nothing", "/[...rest]", params{"rest": "nothing"}},
		// backtracking: static "new" fails deeper, so [slug] is tried
		{"/blog/new/edit", "/blog/new/[id]", params{"id": "edit"}},
		{"/blog/new/comments/3", "/blog/[slug]/comments/[n]", params{"slug": "new", "n": "3"}},
		{"/blog/new/42", "/blog/new/[id]", params{"id": "42"}},
		{"/blog/new/42/x", "/blog/[...rest]", params{"rest": "new/42/x"}},
		{"/blog/hello/edit", "/blog/[slug]/edit", params{"slug": "hello"}},
		{"/blog/hello/like", "/blog/[...rest]", params{"rest": "hello/like"}},
		// rest matching zero segments
		{"/docs", "/docs/[...path]", params{"path": ""}},
		{"/docs/install", "/docs/[...path]", params{"path": "install"}},
		{"/docs/guides/routing", "/docs/[...path]", params{"path": "guides/routing"}},
		// several params
		{"/shop/books/dune", "/shop/[cat]/[item]", params{"cat": "books", "item": "dune"}},
		{"/shop/books", "/[...rest]", params{"rest": "shop/books"}},
		// percent-decoding
		{"/blog/caf%C3%A9", "/blog/[slug]", params{"slug": "café"}},
		{"/blog/a%2Fb", "/blog/[slug]", params{"slug": "a/b"}},
		{"/blog/a%2Fb/edit", "/blog/[slug]/edit", params{"slug": "a/b"}},
		{"/blog/100%", "/blog/[slug]", params{"slug": "100%"}}, // invalid escape kept raw
		{"/blog/%zz", "/blog/[slug]", params{"slug": "%zz"}},   // invalid escape kept raw
		{"/docs/a%20b/c%2Fd", "/docs/[...path]", params{"path": "a b/c/d"}},
		{"/abo%75t", "/about", nil}, // static segments compare decoded
		{"/blog/n%65w", "/blog/new", nil},
		// empty segments never match
		{"/about/", "", nil},
		{"/blog//hello", "", nil},
		{"//", "", nil},
		{"/docs/", "", nil},
		{"", "", nil},
		{"about", "", nil},
		// the 404 page has no URL of its own
		{"/404", "/[...rest]", params{"rest": "404"}},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			r, params, ok := matchParams(t, rt, tt.path)
			if tt.pattern == "" {
				if ok {
					t.Fatalf("match(%q) = %q, want no match", tt.path, r.Pattern)
				}
				return
			}
			if !ok {
				t.Fatalf("match(%q): no match, want %q", tt.path, tt.pattern)
			}
			if r.Pattern != tt.pattern {
				t.Errorf("match(%q) = %q, want %q", tt.path, r.Pattern, tt.pattern)
			}
			if (params == nil) != (tt.params == nil) || !maps.Equal(params, tt.params) {
				t.Errorf("match(%q) params = %#v, want %#v", tt.path, params, tt.params)
			}
		})
	}
}

func TestMatchWithoutCatchAll(t *testing.T) {
	rt := mustRouter(t, "/", "/blog/[slug]", notFoundPattern)
	for _, p := range []string{"/404", "/blog", "/blog/a/b", "/x"} {
		if r, _, ok := matchParams(t, rt, p); ok {
			t.Errorf("match(%q) = %q, want no match", p, r.Pattern)
		}
	}
	if rt.notFound == nil || rt.notFound.File != "src/pages/404.lyr" {
		t.Errorf("notFound = %v, want the 404.lyr route", rt.notFound)
	}
}

func TestMatchRootOnlyRest(t *testing.T) {
	rt := mustRouter(t, "/[...all]")
	r, params, ok := matchParams(t, rt, "/")
	if !ok || r.Pattern != "/[...all]" || params["all"] != "" || len(params) != 1 {
		t.Errorf(`match("/") = %v, %v, %v; want /[...all] with all=""`, r, params, ok)
	}
}

func TestNewRouterErrors(t *testing.T) {
	tests := []struct {
		name     string
		patterns []string
		want     []string
	}{
		{"mixed text and param", []string{"/blog/post-[id]"}, []string{
			`src/pages/blog/post-[id].lyr: route segment "post-[id]" mixes text and a parameter; use a whole-segment parameter such as [id].lyr and parse it in the page`,
		}},
		{"reserved /_lyr prefix", []string{"/_lyr/stats"}, []string{
			`src/pages/_lyr/stats.lyr: route /_lyr/stats is under /_lyr/, which Lyr reserves for its assets and islands; rename the page or its directory`,
		}},
		{"two params in one segment", []string{"/[lang]-[version]"}, []string{
			`src/pages/[lang]-[version].lyr: route segment "[lang]-[version]" mixes text and a parameter; use a whole-segment parameter such as [lang].lyr and parse it in the page`,
		}},
		{"adjacent params", []string{"/[a][b]"}, []string{
			`src/pages/[a][b].lyr: route segment "[a][b]" mixes text and a parameter; use a whole-segment parameter such as [a].lyr and parse it in the page`,
		}},
		{"param then text", []string{"/[id].json"}, []string{
			`src/pages/[id].json.lyr: route segment "[id].json" mixes text and a parameter; use a whole-segment parameter such as [id].lyr and parse it in the page`,
		}},
		{"mixed directory segment", []string{"/v-[n]/docs"}, []string{
			`src/pages/v-[n]/docs.lyr: route segment "v-[n]" mixes text and a parameter; use a whole-segment parameter such as [n]/ and parse it in the page`,
		}},
		{"mixed rest", []string{"/files-[...p]"}, []string{
			`src/pages/files-[...p].lyr: route segment "files-[...p]" mixes text and a parameter; use a whole-segment parameter such as [p].lyr and parse it in the page`,
		}},
		{"non-trailing rest", []string{"/docs/[...path]/edit"}, []string{
			`src/pages/docs/[...path]/edit.lyr: rest parameter [...path] must be the last segment of the route; use a whole-segment parameter such as [path] instead`,
		}},
		{"no leading slash", []string{"blog"}, []string{
			`src/pagesblog.lyr: invalid route pattern "blog": must start with /`,
		}},
		{"empty pattern", []string{""}, []string{
			`src/pages.lyr: invalid route pattern "": must start with /`,
		}},
		{"trailing slash", []string{"/blog/"}, []string{
			`src/pages/blog/.lyr: invalid route pattern "/blog/": must not end with /`,
		}},
		{"empty segment", []string{"/a//b"}, []string{
			`src/pages/a//b.lyr: invalid route pattern "/a//b": empty segment`,
		}},
		{"unclosed bracket", []string{"/[id"}, []string{
			`src/pages/[id.lyr: invalid route pattern "/[id": segment "[id" has unbalanced brackets`,
		}},
		{"stray close bracket", []string{"/id]"}, []string{
			`src/pages/id].lyr: invalid route pattern "/id]": segment "id]" has unbalanced brackets`,
		}},
		{"nested brackets", []string{"/[a[b]]"}, []string{
			`src/pages/[a[b]].lyr: invalid route pattern "/[a[b]]": segment "[a[b]]" has unbalanced brackets`,
		}},
		{"empty param name", []string{"/[]"}, []string{
			`src/pages/[].lyr: invalid route pattern "/[]": segment "[]" has an empty parameter name`,
		}},
		{"empty rest name", []string{"/[...]"}, []string{
			`src/pages/[...].lyr: invalid route pattern "/[...]": segment "[...]" has an empty parameter name`,
		}},
		{"repeated param name", []string{"/[a]/[a]"}, []string{
			`src/pages/[a]/[a].lyr: invalid route pattern "/[a]/[a]": parameter "a" is used twice`,
		}},
		{"duplicate params", []string{"/blog/[slug]", "/blog/[id]"}, []string{
			`src/pages/blog/[id].lyr: route /blog/[id] is already defined by src/pages/blog/[slug].lyr; remove or rename one of them`,
		}},
		{"duplicate rests", []string{"/[...a]", "/[...b]"}, []string{
			`src/pages/[...b].lyr: route /[...b] is already defined by src/pages/[...a].lyr; remove or rename one of them`,
		}},
		{"duplicate 404", []string{notFoundPattern, notFoundPattern}, []string{
			`src/pages/404.lyr: route 404 is already defined by src/pages/404.lyr; remove or rename one of them`,
		}},
		{"all errors reported", []string{"/a-[b]", "/[...c]/d", "/x/[y]", "/x/[z]"}, []string{
			`src/pages/a-[b].lyr: route segment "a-[b]"`,
			`src/pages/[...c]/d.lyr: rest parameter [...c]`,
			`src/pages/x/[z].lyr: route /x/[z] is already defined by src/pages/x/[y].lyr`,
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rt, err := newRouter(routesFor(tt.patterns...))
			if err == nil {
				t.Fatalf("newRouter succeeded, want an error")
			}
			if rt != nil {
				t.Errorf("newRouter returned a router with an error")
			}
			lines := strings.Split(err.Error(), "\n")
			if len(lines) != len(tt.want) {
				t.Fatalf("got %d errors, want %d:\n%v", len(lines), len(tt.want), err)
			}
			for i, w := range tt.want {
				if !strings.HasPrefix(lines[i], w) {
					t.Errorf("error %d:\n got %s\nwant %s", i, lines[i], w)
				}
			}
		})
	}
}

// The duplicate message names both files even when the files differ from
// the pattern (blog.lyr next to blog/index.lyr).
func TestDuplicateNamesBothFiles(t *testing.T) {
	_, err := newRouter([]Route{
		{Pattern: "/blog", File: "src/pages/blog.lyr"},
		{Pattern: "/blog", File: "src/pages/blog/index.lyr"},
	})
	want := "src/pages/blog/index.lyr: route /blog is already defined by src/pages/blog.lyr; remove or rename one of them"
	if err == nil || err.Error() != want {
		t.Errorf("err = %v, want %s", err, want)
	}
}

func TestNotDuplicates(t *testing.T) {
	// These overlap on some URLs but aren't the same route.
	mustRouter(t, "/docs", "/docs/[...rest]", "/[a]", "/[a]/[...b]", "/[...c]", "/x/[y]", "/[y]/x", notFoundPattern)
}

func TestRedirectTarget(t *testing.T) {
	rt := mustRouter(t, "/", "/blog", "/blog/[slug]", "/search", "/docs/[...path]", notFoundPattern)
	tests := []struct {
		path, query string
		want        string // "" means no redirect
	}{
		{"/blog/hello/", "", "/blog/hello"},
		{"/blog/", "", "/blog"},
		{"/search/", "q=go", "/search?q=go"},
		{"/search/", "q=go&page=2", "/search?q=go&page=2"},
		{"/docs/", "", "/docs"},
		{"/docs/a/b/", "", "/docs/a/b"},
		{"/blog/caf%C3%A9/", "", "/blog/caf%C3%A9"}, // stays escaped
		{"/", "", ""},
		{"/", "q=1", ""},
		{"/blog/hello", "", ""},   // no trailing slash
		{"/api/items/", "", ""},   // no page: the request reaches next
		{"/blog/a/b/", "", ""},    // [slug] is one segment
		{"/blog//", "", ""},       // double slash
		{"/blog/hello//", "", ""}, // double slash
		{"//blog/", "", ""},       // empty first segment
		{"//", "", "/"},           // the slash-less path is "/"
		{"/404/", "", ""},         // the 404 page has no URL
		{"", "", ""},
		{"/_lyr/x/", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.path+"?"+tt.query, func(t *testing.T) {
			got, ok := rt.redirectTarget(tt.path, tt.query)
			if ok != (tt.want != "") || got != tt.want {
				t.Errorf("redirectTarget(%q, %q) = %q, %v; want %q", tt.path, tt.query, got, ok, tt.want)
			}
		})
	}
}

func TestRedirectNeverToOtherHost(t *testing.T) {
	rt := mustRouter(t, "/[...rest]")
	for _, p := range []string{"//evil.example/", "///evil.example/", "/_lyr/island/x/"} {
		if got, ok := rt.redirectTarget(p, ""); ok {
			t.Errorf("redirectTarget(%q) = %q, want no redirect", p, got)
		}
	}
	if got, ok := rt.redirectTarget("/a/", ""); !ok || got != "/a" {
		t.Errorf(`redirectTarget("/a/") = %q, %v; want "/a"`, got, ok)
	}
}

// Matching writes values into the caller's buffer, so it doesn't allocate,
// with or without params. Only a value with a %-escape allocates, for its
// decoded copy; clean values are substrings of the path.
func TestMatchAllocs(t *testing.T) {
	rt := benchRouter(t)
	cases := []struct {
		path string
		max  float64
	}{
		{"/", 0},
		{"/about", 0},
		{"/blog/new", 0},
		{"/section7/page3", 0},
		{"/blog/new/comments/3", 0},
		{"/missing/a/b/c", 0},
		{"/blog/hello", 0},
		{"/docs/guides/routing", 0},
		{"/shop/books/dune", 0},
		{"/shop/b%C3%BCcher/dune", 1},
		{"/shop/b%C3%BCcher/d%C3%BCne", 2},
		{"/blog/100%", 0}, // an invalid escape is kept as it is
	}
	var buf [8]string
	for _, c := range cases {
		allocs := testing.AllocsPerRun(100, func() { rt.match(c.path, buf[:0]) })
		if allocs > c.max {
			t.Errorf("match(%q): %v allocs, want ≤ %v", c.path, allocs, c.max)
		}
	}
}

// Routes deeper than the Ctx buffer still match; their values just spill
// into a new slice.
func TestMatchManyParams(t *testing.T) {
	rt := mustRouter(t, "/[a]/[b]/[c]/[d]/[e]/[f]/[g]/[h]/[i]/[...j]")
	r, p, ok := matchParams(t, rt, "/1/2/3/4/5/6/7/8/9/10/11")
	if !ok || r.Pattern != "/[a]/[b]/[c]/[d]/[e]/[f]/[g]/[h]/[i]/[...j]" || p["a"] != "1" || p["i"] != "9" || p["j"] != "10/11" {
		t.Errorf("match = %v, %v, %v", r, p, ok)
	}
}

// benchRoutes is a 50-route table: a mix of statics, params and rests.
func benchRoutes() []string {
	ps := []string{
		"/", "/about", "/contact", "/pricing", "/blog", "/blog/new", "/blog/[slug]",
		"/blog/[slug]/edit", "/blog/new/[id]", "/blog/[slug]/comments/[n]", "/blog/tag/[tag]", "/blog/[...rest]", "/docs/[...path]",
		"/shop", "/shop/[cat]", "/shop/[cat]/[item]", "/users/[id]", "/users/[id]/settings",
		"/api-docs", "/legal/terms", "/legal/privacy", "/[...rest]",
	}
	for i := len(ps); len(ps) < 50; i++ {
		ps = append(ps, fmt.Sprintf("/section%d/page%d", i%10, i))
	}
	ps[len(ps)-1] = "/section7/page3"
	return append(ps, notFoundPattern)
}

func benchRouter(t testing.TB) *router { return mustRouter(t, benchRoutes()...) }

func BenchmarkMatch(b *testing.B) {
	rt := benchRouter(b)
	for _, path := range []string{
		"/",
		"/legal/privacy",
		"/section7/page3",
		"/blog/hello",
		"/blog/new/comments/3", // backtracks from the static "new"
		"/shop/books/dune",
		"/docs/guides/routing",
		"/blog/caf%C3%A9", // decodes one value
		"/missing/a/b/c",  // falls to /[...rest]
	} {
		b.Run(path, func(b *testing.B) {
			var buf [8]string
			b.ReportAllocs()
			for b.Loop() {
				rt.match(path, buf[:0])
			}
		})
	}
}

func BenchmarkRedirectTargetMiss(b *testing.B) {
	rt := mustRouter(b, "/", "/blog/[slug]")
	b.ReportAllocs()
	for b.Loop() {
		rt.redirectTarget("/api/items/", "")
	}
}

// refMatch is a brute-force reference: it tries every route and keeps the
// best by comparing segment kinds from the left (a route that has ended
// beats a rest that matches zero segments).
func refMatch(rt *router, routes []*compiledRoute, path string) *compiledRoute {
	if path == "" || path[0] != '/' {
		return nil
	}
	var segs []string
	if path != "/" {
		segs = strings.Split(path[1:], "/")
	}
	for _, s := range segs {
		if s == "" {
			return nil
		}
	}
	var best *compiledRoute
	for _, cr := range routes {
		if !refMatches(cr, segs) {
			continue
		}
		if best == nil || refBetter(cr, best) {
			best = cr
		}
	}
	return best
}

func refMatches(cr *compiledRoute, segs []string) bool {
	for i, s := range cr.segs {
		if s.kind == segRest {
			return true
		}
		if i >= len(segs) {
			return false
		}
		if s.kind == segStatic && pathUnescape(segs[i]) != s.text {
			return false
		}
	}
	return len(cr.segs) == len(segs)
}

func refBetter(a, b *compiledRoute) bool {
	rank := func(cr *compiledRoute, i int) int {
		if i >= len(cr.segs) {
			return -1 // ended
		}
		return int(cr.segs[i].kind)
	}
	for i := 0; ; i++ {
		ra, rb := rank(a, i), rank(b, i)
		if ra != rb {
			return ra < rb
		}
		if ra == -1 || ra == int(segRest) {
			return false
		}
	}
}

func FuzzMatch(f *testing.F) {
	for _, s := range []string{"/", "/blog/new", "/blog/a%2Fb/edit", "/docs", "/x//y", "/%zz", "/a/b/c/", "//", "/blog/new/42/x"} {
		f.Add(s, "q=1")
	}
	routes := routesFor(
		"/", "/about", "/blog", "/blog/new", "/blog/[slug]", "/blog/[...rest]", "/blog/new/[id]",
		"/blog/[slug]/edit", "/docs/[...path]", "/[a]/[b]/[c]", "/[a]/x/[...r]", "/x/[...r]", notFoundPattern,
	)
	rt, err := newRouter(routes)
	if err != nil {
		f.Fatal(err)
	}
	var all []*compiledRoute
	for i := range routes {
		if routes[i].Pattern == notFoundPattern {
			continue
		}
		segs, _ := parsePattern(routes[i].Pattern, routes[i].File)
		all = append(all, &compiledRoute{route: &routes[i], segs: segs, names: paramNames(segs)})
	}
	f.Fuzz(func(t *testing.T, path, query string) {
		dec, err := url.PathUnescape(path)
		if err != nil {
			dec = path
		}
		if got := pathUnescape(path); got != dec {
			t.Fatalf("pathUnescape(%q) = %q, url.PathUnescape gives %q", path, got, dec)
		}
		r, params, ok := matchParams(t, rt, path)
		want := refMatch(rt, all, path)
		if ok != (want != nil) {
			t.Fatalf("match(%q) ok = %v, reference = %v", path, ok, want != nil)
		}
		if !ok {
			if r != nil || params != nil {
				t.Fatalf("match(%q): no match but non-nil results", path)
			}
		} else {
			if r.Pattern != want.route.Pattern {
				t.Fatalf("match(%q) = %q, reference %q", path, r.Pattern, want.route.Pattern)
			}
			if r.Pattern == notFoundPattern {
				t.Fatalf("match(%q) returned the 404 route", path)
			}
			if len(params) != len(want.names) || (want.names == nil) != (params == nil) {
				t.Fatalf("match(%q) params = %#v, want %q", path, params, want.names)
			}
			for _, s := range want.segs {
				if _, has := params[s.text]; s.kind != segStatic && !has {
					t.Fatalf("match(%q) params %#v lack %q", path, params, s.text)
				}
			}
		}
		if target, ok := rt.redirectTarget(path, query); ok {
			p := strings.TrimSuffix(path, "/")
			want := p
			if query != "" {
				want += "?" + query
			}
			if strings.HasPrefix(target, "//") || p == path || target != want {
				t.Fatalf("redirectTarget(%q, %q) = %q, want %q", path, query, target, want)
			}
			if _, _, ok := matchParams(t, rt, p); !ok {
				t.Fatalf("redirectTarget(%q) = %q, which doesn't match", path, target)
			}
		}
	})
}
