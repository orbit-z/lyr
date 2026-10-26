package project

import (
	"context"
	"errors"
	gotoken "go/token"
	"go/types"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/orbit-z/orbit/internal/compiler"
)

// mdLayout is a layout whose Props a Markdown page fills.
const mdLayout = `---
import "time"

type Props struct {
    Title    string
    Date     time.Time
    Tags     []string
    Order    uint8
    Headings []orbit.Heading
}
---
<!DOCTYPE html>
<html><head><title>{props.Title}</title></head>
<body>
<nav>{for _, h := range props.Headings}<a href={"#" + h.Slug}>{h.Text}</a>{/for}</nav>
<p>{props.Date.Format("2006-01-02")} {len(props.Tags)} {props.Order}</p>
<article class="prose"><slot /></article>
</body></html>
<style>
  .prose :global(h2) { margin-top: 2rem; }
</style>
`

// mdFiles is a project with Markdown pages, an imported Markdown file, and
// Markdown files that aren't compiled (not pages, not imported).
var mdFiles = map[string]string{
	"src/layouts/Post.orbit": mdLayout,
	"src/pages/blog/hello.md": `---
title: Hello, Orbit
date: 2026-10-26
tags: [news, go]
order: 3
layout: ../../layouts/Post.orbit
---

Orbit compiles this file to Go.

## Why Markdown

` + "```go\nfmt.Println(\"hi\")\n```" + `

Read the [routing guide](./routing-guide.md#setup) and [the intro](./routing-guide.md#file-based-routing).
`,
	"src/pages/blog/routing-guide.md": "---\ntitle: File-based routing\nlayout: ../../layouts/Post.orbit\n---\n# File-based routing\n",
	"src/pages/404.md":                "# Not here\n",
	"src/content/intro.md":            "---\nauthor: Ada\n---\nShort **intro**.\n",
	"src/content/unused.md":           "---\nbroken: [\n---\n",
	"src/pages/_drafts/next.md":       "---\nbroken: [\n---\n",
	"src/README.md":                   "---\nbroken: [\n---\n",
	"src/pages/index.orbit": `---
import (
    Intro "../content/intro.md"
    Hello "./blog/hello.md"
)
---
<main><a href={Hello.URL}>{Hello.Frontmatter.Title}</a> ({len(Hello.Headings)})
<Intro /> by {Intro.Frontmatter.Author}</main>
`,
	"internal/apptest/app_test.go": `package apptest

import (
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"example.com/blog/orbit_gen/app"
)

func get(t *testing.T, path string) (int, string) {
	rec := httptest.NewRecorder()
	app.New().Handler().ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
	b, _ := io.ReadAll(rec.Body)
	return rec.Code, string(b)
}

func TestPages(t *testing.T) {
	for _, tt := range []struct {
		path string
		code int
		want []string
	}{
		{"/blog/hello", 200, []string{"<title>Hello, Orbit</title>", "<a href=\"#why-markdown\" data-o-",
			">2026-10-26 2 3</p>", "<article class=\"prose\" data-o-", "<h2 id=\"why-markdown\">Why Markdown</h2>",
			"<a href=\"/blog/routing-guide#setup\">routing guide</a>", "<span class=\"nx\">fmt</span>"}},
		{"/", 200, []string{"<a href=\"/blog/hello\">Hello, Orbit</a> (1)", "<p>Short <strong>intro</strong>.</p>\n by Ada</main>"}},
		{"/nope", 404, []string{"<h1 id=\"not-here\">Not here</h1>"}},
	} {
		code, body := get(t, tt.path)
		if code != tt.code {
			t.Errorf("GET %s: %d", tt.path, code)
		}
		for _, w := range tt.want {
			if !strings.Contains(body, w) {
				t.Errorf("GET %s: no %q in:\n%s", tt.path, w, body)
			}
		}
	}
}
`,
}

