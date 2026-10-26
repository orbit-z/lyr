package compiler

import (
	"os"
	"strings"
	"testing"
)

// TestRestTyped: the typed pass picks the typed constructor of each
// dynamic pass-through attribute (PLAN.md §2.5), keeps RestAny for the
// rest, and reports a class={…} of a wrong type merged with pass-through
// attributes. The rest case's typed index page is pinned by a golden.
func TestRestTyped(t *testing.T) {
	const page = `---
import Button "../components/Button.orbit"

type Slug string

s, n, u, f, f32, b := "a", 1, uint8(2), 0.5, float32(1), true
var slug Slug = "x"
var v any = "y"
---
<Button data-s={s} href={s} data-n={n} data-u={u} data-f={f} data-f32={f32} data-b={b}
	data-slug={slug} src={orbit.URL(s)} data-j={orbit.JSON(n)} data-v={v} data-nil={nil} />
<Button class={n} />
`
	const button = `---
type Props struct{ Attrs orbit.RestAttrs }

n := 1
---
<button {...props.Attrs} />
<p class={n} {...props.Attrs}>x</p>
`
	p := plFront(map[string]string{"src/pages/index.orbit": page, "src/components/Button.orbit": button}, Options{Module: testModule})
	if ds := p.diagnostics(); len(ds) > 0 {
		t.Fatalf("front diagnostics:\n%s", strings.Join(ds, "\n"))
	}
	p.typedPass(t, &tyTestLoader{})
	var got []string
	kinds := map[string]TypedWrite{}
	for _, pf := range p.files {
		if !pf.typed.Checked {
			t.Fatalf("%s not checked (warnings %v)", pf.path, p.warnings)
		}
		for _, d := range pf.typed.Diagnostics {
			got = append(got, d.Format(pf.path, pf.f.Lines))
		}
		if pf.path != "src/pages/index.orbit" {
			continue
		}
		for _, cs := range pf.pass1.CallSites {
			if cs.Kind == SiteRest {
				kinds[cs.AttrName] = pf.typed.Writes[cs.Key]
			}
		}
	}
	want := []string{
		"src/components/Button.orbit:7:11: class={…} merged with pass-through attributes must be a string or []string, not int",
		"src/pages/index.orbit:12:16: class={…} merged with pass-through attributes must be a string or []string, not int",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("got:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	for name, w := range map[string]TypedWrite{
		"data-s":    {Kind: WriteRestString},
		"href":      {Kind: WriteRestStringURL},
		"data-n":    {Kind: WriteRestInt, Conv: "int64"},
		"data-u":    {Kind: WriteRestUint, Conv: "uint64"},
		"data-f":    {Kind: WriteRestFloat},
		"data-f32":  {Kind: WriteRestFloat32},
		"data-b":    {Kind: WriteRestBool},
		"data-slug": {Kind: WriteRestString, Conv: "string"},
		"src":       {Kind: WriteRestURL},
		"data-j":    {Kind: WriteRestJSON},
		"data-v":    {},
		"data-nil":  {},
	} {
		if g := kinds[name]; g != w {
			t.Errorf("%s: %+v, want %+v", name, g, w)
		}
	}
}

// TestRestTypedGolden pins the typed pass-2 output of the rest case's
// index page (every dynamic kind), next to its pass-1 golden.
func TestRestTypedGolden(t *testing.T) {
	c := loadCase(t, "testdata/codegen/rest")
	m := newGenModule(t)
	p := plPipeline(t, m, plReadCase(t, c), c.opts)
	for _, pf := range p.files {
		if pf.path == "src/pages/index.orbit" {
			golden(t, "testdata/codegen/rest/pages_index.render.go.typed.golden", string(pf.typed.Output.Impl.Content))
		}
	}
}

// TestRestErrors: the call-site errors of pass-through attributes, each at
// its .orbit position.
func TestRestErrors(t *testing.T) {
	const page = `---
import Button "../components/Button.orbit"
import Card "../components/Card.orbit"

attrs := orbit.Attrs{}
---
<Button name="a" data-x="1" NAME="b" />
<Button attrs={orbit.RestOf(attrs)} name="a" />
<Button attrs={orbit.RestOf(attrs)} {...attrs} />
<Card {...attrs} />
<Card name="a" />
<Button class="a" class="b" />
`
	p := plFront(map[string]string{
		"src/pages/index.orbit":       page,
		"src/components/Button.orbit": "---\ntype Props struct{ Attrs orbit.RestAttrs }\n---\n<button {...props.Attrs} />\n",
		"src/components/Card.orbit":   "---\ntype Props struct{ Title string }\n---\n<p>{props.Title}</p>\n",
	}, Options{Module: testModule})
	want := []string{
		"src/pages/index.orbit:7:29: attribute NAME is passed twice to <Button>",
		"src/pages/index.orbit:8:2: <Button> gets attrs={…} and pass-through attributes; use {...x} to merge them",
		"src/pages/index.orbit:9:2: <Button> gets attrs={…} and pass-through attributes; use {...x} to merge them",
		"src/pages/index.orbit:10:7: {...} spread on <Card> needs an orbit.RestAttrs field in its Props, to pass HTML attributes through",
		`src/pages/index.orbit:11:7: unknown prop "name" on <Card> (props: title; add an orbit.RestAttrs field to pass HTML attributes through)`,
		"src/pages/index.orbit:12:19: attribute class is passed twice to <Button>",
	}
	if got := p.diagnostics(); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("got:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// restAllocTestSrc renders the rest case's allocs page (static and dynamic
// pass-through attributes, forwarding through Wrap and the scoped Field)
// and prints its allocations per render, and its time with -bench.
const restAllocTestSrc = `package alloc

import (
	"bytes"
	"testing"

	_ "example.com/blog/orbit_gen/impl/components/button"
	_ "example.com/blog/orbit_gen/impl/components/field"
	_ "example.com/blog/orbit_gen/impl/components/input"
	_ "example.com/blog/orbit_gen/impl/components/wrap"
	allocs "example.com/blog/orbit_gen/decl/pages/allocs"
	_ "example.com/blog/orbit_gen/impl/pages/allocs"
	"github.com/orbit-z/orbit"
)

func TestAllocs(t *testing.T) {
	var buf bytes.Buffer
	w := orbit.NewWriter(&buf)
	ctx := &orbit.Ctx{}
	n := testing.AllocsPerRun(100, func() {
		buf.Reset()
		if err := allocs.Render(ctx, w, allocs.Props{}, orbit.Slots{}); err != nil {
			t.Fatal(err)
		}
	})
	t.Logf("allocs=%v", n)
}

func BenchmarkRender(b *testing.B) {
	var buf bytes.Buffer
	w := orbit.NewWriter(&buf)
	ctx := &orbit.Ctx{}
	b.ReportAllocs()
	for b.Loop() {
		buf.Reset()
		allocs.Render(ctx, w, allocs.Props{}, orbit.Slots{})
	}
}
`

// TestRestDirectAllocs checks PLAN.md §2.5's allocation rule: the rest
// case's allocs page renders with 0 allocations in the build flavor (go
// test -overlay), and through the hooks with exactly one, for the call
// site with dynamic attributes (its slice of them escapes through the
// hook); all-static call sites and forwarding cost nothing.
func TestRestDirectAllocs(t *testing.T) {
	c := loadCase(t, "testdata/codegen/rest")
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
	m.write("alloc/alloc_test.go", restAllocTestSrc)
	allocs := func(args ...string) string {
		out, err := m.goCmd(append(append([]string{"test", "-count=1", "-v"}, args...), "./alloc")...)
		if err != nil {
			t.Fatalf("go test %v: %v\n%s", args, err, out)
		}
		if testing.Verbose() {
			t.Log(out)
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
	if got := allocs(); got != "1" {
		t.Errorf("hook flavor: %s allocations per render, want 1", got)
	}
	if os.Getenv("ORBIT_REST_BENCH") != "" { // the numbers in PLAN.md §2.5
		for _, args := range [][]string{{"-overlay=" + OverlayFile}, nil} {
			out, err := m.goCmd(append(append([]string{"test", "-run=^$", "-bench=.", "-benchtime=200000x", "-count=5"}, args...), "./alloc")...)
			if err != nil {
				t.Fatal(err, out)
			}
			t.Logf("%v:\n%s", args, out)
		}
	}
}
