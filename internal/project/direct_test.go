package project

import (
	"bytes"
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/orbit-z/orbit/internal/compiler"
)

// cycleFiles is a project with every shape of PLAN.md §3.5's cycle rule:
// A ↔ B (mutual recursion), a three-component cycle C → D → E → C, ToA
// calling into the A ↔ B cycle from outside, a <Self> tree, a page used as
// a component, and a component used only with server:defer.
var cycleFiles = map[string]string{
	"src/components/A.orbit": `---
import B "./B.orbit"

type Props struct{ N int }
---
<div class="a">{if props.N > 0}<B n={props.N - 1}><i>{props.N}</i></B>{/if}<slot /></div>
`,
	"src/components/B.orbit": `---
import A "./A.orbit"

type Props struct{ N int }
---
<div class="b"><slot />{if props.N > 0}<A n={props.N - 1} />{/if}</div>
`,
	"src/components/C.orbit": `---
import D "./D.orbit"

type Props struct{ N int }
---
<c>{if props.N > 0}<D n={props.N - 1}>{props.N}</D>{/if}<slot /></c>
`,
	"src/components/D.orbit": `---
import E "./E.orbit"

type Props struct{ N int }
---
<d>{if props.N > 0}<E n={props.N - 1}><slot /></E>{/if}</d>
`,
	"src/components/E.orbit": `---
import C "./C.orbit"

type Props struct{ N int }
---
<e>{if props.N > 0}<C n={props.N - 1} />{/if}<slot /></e>
`,
	"src/components/ToA.orbit": `---
import A "./A.orbit"

type Props struct{ N int }
---
<section><A n={props.N}><slot /></A></section>
`,
	"src/components/Tree.orbit": `---
type Props struct{ Depth int }
---
<ul>{if props.Depth > 0}<Self depth={props.Depth - 1}><li>{props.Depth}</li></Self>{/if}<slot /></ul>
`,
	"src/components/Late.orbit": `---
type Props struct{ N int }
---
<p>late {props.N}</p>
`,
	"src/pages/about.orbit": "<p>about</p>\n",
	"src/pages/index.orbit": `---
import (
	ToA "../components/ToA.orbit"
	C "../components/C.orbit"
	Tree "../components/Tree.orbit"
	Late "../components/Late.orbit"
	About "./about.orbit"
)

word := "hi"
---
<main>
<ToA n={3}>{word}</ToA>
<C n={4}>{word}!</C>
<Tree depth={2}><b>{word}</b></Tree>
<Late server:defer n={1} />
<About />
</main>
`,
	"cmd/dump/main.go": `package main

import (
	"fmt"
	"io"
	"net/http/httptest"

	"example.com/blog/orbit_gen/app"
)

func main() {
	h := app.New().Handler()
	for _, path := range []string{"/", "/about"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		b, _ := io.ReadAll(rec.Body)
		fmt.Printf("%s %d\n%s\n", path, rec.Code, b)
	}
}
`,
}

// loadFront loads and fronts the project at dir, failing on diagnostics.
func loadFront(t *testing.T, dir string) *Project {
	t.Helper()
	p, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if p.Front() {
		t.Fatal(joinLines(diagStrings(p.Diagnostics)))
	}
	return p
}

// TestDirectCallsRule is a table test of the SCC rule (PLAN.md §3.5,
// Cycles): which callees each file calls directly.
func TestDirectCallsRule(t *testing.T) {
	p := loadFront(t, newTestProject(t, cycleFiles).Dir)
	type want struct {
		callees []string
		self    bool
	}
	got := map[string]want{}
	for path, d := range p.DirectCalls() {
		got[path] = want{slices.Sorted(maps.Keys(d.Callees)), d.Self}
	}
	wantAll := map[string]want{
		// A ↔ B and C → D → E → C keep their hooks; ToA calls into the
		// A ↔ B cycle directly; index calls ToA, C and Tree directly, but
		// not About (a page) or Late (server:defer only).
		"src/components/ToA.orbit":  {[]string{"src/components/A.orbit"}, false},
		"src/components/Tree.orbit": {nil, true},
		"src/pages/index.orbit":     {[]string{"src/components/C.orbit", "src/components/ToA.orbit", "src/components/Tree.orbit"}, false},
	}
	if !reflect.DeepEqual(got, wantAll) {
		t.Errorf("DirectCalls:\n got %v\nwant %v", got, wantAll)
	}
}

