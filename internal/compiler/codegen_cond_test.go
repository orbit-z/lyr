package compiler

import (
	"strings"
	"testing"
)

// TestCondTypedGolden pins the typed pass-2 output of the cond case
// (PLAN.md §2.6): lyr.If lowered on HTML attributes and in content,
// constants folded, and left as a call in larger expressions, spreads,
// component props and pass-through attributes, and for a user's own If or a
// shadowed lyr. The byte-identity with the fallback is
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
		case "src/components/Cond.lyr", "src/pages/allocs.lyr":
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
		`lyr.If(props.On, "page")`:                                 "folded",
		`lyr.If(props.On, props.Rel)`:                              "lowered",
		`lyr.If(props.On, -1)`:                                     "folded",
		`lyr.If(props.On, props.F)`:                                "lowered",
		`lyr.If(props.On, " JavaScript:alert(1)")`:                 "folded",
		`lyr.If(props.On, lyr.URL("javascript:ok()"))`:             "folded",
		`lyr.If(props.On, lyr.JSON(data))`:                         "lowered",
		`lyr.If(props.On, anyv)`:                                   "lowered",
		`lyr.If(props.On, true)`:                                   "folded",
		`lyr.If[any](props.On, 3)`:                                 "lowered",
		`(lyr.If(props.On, lyr.HTML("<b>h</b>")))`:                 "folded",
		`lyr.If(note("c1", true), val("v1"))`:                      "lowered",
		`fmt.Sprint(lyr.If(props.On, "a"))`:                        "",
		`[]any{lyr.If(props.On, "x"), "y"}`:                        "",
		`lyr.Cond(props.On, "yes", "no")`:                          "",
		`If(true, "x")`:                                            "",
		`lyr.If(true, "z")`:                                        "",
		`lyr.If(on, "page")`:                                       "", // a pass-through attribute
		`lyr.If(it.Href == current, "page")`:                       "folded",
		`lyr.If(!it.Ext, -1)`:                                      "folded",
		`lyr.If(true, saved)`:                                      "lowered",
		`lyr.Cond(saved, "Remove from library", "Add to library")`: "",
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

// condAllocTestSrc renders the cond case's allocs page (lyr.If and
// lyr.Cond on attributes and in content) and prints its allocations per
// render, and its time with -bench.
const condAllocTestSrc = `package alloc

import (
	"bytes"
	"testing"

	allocs "example.com/blog/lyr_gen/decl/pages/allocs"
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

// TestCondAllocs checks PLAN.md §2.6's allocation rule: a page using both
// helpers renders with 0 allocations in Lyr's render path.
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

// TestIfPanicSame: a lyr.If argument that panics (the eager-evaluation
// trap, PLAN.md §2.6) panics the same way lowered and not, after the same
// output.
func TestIfPanicSame(t *testing.T) {
	srcs := map[string]string{
		"src/components/P.lyr": "---\ntype user struct{ Email string }\n\nvar viewer *user\n---\n" +
			"<p>before</p><a title={lyr.If(viewer != nil, viewer.Email)}>x</a>{lyr.If(false, viewer.Email)}\n",
	}
	const mainSrc = `package main

import (
	"bytes"
	"fmt"

	p "example.com/blog/lyr_gen/decl/components/p"
	_ "example.com/blog/lyr_gen/impl/components/p"
	"github.com/orbit-z/lyr"
)

func main() {
	var buf bytes.Buffer
	defer func() { fmt.Printf("%s|%v", buf.String(), recover()) }()
	p.Render(&lyr.Ctx{}, lyr.NewWriter(&buf), p.Props{}, lyr.Slots{})
}
`
	m := newGenModule(t)
	pl := plPipeline(t, m, srcs, Options{Module: testModule})
	if w := pl.files[0].typed.Writes; len(w) == 0 || !strings.Contains(string(pl.files[0].typed.Output.Impl.Content), "lyr__c, lyr__v :=") {
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
