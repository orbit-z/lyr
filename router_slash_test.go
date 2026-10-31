package lyr

import (
	"maps"
	"strings"
	"testing"
)

// FuzzMatchAlways checks always mode against never mode: a path matches in
// always mode iff it's the canonical form of a never-mode match, and a
// redirect always lands on a matching path.
func FuzzMatchAlways(f *testing.F) {
	for _, s := range []string{"/", "/blog/new/", "/blog/v1.2", "/docs/", "/x//y/", "//", "//evil", "/rss.xml", "/rss.xml/", "/_lyr/x"} {
		f.Add(s, "q=1")
	}
	patterns := []string{"/", "/blog", "/blog/new", "/blog/[slug]", "/docs/[...path]", "/rss.xml", "/[a]/[b]", notFoundPattern}
	never := mustRouter(f, patterns...)
	always := mustAlwaysRouter(f, patterns...)
	f.Fuzz(func(t *testing.T, path, query string) {
		cr, _ := always.find(path)
		var want *compiledRoute
		switch {
		case path == "/":
			want, _ = never.find(path)
		case strings.HasSuffix(path, "/") && path != "//":
			if c, _ := never.find(path[:len(path)-1]); c != nil && !c.ext {
				want = c
			}
		default:
			if c, _ := never.find(path); c != nil && c.ext {
				want = c
			}
		}
		if (cr == nil) != (want == nil) || cr != nil && cr.route.Pattern != want.route.Pattern {
			t.Fatalf("always.find(%q) = %v, want %v", path, cr, want)
		}
		if target, ok := always.redirectTarget(path, query); ok {
			if cr != nil {
				t.Fatalf("redirectTarget(%q) = %q for a matching path", path, target)
			}
			p, want := path+"/", path+"/"
			if query != "" {
				want += "?" + query
			}
			if strings.HasPrefix(target, "//") || target != want {
				t.Fatalf("redirectTarget(%q, %q) = %q", path, query, target)
			}
			if c, _ := always.find(p); c == nil {
				t.Fatalf("redirectTarget(%q) = %q, which doesn't match", path, target)
			}
		}
	})
}

func mustAlwaysRouter(t testing.TB, patterns ...string) *router {
	t.Helper()
	rt := mustRouter(t, patterns...)
	rt.always = true
	return rt
}

