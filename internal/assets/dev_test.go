package assets

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func readDevManifest(t *testing.T, devDir string) devManifest {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(devDir, DevManifest))
	if err != nil {
		t.Fatal(err)
	}
	var m devManifest
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func devFiles(t *testing.T, devDir string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(devDir, "_lyr"))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

// TestDevRebuild drives the incremental API as lyr dev will: a style
// edit is a Rebuild of the same esbuild context, with a new hash in the
// dev manifest and the old file removed; a new page recreates the context.
func TestDevRebuild(t *testing.T) {
	dir := project(t, basicFiles)
	devDir := filepath.Join(dir, ".lyr", "dev")
	b := New(Options{Root: dir, Dev: true}).(*esbuildBundler)
	defer b.Close()

	res, err := b.Bundle(basicInput(t, dir))
	if err != nil {
		t.Fatal(err)
	}
	noErrors(t, res)
	if err := WriteDev(devDir, res); err != nil {
		t.Fatal(err)
	}
	m1 := readDevManifest(t, devDir)
	css1 := m1.Pages["src/pages/index.lyr"][0].URL
	if !strings.HasSuffix(css1, ".css") {
		t.Fatalf("manifest: %+v", m1)
	}
	for _, f := range res.Files {
		if _, err := os.Stat(filepath.Join(devDir, "_lyr", f.Name)); err != nil {
			t.Errorf("%s not written: %v", f.Name, err)
		}
	}

	// Edit the card's style: same entries, so a Rebuild.
	card := filepath.Join(dir, "src/components/Card.lyr")
	src := strings.Replace(basicFiles["src/components/Card.lyr"], "color: red", "color: green", 1)
	if err := os.WriteFile(card, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err = b.Bundle(basicInput(t, dir))
	if err != nil {
		t.Fatal(err)
	}
	noErrors(t, res)
	if b.contexts != 1 {
		t.Errorf("a style edit created %d contexts, want 1 (a Rebuild)", b.contexts)
	}
	if err := WriteDev(devDir, res); err != nil {
		t.Fatal(err)
	}
	m2 := readDevManifest(t, devDir)
	css2 := m2.Pages["src/pages/index.lyr"][0].URL
	if css2 == css1 {
		t.Errorf("CSS URL unchanged after an edit: %s", css2)
	}
	data, err := os.ReadFile(filepath.Join(devDir, filepath.FromSlash(strings.TrimPrefix(css2, "/"))))
	if err != nil || !strings.Contains(string(data), "green") {
		t.Errorf("new CSS: %v\n%s", err, data)
	}
	if _, err := os.Stat(filepath.Join(devDir, filepath.FromSlash(strings.TrimPrefix(css1, "/")))); !os.IsNotExist(err) {
		t.Errorf("old CSS %s not removed: %v", css1, err)
	}
	if got, want := len(devFiles(t, devDir)), len(res.Files); got != want {
		t.Errorf("_lyr has %d files, want %d: %v", got, want, devFiles(t, devDir))
	}

	// A new page with a style: new entries, new context.
	if err := os.WriteFile(filepath.Join(dir, "src/pages/new.lyr"), []byte("<p>new</p>\n<style>p{color:red}</style>\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	in := basicInput(t, dir)
	in.Pages = append(in.Pages, Page{File: "src/pages/new.lyr", Modules: []string{"src/pages/new.lyr"}})
	res, err = b.Bundle(in)
	if err != nil {
		t.Fatal(err)
	}
	noErrors(t, res)
	if b.contexts != 2 {
		t.Errorf("contexts = %d after a new page, want 2", b.contexts)
	}
	if len(res.Pages["src/pages/new.lyr"]) != 1 {
		t.Errorf("new page assets: %+v", res.Pages["src/pages/new.lyr"])
	}
}

// TestDeterministic checks that the output doesn't depend on where the
// project is: lyr generate and lyr build on different machines write
// the same manifest.
func TestDeterministic(t *testing.T) {
	a, b := project(t, basicFiles), project(t, basicFiles)
	ra := bundle(t, Options{Root: a, Sourcemap: true}, basicInput(t, a))
	rb := bundle(t, Options{Root: b, Sourcemap: true}, basicInput(t, b))
	noErrors(t, ra)
	if !reflect.DeepEqual(ra.Pages, rb.Pages) {
		t.Errorf("pages differ:\n%v\n%v", ra.Pages, rb.Pages)
	}
	if !reflect.DeepEqual(ra.Files, rb.Files) {
		t.Errorf("files differ")
	}
}

// TestDevRebuildChunks: shared chunks (PLAN.md §5e) are written to
// .lyr/dev/_lyr/ and listed as modulepreload rows; an edit of the
// shared file renames the chunk, and the old one is pruned.
func TestDevRebuildChunks(t *testing.T) {
	dir := project(t, basicFiles)
	devDir := filepath.Join(dir, ".lyr", "dev")
	b := New(Options{Root: dir, Dev: true}).(*esbuildBundler)
	defer b.Close()
	preload := func() string {
		t.Helper()
		res, err := b.Bundle(basicInput(t, dir))
		if err != nil {
			t.Fatal(err)
		}
		noErrors(t, res)
		if err := WriteDev(devDir, res); err != nil {
			t.Fatal(err)
		}
		m := readDevManifest(t, devDir)
		var urls []string
		for _, page := range []string{"src/pages/index.lyr", "src/pages/blog/[slug].lyr"} {
			as := m.Pages[page]
			if len(as) != 3 || as[2].Kind != "modulepreload" {
				t.Fatalf("%s manifest rows %+v", page, as)
			}
			urls = append(urls, as[2].URL)
		}
		if urls[0] != urls[1] || !strings.HasPrefix(urls[0], "/_lyr/chunk-") {
			t.Fatalf("preloads %v", urls)
		}
		if got, want := len(devFiles(t, devDir)), len(res.Files); got != want {
			t.Errorf("_lyr has %d files, want %d: %v", got, want, devFiles(t, devDir))
		}
		return urls[0]
	}
	chunk1 := preload()
	data, err := os.ReadFile(filepath.Join(devDir, filepath.FromSlash(strings.TrimPrefix(chunk1, "/"))))
	if err != nil || !strings.Contains(string(data), "hi ") {
		t.Fatalf("chunk %s: %v\n%s", chunk1, err, data)
	}

	if err := os.WriteFile(filepath.Join(dir, "src/scripts/util.ts"), []byte("export function greet(name: string): string {\n  return `hello ${name}`;\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	chunk2 := preload()
	if chunk2 == chunk1 {
		t.Errorf("chunk not renamed after an edit: %s", chunk2)
	}
	if b.contexts != 1 {
		t.Errorf("contexts = %d, want 1", b.contexts)
	}
	if _, err := os.Stat(filepath.Join(devDir, filepath.FromSlash(strings.TrimPrefix(chunk1, "/")))); !os.IsNotExist(err) {
		t.Errorf("stale chunk %s not removed: %v", chunk1, err)
	}
}

func TestBundleImports(t *testing.T) {
	dir := project(t, map[string]string{
		"tsconfig.json":                   `{"compilerOptions": {"baseUrl": ".", "paths": {"@lib/*": ["src/lib/*"]}}}`,
		"node_modules/fancy/package.json": `{"name": "fancy", "main": "index.js"}`,
		"node_modules/fancy/index.js":     "export default function fancy() { return 'FANCY'; }\n",
		"node_modules/fancy/fancy.css":    ".fancy-lib { color: purple }\n",
		"src/lib/math.ts":                 "export const twice = (n: number): number => n * 2;\n",
		"src/fonts/a.woff2":               "font",
		"src/pages/index.lyr": "<p>x</p>\n<style>\n  @font-face { font-family: A; src: url(\"../fonts/a.woff2\") }\n  p { color: red }\n</style>\n" +
			"<script>\n  import \"fancy/fancy.css\";\n  import fancy from \"fancy\";\n  import { twice } from \"@lib/math\";\n  console.log(fancy(), twice(2));\n</script>\n",
	})
	res := bundle(t, Options{Root: dir}, input(t, dir, map[string][]string{"src/pages/index.lyr": {"src/pages/index.lyr"}}))
	noErrors(t, res)
	as := res.Pages["src/pages/index.lyr"]
	if len(as) != 2 {
		t.Fatalf("assets %+v", as)
	}
	css := string(res.File(as[0].URL).Contents)
	// The page's <style> comes first, then the CSS its script imports.
	if p, lib := strings.Index(css, "{color:red}"), strings.Index(css, ".fancy-lib"); p < 0 || lib < 0 || p > lib {
		t.Errorf("CSS:\n%s", css)
	}
	if !strings.Contains(css, `url("/_lyr/a-`) {
		t.Errorf("font URL not rewritten:\n%s", css)
	}
	js := string(res.File(as[1].URL).Contents)
	if !strings.Contains(js, "FANCY") || !strings.Contains(js, "*2") {
		t.Errorf("JS:\n%s", js)
	}
	var font bool
	for _, f := range res.Files {
		font = font || strings.HasSuffix(f.Name, ".woff2")
	}
	if !font {
		t.Errorf("font file not in the output")
	}
}
