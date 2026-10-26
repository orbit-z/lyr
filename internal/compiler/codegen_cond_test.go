package compiler

import (
	"strings"
	"testing"
)

// TestCondTypedGolden pins the typed pass-2 output of the cond case
// (PLAN.md §2.6): orbit.If lowered on HTML attributes and in content,
// constants folded, and left as a call in larger expressions, spreads,
// component props and pass-through attributes, and for a user's own If or a
// shadowed orbit. The byte-identity with the fallback is
// TestPipelineTypedVsFallback's.
func TestCondTypedGolden(t *testing.T) {
	c := loadCase(t, "testdata/codegen/cond")
	m := newGenModule(t)
	p := plPipeline(t, m, plReadCase(t, c), c.opts)
	lowered := map[string]string{} // code → "folded", "lowered"
	for _, pf := range p.files {
		if !pf.typed.Checked {
			t.Fatalf("%s: not type-checked", pf.path)
		}
		switch pf.path {
		case "src/components/Cond.orbit", "src/pages/allocs.orbit":
			pkg, _ := PackageFor(pf.path)
			golden(t, "testdata/codegen/cond/"+strings.ReplaceAll(pkg.Rel, "/", "_")+".render.go.typed.golden", string(pf.typed.Output.Impl.Content))
		}
		for _, cs := range pf.pass1.CallSites {
			w := pf.typed.Writes[cs.Key]
			switch {
			case w.If != nil && w.If.Folded:
				lowered[cs.Code.Text] = "folded"
			case w.If != nil:
				lowered[cs.Code.Text] = "lowered"
			default:
				if _, ok := lowered[cs.Code.Text]; !ok {
					lowered[cs.Code.Text] = ""
				}
			}
		}
	}
	for code, want := range map[string]string{
		`orbit.If(props.On, "page")`:                                 "folded",
		`orbit.If(props.On, props.Rel)`:                              "lowered",
		`orbit.If(props.On, -1)`:                                     "folded",
		`orbit.If(props.On, props.F)`:                                "lowered",
		`orbit.If(props.On, " JavaScript:alert(1)")`:                 "folded",
		`orbit.If(props.On, orbit.URL("javascript:ok()"))`:           "folded",
		`orbit.If(props.On, orbit.JSON(data))`:                       "lowered",
		`orbit.If(props.On, anyv)`:                                   "lowered",
		`orbit.If(props.On, true)`:                                   "folded",
		`orbit.If[any](props.On, 3)`:                                 "lowered",
		`(orbit.If(props.On, orbit.HTML("<b>h</b>")))`:               "folded",
		`orbit.If(note("c1", true), val("v1"))`:                      "lowered",
		`fmt.Sprint(orbit.If(props.On, "a"))`:                        "",
		`[]any{orbit.If(props.On, "x"), "y"}`:                        "",
		`orbit.Cond(props.On, "yes", "no")`:                          "",
		`If(true, "x")`:                                              "",
		`orbit.If(true, "z")`:                                        "",
		`orbit.If(on, "page")`:                                       "", // a pass-through attribute
		`orbit.If(it.Href == current, "page")`:                       "folded",
		`orbit.If(!it.Ext, -1)`:                                      "folded",
		`orbit.If(true, saved)`:                                      "lowered",
		`orbit.Cond(saved, "Remove from library", "Add to library")`: "",
	} {
		got, ok := lowered[code]
		if !ok {
			t.Errorf("no call site %s", code)
			continue
		}
		if got != want {
			t.Errorf("%s: %q, want %q", code, got, want)
		}
	}
}

// condAllocTestSrc renders the cond case's allocs page (orbit.If and
// orbit.Cond on attributes and in content) and prints its allocations per
// render, and its time with -bench.
const condAllocTestSrc = `package alloc

import (
	"bytes"
	"testing"

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
`

// TestCondAllocs checks PLAN.md §2.6's allocation rule: a page using both
// helpers renders with 0 allocations in Orbit's render path.
func TestCondAllocs(t *testing.T) {
	c := loadCase(t, "testdata/codegen/cond")
	m := newGenModule(t)
	p := plPipeline(t, m, plReadCase(t, c), c.opts)
	p.write(m, true)
	m.write("alloc/alloc_test.go", condAllocTestSrc)
	out, err := m.goCmd("test", "-count=1", "-v", "./alloc")
	if err != nil {
		t.Fatalf("go test: %v\n%s", err, out)
	}
	_, n, ok := strings.Cut(out, "allocs=")
	if !ok {
		t.Fatalf("no allocs= in\n%s", out)
	}
	if n, _, _ = strings.Cut(n, "\n"); n != "0" {
		t.Errorf("%s allocations per render, want 0", n)
	}
}

// TestIfPanicSame: an orbit.If argument that panics (the eager-evaluation
// trap, PLAN.md §2.6) panics the same way lowered and not, after the same
// output.
func TestIfPanicSame(t *testing.T) {
	srcs := map[string]string{
		"src/components/P.orbit": "---\ntype user struct{ Email string }\n\nvar viewer *user\n---\n" +
			"<p>before</p><a title={orbit.If(viewer != nil, viewer.Email)}>x</a>{orbit.If(false, viewer.Email)}\n",
	}
	const mainSrc = `package main

import (
	"bytes"
	"fmt"

	p "example.com/blog/orbit_gen/decl/components/p"
	_ "example.com/blog/orbit_gen/impl/components/p"
	"github.com/orbit-z/orbit"
)

func main() {
	var buf bytes.Buffer
	defer func() { fmt.Printf("%s|%v", buf.String(), recover()) }()
	p.Render(&orbit.Ctx{}, orbit.NewWriter(&buf), p.Props{}, orbit.Slots{})
}
`
	m := newGenModule(t)
	pl := plPipeline(t, m, srcs, Options{Module: testModule})
	if w := pl.files[0].typed.Writes; len(w) == 0 || !strings.Contains(string(pl.files[0].typed.Output.Impl.Content), "orbit__c, orbit__v :=") {
		t.Fatalf("not lowered:\n%s", pl.files[0].typed.Output.Impl.Content)
	}
	var got [2]string
	for i, typed := range []bool{false, true} {
		pl.write(m, typed)
		got[i] = m.run(mainSrc)
	}
	if got[0] != got[1] || !strings.Contains(got[0], "nil pointer dereference") {
		t.Errorf("fallback %q, lowered %q", got[0], got[1])
	}
}
