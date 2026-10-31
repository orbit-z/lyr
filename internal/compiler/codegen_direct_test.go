package compiler

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// directCalls computes each file's DirectCalls with PLAN.md §3.5's rule,
// the way internal/project does it: an edge X → Y (a render call site, not
// server:defer, not <Self>, not to a page) is direct unless Y reaches X.
// Files that neither call nor are called directly are left out.
func directCalls(res []*cgResult) map[string]*DirectCalls {
	role := map[string]Role{}
	for _, r := range res {
		role[r.path] = r.a.Info.Role
	}
	edges := map[string][]string{}
	self := map[string]bool{}
	for _, r := range res {
		for _, c := range ComponentCalls(r.f, r.a) {
			switch {
			case c.Defer || c.Target == "":
			case c.Target == r.path:
				self[r.path] = r.a.Info.Role != RolePage
			case role[c.Target] != RolePage:
				edges[r.path] = append(edges[r.path], c.Target)
			}
		}
	}
	reaches := func(from, to string) bool {
		seen := map[string]bool{}
		stack := []string{from}
		for len(stack) > 0 {
			v := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if v == to {
				return true
			}
			if !seen[v] {
				seen[v] = true
				stack = append(stack, edges[v]...)
			}
		}
		return false
	}
	out := map[string]*DirectCalls{}
	for _, r := range res {
		d := &DirectCalls{Callees: map[string]bool{}, Self: self[r.path]}
		for _, to := range edges[r.path] {
			if !reaches(to, r.path) {
				d.Callees[to] = true
			}
		}
		if len(d.Callees) > 0 || d.Self {
			out[r.path] = d
		}
	}
	// A direct callee is in the build flavor too, for its exported name.
	for _, d := range out {
		for to := range d.Callees {
			if out[to] == nil {
				out[to] = &DirectCalls{}
			}
		}
	}
	return out
}

// TestCodegenDirectGolden checks the build flavor (PLAN.md §3.5) of every
// strict codegen case: <pkg>.render.build.go.golden for each file with a
// direct call site, the hook flavor's line count, positions read back
// through its overlay location, and, for cases with a main.go.txt, the same
// HTML as the hook flavor when built with go build -overlay.
func TestCodegenDirectGolden(t *testing.T) {
	dirs, err := filepath.Glob("testdata/codegen/*")
	if err != nil {
		t.Fatal(err)
	}
	for _, dir := range dirs {
		c := loadCase(t, dir)
		if c.opts.Mode == Tolerant {
			continue
		}
		t.Run(filepath.Base(dir), func(t *testing.T) {
			res := generateCase(t, c)
			direct := directCalls(res)
			builds := map[string]*Output{}
			for _, r := range res {
				pkg, _ := PackageFor(r.path)
				name := filepath.Join(dir, strings.ReplaceAll(pkg.Rel, "/", "_")+".render.build.go.golden")
				d := direct[r.path]
				if d == nil {
					if _, err := os.Stat(name); err == nil && !*update {
						t.Errorf("%s: stale golden %s", r.path, name)
					} else if *update {
						os.Remove(name)
					}
					continue
				}
				in := *r.in
				in.Direct = d
				out := Generate(&in)
				builds[r.path] = out
				if len(r.out.Diagnostics) == 0 && len(out.Diagnostics) > 0 {
					t.Errorf("%s: build flavor diagnostics: %v", r.path, out.Diagnostics)
				}
				golden(t, name, string(out.Impl.Content))
				if got, want := bytes.Count(out.Impl.Content, []byte("\n")), bytes.Count(r.out.Impl.Content, []byte("\n")); got != want {
					t.Errorf("%s: build flavor has %d lines, hook flavor %d", r.path, got, want)
				}
				if !bytes.Equal(out.Decl.Content, r.out.Decl.Content) {
					t.Errorf("%s: build flavor changed the decl file", r.path)
				}
				if bytes.Contains(out.Impl.Content, []byte("/*line ../../../../src/")) && !bytes.Contains(out.Impl.Content, []byte("../../../../../src/")) {
					t.Errorf("%s: directives name the lyr_gen location", r.path)
				}
				gf := *out.Impl
				gf.Path = BuildPath(gf.Path)
				checkPositions(t, &cgResult{path: r.path, f: r.f, a: r.a, in: &in, out: out}, &gf)
				again := Generate(&in)
				if !bytes.Equal(again.Impl.Content, out.Impl.Content) {
					t.Errorf("%s: build flavor isn't deterministic", r.path)
				}
			}
			mainSrc, err := os.ReadFile(filepath.Join(dir, "main.go.txt"))
			if err != nil || len(builds) == 0 {
				return
			}
			want, err := os.ReadFile(filepath.Join(dir, "want.html"))
			if err != nil {
				if *update {
					return // TestCodegenGolden writes it
				}
				t.Fatal(err)
			}
			t.Parallel()
			m := newGenModule(t)
			for _, r := range res {
				m.writeOutput(r.out)
			}
			m.writeOverlay(builds)
			for _, args := range [][]string{{"build", "-overlay=" + OverlayFile, "./..."}, {"vet", "-overlay=" + OverlayFile, "./..."}} {
				if out, err := m.goCmd(args...); err != nil || strings.TrimSpace(out) != "" {
					t.Fatalf("go %s: %v\n%s", strings.Join(args, " "), err, out)
				}
			}
			m.write("cmd/render/main.go", string(mainSrc))
			out, err := m.goCmd("run", "-overlay="+OverlayFile, "./cmd/render")
			if err != nil {
				t.Fatalf("go run: %v\n%s", err, out)
			}
			if got := normalizeIslands(out); got != string(want) {
				t.Errorf("build flavor renders differently:\n--- got\n%s\n--- want\n%s", got, want)
			}
		})
	}
}

