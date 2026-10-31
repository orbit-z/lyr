package assets

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/orbit-z/lyr/internal/compiler"
)

// Code splitting (PLAN.md §5e).

// splitFiles is §5e's example: QuickSearch imports analytics.ts and loads
// heavy.ts with import(); the lazy Comments and Rating share lazyonly.ts,
// and Comments also imports analytics.ts.
var splitFiles = map[string]string{
	"src/scripts/analytics.ts": "export function track(e: string): void {\n  console.log(\"TRACK_CODE\", e);\n}\n",
	"src/scripts/lazyonly.ts":  "export function lazyOnly(): void {\n  console.log(\"LAZY_ONLY_CODE\");\n}\n",
	"src/lib/heavy.ts":         "import \"./heavy.css\";\nexport function heavy(): string {\n  return \"HEAVY_CODE\";\n}\n",
	"src/lib/heavy.css":        ".heavy-style { color: red }\n",
	"src/components/QuickSearch.lyr": "<quick-search></quick-search>\n<style>\n  quick-search { display: block }\n</style>\n<script>\n" +
		"  import { track } from \"../scripts/analytics\";\n  track(\"qs\");\n" +
		"  document.addEventListener(\"click\", async () => {\n    const { heavy } = await import(\"../lib/heavy\");\n    console.log(heavy());\n  });\n</script>\n",
	"src/components/Comments.lyr": "<div>c</div>\n<script>\n  import { track } from \"../scripts/analytics\";\n  import { lazyOnly } from \"../scripts/lazyonly\";\n  track(\"COMMENTS_BLOCK\");\n  lazyOnly();\n</script>\n",
	"src/components/Rating.lyr":   "<div>r</div>\n<script>\n  import { lazyOnly } from \"../scripts/lazyonly\";\n  console.log(\"RATING_BLOCK\");\n  lazyOnly();\n</script>\n",
	"src/pages/index.lyr":         "<p>home</p>\n<script>\n  console.log(\"INDEX_BLOCK\");\n</script>\n",
	"src/pages/about.lyr":         "<p>about</p>\n<style>\n  p { color: blue }\n</style>\n<script>\n  console.log(\"ABOUT_BLOCK\");\n</script>\n",
}

func splitInput(t *testing.T, dir string) *Input {
	in := input(t, dir, map[string][]string{
		"src/pages/index.lyr": {"src/components/QuickSearch.lyr", "src/components/Comments.lyr", "src/components/Rating.lyr", "src/pages/index.lyr"},
		"src/pages/about.lyr": {"src/components/QuickSearch.lyr", "src/pages/about.lyr"},
	})
	for i := range in.Pages {
		if in.Pages[i].File == "src/pages/index.lyr" {
			in.Pages[i].Lazy = []Lazy{
				{ID: compiler.ComponentID("src/components/Comments.lyr"), Modules: []string{"src/components/Comments.lyr"}},
				{ID: compiler.ComponentID("src/components/Rating.lyr"), Modules: []string{"src/components/Rating.lyr"}},
			}
		}
	}
	slices.SortFunc(in.Pages, func(a, b Page) int { return strings.Compare(a.File, b.File) })
	return in
}

// asset returns the page's only asset of kind.
func asset(t *testing.T, res *Result, page, kind string) Asset {
	t.Helper()
	var found []Asset
	for _, a := range res.Pages[page] {
		if a.Kind == kind {
			found = append(found, a)
		}
	}
	if len(found) != 1 {
		t.Fatalf("%s: %d %s assets: %+v", page, len(found), kind, res.Pages[page])
	}
	return found[0]
}

func contents(t *testing.T, res *Result, url string) string {
	t.Helper()
	f := res.File(url)
	if f == nil {
		t.Fatalf("no file %s", url)
	}
	return string(f.Contents)
}

// filesWith returns the names of the JS files that contain s.
func filesWith(res *Result, s string) []string {
	var names []string
	for _, f := range res.Files {
		if strings.HasSuffix(f.Name, ".js") && strings.Contains(string(f.Contents), s) {
			names = append(names, f.Name)
		}
	}
	return names
}

