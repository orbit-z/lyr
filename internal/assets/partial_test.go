package assets

import (
	"slices"
	"strings"
	"testing"

	"github.com/orbit-z/lyr/internal/compiler"
)

// Page partials (PLAN.md §4k).

var partialFiles = map[string]string{
	"src/scripts/shared.ts": "export const shared = { n: 0 };\nconsole.log(\"SHARED_MODULE\");\n",
	"src/components/List.lyr": "<ul>l</ul>\n<style>\n  ul { color: red }\n</style>\n<script>\n" +
		"  import { shared } from \"../scripts/shared\";\n  shared.n++;\n  console.log(\"LIST_BLOCK\");\n</script>\n",
	"src/components/Empty.lyr":   "<p>e</p>\n<script>\n  console.log(\"EMPTY_BLOCK\");\n</script>\n",
	"src/components/Rating.lyr":  "<div>r</div>\n<script>\n  import { shared } from \"../scripts/shared\";\n  console.log(\"RATING_BLOCK\", shared);\n</script>\n",
	"src/components/NoJS.lyr":    "<div>n</div>\n<style>\n  div { color: blue }\n</style>\n",
	"src/components/Other.lyr":   "<div>o</div>\n<script>\n  console.log(\"OTHER_BLOCK\");\n</script>\n",
	"src/pages/post.lyr":         "<p>post</p>\n<script>\n  console.log(\"POST_BLOCK\");\n</script>\n",
	"src/pages/home.lyr":         "<p>home</p>\n<script>\n  console.log(\"HOME_BLOCK\");\n</script>\n",
	"src/pages/partials/c.lyr":   "---\nconst partial = true\n---\n<p>c</p>\n<style>\n  p { color: green }\n</style>\n<script>\n  console.log(\"PARTIAL_BLOCK\");\n</script>\n",
	"src/pages/partials/css.lyr": "---\nconst partial = true\n---\n<p>c</p>\n",
}

func partialInput(t *testing.T, dir string, partials bool) *Input {
	in := input(t, dir, map[string][]string{
		// The host renders List and Rating, and blank-imports Empty.
		"src/pages/post.lyr": {"src/components/List.lyr", "src/components/Empty.lyr", "src/components/Rating.lyr", "src/pages/post.lyr"},
		// Includes none of the partial's components except Other.
		"src/pages/home.lyr": {"src/components/Other.lyr", "src/pages/home.lyr"},
		// The partial renders List, Empty, NoJS, Rating (lazy) and Other.
		"src/pages/partials/c.lyr":   {"src/components/List.lyr", "src/components/Empty.lyr", "src/components/NoJS.lyr", "src/components/Rating.lyr", "src/components/Other.lyr", "src/pages/partials/c.lyr"},
		"src/pages/partials/css.lyr": {"src/components/NoJS.lyr", "src/pages/partials/css.lyr"},
	})
	for i := range in.Pages {
		pg := &in.Pages[i]
		if strings.HasPrefix(pg.File, "src/pages/partials/") {
			pg.Partial = partials
		}
		if pg.File == "src/pages/partials/c.lyr" {
			pg.Lazy = []Lazy{{ID: compiler.ComponentID("src/components/Rating.lyr"), Modules: []string{"src/components/Rating.lyr"}}}
		}
	}
	slices.SortFunc(in.Pages, func(a, b Page) int { return strings.Compare(a.File, b.File) })
	return in
}

