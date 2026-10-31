package compiler

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The propspread case (PLAN.md §2.11): spreading props into a component.
// Its pass-1 goldens are TestCodegenGolden's; its typed output can't be
// compared with pass 1 (which renders a struct spread as an error), so it
// has no main.go.txt but a main.typed.go.txt and want.typed.html, and the
// user package under user/ that its components import.

const spreadCase = "testdata/codegen/propspread"

// spreadModule writes the case's user package into m.
func spreadModule(t *testing.T, m *genModule) {
	t.Helper()
	root := filepath.Join(spreadCase, "user")
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		m.write(filepath.ToSlash(rel), string(b))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// spreadPipeline runs the case through the real pipeline and returns the
// project, the build flavor of each direct file (typed) and the module.
func spreadPipeline(t *testing.T) (*plProject, map[string]*Output, *genModule) {
	t.Helper()
	c := loadCase(t, spreadCase)
	m := newGenModule(t)
	spreadModule(t, m)
	p := plPipeline(t, m, plReadCase(t, c), c.opts)
	if d := p.diagnostics(); len(d) > 0 {
		t.Fatalf("diagnostics:\n%s", strings.Join(d, "\n"))
	}
	var res []*cgResult
	for _, pf := range p.files {
		if !pf.typed.Checked {
			t.Fatalf("%s: not type-checked (warnings %v)", pf.path, p.warnings)
		}
		for _, d := range pf.typed.Diagnostics {
			t.Errorf("%s", d.Format(pf.path, pf.f.Lines))
		}
		res = append(res, &cgResult{path: pf.path, f: pf.f, a: pf.a, in: pf.in})
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
	return p, builds, m
}

// TestSpreadGolden pins the typed output of the propspread case (both
// flavors), checks its map invariants, and renders it in both flavors.
func TestSpreadGolden(t *testing.T) {
	p, builds, m := spreadPipeline(t)
	spreads := 0
	for _, pf := range p.files {
		pkg, _ := PackageFor(pf.path)
		base := filepath.Join(spreadCase, strings.ReplaceAll(pkg.Rel, "/", "_"))
		out := pf.typed.Output
		golden(t, base+".render.go.typed.golden", string(out.Impl.Content))
		in := *pf.in
		in.Typed = pf.typed.Writes
		checkOutput(t, &cgResult{path: pf.path, f: pf.f, a: pf.a, in: &in, out: out})
		keys := map[int]bool{}
		for _, cs := range pf.pass1.CallSites {
			keys[cs.Key] = true
		}
		if len(out.CallSites) != len(pf.pass1.CallSites) {
			t.Errorf("%s: %d call sites in pass 2, %d in pass 1", pf.path, len(out.CallSites), len(pf.pass1.CallSites))
		}
		for _, cs := range out.CallSites {
			if !keys[cs.Key] {
				t.Errorf("%s: pass 2 site %d isn't in pass 1", pf.path, cs.Key)
			}
		}
		for _, w := range pf.typed.Writes {
			if w.Kind == WriteSpreadProps {
				spreads++
			}
		}
		if b := builds[pf.path]; b != nil {
			golden(t, base+".render.build.go.typed.golden", string(b.Impl.Content))
			if got, want := bytes.Count(b.Impl.Content, []byte("\n")), bytes.Count(out.Impl.Content, []byte("\n")); got != want {
				t.Errorf("%s: build flavor has %d lines, hook flavor %d", pf.path, got, want)
			}
			gf := *b.Impl
			gf.Path = BuildPath(gf.Path)
			bin := in
			bin.Direct = &DirectCalls{}
			checkPositions(t, &cgResult{path: pf.path, f: pf.f, a: pf.a, in: &bin, out: b}, &gf)
		}
	}
	if spreads < 10 {
		t.Errorf("%d props spreads typed, want every one of the case's", spreads)
	}
	mainSrc, err := os.ReadFile(filepath.Join(spreadCase, "main.typed.go.txt"))
	if err != nil {
		t.Fatal(err)
	}
	p.write(m, true)
	m.buildAndVet()
	got := m.run(string(mainSrc))
	wantPath := filepath.Join(spreadCase, "want.typed.html")
	if *update {
		if err := os.WriteFile(wantPath, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(wantPath)
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Errorf("rendered HTML differs:\n--- got\n%s\n--- want\n%s", got, want)
	}
	m.writeOverlay(builds)
	for _, args := range [][]string{{"build", "-overlay=" + OverlayFile, "./..."}, {"vet", "-overlay=" + OverlayFile, "./..."}} {
		if out, err := m.goCmd(args...); err != nil || strings.TrimSpace(out) != "" {
			t.Fatalf("go %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	out, err := m.goCmd("run", "-overlay="+OverlayFile, "./cmd/render")
	if err != nil {
		t.Fatalf("go run: %v\n%s", err, out)
	}
	if got := normalizeIslands(out); got != string(want) {
		t.Errorf("build flavor renders differently:\n--- got\n%s\n--- want\n%s", got, want)
	}
}

// TestSpreadAllocs: the allocs page (each spread form in a {for}: a helper
// result, the callee's own Props plus overrides, Opt widening, a nil and a
// set pointer next to an embedded struct, a call site without spreads)
// renders with 0 allocations in both flavors (PLAN.md §2.11 Cost).
func TestSpreadAllocs(t *testing.T) {
	p, builds, m := spreadPipeline(t)
	p.write(m, true)
	m.writeOverlay(builds)
	m.write("alloc/alloc_test.go", strings.Replace(condAllocTestSrc, `_ "example.com/blog/lyr_gen/impl/pages/allocs"`,
		`_ "example.com/blog/lyr_gen/impl/pages/allocs"
	_ "example.com/blog/lyr_gen/impl/components/actions"
	_ "example.com/blog/lyr_gen/impl/components/cached"
	_ "example.com/blog/lyr_gen/impl/components/card"
	_ "example.com/blog/lyr_gen/impl/components/tree"`, 1))
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
	if got := allocs(); got != "0" {
		t.Errorf("hook flavor: %s allocations per render, want 0", got)
	}
}

// TestSpreadPass1: pass 1's output type-checks with struct and attribute
// spreads, and when it's served (no typed pass), a struct spread is a
// positioned render error while an attribute spread renders as before.
func TestSpreadPass1(t *testing.T) {
	srcs := map[string]string{
		"src/components/Card.lyr": "---\ntype Props struct{ Title string }\n---\n<p>{props.Title}</p>\n",
		"src/components/Box.lyr":  "---\ntype Props struct{ Attrs lyr.RestAttrs }\n---\n<div {...props.Attrs}></div>\n",
		"src/pages/index.lyr":     "---\nimport Card \"../components/Card.lyr\"\nimport Box \"../components/Box.lyr\"\n\ntype T struct{ Title string }\n\nfail := false\n---\n<Box {...lyr.Attrs{\"id\": \"x\"}} />\n{if fail}<Card {...T{\"t\"}} />{/if}\n{if !fail}<Box {...T{\"t\"}} />{/if}\n",
	}
	m := newGenModule(t)
	p := plPipeline(t, m, srcs, Options{Module: testModule})
	if d := p.diagnostics(); len(d) > 0 {
		t.Fatalf("diagnostics:\n%s", strings.Join(d, "\n"))
	}
	for _, pf := range p.files {
		if !pf.typed.Checked {
			t.Errorf("%s: pass 1 doesn't type-check", pf.path)
		}
	}
	p.write(m, false)
	m.buildAndVet()
	m.write("cmd/render/main.go", `package main

import (
	"bytes"
	"fmt"

	_ "example.com/blog/lyr_gen/impl/components/box"
	_ "example.com/blog/lyr_gen/impl/components/card"
	index "example.com/blog/lyr_gen/decl/pages/index"
	_ "example.com/blog/lyr_gen/impl/pages/index"
	"github.com/orbit-z/lyr"
)

func main() {
	var buf bytes.Buffer
	err := index.Render(&lyr.Ctx{}, lyr.NewWriter(&buf), index.Props{}, lyr.Slots{})
	fmt.Printf("%s\n%v\n", buf.String(), err)
}
`)
	out, err := m.goCmd("run", "./cmd/render")
	if err != nil {
		t.Fatalf("go run: %v\n%s", err, out)
	}
	want := "<div id=\"x\"></div>\nsrc/pages/index.lyr:11:16: {...T{\"t\"}} on <Box> sets props, which needs Lyr's type check; it was skipped (see the warning above)\n"
	if out != want {
		t.Errorf("got:\n%s\nwant:\n%s", out, want)
	}
}

// TestSpreadErrors: the typed pass's spread errors, each at its .lyr
// position (PLAN.md §2.11 Errors).
func TestSpreadErrors(t *testing.T) {
	m := newGenModule(t)
	m.write("internal/cards/cards.go", `package cards

type Summary struct {
	Title       string
	Blurb, Rank string
}

type Wrong struct{ Lines int }

type OptT struct{ Note string }

type Inner struct{ Lines int }

type Deep struct{ *Inner }
`)
	srcs := map[string]string{
		"src/components/Card.lyr": "---\ntype Props struct {\n\tTitle string\n\tNote  lyr.Opt[string]\n\tLines lyr.Opt[string]\n\tPlain string\n}\n---\n<p>{props.Title}</p>\n",
		"src/components/Ptr.lyr":  "---\nimport \"example.com/blog/internal/cards\"\n\ntype Props struct{ *cards.Inner }\n---\n<p></p>\n",
		"src/components/Box.lyr":  "---\ntype Props struct{ Title string }\n---\n<p></p>\n",
		"src/pages/index.lyr": "---\nimport Card \"../components/Card.lyr\"\nimport Ptr \"../components/Ptr.lyr\"\nimport Box \"../components/Box.lyr\"\nimport \"example.com/blog/internal/cards\"\n\n" +
			"type O struct{ Plain lyr.Opt[string] }\n\nn := 3\nvar s cards.Summary\nvar w cards.Wrong\nvar o O\nvar a, b struct{ Title string }\nattrs := lyr.Attrs{}\nvar d cards.Deep\n---\n" +
			"<Card {...s} />\n" + // 17: extra fields
			"<Card {...w} />\n" + // 18: type mismatch
			"<Card {...o} />\n" + // 19: Opt[T] into T
			"<Card title=\"x\" {...a} />\n" + // 20: attribute before a spread
			"<Card {...a} {...b} />\n" + // 21: two spreads
			"<Card {...n} />\n" + // 22: not a struct
			"<Box {...attrs} />\n" + // 23: attribute type, no RestAttrs
			"<Ptr {...cards.Inner{}} />\n" + // 24: promoted through an embedded pointer
			"<Box {...d} />\n" + // 25: read through an embedded pointer
			"<Card {...a} title=\"ok\" />\n", // 26: fine
	}
	p := plFront(srcs, Options{Module: testModule})
	var got []string
	for _, d := range p.diagnostics() {
		if strings.HasPrefix(d, "src/pages/") {
			got = append(got, d)
		}
	}
	p.typedPass(t, tyGoListLoader(m))
	for _, pf := range p.files {
		for _, d := range pf.typed.Diagnostics {
			got = append(got, d.Format(pf.path, pf.f.Lines))
		}
	}
	want := []string{
		"src/pages/index.lyr:17:7: {...s} on <Card>: cards.Summary has fields that aren't props of Card (Blurb, Rank); spread a struct whose fields are all props, or pass them one by one (props: title, note, lines, plain)",
		"src/pages/index.lyr:18:7: {...w} sets Lines of <Card>: w.Lines is int, the prop is lyr.Opt[string]",
		"src/pages/index.lyr:19:7: {...o} sets Plain of <Card>: o.Plain is lyr.Opt[string], the prop is string",
		"src/pages/index.lyr:22:7: {...n} on <Card>: n is int; spread a struct (its fields set props) or a lyr.Attrs, lyr.AttrList or lyr.RestAttrs (pass-through attributes)",
		"src/pages/index.lyr:23:6: {...} spread on <Box> needs a lyr.RestAttrs field in its Props, to pass HTML attributes through",
		"src/pages/index.lyr:24:6: {...cards.Inner{}} can't set Lines of <Ptr>: it's promoted through the embedded pointer *cards.Inner",
		"src/pages/index.lyr:25:6: {...d} can't read Lines: it's promoted through the embedded pointer *cards.Inner; pass it as an attribute",
		"src/pages/index.lyr:20:7: title=\"…\" is overwritten by {...a} after it (it sets Title); move it after the spread to override",
		"src/pages/index.lyr:21:14: {...a} and {...b} both set Title of <Card>; a spread can't override another",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("got:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}