func TestSplit(t *testing.T) {
	dir := project(t, splitFiles)
	res := bundle(t, Options{Root: dir}, splitInput(t, dir))
	noErrors(t, res)
	const index, about = "src/pages/index.lyr", "src/pages/about.lyr"
	indexJS, aboutJS := asset(t, res, index, "js").URL, asset(t, res, about, "js").URL

	t.Run("dynamic import", func(t *testing.T) {
		heavy := filesWith(res, "HEAVY_CODE")
		if len(heavy) != 1 || !strings.HasPrefix(heavy[0], "heavy-") {
			t.Fatalf("HEAVY_CODE in %v, want one heavy-*.js", heavy)
		}
		for _, js := range []string{indexJS, aboutJS} {
			if c := contents(t, res, js); !strings.Contains(c, `import("/_lyr/heavy-`) {
				t.Errorf("%s doesn't import() the chunk:\n%s", js, c)
			}
		}
		// Its CSS is in the stylesheet of every page that can reach it,
		// and no heavy-*.css is written.
		for _, page := range []string{index, about} {
			if css := contents(t, res, asset(t, res, page, "css").URL); !strings.Contains(css, ".heavy-style") {
				t.Errorf("%s stylesheet has no .heavy-style:\n%s", page, css)
			}
		}
		for _, f := range res.Files {
			if strings.HasPrefix(f.Name, "heavy-") && strings.HasSuffix(f.Name, ".css") {
				t.Errorf("unlinked chunk stylesheet %s written", f.Name)
			}
		}
	})

	t.Run("shared module once", func(t *testing.T) {
		track := filesWith(res, "TRACK_CODE")
		if len(track) != 1 || !strings.HasPrefix(track[0], "chunk-") {
			t.Fatalf("TRACK_CODE in %v, want one chunk", track)
		}
		comments := ""
		for _, a := range res.Pages[index] {
			if a.Kind == "lazy" && a.ID == compiler.ComponentID("src/components/Comments.lyr") {
				comments = a.URL
			}
		}
		if comments == "" {
			t.Fatalf("no lazy Comments asset: %+v", res.Pages[index])
		}
		for _, js := range []string{indexJS, aboutJS, comments} {
			if c := contents(t, res, js); !strings.Contains(c, `"/_lyr/`+track[0]+`"`) {
				t.Errorf("%s doesn't import %s:\n%s", js, track[0], c)
			}
		}
		if lazy := filesWith(res, "LAZY_ONLY_CODE"); len(lazy) != 1 || !strings.HasPrefix(lazy[0], "chunk-") {
			t.Errorf("LAZY_ONLY_CODE in %v, want one chunk", lazy)
		}
	})

	t.Run("modulepreload", func(t *testing.T) {
		// Only the entry's static chunks: analytics, not the lazy entries'
		// lazyonly chunk nor the dynamic heavy chunk.
		track := filesWith(res, "TRACK_CODE")
		for _, page := range []string{index, about} {
			pre := asset(t, res, page, "modulepreload")
			if pre.URL != URLPrefix+track[0] {
				t.Errorf("%s preloads %s, want %s", page, pre.URL, track[0])
			}
		}
		// Asset order: css, js, lazy, modulepreload.
		var kinds []string
		for _, a := range res.Pages[index] {
			kinds = append(kinds, a.Kind)
		}
		if want := []string{"css", "js", "lazy", "lazy", "modulepreload"}; !slices.Equal(kinds, want) {
			t.Errorf("index asset kinds = %v, want %v", kinds, want)
		}
	})

	t.Run("blocks stay in their entries", func(t *testing.T) {
		for block, want := range map[string]string{"INDEX_BLOCK": indexJS, "ABOUT_BLOCK": aboutJS} {
			if got := filesWith(res, block); len(got) != 1 || URLPrefix+got[0] != want {
				t.Errorf("%s in %v, want %s", block, got, want)
			}
		}
		// QuickSearch's block is on both pages: a copy in each entry.
		if got := filesWith(res, `track("qs")`); len(got) != 0 {
			t.Errorf("minified block found unminified in %v", got)
		}
		if got := filesWith(res, `"qs"`); len(got) != 2 {
			t.Errorf(`QuickSearch's block in %v, want both entries`, got)
		}
	})
}

