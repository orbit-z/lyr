package project

import (
	"bytes"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/orbit-z/lyr/internal/compiler"
)

const reloadCard = `---
type Props struct {
    Title string
}
n := len(props.Title)
---
<div class="card"><h2>{props.Title}</h2> {n}</div>
`

const reloadPage = `---
import (
    Card "../components/Card.lyr"
    Base "../layouts/Post.lyr"
)
---
<Base title="Home"><Card title="One" /><Card title="Two" /></Base>
`

// TestReloadMatchesFront applies a sequence of edits and checks, after
// each, that Reload of the previous project gives the same result as a
// fresh Load and Front.
func TestReloadMatchesFront(t *testing.T) {
	files := maps.Clone(mdFiles)
	files["src/components/Card.lyr"] = reloadCard
	files["src/pages/home.lyr"] = reloadPage
	p := newTestProject(t, files)

	remove := func(rel string) {
		t.Helper()
		if err := os.RemoveAll(filepath.Join(p.Dir, filepath.FromSlash(rel))); err != nil {
			t.Fatal(err)
		}
	}
	rename := func(from, to string) {
		t.Helper()
		if err := os.Rename(filepath.Join(p.Dir, filepath.FromSlash(from)), filepath.Join(p.Dir, filepath.FromSlash(to))); err != nil {
			t.Fatal(err)
		}
	}
	edit := func(rel, old, new string) {
		t.Helper()
		src := p.read(rel)
		if !strings.Contains(src, old) {
			t.Fatalf("%s has no %q", rel, old)
		}
		p.write(rel, strings.Replace(src, old, new, 1))
	}
	badge := "---\ntype Props struct{ Label string }\n---\n<span>{props.Label}</span>\n"

	steps := []struct {
		name string
		do   func() []string // returns the changed paths
		// full is set when Reload is expected to fall back to Load.
		full bool
	}{
		{"body", func() []string {
			edit("src/components/Card.lyr", "<h2>", "<h2 class=\"t\">")
			return []string{"src/components/Card.lyr"}
		}, false},
		{"frontmatter", func() []string {
			edit("src/components/Card.lyr", "n := len(props.Title)", "n := len(props.Title) + 1")
			return []string{"src/components/Card.lyr"}
		}, false},
		{"frontmatter line added above Props", func() []string {
			edit("src/components/Card.lyr", "---\ntype Props", "---\n// Card is a card.\ntype Props")
			return []string{"src/components/Card.lyr"}
		}, false},
		{"props field added", func() []string {
			edit("src/components/Card.lyr", "Title string\n", "Title string\n    Featured bool\n")
			return []string{"src/components/Card.lyr"}
		}, false},
		{"props field renamed", func() []string {
			src := strings.ReplaceAll(p.read("src/components/Card.lyr"), "Title", "Heading")
			p.write("src/components/Card.lyr", src)
			return []string{"src/components/Card.lyr"}
		}, false},
		{"props field renamed back", func() []string {
			src := strings.ReplaceAll(p.read("src/components/Card.lyr"), "Heading", "Title")
			p.write("src/components/Card.lyr", src)
			return []string{"src/components/Card.lyr"}
		}, false},
		{"unknown prop", func() []string {
			edit("src/pages/home.lyr", `title="Two"`, `title="Two" psot={1}`)
			return []string{"src/pages/home.lyr"}
		}, false},
		{"unknown prop fixed", func() []string {
			edit("src/pages/home.lyr", ` psot={1}`, ``)
			return []string{"src/pages/home.lyr"}
		}, false},
		{"new imported file", func() []string {
			p.write("src/components/Badge.lyr", badge)
			edit("src/pages/home.lyr", "Card \"../components/Card.lyr\"\n", "Card \"../components/Card.lyr\"\n    Badge \"../components/Badge.lyr\"\n")
			edit("src/pages/home.lyr", `<Card title="Two" />`, `<Card title="Two" /><Badge label="new" />`)
			return []string{"src/components/Badge.lyr", "src/pages/home.lyr"}
		}, false},
		{"new page", func() []string {
			p.write("src/pages/about.lyr", "<h1>About</h1>\n")
			return []string{"src/pages/about.lyr"}
		}, false},
		{"deleted imported file", func() []string {
			remove("src/components/Badge.lyr")
			return []string{"src/components/Badge.lyr"}
		}, false},
		{"deleted file restored", func() []string {
			p.write("src/components/Badge.lyr", badge)
			return []string{p.Dir + "/src/components/Badge.lyr"} // absolute
		}, false},
		{"renamed import alias", func() []string {
			edit("src/pages/home.lyr", "Card \"../components/Card.lyr\"", "Box \"../components/Card.lyr\"")
			src := strings.ReplaceAll(p.read("src/pages/home.lyr"), "<Card ", "<Box ")
			p.write("src/pages/home.lyr", src)
			return []string{"src/pages/home.lyr"}
		}, false},
		{"renamed file", func() []string {
			rename("src/components/Card.lyr", "src/components/Tile.lyr")
			edit("src/pages/home.lyr", "../components/Card.lyr", "../components/Tile.lyr")
			return []string{"src/components/Card.lyr", "src/components/Tile.lyr", "src/pages/home.lyr"}
		}, false},
		{"renamed directory", func() []string {
			rename("src/components", "src/parts")
			return []string{"src/components", "src/parts"}
		}, false},
		{"importers fixed", func() []string {
			src := strings.ReplaceAll(p.read("src/pages/home.lyr"), "../components/", "../parts/")
			p.write("src/pages/home.lyr", src)
			return []string{"src/pages/home.lyr"}
		}, false},
		{"style gained", func() []string {
			p.write("src/parts/Tile.lyr", p.read("src/parts/Tile.lyr")+"<style>\n  .card { color: red; }\n</style>\n")
			return []string{"src/parts/Tile.lyr"}
		}, false},
		{"style lost", func() []string {
			src := p.read("src/parts/Tile.lyr")
			p.write("src/parts/Tile.lyr", src[:strings.Index(src, "<style>")])
			return []string{"src/parts/Tile.lyr"}
		}, false},
		{"parse error", func() []string {
			edit("src/parts/Tile.lyr", "<h2 class=\"t\">", "<h2 class=\"t\"><b>{")
			return []string{"src/parts/Tile.lyr"}
		}, false},
		{"parse error fixed", func() []string {
			edit("src/parts/Tile.lyr", "<b>{", "")
			return []string{"src/parts/Tile.lyr"}
		}, false},
		{"saved unchanged", func() []string {
			p.write("src/parts/Tile.lyr", p.read("src/parts/Tile.lyr"))
			return []string{"src/parts/Tile.lyr"}
		}, false},
		{"md body", func() []string {
			edit("src/pages/blog/hello.md", "Lyr compiles this file to Go.", "Lyr compiles this file to *Go*.")
			return []string{"src/pages/blog/hello.md"}
		}, false},
		{"md frontmatter", func() []string {
			edit("src/pages/blog/hello.md", "title: Hello, Lyr", "title: Hello again")
			return []string{"src/pages/blog/hello.md"}
		}, false},
		{"md frontmatter error", func() []string {
			edit("src/pages/blog/hello.md", "order: 3", "order: [")
			return []string{"src/pages/blog/hello.md"}
		}, false},
		{"md frontmatter fixed", func() []string {
			edit("src/pages/blog/hello.md", "order: [", "order: 3")
			return []string{"src/pages/blog/hello.md"}
		}, false},
		{"md link target deleted", func() []string {
			remove("src/pages/blog/routing-guide.md")
			return []string{"src/pages/blog/routing-guide.md"}
		}, false},
		{"md link target restored", func() []string {
			p.write("src/pages/blog/routing-guide.md", files["src/pages/blog/routing-guide.md"])
			return []string{"src/pages/blog/routing-guide.md"}
		}, false},
		{"md heading gained", func() []string {
			edit("src/pages/blog/routing-guide.md", "# File-based routing\n", "# File-based routing\n\n## Setup\n")
			return []string{"src/pages/blog/routing-guide.md"}
		}, false},
		{"md new page", func() []string {
			p.write("src/pages/blog/second.md", "---\ntitle: Second\nlayout: ../../layouts/Post.lyr\n---\nSee [hello](./hello.md).\n")
			return []string{"src/pages/blog/second.md"}
		}, false},
		{"md newly imported", func() []string {
			p.write("src/content/unused.md", "---\nauthor: Bob\n---\nNow used.\n")
			edit("src/pages/index.lyr", "Intro \"../content/intro.md\"\n", "Intro \"../content/intro.md\"\n    Unused \"../content/unused.md\"\n")
			edit("src/pages/index.lyr", "<Intro />", "<Intro /><Unused />")
			return []string{"src/content/unused.md", "src/pages/index.lyr"}
		}, false},
		{"md no longer imported", func() []string {
			edit("src/pages/index.lyr", "    Unused \"../content/unused.md\"\n", "")
			edit("src/pages/index.lyr", "<Unused />", "")
			return []string{"src/pages/index.lyr"}
		}, false},
		{"layout props changed", func() []string {
			edit("src/layouts/Post.lyr", "    Order    uint8\n", "    Order    uint8\n    Draft    bool\n")
			return []string{"src/layouts/Post.lyr"}
		}, false},
		{"layout deleted", func() []string {
			remove("src/layouts/Post.lyr")
			return []string{"src/layouts/Post.lyr"}
		}, false},
		{"layout restored", func() []string {
			p.write("src/layouts/Post.lyr", mdLayout)
			return []string{"src/layouts/Post.lyr"}
		}, false},
		{"go file outside src/locals", func() []string {
			p.write("internal/x/x.go", "package x\n")
			return []string{"internal/x/x.go"}
		}, false},
		{"locals gained", func() []string {
			p.write("src/locals/locals.go", "package locals\n\ntype Locals struct{ User string }\n")
			return []string{"src/locals/locals.go"}
		}, true},
		{"locals file edited", func() []string {
			p.write("src/locals/locals.go", "package locals\n\n// Locals are the request's.\ntype Locals struct{ User string }\n")
			return []string{"src/locals/locals.go"}
		}, false},
		{"locals RouteInfo field added", func() []string {
			p.write("src/locals/locals.go", "package locals\n\nimport \"github.com/orbit-z/lyr\"\n\ntype Locals struct {\n\tUser  string\n\tRoute lyr.RouteInfo\n}\n")
			return []string{"src/locals/locals.go"}
		}, false},
		{"locals import error", func() []string {
			p.write("src/locals/locals.go", "package locals\n\nimport _ \"example.com/blog/internal/y\"\n\ntype Locals struct{ User string }\n")
			p.write("internal/y/y.go", "package y\n")
			return []string{"src/locals/locals.go", "internal/y/y.go"}
		}, false},
		{"locals import leads to app", func() []string {
			p.write("internal/y/y.go", "package y\n\nimport _ \"example.com/blog/lyr_gen/app\"\n")
			return []string{"internal/y/y.go"}
		}, true},
		{"lyr.json", func() []string {
			p.write("lyr.json", `{"compressHTML": false}`)
			return []string{"lyr.json"}
		}, true},
		{"server entry added", func() []string {
			p.write("cmd/server/main.go", "package main\n\nfunc main() {}\n")
			return []string{"cmd/server/main.go"}
		}, false},
		{"src directory", func() []string {
			p.write("src/pages/new.lyr", "<p>new</p>\n")
			return []string{"src"}
		}, false},
	}

	inc, err := Load(p.Dir)
	if err != nil {
		t.Fatal(err)
	}
	inc.Front()
	for _, st := range steps {
		paths := st.do()
		got, _, err := Reload(inc, p.Dir, paths)
		if err != nil {
			t.Fatalf("%s: Reload: %v", st.name, err)
		}
		if (got != inc) != st.full {
			t.Errorf("%s: Reload fell back to Load: %v, want %v", st.name, got != inc, st.full)
		}
		want, err := Load(p.Dir)
		if err != nil {
			t.Fatal(err)
		}
		want.Front()
		compareFront(t, st.name, got, want)
		// As if Typecheck ran: Reload must reset this.
		for _, f := range got.Files {
			f.Typed, f.Output = true, &compiler.Output{}
		}
		inc = got
	}
}

