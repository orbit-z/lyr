package project

import (
	"bytes"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/orbit-z/orbit/internal/compiler"
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
    Card "../components/Card.orbit"
    Base "../layouts/Post.orbit"
)
---
<Base title="Home"><Card title="One" /><Card title="Two" /></Base>
`

// TestReloadMatchesFront applies a sequence of edits and checks, after
// each, that Reload of the previous project gives the same result as a
// fresh Load and Front.
func TestReloadMatchesFront(t *testing.T) {
	files := maps.Clone(mdFiles)
	files["src/components/Card.orbit"] = reloadCard
	files["src/pages/home.orbit"] = reloadPage
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
			edit("src/components/Card.orbit", "<h2>", "<h2 class=\"t\">")
			return []string{"src/components/Card.orbit"}
		}, false},
		{"frontmatter", func() []string {
			edit("src/components/Card.orbit", "n := len(props.Title)", "n := len(props.Title) + 1")
			return []string{"src/components/Card.orbit"}
		}, false},
		{"frontmatter line added above Props", func() []string {
			edit("src/components/Card.orbit", "---\ntype Props", "---\n// Card is a card.\ntype Props")
			return []string{"src/components/Card.orbit"}
		}, false},
		{"props field added", func() []string {
			edit("src/components/Card.orbit", "Title string\n", "Title string\n    Featured bool\n")
			return []string{"src/components/Card.orbit"}
		}, false},
		{"props field renamed", func() []string {
			src := strings.ReplaceAll(p.read("src/components/Card.orbit"), "Title", "Heading")
			p.write("src/components/Card.orbit", src)
			return []string{"src/components/Card.orbit"}
		}, false},
		{"props field renamed back", func() []string {
			src := strings.ReplaceAll(p.read("src/components/Card.orbit"), "Heading", "Title")
			p.write("src/components/Card.orbit", src)
			return []string{"src/components/Card.orbit"}
		}, false},
		{"unknown prop", func() []string {
			edit("src/pages/home.orbit", `title="Two"`, `title="Two" psot={1}`)
			return []string{"src/pages/home.orbit"}
		}, false},
		{"unknown prop fixed", func() []string {
			edit("src/pages/home.orbit", ` psot={1}`, ``)
			return []string{"src/pages/home.orbit"}
		}, false},
		{"new imported file", func() []string {
			p.write("src/components/Badge.orbit", badge)
			edit("src/pages/home.orbit", "Card \"../components/Card.orbit\"\n", "Card \"../components/Card.orbit\"\n    Badge \"../components/Badge.orbit\"\n")
			edit("src/pages/home.orbit", `<Card title="Two" />`, `<Card title="Two" /><Badge label="new" />`)
			return []string{"src/components/Badge.orbit", "src/pages/home.orbit"}
		}, false},
		{"new page", func() []string {
			p.write("src/pages/about.orbit", "<h1>About</h1>\n")
			return []string{"src/pages/about.orbit"}
		}, false},
		{"deleted imported file", func() []string {
			remove("src/components/Badge.orbit")
			return []string{"src/components/Badge.orbit"}
		}, false},
		{"deleted file restored", func() []string {
			p.write("src/components/Badge.orbit", badge)
			return []string{p.Dir + "/src/components/Badge.orbit"} // absolute
		}, false},
		{"renamed import alias", func() []string {
			edit("src/pages/home.orbit", "Card \"../components/Card.orbit\"", "Box \"../components/Card.orbit\"")
			src := strings.ReplaceAll(p.read("src/pages/home.orbit"), "<Card ", "<Box ")
			p.write("src/pages/home.orbit", src)
			return []string{"src/pages/home.orbit"}
		}, false},
		{"renamed file", func() []string {
			rename("src/components/Card.orbit", "src/components/Tile.orbit")
			edit("src/pages/home.orbit", "../components/Card.orbit", "../components/Tile.orbit")
			return []string{"src/components/Card.orbit", "src/components/Tile.orbit", "src/pages/home.orbit"}
		}, false},
		{"renamed directory", func() []string {
			rename("src/components", "src/parts")
			return []string{"src/components", "src/parts"}
		}, false},
		{"importers fixed", func() []string {
			src := strings.ReplaceAll(p.read("src/pages/home.orbit"), "../components/", "../parts/")
			p.write("src/pages/home.orbit", src)
			return []string{"src/pages/home.orbit"}
		}, false},
		{"style gained", func() []string {
			p.write("src/parts/Tile.orbit", p.read("src/parts/Tile.orbit")+"<style>\n  .card { color: red; }\n</style>\n")
			return []string{"src/parts/Tile.orbit"}
		}, false},
		{"style lost", func() []string {
			src := p.read("src/parts/Tile.orbit")
			p.write("src/parts/Tile.orbit", src[:strings.Index(src, "<style>")])
			return []string{"src/parts/Tile.orbit"}
		}, false},
		{"parse error", func() []string {
			edit("src/parts/Tile.orbit", "<h2 class=\"t\">", "<h2 class=\"t\"><b>{")
			return []string{"src/parts/Tile.orbit"}
		}, false},
		{"parse error fixed", func() []string {
			edit("src/parts/Tile.orbit", "<b>{", "")
			return []string{"src/parts/Tile.orbit"}
		}, false},
		{"saved unchanged", func() []string {
			p.write("src/parts/Tile.orbit", p.read("src/parts/Tile.orbit"))
			return []string{"src/parts/Tile.orbit"}
		}, false},
		{"md body", func() []string {
			edit("src/pages/blog/hello.md", "Orbit compiles this file to Go.", "Orbit compiles this file to *Go*.")
			return []string{"src/pages/blog/hello.md"}
		}, false},
		{"md frontmatter", func() []string {
			edit("src/pages/blog/hello.md", "title: Hello, Orbit", "title: Hello again")
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
			p.write("src/pages/blog/second.md", "---\ntitle: Second\nlayout: ../../layouts/Post.orbit\n---\nSee [hello](./hello.md).\n")
			return []string{"src/pages/blog/second.md"}
		}, false},
		{"md newly imported", func() []string {
			p.write("src/content/unused.md", "---\nauthor: Bob\n---\nNow used.\n")
			edit("src/pages/index.orbit", "Intro \"../content/intro.md\"\n", "Intro \"../content/intro.md\"\n    Unused \"../content/unused.md\"\n")
			edit("src/pages/index.orbit", "<Intro />", "<Intro /><Unused />")
			return []string{"src/content/unused.md", "src/pages/index.orbit"}
		}, false},
		{"md no longer imported", func() []string {
			edit("src/pages/index.orbit", "    Unused \"../content/unused.md\"\n", "")
			edit("src/pages/index.orbit", "<Unused />", "")
			return []string{"src/pages/index.orbit"}
		}, false},
		{"layout props changed", func() []string {
			edit("src/layouts/Post.orbit", "    Order    uint8\n", "    Order    uint8\n    Draft    bool\n")
			return []string{"src/layouts/Post.orbit"}
		}, false},
		{"layout deleted", func() []string {
			remove("src/layouts/Post.orbit")
			return []string{"src/layouts/Post.orbit"}
		}, false},
		{"layout restored", func() []string {
			p.write("src/layouts/Post.orbit", mdLayout)
			return []string{"src/layouts/Post.orbit"}
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
		{"locals import error", func() []string {
			p.write("src/locals/locals.go", "package locals\n\nimport _ \"example.com/blog/internal/y\"\n\ntype Locals struct{ User string }\n")
			p.write("internal/y/y.go", "package y\n")
			return []string{"src/locals/locals.go", "internal/y/y.go"}
		}, false},
		{"locals import leads to app", func() []string {
			p.write("internal/y/y.go", "package y\n\nimport _ \"example.com/blog/orbit_gen/app\"\n")
			return []string{"internal/y/y.go"}
		}, true},
		{"orbit.json", func() []string {
			p.write("orbit.json", `{"compressHTML": false}`)
			return []string{"orbit.json"}
		}, true},
		{"server entry added", func() []string {
			p.write("cmd/server/main.go", "package main\n\nfunc main() {}\n")
			return []string{"cmd/server/main.go"}
		}, false},
		{"src directory", func() []string {
			p.write("src/pages/new.orbit", "<p>new</p>\n")
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