// TestSplitOrder: a component used on another page stays in each page's
// entry, in graph order (§5e rule 2 and 6).
func TestSplitOrder(t *testing.T) {
	dir := project(t, map[string]string{
		"src/components/First.lyr":  "<p>1</p>\n<script>\n  console.log(\"FIRST_BLOCK\");\n</script>\n",
		"src/components/Second.lyr": "<p>2</p>\n<script>\n  console.log(\"SECOND_BLOCK\");\n</script>\n",
		"src/pages/p1.lyr":          "<p>p1</p>\n",
		"src/pages/p2.lyr":          "<p>p2</p>\n",
	})
	res := bundle(t, Options{Root: dir}, input(t, dir, map[string][]string{
		"src/pages/p1.lyr": {"src/components/First.lyr", "src/components/Second.lyr", "src/pages/p1.lyr"},
		"src/pages/p2.lyr": {"src/components/Second.lyr", "src/pages/p2.lyr"},
	}))
	noErrors(t, res)
	p1 := contents(t, res, asset(t, res, "src/pages/p1.lyr", "js").URL)
	if i, j := strings.Index(p1, "FIRST_BLOCK"), strings.Index(p1, "SECOND_BLOCK"); i < 0 || j < 0 || i > j {
		t.Errorf("p1 entry:\n%s", p1)
	}
	for _, f := range res.Files {
		if strings.HasPrefix(f.Name, "chunk-") {
			t.Errorf("chunk %s:\n%s", f.Name, f.Contents)
		}
	}
	if pre := res.Pages["src/pages/p1.lyr"]; len(pre) != 1 {
		t.Errorf("p1 assets: %+v", pre)
	}
}

var emptyChunkFiles = map[string]string{
	"src/styles/lib.css":   ".lib { color: red }\n",
	"src/scripts/types.ts": "export type T = { a: number };\n",
	"src/pages/e1.lyr":     "<p>e1</p>\n<script>\n  import \"../styles/lib.css\";\n  console.log(\"E1_BLOCK\");\n</script>\n",
	"src/pages/e2.lyr":     "<p>e2</p>\n<script>\n  import \"../styles/lib.css\";\n  console.log(\"E2_BLOCK\");\n</script>\n",
	"src/pages/t1.lyr":     "<p>t1</p>\n<script>\n  import \"../scripts/types\";\n</script>\n",
	"src/pages/t2.lyr":     "<p>t2</p>\n<script>\n  import \"../scripts/types\";\n</script>\n",
}

func emptyChunkInput(t *testing.T, dir string) *Input {
	pages := map[string][]string{}
	for _, p := range []string{"e1", "e2", "t1", "t2"} {
		pages["src/pages/"+p+".lyr"] = []string{"src/pages/" + p + ".lyr"}
	}
	return input(t, dir, pages)
}

var importRe = regexp.MustCompile(`"/_lyr/([^"]+\.js)"`)

