package content

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
)

func load(t *testing.T) *Docs {
	t.Helper()
	dir, err := filepath.Abs("../../..") // docs/
	if err != nil {
		t.Fatal(err)
	}
	d, err := loadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// TestLinksResolve checks every relative link and #anchor in the docs.
func TestLinksResolve(t *testing.T) {
	d := load(t)
	for _, p := range d.Problems {
		t.Error(p)
	}
}

// TestBrokenLinks checks that the link checker reports what it should.
func TestBrokenLinks(t *testing.T) {
	d, err := Load(fstest.MapFS{
		"README.md":      {Data: []byte("# Docs\n\n## Guides\n- [A](a.md): the A page\n")},
		"a.md":           {Data: []byte("# A\n\n## Two words\n\n[ok](#two-words) [bad](#nope) [gone](b.md) [x](reference/c.md#two-words)\n")},
		"reference/c.md": {Data: []byte("# C\n\n## Two words\n\n[up](../a.md#two-words) [repo](../../go.mod)\n")},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"a.md: link #nope: a.md has no heading #nope",
		"a.md: link b.md: no such page",
	}
	if !slices.Equal(d.Problems, want) {
		t.Errorf("problems:\n%q\nwant\n%q", d.Problems, want)
	}
	c := string(d.Page("reference/c").HTML)
	if !strings.Contains(c, `href="/a#two-words"`) || !strings.Contains(c, `href="`+RepoURL+`go.mod"`) {
		t.Errorf("links in reference/c.md:\n%s", c)
	}
	if len(d.Nav) != 2 || d.Nav[0].Items[0] != (NavItem{Title: "A", URL: "/a", Desc: "the A page"}) || d.Nav[1].Title != "More" {
		t.Errorf("nav: %+v", d.Nav)
	}
}

func TestSlugify(t *testing.T) {
	for in, want := range map[string]string{
		"Catch-all routes with [...rest]":     "catch-all-routes-with-rest",
		"Route params: params":                "route-params-params",
		"Single binary: --embed":              "single-binary---embed",
		"Island key (LYR_KEY)":                "island-key-lyr_key",
		"lyr dev":                             "lyr-dev",
		"The pinned CLI: lyr and go tool lyr": "the-pinned-cli-lyr-and-go-tool-lyr",
		"Café: déjà vu":                       "café-déjà-vu",
	} {
		if got := slugify(in); got != want {
			t.Errorf("slugify(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPages(t *testing.T) {
	d := load(t)
	for _, slug := range []string{"", "getting-started", "reference/cli", "changelog"} {
		p := d.Page(slug)
		if p == nil {
			t.Fatalf("no page %q", slug)
		}
		if p.Title == "" || !strings.Contains(string(p.HTML), "<h1") {
			t.Errorf("page %q: title %q", slug, p.Title)
		}
	}
	if len(d.Nav) < 3 {
		t.Errorf("nav: %+v", d.Nav)
	}
	routing := string(d.Page("routing").HTML)
	if !strings.Contains(routing, `href="/rendering"`) {
		t.Errorf("routing.md links not rewritten")
	}
	if !strings.Contains(string(d.Page("reference/cli").HTML), `href="/configuration#site"`) {
		t.Errorf("reference/cli.md: ../configuration.md#site not rewritten")
	}
}