// compareFront fails when got and want, both through Front, differ in
// anything the later stages read.
func compareFront(t *testing.T, step string, got, want *Project) {
	t.Helper()
	if g, w := diagStrings(got.Diagnostics), diagStrings(want.Diagnostics); !reflect.DeepEqual(g, w) {
		t.Fatalf("%s: diagnostics differ:\ngot:\n%s\nwant:\n%s", step, joinLines(g), joinLines(w))
	}
	if !reflect.DeepEqual(got.Diagnostics, want.Diagnostics) {
		t.Fatalf("%s: diagnostics differ in detail", step)
	}
	var gp, wp []string
	for _, f := range got.Files {
		gp = append(gp, f.Path)
	}
	for _, f := range want.Files {
		wp = append(wp, f.Path)
	}
	if !reflect.DeepEqual(gp, wp) {
		t.Fatalf("%s: files differ:\ngot  %v\nwant %v", step, gp, wp)
	}
	for i, g := range got.Files {
		w := want.Files[i]
		if g.Src != w.Src || g.Typed || g.Output != g.Pass1 || g.Writes != nil {
			t.Errorf("%s: %s: source, or Output not reset to Pass1", step, g.Path)
		}
		if !bytes.Equal(g.Pass1.Decl.Content, w.Pass1.Decl.Content) || !bytes.Equal(g.Pass1.Impl.Content, w.Pass1.Impl.Content) {
			t.Errorf("%s: %s: pass-1 output differs", step, g.Path)
		}
		if !reflect.DeepEqual(g.Pass1.Diagnostics, w.Pass1.Diagnostics) || !reflect.DeepEqual(g.Pass1.CallSites, w.Pass1.CallSites) ||
			!reflect.DeepEqual(g.Pass1.Decl.Map, w.Pass1.Decl.Map) || !reflect.DeepEqual(g.Pass1.Impl.Map, w.Pass1.Impl.Map) {
			t.Errorf("%s: %s: pass-1 diagnostics, call sites or source maps differ", step, g.Path)
		}
		if !reflect.DeepEqual(g.Analysis, w.Analysis) || !reflect.DeepEqual(g.MD, w.MD) {
			t.Errorf("%s: %s: analysis differs", step, g.Path)
		}
		if !reflect.DeepEqual(g.calls(), w.calls()) {
			t.Errorf("%s: %s: component calls differ", step, g.Path)
		}
	}
	if !reflect.DeepEqual(got.Index, want.Index) {
		t.Errorf("%s: component index differs", step)
	}
	if got.localsFields != want.localsFields {
		t.Errorf("%s: Locals fields %+v, want %+v", step, got.localsFields, want.localsFields)
	}
	if got.Options != want.Options || got.ServerEntry != want.ServerEntry || got.GeneratedServer != want.GeneratedServer {
		t.Errorf("%s: options or server entry differ: %+v %s vs %+v %s", step, got.Options, got.ServerEntry, want.Options, want.ServerEntry)
	}
	if !reflect.DeepEqual(got.markdown, want.markdown) || !reflect.DeepEqual(got.mdPaths, want.mdPaths) {
		t.Errorf("%s: Markdown sources differ:\ngot  %v\nwant %v", step, got.mdPaths, want.mdPaths)
	}
	if !reflect.DeepEqual(got.AssetInput(), want.AssetInput()) {
		t.Errorf("%s: asset input differs", step)
	}
	gf, wf := got.GenFiles(), want.GenFiles()
	if len(gf) != len(wf) {
		t.Fatalf("%s: %d generated files, want %d", step, len(gf), len(wf))
	}
	for i := range gf {
		if gf[i].Path != wf[i].Path || !bytes.Equal(gf[i].Content, wf[i].Content) {
			t.Errorf("%s: generated %s differs from %s", step, gf[i].Path, wf[i].Path)
		}
	}
}