// TestSplitEmptyChunks: CSS or types-only modules imported from several
// pages leave no empty chunk and no import of one (§5e rule 8).
func TestSplitEmptyChunks(t *testing.T) {
	for _, tt := range []struct {
		name string
		opts func(dir string) Options
	}{
		{"prod", func(dir string) Options { return Options{Root: dir} }},
		{"prod sourcemap", func(dir string) Options { return Options{Root: dir, Sourcemap: true} }},
		{"dev", func(dir string) Options { return Options{Root: dir, Dev: true} }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := project(t, emptyChunkFiles)
			opts := tt.opts(dir)
			res := bundle(t, opts, emptyChunkInput(t, dir))
			noErrors(t, res)
			for _, f := range res.Files {
				if !strings.HasSuffix(f.Name, ".js") {
					continue
				}
				if isEmptyJS(f.Contents) {
					t.Errorf("empty JS file %s", f.Name)
				}
				for _, m := range importRe.FindAllStringSubmatch(string(f.Contents), -1) {
					if res.File(URLPrefix+m[1]) == nil {
						t.Errorf("%s imports missing %s:\n%s", f.Name, m[1], f.Contents)
					}
				}
			}
			for _, p := range []string{"t1", "t2"} {
				if as, ok := res.Pages["src/pages/"+p+".lyr"]; ok {
					t.Errorf("%s (types only) has assets %+v", p, as)
				}
			}
			for _, p := range []string{"e1", "e2"} {
				page := "src/pages/" + p + ".lyr"
				js := asset(t, res, page, "js").URL
				if css := contents(t, res, asset(t, res, page, "css").URL); !strings.Contains(css, ".lib") {
					t.Errorf("%s stylesheet:\n%s", p, css)
				}
				for _, a := range res.Pages[page] {
					if a.Kind == "modulepreload" {
						t.Errorf("%s preloads %s", p, a.URL)
					}
				}
				if opts.Dev || opts.Sourcemap {
					// The blanked import keeps the map valid: the block
					// still maps to its .lyr line:col.
					src, line, col := lookup(t, contents(t, res, js), readMap(t, res, js), "console.log(")
					if src != "/"+page || line != 4 || col != 3 {
						t.Errorf("%s console.log maps to %s:%d:%d, want /%s:4:3", p, src, line, col, page)
					}
				}
			}
		})
	}
}

// TestSplitCSSUnchanged: every page stylesheet is byte-identical with and
// without code splitting (§5e rule 7).
func TestSplitCSSUnchanged(t *testing.T) {
	for name, fx := range map[string]struct {
		files map[string]string
		in    func(*testing.T, string) *Input
	}{
		"basic": {basicFiles, basicInput},
		"split": {splitFiles, splitInput},
		"empty": {emptyChunkFiles, emptyChunkInput},
	} {
		t.Run(name, func(t *testing.T) {
			dir := project(t, fx.files)
			split := bundle(t, Options{Root: dir}, fx.in(t, dir))
			b := New(Options{Root: dir}).(*esbuildBundler)
			b.noSplit = true
			defer b.Close()
			whole, err := b.Bundle(fx.in(t, dir))
			if err != nil {
				t.Fatal(err)
			}
			noErrors(t, split)
			noErrors(t, whole)
			for page := range whole.Pages {
				var a, b []string
				for _, x := range split.Pages[page] {
					if x.Kind == "css" {
						a = append(a, contents(t, split, x.URL))
					}
				}
				for _, x := range whole.Pages[page] {
					if x.Kind == "css" {
						b = append(b, contents(t, whole, x.URL))
					}
				}
				if !slices.Equal(a, b) {
					t.Errorf("%s stylesheets differ:\nsplit %q\nwhole %q", page, a, b)
				}
			}
		})
	}
}