// TestBuildFlavorCycles generates the cycle project, writes its build
// flavor, and checks that go build and go vet pass with -overlay, that
// every route's response is byte-identical between the flavors, that
// BuildFiles is deterministic and sorted, and that it leaves orbit_gen/
// as generate wrote it.
func TestBuildFlavorCycles(t *testing.T) {
	needGo(t)
	tp := newTestProject(t, cycleFiles)
	res := tp.generate()
	if res.HasErrors() {
		t.Fatal(joinLines(diagStrings(res.Diagnostics)))
	}
	files, err := res.Project.BuildFiles()
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, f := range files {
		paths = append(paths, f.Path)
	}
	wantPaths := []string{
		".orbit/build/impl/components/toa/render.go",
		".orbit/build/impl/components/tree/render.go",
		".orbit/build/impl/pages/index/render.go",
		compiler.OverlayFile,
	}
	if !slices.Equal(paths, wantPaths) {
		t.Errorf("BuildFiles: %v, want %v", paths, wantPaths)
	}
	var ov struct{ Replace map[string]string }
	if err := json.Unmarshal(files[len(files)-1].Content, &ov); err != nil {
		t.Fatal(err)
	}
	for _, p := range wantPaths[:3] {
		if gen := "orbit_gen" + strings.TrimPrefix(p, compiler.BuildDir); ov.Replace[gen] != p {
			t.Errorf("overlay maps %s to %q, want %s", gen, ov.Replace[gen], p)
		}
	}
	again, err := res.Project.BuildFiles()
	if err != nil || !reflect.DeepEqual(files, again) {
		t.Errorf("BuildFiles isn't deterministic (%v)", err)
	}
	index := string(files[2].Content)
	for _, want := range []string{"orbit__C.Orbit__RenderImpl", "orbit__ToA.Orbit__RenderImpl", "orbit__Tree.Orbit__RenderImpl", "About.Render", "orbit__rt.Defer("} {
		if !strings.Contains(index, want) {
			t.Errorf("index build flavor lacks %q", want)
		}
	}

	before := snapshot(t, filepath.Join(tp.Dir, "orbit_gen"))
	if err := WriteBuildFiles(tp.Dir, files); err != nil {
		t.Fatal(err)
	}
	if after := snapshot(t, filepath.Join(tp.Dir, "orbit_gen")); !reflect.DeepEqual(before, after) {
		t.Error("WriteBuildFiles changed orbit_gen/")
	}
	ovFlag := "-overlay=" + compiler.OverlayFile
	for _, args := range [][]string{{"build", ovFlag, "./..."}, {"vet", ovFlag, "./..."}} {
		if out, err := tp.goCmd(args...); err != nil || out != "" {
			t.Fatalf("go %v: %v\n%s", args, err, out)
		}
	}
	hook, err := tp.goCmd("run", "./cmd/dump")
	if err != nil {
		t.Fatalf("go run: %v\n%s", err, hook)
	}
	direct, err := tp.goCmd("run", ovFlag, "./cmd/dump")
	if err != nil {
		t.Fatalf("go run -overlay: %v\n%s", err, direct)
	}
	hook, direct = islandRE.ReplaceAllString(hook, "<island>"), islandRE.ReplaceAllString(direct, "<island>")
	if hook != direct {
		t.Errorf("the flavors render differently:\n--- hook\n%s\n--- build\n%s", hook, direct)
	}
	if !strings.Contains(hook, `<section><div class="a"><div class="b"><i>3</i><div class="a"><div class="b"><i>1</i></div></div></div>hi</div></section>`) {
		t.Errorf("unexpected output:\n%s", hook)
	}
}

// islandRE matches what changes between runs of a binary not built by
// orbit build: the key warning, and the island URL and token.
var islandRE = regexp.MustCompile(`(?m)^.*no island key.*$|/_orbit/island/[0-9a-f]+/[^"]*" props="[^"]*"`)

// snapshot reads every file under dir.
func snapshot(t *testing.T, dir string) map[string][]byte {
	t.Helper()
	out := map[string][]byte{}
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(p)
		out[p] = b
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// TestBuildFlavorMarkdown checks Markdown in the build flavor (PLAN.md
// §3.5): an .orbit file calls an imported .md component directly and a
// Markdown page through its hook (a page), a Markdown page's call to its
// layout keeps the hook (no build flavor for .md files), and the
// project's own tests pass with go test -overlay.
func TestBuildFlavorMarkdown(t *testing.T) {
	needGo(t)
	tp := newTestProject(t, mdFiles)
	res := tp.generate()
	if res.HasErrors() {
		t.Fatal(joinLines(diagStrings(res.Diagnostics)))
	}
	files, err := res.Project.BuildFiles()
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 || files[0].Path != ".orbit/build/impl/pages/index/render.go" {
		var paths []string
		for _, f := range files {
			paths = append(paths, f.Path)
		}
		t.Fatalf("BuildFiles: %v, want only the index page and the overlay", paths)
	}
	golden := filepath.Join("testdata", "markdown", "build_impl_pages_index_render.go.golden")
	if *update {
		if err := os.WriteFile(golden, files[0].Content, 0o644); err != nil {
			t.Fatal(err)
		}
	} else if want, err := os.ReadFile(golden); err != nil || !bytes.Equal(files[0].Content, want) {
		t.Errorf("index build flavor differs from %s (go test ./internal/project -update)\n%s", golden, files[0].Content)
	}
	hook := tp.read("orbit_gen/impl/pages/index/render.go")
	if bytes.Count(files[0].Content, []byte("\n")) != strings.Count(hook, "\n") {
		t.Error("the build flavor's line count differs")
	}
	if !bytes.Contains(files[0].Content, []byte("orbit__Intro.Orbit__RenderImpl")) || bytes.Contains(files[0].Content, []byte("orbit__Hello")) {
		t.Errorf("index build flavor doesn't call Intro (only) directly:\n%s", files[0].Content)
	}
	if err := WriteBuildFiles(tp.Dir, files); err != nil {
		t.Fatal(err)
	}
	if out, err := tp.goCmd("test", "-count=1", "-overlay="+compiler.OverlayFile, "./internal/apptest"); err != nil {
		t.Fatalf("go test -overlay: %v\n%s", err, out)
	}
}