func TestPartialAssets(t *testing.T) {
	dir := project(t, partialFiles)
	res := bundle(t, Options{Root: dir}, partialInput(t, dir, true))
	noErrors(t, res)

	id := compiler.ComponentID
	// The partial: no stylesheet, no module, no preload; a component asset
	// per file with scripts in graph order (the lazy Rating included, for
	// its ID), and Rating's lazy asset.
	var kinds, ids []string
	for _, a := range res.Pages["src/pages/partials/c.lyr"] {
		kinds = append(kinds, a.Kind)
		ids = append(ids, a.ID)
	}
	wantKinds := []string{"component", "component", "component", "component", "component", "lazy"}
	wantIDs := []string{id("src/components/List.lyr"), id("src/components/Empty.lyr"), id("src/components/Rating.lyr"),
		id("src/components/Other.lyr"), id("src/pages/partials/c.lyr"), id("src/components/Rating.lyr")}
	if !slices.Equal(kinds, wantKinds) || !slices.Equal(ids, wantIDs) {
		t.Fatalf("partial assets:\n%+v\nwant kinds %q IDs %q", res.Pages["src/pages/partials/c.lyr"], wantKinds, wantIDs)
	}
	// A partial without scripts has no assets at all.
	if as := res.Pages["src/pages/partials/css.lyr"]; len(as) != 0 {
		t.Errorf("css-only partial: %+v", as)
	}
	urls := map[string]string{} // by ID
	for _, a := range res.Pages["src/pages/partials/c.lyr"] {
		if a.Kind == "component" {
			urls[a.ID] = a.URL
		}
	}
	// Component entries are the lazy entries: one URL, holding only that
	// component's blocks; the partial's own <style> is in no output.
	for file, block := range map[string]string{
		"src/components/List.lyr": "LIST_BLOCK", "src/components/Empty.lyr": "EMPTY_BLOCK",
		"src/components/Rating.lyr": "RATING_BLOCK", "src/components/Other.lyr": "OTHER_BLOCK",
		"src/pages/partials/c.lyr": "PARTIAL_BLOCK",
	} {
		js := contents(t, res, urls[id(file)])
		if !strings.Contains(js, block) {
			t.Errorf("%s's entry lacks its block:\n%s", file, js)
		}
	}
	if lazy := asset(t, res, "src/pages/partials/c.lyr", "lazy"); lazy.URL != urls[id("src/components/Rating.lyr")] {
		t.Errorf("lazy entry %s, component entry %s: want one URL", lazy.URL, urls[id("src/components/Rating.lyr")])
	}
	for _, f := range res.Files {
		if strings.Contains(string(f.Contents), "green") {
			t.Errorf("the partial's own style is in %s", f.Name)
		}
		if strings.HasSuffix(f.Name, ".css") && strings.Contains(f.Name, "partials") {
			t.Errorf("partial stylesheet %s", f.Name)
		}
	}

	// The host bundles mark exactly the partial-used components they
	// inline, first: post inlines List, Empty and Rating (all used by the
	// partial), home inlines Other.
	marks := func(page string) []string {
		js := contents(t, res, asset(t, res, page, "js").URL)
		i := strings.Index(js, "lyr__ran")
		if i < 0 {
			return nil
		}
		for _, block := range []string{"POST_BLOCK", "HOME_BLOCK", "LIST_BLOCK"} {
			if j := strings.Index(js, block); j >= 0 && j < i {
				t.Errorf("%s: %s runs before the marks", page, block)
			}
		}
		var out []string
		for _, f := range []string{"src/components/List.lyr", "src/components/Empty.lyr", "src/components/Rating.lyr", "src/components/Other.lyr", "src/pages/post.lyr"} {
			if strings.Contains(js, `"`+id(f)+`"`) {
				out = append(out, f)
			}
		}
		return out
	}
	if got, want := marks("src/pages/post.lyr"), []string{"src/components/List.lyr", "src/components/Empty.lyr", "src/components/Rating.lyr"}; !slices.Equal(got, want) {
		t.Errorf("post marks %q, want %q", got, want)
	}
	if got, want := marks("src/pages/home.lyr"), []string{"src/components/Other.lyr"}; !slices.Equal(got, want) {
		t.Errorf("home marks %q, want %q", got, want)
	}
	// The host keeps its CSS (List's and NoJS-less), as before.
	if css := contents(t, res, asset(t, res, "src/pages/post.lyr", "css").URL); !strings.Contains(css, "red") {
		t.Errorf("post stylesheet lacks List's style:\n%s", css)
	}
	// shared.ts, imported by List's and Rating's blocks in the page bundle
	// and the component entries, is one shared chunk: one module state.
	if got := filesWith(res, "SHARED_MODULE"); len(got) != 1 {
		t.Errorf("shared.ts is in %q, want one chunk", got)
	}
}

// TestPartialNoneUnchanged: a project without partial pages gets no marks
// and the same files as the same pages built with Partial unset.
func TestPartialNoneUnchanged(t *testing.T) {
	files := map[string]string{}
	for k, v := range partialFiles {
		if !strings.HasPrefix(k, "src/pages/partials/") {
			files[k] = v
		}
	}
	dir := project(t, files)
	in := input(t, dir, map[string][]string{
		"src/pages/post.lyr": {"src/components/List.lyr", "src/components/Empty.lyr", "src/components/Rating.lyr", "src/pages/post.lyr"},
		"src/pages/home.lyr": {"src/components/Other.lyr", "src/pages/home.lyr"},
	})
	res := bundle(t, Options{Root: dir}, in)
	noErrors(t, res)
	if got := filesWith(res, "lyr__ran"); len(got) > 0 {
		t.Errorf("marks without partials in %q", got)
	}
	p := newPlan(in)
	for path := range p.pageSrc {
		if strings.HasSuffix(path, markSuffix) {
			t.Errorf("mark module %s without partials", path)
		}
	}
	// With partials marked but not used by these pages: the same output.
	dir2 := project(t, partialFiles)
	in2 := partialInput(t, dir2, true)
	in2.Pages = slices.DeleteFunc(in2.Pages, func(pg Page) bool { return pg.File != "src/pages/home.lyr" })
	res2 := bundle(t, Options{Root: dir2}, in2)
	noErrors(t, res2)
	if got := filesWith(res2, "lyr__ran"); len(got) > 0 {
		t.Errorf("marks without partial pages in %q", got)
	}
}