// TestSplitDeterministic: fresh bundlers give identical files, and adding a
// page renames no other page's files in a production build.
func TestSplitDeterministic(t *testing.T) {
	dir := project(t, splitFiles)
	a := bundle(t, Options{Root: dir, Sourcemap: true}, splitInput(t, dir))
	b := bundle(t, Options{Root: dir, Sourcemap: true}, splitInput(t, dir))
	noErrors(t, a)
	if !reflect.DeepEqual(a.Files, b.Files) || !reflect.DeepEqual(a.Pages, b.Pages) {
		t.Errorf("two builds differ")
	}

	more := project(t, splitFiles)
	if err := os.WriteFile(filepath.Join(more, "src/pages/zz.lyr"), []byte("<p>zz</p>\n<script>\n  console.log(\"ZZ\");\n</script>\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	in := splitInput(t, more)
	in.Pages = append(in.Pages, Page{File: "src/pages/zz.lyr", Modules: []string{"src/pages/zz.lyr"}})
	c := bundle(t, Options{Root: more}, in)
	noErrors(t, c)
	base := bundle(t, Options{Root: more}, splitInput(t, more))
	for _, page := range []string{"src/pages/index.lyr", "src/pages/about.lyr"} {
		if !reflect.DeepEqual(base.Pages[page], c.Pages[page]) {
			t.Errorf("%s assets changed after adding a page:\n%+v\n%+v", page, base.Pages[page], c.Pages[page])
		}
	}
}

// TestSplitDiagnosticsOnce: an error in a block copied into three pages is
// reported once, at its .lyr position.
func TestSplitDiagnosticsOnce(t *testing.T) {
	dir := project(t, map[string]string{
		"src/components/Bad.lyr": "<p>x</p>\n<script>\n  const x = ;\n</script>\n",
		"src/pages/a.lyr":        "<p>a</p>\n",
		"src/pages/b.lyr":        "<p>b</p>\n",
		"src/pages/c.lyr":        "<p>c</p>\n",
	})
	pages := map[string][]string{}
	for _, p := range []string{"a", "b", "c"} {
		pages["src/pages/"+p+".lyr"] = []string{"src/components/Bad.lyr", "src/pages/" + p + ".lyr"}
	}
	res := bundle(t, Options{Root: dir}, input(t, dir, pages))
	var got []string
	for _, d := range res.Diagnostics {
		got = append(got, fmt.Sprintf("%s:%d:%d: %s", d.File, d.Line, d.Col, d.Message))
	}
	if want := []string{`src/components/Bad.lyr:3:13: Unexpected ";"`}; !slices.Equal(got, want) {
		t.Errorf("diagnostics = %q, want %q", got, want)
	}
}

// TestSplitModulePreloadClosure: a two-level chunk graph (a.ts imports
// b.ts; a.ts is on p1 and p2, b.ts also on p3) preloads both chunks from
// p1, each once, in the entry's import order.
func TestSplitModulePreloadClosure(t *testing.T) {
	dir := project(t, map[string]string{
		"src/scripts/a.ts": "import { b } from \"./b\";\nexport const a = (): string => \"A_CODE\" + b();\n",
		"src/scripts/b.ts": "export const b = (): string => \"B_CODE\";\n",
		"src/pages/p1.lyr": "<p>1</p>\n<script>\n  import { a } from \"../scripts/a\";\n  console.log(a());\n</script>\n",
		"src/pages/p2.lyr": "<p>2</p>\n<script>\n  import { a } from \"../scripts/a\";\n  console.log(a(), 2);\n</script>\n",
		"src/pages/p3.lyr": "<p>3</p>\n<script>\n  import { b } from \"../scripts/b\";\n  console.log(b(), 3);\n</script>\n",
	})
	res := bundle(t, Options{Root: dir}, input(t, dir, map[string][]string{
		"src/pages/p1.lyr": {"src/pages/p1.lyr"},
		"src/pages/p2.lyr": {"src/pages/p2.lyr"},
		"src/pages/p3.lyr": {"src/pages/p3.lyr"},
	}))
	noErrors(t, res)
	aChunk, bChunk := filesWith(res, "A_CODE"), filesWith(res, "B_CODE")
	if len(aChunk) != 1 || len(bChunk) != 1 || aChunk[0] == bChunk[0] {
		t.Fatalf("A_CODE in %v, B_CODE in %v", aChunk, bChunk)
	}
	// The closure in breadth-first import order, from the entry's own
	// imports.
	entry := asset(t, res, "src/pages/p1.lyr", "js").URL
	var want []string
	seen := map[string]bool{}
	queue := []string{entry}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, m := range importRe.FindAllStringSubmatch(contents(t, res, cur), -1) {
			if u := URLPrefix + m[1]; !seen[u] {
				seen[u] = true
				want = append(want, u)
				queue = append(queue, u)
			}
		}
	}
	var got []string
	for _, a := range res.Pages["src/pages/p1.lyr"] {
		if a.Kind == "modulepreload" {
			got = append(got, a.URL)
		}
	}
	if !slices.Equal(got, want) || len(got) != 2 {
		t.Errorf("p1 preloads %v, want %v (the chunks of %v and %v)", got, want, aChunk, bChunk)
	}
	p3 := res.Pages["src/pages/p3.lyr"]
	if pre := asset(t, res, "src/pages/p3.lyr", "modulepreload"); pre.URL != URLPrefix+bChunk[0] {
		t.Errorf("p3 assets %+v", p3)
	}
}