// writeOverlay writes the build-flavor impl files under BuildDir and the
// overlay file mapping them, as lyr build does.
func (m *genModule) writeOverlay(builds map[string]*Output) {
	m.t.Helper()
	replace := map[string]string{}
	for _, out := range builds {
		m.write(BuildPath(out.Impl.Path), string(out.Impl.Content))
		replace[out.Impl.Path] = BuildPath(out.Impl.Path)
	}
	b, err := json.Marshal(map[string]any{"Replace": replace})
	if err != nil {
		m.t.Fatal(err)
	}
	m.write(OverlayFile, string(b))
}

// TestDirectCallShapes pins the build flavor's call shapes in the direct
// case: impl imports on the runtime's line, the direct calls, and the
// sites that keep the hook.
func TestDirectCallShapes(t *testing.T) {
	c := loadCase(t, "testdata/codegen/direct")
	res := generateCase(t, c)
	direct := directCalls(res)
	gen := func(p string) string {
		for _, r := range res {
			if r.path == p {
				in := *r.in
				in.Direct = direct[p]
				return string(Generate(&in).Impl.Content)
			}
		}
		t.Fatalf("no file %s", p)
		return ""
	}
	if d := direct["src/components/Card.lyr"]; d == nil || len(d.Callees) > 0 || d.Self {
		t.Errorf("Card, a direct callee that calls nothing, has %+v", d)
	}
	if _, ok := direct["src/pages/about.lyr"]; ok {
		t.Error("about, a page callee, has a build flavor")
	}
	if card := gen("src/components/Card.lyr"); !strings.Contains(card, "Lyr__Render = Lyr__RenderImpl;") || !strings.Contains(card, "\nfunc Lyr__RenderImpl(") {
		t.Errorf("Card's build flavor doesn't export its render function:\n%s", card)
	}
	index := gen("src/pages/index.lyr")
	for _, want := range []string{
		`lyr__rt "github.com/orbit-z/lyr"; lyr__Card "example.com/blog/lyr_gen/impl/components/card"; lyr__Frame "example.com/blog/lyr_gen/impl/components/frame"; lyr__Tree "example.com/blog/lyr_gen/impl/components/tree"` + "\n",
		"*/lyr__Card.Lyr__RenderImpl/*line",
		"*/lyr__Frame.Lyr__RenderImpl/*line",
		"*/About.Render/*line",                  // a page callee keeps the hook
		`lyr__rt.Defer(lyr__ctx, lyr__w, "Box-`, // server:defer: no render call
		"/*line ../../../../../src/pages/index.lyr:",
	} {
		if !strings.Contains(index, want) {
			t.Errorf("index build flavor lacks %q", want)
		}
	}
	if strings.Contains(index, "lyr__Box") || strings.Contains(index, "lyr__About") {
		t.Error("index imports the impl of a callee it doesn't call directly")
	}
	tree := gen("src/components/Tree.lyr")
	if !strings.Contains(tree, "*/Lyr__RenderImpl/*line") || strings.Contains(tree, "*/Render/*line") {
		t.Errorf("<Self> isn't a direct self-call:\n%s", tree)
	}
	// Without DirectCalls, the hook flavor.
	for _, r := range res {
		in := *r.in
		in.Direct = nil
		if got := Generate(&in).Impl.Content; !bytes.Equal(got, r.out.Impl.Content) {
			t.Errorf("%s: hook flavor differs", r.path)
		}
	}
	for _, r := range res {
		if d := direct[r.path]; d != nil {
			keys := make([]string, 0, len(d.Callees))
			for k := range d.Callees {
				keys = append(keys, k)
			}
			slices.Sort(keys)
			t.Logf("%s → %v self=%v", r.path, keys, d.Self)
		}
	}
}