func TestHasFileExtension(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"/rss.xml", true},
		{"/a/b.min.js", true},
		{"/v1.2", true},
		{"/blog/v1.2", true},
		{"/a_b.Z_9", true},
		{"/x.", false},
		{"/.env", false},
		{"/a/.env", false},
		{"/a.b/", false},
		{"/a.%41", false},
		{"/a.b-c", false},
		{"/a.b/c", false},
		{"/", false},
		{"", false},
		{"/blog", false},
	}
	for _, tt := range tests {
		if got := hasFileExtension(tt.path); got != tt.want {
			t.Errorf("hasFileExtension(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}

func TestExtRoute(t *testing.T) {
	rt := mustRouter(t, "/rss.xml", "/a/b.min.js", "/blog", "/v1.2/[slug]", "/feed/[x]")
	for _, tt := range []struct {
		path string
		ext  bool
	}{{"/rss.xml", true}, {"/a/b.min.js", true}, {"/blog", false}, {"/v1.2/x", false}, {"/feed/a.xml", false}} {
		cr, _ := rt.find(tt.path)
		if cr == nil || cr.ext != tt.ext {
			t.Errorf("find(%q) = %v, want ext %v", tt.path, cr, tt.ext)
		}
	}
}

// TestMatchModes matches the same paths with both trailing-slash modes.
func TestMatchModes(t *testing.T) {
	patterns := []string{"/", "/blog", "/blog/[slug]", "/docs/[...path]", "/rss.xml", "/search", notFoundPattern}
	never := mustRouter(t, patterns...)
	always := mustAlwaysRouter(t, patterns...)
	type want struct {
		pattern string // "" means no match
		params  params
	}
	tests := []struct {
		path          string
		never, always want
	}{
		{"/", want{"/", nil}, want{"/", nil}},
		{"/blog", want{"/blog", nil}, want{}},
		{"/blog/", want{}, want{"/blog", nil}},
		{"/blog/hello", want{"/blog/[slug]", params{"slug": "hello"}}, want{}},
		{"/blog/hello/", want{}, want{"/blog/[slug]", params{"slug": "hello"}}},
		{"/blog/caf%C3%A9/", want{}, want{"/blog/[slug]", params{"slug": "café"}}},
		{"/docs", want{"/docs/[...path]", params{"path": ""}}, want{}},
		{"/docs/", want{}, want{"/docs/[...path]", params{"path": ""}}},
		{"/docs/a/b", want{"/docs/[...path]", params{"path": "a/b"}}, want{}},
		{"/docs/a/b/", want{}, want{"/docs/[...path]", params{"path": "a/b"}}},
		{"/rss.xml", want{"/rss.xml", nil}, want{"/rss.xml", nil}},
		{"/rss.xml/", want{}, want{}},
		{"/blog/v1.2", want{"/blog/[slug]", params{"slug": "v1.2"}}, want{}},
		{"/blog/v1.2/", want{}, want{"/blog/[slug]", params{"slug": "v1.2"}}},
		{"//", want{}, want{}},
		{"/blog//", want{}, want{}},
		{"/docs/a//", want{}, want{}},
		{"/404/", want{}, want{}},
		{"", want{}, want{}},
	}
	for _, tt := range tests {
		for _, m := range []struct {
			name string
			rt   *router
			want want
		}{{"never", never, tt.never}, {"always", always, tt.always}} {
			r, p, ok := matchParams(t, m.rt, tt.path)
			got := ""
			if ok {
				got = r.Pattern
			}
			if got != m.want.pattern || !maps.Equal(p, m.want.params) {
				t.Errorf("%s: match(%q) = %q %v, want %q %v", m.name, tt.path, got, p, m.want.pattern, m.want.params)
			}
		}
	}
}

func TestRedirectTargetAlways(t *testing.T) {
	rt := mustAlwaysRouter(t, "/", "/blog", "/blog/[slug]", "/search", "/docs/[...path]", "/rss.xml", "/feed/[name]", notFoundPattern)
	tests := []struct {
		path, query string
		want        string // "" means no redirect
	}{
		{"/blog/hello", "", "/blog/hello/"},
		{"/blog", "", "/blog/"},
		{"/search", "q=go", "/search/?q=go"},
		{"/docs", "", "/docs/"},
		{"/docs/a/b", "", "/docs/a/b/"},
		{"/blog/caf%C3%A9", "", "/blog/caf%C3%A9/"},
		{"/", "", ""},
		{"/", "q=1", ""},
		{"/blog/hello/", "", ""},   // already canonical
		{"/api/items", "", ""},     // no page: the request reaches next
		{"/blog/a/b", "", ""},      // [slug] is one segment
		{"/blog/v1.2", "", ""},     // a file extension
		{"/feed/atom.xml", "", ""}, // a file extension
		{"/rss.xml", "", ""},       // an extension route matches as is
		{"/rss.xml/", "", ""},      // and its slash form isn't redirected
		{"/x//", "", ""},           // no collapsing
		{"/blog//x", "", ""},
		{"//evil.example", "", ""}, // never a protocol-relative Location
		{"//", "", ""},
		{"/404", "", ""},
		{"", "", ""},
		{"/_lyr/x", "", ""},
		{"/_lyr/island/0000000000000000/x", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.path+"?"+tt.query, func(t *testing.T) {
			got, ok := rt.redirectTarget(tt.path, tt.query)
			if ok != (tt.want != "") || got != tt.want {
				t.Errorf("redirectTarget(%q, %q) = %q, %v; want %q", tt.path, tt.query, got, ok, tt.want)
			}
		})
	}
	// A root rest page claims every slash-less path without an extension.
	rest := mustAlwaysRouter(t, "/[...rest]")
	for _, p := range []string{"//evil.example", "///evil.example", "/_lyr/island/x"} {
		if got, ok := rest.redirectTarget(p, ""); ok {
			t.Errorf("redirectTarget(%q) = %q, want no redirect", p, got)
		}
	}
	if got, ok := rest.redirectTarget("/api/items", ""); !ok || got != "/api/items/" {
		t.Errorf(`redirectTarget("/api/items") = %q, %v; want "/api/items/"`, got, ok)
	}
}

// Matching in always mode cuts the slash as a substring: no allocation.
func TestMatchAllocsAlways(t *testing.T) {
	rt := benchRouter(t)
	rt.always = true
	cases := []struct {
		path string
		max  float64
	}{
		{"/", 0},
		{"/about/", 0},
		{"/about", 0},
		{"/blog/hello/", 0},
		{"/blog/new/comments/3/", 0},
		{"/docs/guides/routing/", 0},
		{"/shop/b%C3%BCcher/dune/", 1},
	}
	var buf [8]string
	for _, c := range cases {
		allocs := testing.AllocsPerRun(100, func() { rt.match(c.path, buf[:0]) })
		if allocs > c.max {
			t.Errorf("match(%q): %v allocs, want ≤ %v", c.path, allocs, c.max)
		}
	}
	if allocs := testing.AllocsPerRun(100, func() { rt.redirectTarget("/api/items/", "") }); allocs != 0 {
		t.Errorf("redirectTarget miss: %v allocs, want 0", allocs)
	}
}
