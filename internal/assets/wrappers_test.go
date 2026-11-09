package assets

import (
	"regexp"
	"strings"
	"testing"

	"github.com/orbit-z/lyr/internal/loader"
)

// TestWrappers: a page with call sites (Page.Wrappers) gets the
// display:contents rule first in its stylesheet, even with no styles of
// its own; other pages don't (PLAN.md §5f).
func TestWrappers(t *testing.T) {
	dir := project(t, map[string]string{
		"src/pages/styled.lyr": "<p>a</p>\n<style>p{color:red}</style>\n",
		"src/pages/bare.lyr":   "<p>b</p>\n",
		"src/pages/script.lyr": "<p>c</p>\n<script>console.log(1)</script>\n",
		"src/pages/plain.lyr":  "<p>d</p>\n<style>p{color:blue}</style>\n",
	})
	for _, dev := range []bool{false, true} {
		in := input(t, dir, map[string][]string{
			"src/pages/styled.lyr": {"src/pages/styled.lyr"},
			"src/pages/bare.lyr":   {"src/pages/bare.lyr"},
			"src/pages/script.lyr": {"src/pages/script.lyr"},
			"src/pages/plain.lyr":  {"src/pages/plain.lyr"},
		})
		for i := range in.Pages {
			in.Pages[i].Wrappers = in.Pages[i].File != "src/pages/plain.lyr"
		}
		res := bundle(t, Options{Root: dir, Dev: dev}, in)
		noErrors(t, res)
		css := func(page string) string {
			for _, a := range res.Pages[page] {
				if a.Kind == "css" {
					return string(res.File(a.URL).Contents)
				}
			}
			return ""
		}
		comment := regexp.MustCompile(`/\*[^*]*\*/`)
		norm := func(s string) string { // dev output isn't minified
			s = strings.Join(strings.Fields(comment.ReplaceAllString(s, "")), "")
			return strings.ReplaceAll(s, ";}", "}")
		}
		for _, page := range []string{"styled", "bare", "script"} {
			got := norm(css("src/pages/" + page + ".lyr"))
			if i := strings.Index(got, loader.WrappersCSS); i < 0 || strings.Contains(got[:i], "{") {
				t.Errorf("dev=%v %s: the rule isn't first:\n%s", dev, page, got)
			}
		}
		if got := norm(css("src/pages/styled.lyr")); !strings.Contains(got, "color:red") {
			t.Errorf("dev=%v styled: %s", dev, got)
		}
		if got := css("src/pages/plain.lyr"); strings.Contains(got, "lyr-island") {
			t.Errorf("dev=%v plain has the rule:\n%s", dev, got)
		}
	}
}

// TestLoaderFiles: every successful result carries the loader files, a
// project with no assets included, so lyr build writes them and WriteDev
// keeps them.
func TestLoaderFiles(t *testing.T) {
	dir := project(t, map[string]string{"src/pages/a.lyr": "<p>a</p>\n"})
	for _, in := range []*Input{
		{Sources: map[string]*Source{}},
		input(t, dir, map[string][]string{"src/pages/a.lyr": {"src/pages/a.lyr"}}),
	} {
		res := bundle(t, Options{Root: dir}, in)
		noErrors(t, res)
		for _, f := range loader.Files {
			got := res.File("/_lyr/" + f.Path)
			if got == nil || string(got.Contents) != f.Contents {
				t.Errorf("%s missing or different in %v", f.Path, res.Files)
			}
		}
	}
}

// TestWrappersPartial: a partial with Wrappers gets the wrappers
// stylesheet as its first asset (its tail links it), also with no entry
// at all; one without gets none.
func TestWrappersPartial(t *testing.T) {
	dir := project(t, map[string]string{
		"src/pages/m.lyr": "<p>m</p>\n",
		"src/pages/n.lyr": "<p>n</p>\n",
	})
	in := input(t, dir, map[string][]string{"src/pages/m.lyr": {"src/pages/m.lyr"}, "src/pages/n.lyr": {"src/pages/n.lyr"}})
	for i := range in.Pages {
		in.Pages[i].Partial = true
		in.Pages[i].Wrappers = in.Pages[i].File == "src/pages/m.lyr"
	}
	res := bundle(t, Options{Root: dir}, in)
	noErrors(t, res)
	if got := res.Pages["src/pages/m.lyr"]; len(got) != 1 || got[0] != (Asset{Kind: "css", URL: loader.WrappersURL}) {
		t.Errorf("m = %+v", got)
	}
	if got := res.Pages["src/pages/n.lyr"]; len(got) != 0 {
		t.Errorf("n = %+v", got)
	}
}