// allocTestSrc renders the direct case's allocs page (capturing default and
// named slots in a loop, slot forwarding through Frame, a <Self> tree with
// capturing content) and prints its allocations per render.
const allocTestSrc = `package alloc

import (
	"bytes"
	"testing"

	allocs "example.com/blog/lyr_gen/decl/pages/allocs"
	_ "example.com/blog/lyr_gen/impl/components/card"
	_ "example.com/blog/lyr_gen/impl/components/frame"
	_ "example.com/blog/lyr_gen/impl/components/tree"
	_ "example.com/blog/lyr_gen/impl/pages/allocs"
	"github.com/orbit-z/lyr"
)

func TestAllocs(t *testing.T) {
	var buf bytes.Buffer
	w := lyr.NewWriter(&buf)
	ctx := &lyr.Ctx{}
	n := testing.AllocsPerRun(100, func() {
		buf.Reset()
		if err := allocs.Render(ctx, w, allocs.Props{}, lyr.Slots{}); err != nil {
			t.Fatal(err)
		}
	})
	t.Logf("allocs=%v", n)
}
`

// TestDirectAllocs checks the point of the build flavor (PLAN.md §3.5):
// the allocs page renders with 0 allocations under go test -overlay, and
// with one per capturing slot closure (and named-slot list) through the
// hooks, so the test notices if the hook flavor stops allocating.
func TestDirectAllocs(t *testing.T) {
	c := loadCase(t, "testdata/codegen/direct")
	m := newGenModule(t)
	p := plPipeline(t, m, plReadCase(t, c), c.opts)
	var res []*cgResult
	for _, pf := range p.files {
		if !pf.typed.Checked {
			t.Fatalf("%s: not type-checked", pf.path)
		}
		res = append(res, &cgResult{path: pf.path, f: pf.f, a: pf.a, in: pf.in})
	}
	direct := directCalls(res)
	builds := map[string]*Output{}
	for _, pf := range p.files {
		m.writeOutput(pf.typed.Output)
		if d := direct[pf.path]; d != nil {
			in := *pf.in
			in.Typed, in.Direct = pf.typed.Writes, d
			builds[pf.path] = Generate(&in)
		}
	}
	m.writeOverlay(builds)
	m.write("alloc/alloc_test.go", allocTestSrc)
	allocs := func(args ...string) string {
		out, err := m.goCmd(append(append([]string{"test", "-count=1", "-v"}, args...), "./alloc")...)
		if err != nil {
			t.Fatalf("go test %v: %v\n%s", args, err, out)
		}
		_, n, ok := strings.Cut(out, "allocs=")
		if !ok {
			t.Fatalf("no allocs= in\n%s", out)
		}
		n, _, _ = strings.Cut(n, "\n")
		return n
	}
	if got := allocs("-overlay=" + OverlayFile); got != "0" {
		t.Errorf("build flavor: %s allocations per render, want 0", got)
	}
	// Through the hooks: per iteration (2), Card's default and named
	// closures and its NamedSlot list, Frame's default closure and Frame's
	// own closure forwarding it to Card (10); the page's Tree content and
	// Tree's <Self> content at its 2 recursive levels (3).
	const hookAllocs = "13"
	if got := allocs(); got != hookAllocs {
		t.Errorf("hook flavor: %s allocations per render, want %s", got, hookAllocs)
	}
}