// TestMarkdownProject generates a project with Markdown pages and imports,
// compares the generated packages of hello.md and intro.md with
// testdata/markdown/*.golden (go test ./internal/project -update), and
// serves the pages.
func TestMarkdownProject(t *testing.T) {
	needGo(t)
	p := newTestProject(t, mdFiles)
	res := p.generate()
	want := "src/pages/blog/hello.md:17:10: warning: link ./routing-guide.md#setup: src/pages/blog/routing-guide.md has no heading #setup"
	if got := joinLines(diagStrings(res.Diagnostics)); got != want {
		t.Fatalf("diagnostics:\n%s\nwant:\n%s", got, want)
	}
	for _, path := range []string{
		"orbit_gen/decl/pages/blog/hello_md/decl.go", "orbit_gen/impl/pages/blog/hello_md/render.go",
		"orbit_gen/decl/content/intro_md/decl.go", "orbit_gen/impl/content/intro_md/render.go",
	} {
		content := p.read(path)
		golden := filepath.Join("testdata", "markdown", strings.ReplaceAll(strings.TrimPrefix(path, "orbit_gen/"), "/", "_")+".golden")
		if *update {
			if err := os.MkdirAll(filepath.Dir(golden), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(golden, []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if want, err := os.ReadFile(golden); err != nil || string(want) != content {
			t.Errorf("%s differs from %s (run with -update to accept):\n%s", path, golden, content)
		}
	}
	for _, path := range []string{"orbit_gen/decl/content/unused_md", "orbit_gen/decl/p_readme_md", "orbit_gen/decl/pages/p_drafts"} {
		if p.exists(path) {
			t.Errorf("%s generated", path)
		}
	}
	app := p.read("orbit_gen/app/app.go")
	for _, w := range []string{`Pattern: "/blog/hello"`, `File:    "src/pages/blog/hello.md"`, `Pattern: "404"`, `File:    "src/pages/404.md"`} {
		if !strings.Contains(app, w) {
			t.Errorf("no %q in app.go:\n%s", w, app)
		}
	}
	// The highlight stylesheet is first in the stylesheet of the page with
	// highlighted code, and only there.
	css := res.Project.Assets.Pages["src/pages/blog/hello.md"]
	if len(css) != 1 || css[0].Kind != "css" {
		t.Fatalf("hello.md assets: %v", css)
	}
	sheet := string(res.Project.Assets.File(css[0].URL).Contents)
	if !strings.HasPrefix(sheet, ".chroma{") || !strings.Contains(sheet, "light-dark(") || !strings.Contains(sheet, ".prose[data-o-") {
		t.Errorf("hello.md stylesheet:\n%s", sheet)
	}
	if got := res.Project.Assets.Pages["src/pages/blog/routing-guide.md"]; len(got) != 1 {
		t.Errorf("routing-guide.md assets: %v", got)
	} else if s := string(res.Project.Assets.File(got[0].URL).Contents); strings.Contains(s, "chroma") {
		t.Errorf("routing-guide.md has the highlight CSS:\n%s", s)
	}
	if out, err := p.goCmd("vet", "./..."); err != nil {
		t.Fatalf("go vet: %v\n%s", err, out)
	}
	if out, err := p.goCmd("test", "./internal/apptest"); err != nil {
		t.Fatalf("go test: %v\n%s", err, out)
	}
}

// TestMarkdownFrontErrors checks the errors found before the typed pass,
// with their positions.
func TestMarkdownFrontErrors(t *testing.T) {
	p := newTestProject(t, map[string]string{
		"src/layouts/Post.orbit":           mdLayout,
		"src/pages/about.md":               "# About\n",
		"src/pages/about.orbit":            "<p>About</p>\n",
		"src/pages/blog/[slug].md":         "# Post\n",
		"src/pages/a.md":                   "---\ntitle: A\ntilte: typo\nheadings: x\nlayout: ../layouts/Post.orbit\n---\n",
		"src/pages/b.md":                   "---\nlayout: ../layouts/Nope.orbit\n---\n",
		"src/pages/c.md":                   "---\ntitle: [\n---\n",
		"src/pages/d.md":                   "See [x](./nope.md) and [intro](../content/intro.md).\n",
		"src/content/intro.md":             "Intro.\n",
		"src/pages/e.orbit":                "---\nimport Missing \"../content/missing.md\"\n---\n<Missing x=\"1\" />\n",
		"src/pages/f.orbit":                "---\nimport Intro \"../content/intro.md\"\n---\n<Intro x=\"1\" />\n",
		"src/pages/hello.markdown":         "Hi\n",
		"src/pages/hello.md":               "Hi\n",
		"src/pages/docs/index.md":          "Hi\n",
		"src/pages/docs.orbit":             "<p>docs</p>\n",
		"src/components/Unused.orbit":      "---\nimport Intro \"../content/intro.md\"\n---\n<p></p>\n",
		"src/pages/_drafts/broken.md":      "---\n: [\n---\n",
		"src/components/NotImported.orbit": "<p></p>\n",
	})
	proj, err := Load(p.Dir)
	if err != nil {
		t.Fatal(err)
	}
	proj.Front()
	want := []string{
		`src/components/Unused.orbit:2:8: component Intro is imported but not used; remove the import`,
		`src/pages/a.md:3:1: unknown frontmatter key "tilte" for layout Post (props: title, date, tags, order, headings)`,
		`src/pages/a.md:4:1: frontmatter key "headings" can't be set: Orbit fills Post.Props.Headings with the page's headings; rename the key`,
		`src/pages/about.orbit: route /about is already defined by src/pages/about.md; remove or rename one of them`,
		`src/pages/b.md:2:9: layout ../layouts/Nope.orbit: no such file`,
		`src/pages/blog/[slug].md: a Markdown page can't have route params; rename it, or write a [slug].orbit page`,
		`src/pages/c.md:2:1: invalid YAML in the frontmatter: did not find expected node content`,
		`src/pages/d.md:1:5: link ./nope.md: no such file`,
		`src/pages/d.md:1:24: link ../content/intro.md: src/content/intro.md isn't a page, so it has no URL; link to a page, or import it into one`,
		`src/pages/docs/index.md: route /docs is already defined by src/pages/docs.orbit; remove or rename one of them`,
		`src/pages/e.orbit:2:16: no such component: src/content/missing.md`,
		`src/pages/f.orbit:4:8: unknown prop "x" on <Intro> (Intro takes no props)`,
		`src/pages/hello.md: generates the same Go package as src/pages/hello.markdown (orbit_gen/decl/pages/hello_md); rename one of them`,
		`src/pages/hello.md: route /hello is already defined by src/pages/hello.markdown; remove or rename one of them`,
	}
	if got := joinLines(diagStrings(proj.Diagnostics)); got != joinLines(want) {
		t.Errorf("diagnostics:\n%s\nwant:\n%s", got, joinLines(want))
	}
}

// TestMarkdownTypedErrors checks the frontmatter conversions of the typed
// pass, at YAML positions.
func TestMarkdownTypedErrors(t *testing.T) {
	needGo(t)
	p := newTestProject(t, map[string]string{
		"src/layouts/Post.orbit": mdLayout,
		"src/pages/a.md":         "---\ntitle: Hi\norder: 300\ndate: \"2026-10-26\"\nlayout: ../layouts/Post.orbit\n---\n",
		"src/pages/b.md":         "---\ntags: news\nlayout: ../layouts/Post.orbit\n---\n",
	})
	res := p.generate()
	want := []string{
		`src/pages/a.md:3:8: frontmatter order: 300 overflows uint8 (Post.Props.Order is uint8)`,
		`src/pages/a.md:4:7: frontmatter date: "2026-10-26" isn't a date (Post.Props.Date is time.Time)`,
		`src/pages/b.md:2:7: frontmatter tags: "news" isn't a list (Post.Props.Tags is []string)`,
	}
	if got := joinLines(diagStrings(res.Diagnostics)); got != joinLines(want) {
		t.Errorf("diagnostics:\n%s\nwant:\n%s", got, joinLines(want))
	}
	if res.Write != nil {
		t.Error("wrote orbit_gen/ despite errors")
	}
}

// TestMarkdownFallback: when the layout's decl package doesn't type-check
// (or no export data can be read), the frontmatter assignments keep their
// literals guessed from the YAML, so go build reports a mismatch at the .md
// line through the //line directives.
func TestMarkdownFallback(t *testing.T) {
	needGo(t)
	p := newTestProject(t, map[string]string{
		"src/layouts/Post.orbit": "---\ntype Props struct {\n    Title string\n    Broken undefinedType\n}\n---\n<slot />\n",
		"src/pages/a.md":         "---\ntitle: 3\nlayout: ../layouts/Post.orbit\n---\n",
	})
	res := p.generate()
	if res.HasErrors() || res.Write == nil {
		t.Fatalf("generate: %v", diagStrings(res.Diagnostics))
	}
	if impl := p.read("orbit_gen/impl/pages/a_md/render.go"); !strings.Contains(impl, "//line ../../../../src/pages/a.md:2:1\np.Title = 3\n") {
		t.Errorf("not the fallback:\n%s", impl)
	}
	out, err := p.goCmd("build", "./...")
	if clean := string(res.Project.CleanGoOutput([]byte(out))); err == nil || !strings.Contains(clean, "src/layouts/Post.orbit:4:12: undefined: undefinedType") {
		t.Errorf("go build: %v\n%s", err, clean)
	}

	// The layout fixed, but no export data: the mismatch is reported at
	// the frontmatter key.
	p.write("src/layouts/Post.orbit", "---\ntype Props struct {\n    Title string\n}\n---\n<slot />\n")
	proj, err := Load(p.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if proj.Front() {
		t.Fatal(diagStrings(proj.Diagnostics))
	}
	if _, err := proj.Typecheck(context.Background(), compiler.NewChecker(failLoader{})); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteGenerated(p.Dir, proj.GenFiles()); err != nil {
		t.Fatal(err)
	}
	out, err = p.goCmd("build", "./...")
	clean := string(proj.CleanGoOutput([]byte(out)))
	if err == nil || !strings.Contains(clean, "src/pages/a.md:2:") || !strings.Contains(clean, "cannot use 3 (untyped int constant) as string value in assignment") {
		t.Errorf("go build: %v\n%s", err, clean)
	}
}

// failLoader is an ExportLoader with no export data.
type failLoader struct{}

func (failLoader) Load(context.Context, *gotoken.FileSet, []string) (types.Importer, error) {
	return nil, errors.New("no export data")
}
