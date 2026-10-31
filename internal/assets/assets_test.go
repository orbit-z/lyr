package assets

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/orbit-z/lyr/internal/compiler"
)

// project writes files into a new directory and returns it.
func project(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// input parses the .lyr files of dir and returns an Input whose pages
// have the given module lists.
func input(t *testing.T, dir string, pages map[string][]string) *Input {
	t.Helper()
	in := &Input{Sources: map[string]*Source{}}
	err := filepath.WalkDir(dir, func(name string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(name, ".lyr") {
			return err
		}
		src, err := os.ReadFile(name)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, name)
		rel = filepath.ToSlash(rel)
		f := compiler.Parse(string(src), compiler.Strict)
		if f.HasErrors() {
			t.Fatalf("%s: %v", rel, f.Diagnostics)
		}
		if s := SourceOf(rel, f); s != nil {
			in.Sources[rel] = s
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for page, mods := range pages {
		in.Pages = append(in.Pages, Page{File: page, Modules: mods})
	}
	return in
}

func bundle(t *testing.T, opts Options, in *Input) *Result {
	t.Helper()
	b := New(opts)
	defer b.Close()
	res, err := b.Bundle(in)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func noErrors(t *testing.T, res *Result) {
	t.Helper()
	for _, d := range res.Diagnostics {
		t.Errorf("diagnostic: %+v", d)
	}
	if t.Failed() {
		t.FailNow()
	}
}

var basicFiles = map[string]string{
	"src/layouts/Base.lyr": "<!DOCTYPE html>\n<html><head></head><body><slot /></body></html>\n<style is:global>\n  body { margin: 0 }\n</style>\n",
	"src/components/Card.lyr": "---\ntype Props struct{ Title string }\n---\n<div class=\"card\">{props.Title}</div>\n" +
		"<style>\n  .card { color: red }\n</style>\n<script>\n  import { greet } from \"../scripts/util\";\n  const n: number = 1;\n  console.log(greet(\"card\"), n);\n</script>\n",
	"src/scripts/util.ts":       "export function greet(name: string): string {\n  return `hi ${name}`;\n}\n",
	"src/pages/index.lyr":       "<p>home</p>\n<style>\n  p { color: blue }\n</style>\n",
	"src/pages/plain.lyr":       "<p>no assets</p>\n",
	"src/pages/blog/[slug].lyr": "<p>post</p>\n<script src=\"../../scripts/post.ts\"></script>\n",
	"src/scripts/post.ts":       "import { greet } from \"./util\";\nconsole.log(greet(\"post\"));\n",
}

func basicInput(t *testing.T, dir string) *Input {
	return input(t, dir, map[string][]string{
		"src/pages/index.lyr":       {"src/layouts/Base.lyr", "src/components/Card.lyr", "src/pages/index.lyr"},
		"src/pages/plain.lyr":       {"src/pages/plain.lyr"},
		"src/pages/blog/[slug].lyr": {"src/layouts/Base.lyr", "src/pages/blog/[slug].lyr"},
	})
}

var hashed = regexp.MustCompile(`^/_lyr/[A-Za-z0-9_-]+-[A-Z0-9]{8}\.(css|js)$`)

func TestBundle(t *testing.T) {
	dir := project(t, basicFiles)
	res := bundle(t, Options{Root: dir}, basicInput(t, dir))
	noErrors(t, res)

	if _, ok := res.Pages["src/pages/plain.lyr"]; ok {
		t.Errorf("plain page has assets: %v", res.Pages["src/pages/plain.lyr"])
	}
	idx := res.Pages["src/pages/index.lyr"]
	// util.ts is shared with blog/[slug]: a chunk, preloaded.
	if len(idx) != 3 || idx[0].Kind != "css" || idx[1].Kind != "js" || idx[2].Kind != "modulepreload" {
		t.Fatalf("index assets = %+v", idx)
	}
	for _, a := range idx[:2] {
		if !hashed.MatchString(a.URL) || !strings.Contains(a.URL, "/index-") {
			t.Errorf("URL %q", a.URL)
		}
	}
	css := string(res.File(idx[0].URL).Contents)
	// Graph order: the layout, then the card, then the page.
	body, card, p := strings.Index(css, "body{"), strings.Index(css, ".card[data-l-"), strings.Index(css, "p[data-l-")
	if body < 0 || card < 0 || p < 0 || !(body < card && card < p) {
		t.Errorf("CSS order wrong:\n%s", css)
	}
	js := string(res.File(idx[1].URL).Contents) + string(res.File(idx[2].URL).Contents)
	if strings.Contains(js, ": number") || strings.Contains(js, ": string") || !strings.Contains(js, "hi ") {
		t.Errorf("JS bundle:\n%s", js)
	}
	if strings.Contains(js, "sourceMappingURL") {
		t.Errorf("prod JS links a source map:\n%s", js)
	}

	// A page with a src script and a global style.
	post := res.Pages["src/pages/blog/[slug].lyr"]
	if len(post) != 3 || !strings.Contains(post[1].URL, "/blog-slug-") || post[2] != idx[2] {
		t.Fatalf("post assets = %+v", post)
	}
	for _, f := range res.Files {
		if strings.HasSuffix(f.Name, ".map") {
			t.Errorf("prod build without sourcemap wrote %s", f.Name)
		}
	}
}

func TestBundleCSSOnlyAndEmptyJS(t *testing.T) {
	dir := project(t, map[string]string{
		"src/pages/a.lyr": "<p>a</p>\n<style>p{color:red}</style>\n",
		"src/pages/b.lyr": "<p>b</p>\n<script>\n  type T = { a: number };\n  export type { T };\n</script>\n",
	})
	res := bundle(t, Options{Root: dir}, input(t, dir, map[string][]string{
		"src/pages/a.lyr": {"src/pages/a.lyr"},
		"src/pages/b.lyr": {"src/pages/b.lyr"},
	}))
	noErrors(t, res)
	if a := res.Pages["src/pages/a.lyr"]; len(a) != 1 || a[0].Kind != "css" {
		t.Errorf("a = %+v", a)
	}
	if b, ok := res.Pages["src/pages/b.lyr"]; ok {
		t.Errorf("b (types only) = %+v", b)
	}
	for _, f := range res.Files {
		if strings.HasSuffix(f.Name, ".js") {
			t.Errorf("JS output %s", f.Name)
		}
	}
}

func TestBundleErrors(t *testing.T) {
	tests := []struct {
		name, file, src, want string
	}{
		{"syntax", "src/pages/a.lyr", "<p>a</p>\n<script>\n  const x = ;\n</script>\n", "src/pages/a.lyr:3:13: Unexpected \";\""},
		{"resolve", "src/pages/a.lyr", "<p>a</p>\n<script>\n  import x from \"nope-pkg\";\n  console.log(x);\n</script>\n", `src/pages/a.lyr:3:17: Could not resolve "nope-pkg"`},
		{"src", "src/pages/a.lyr", "<p>a</p>\n<script src=\"./missing.ts\"></script>\n", `src/pages/a.lyr:2:13: Could not resolve "./missing.ts"`},
		{"first line", "src/pages/a.lyr", "<script>const x = ;</script>\n", "src/pages/a.lyr:1:19: Unexpected \";\""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := project(t, map[string]string{tt.file: tt.src})
			res := bundle(t, Options{Root: dir}, input(t, dir, map[string][]string{tt.file: {tt.file}}))
			if !res.HasErrors() {
				t.Fatalf("no errors: %+v", res)
			}
			d := res.Diagnostics[0]
			got := fmt.Sprintf("%s:%d:%d: %s", d.File, d.Line, d.Col, d.Message)
			if got != tt.want {
				t.Errorf("got  %s\nwant %s", got, tt.want)
			}
			if res.Files != nil {
				t.Errorf("files on error: %d", len(res.Files))
			}
		})
	}
}