// TestLineDirectivePositionsBuildFlavor builds the position project of
// TestLineDirectivePositions (PLAN.md §3.3) in both flavors: with the build
// flavor overlaid (its pages call Emb directly, and their errors are in a
// hoisted declaration and a direct call's field key), go build and go vet
// must report every error at the same .lyr position as the hook build,
// with the same cleaned output.
func TestLineDirectivePositionsBuildFlavor(t *testing.T) {
	c := loadCase(t, "testdata/positions")
	m := newGenModule(t)
	p := plPipeline(t, m, plReadCase(t, c), c.opts)
	var res []*cgResult
	var analyses []*Analysis
	for _, pf := range p.files {
		res = append(res, &cgResult{path: pf.path, f: pf.f, a: pf.a, in: pf.in})
		analyses = append(analyses, pf.a)
	}
	direct := directCalls(res)
	builds := map[string]*Output{}
	for _, pf := range p.files {
		if d := direct[pf.path]; d != nil {
			in := *pf.in
			in.Typed, in.Direct = pf.typed.Writes, d
			builds[pf.path] = Generate(&in)
		}
	}
	for _, want := range []string{"src/pages/index.lyr", "src/pages/alias.lyr"} {
		if builds[want] == nil {
			t.Fatalf("%s has no build flavor", want)
		}
	}
	p.write(m, true)
	m.writeOverlay(builds)
	names := NewErrorNames(testModule, analyses)
	clean := func(args ...string) map[string]string {
		out := map[string]string{}
		for _, cmd := range []string{"build", "vet"} {
			o, err := m.goCmd(append(append([]string{cmd}, args...), "./...")...)
			if err == nil {
				t.Fatalf("go %s %v succeeded, want errors", cmd, args)
			}
			out[cmd] = sortPackages(string(CleanGoOutput([]byte(o), names)))
		}
		return out
	}
	hook, overlay := clean(), clean("-overlay="+OverlayFile)
	for _, cmd := range []string{"build", "vet"} {
		if hook[cmd] != overlay[cmd] {
			t.Errorf("go %s: the build flavor's errors differ\n--- hook\n%s--- build flavor\n%s", cmd, hook[cmd], overlay[cmd])
		}
	}
	for _, line := range []string{
		"src/pages/index.lyr:5:8: unknown field Nmae in struct literal of type Emb.Props",
		"src/pages/alias.lyr:4:13: cannot use Emb.Props{} (value of struct type Emb.Props) as int value in variable declaration",
	} {
		if !hasLine(overlay["build"], line) {
			t.Errorf("build flavor lacks %q:\n%s", line, overlay["build"])
		}
	}
}
