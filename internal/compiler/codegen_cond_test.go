package compiler

import (
	"os"
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
			case w.Cond != nil:
				lowered[cs.Code.Text] = "cond"
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
		`lyr.Cond(props.On, "yes", "no")`:                          "cond",
		`If(true, "x")`:                                            "",
		`lyr.If(true, "z")`:                                        "",
		`lyr.If(on, "page")`:                                       "", // a pass-through attribute
		`lyr.If(it.Href == current, "page")`:                       "folded",
		`lyr.If(!it.Ext, -1)`:                                      "folded",
		`lyr.If(true, saved)`:                                      "lowered",
		`lyr.Cond(saved, "Remove from library", "Add to library")`: "cond",
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

// TestCondFoldTypedGolden pins the typed pass-2 output of the condfold
// case (PLAN.md §2.6): lyr.Cond lowered on HTML attributes, in content and
// in template literal pieces, constant branches folded, and constant
// expressions folded into the static HTML. The byte-identity with the
// fallback (and want.html) is TestPipelineTypedVsFallback's; the build
// flavor's, TestCodegenDirectGolden's.
func TestCondFoldTypedGolden(t *testing.T) {
	c := loadCase(t, "testdata/codegen/condfold")
	m := newGenModule(t)
	p := plPipeline(t, m, plReadCase(t, c), c.opts)
	got := map[string]string{} // code → "cond A/B" (folded branches), "fold", or ""
	for _, pf := range p.files {
		if !pf.typed.Checked {
			t.Fatalf("%s: not type-checked", pf.path)
		}
		switch pf.path {
		case "src/components/CF.lyr", "src/components/Consts.lyr":
			pkg, _ := PackageFor(pf.path)
			golden(t, "testdata/codegen/condfold/"+strings.ReplaceAll(pkg.Rel, "/", "_")+".render.go.typed.golden", string(pf.typed.Output.Impl.Content))
		}
		for _, cs := range pf.pass1.CallSites {
			w := pf.typed.Writes[cs.Key]
			v := ""
			switch {
			case w.Cond != nil:
				v = "cond "
				for _, arm := range []CondArm{w.Cond.A, w.Cond.B} {
					if arm.Folded {
						v += "F"
					} else {
						v += "-"
					}
				}
			case w.Kind == WriteFold && w.Ref:
				v = "fold ref"
			case w.Kind == WriteFold:
				v = "fold"
			case w.If != nil && w.If.Ref:
				v = "if ref"
			}
			got[cs.Code.Text] = v
		}
	}
	for code, want := range map[string]string{
		`lyr.Cond(props.On, "yes <", "no &")`:                           "cond FF",
		`lyr.Cond(props.On, "Saved", "Save")`:                           "cond FF",
		`lyr.Cond(props.On, props.Name, "anon")`:                        "cond -F",
		`lyr.Cond(props.On, props.N, -1)`:                               "cond -F",
		`lyr.Cond(props.On, "<b>", props.Name)`:                         "cond F-",
		`lyr.Cond(props.On, props.Name, props.Link)`:                    "cond --",
		`lyr.Cond(note("c1", props.On), val("a1"), val("b1"))`:          "cond --",
		`lyr.Cond(note("c2", props.On), "k", val("b2"))`:                "cond F-",
		`lyr.Cond(props.On, " JavaScript:alert(1)", "/ok?a&b")`:         "cond FF",
		`lyr.Cond(props.On, "/s", props.Link)`:                          "cond F-",
		`lyr.Cond(props.On, lyr.URL("javascript:ok()"), lyr.URL("/u"))`: "cond FF",
		`lyr.Cond(props.On, true, false)`:                               "cond FF",
		`lyr.Cond(props.On, -1, 0)`:                                     "cond FF",
		`lyr.Cond(props.On, 1, 2.5)`:                                    "cond FF",
		`lyr.Cond(props.On, 1e21, 0.1)`:                                 "cond FF",
		`lyr.Cond(props.On, true, props.N > 1)`:                         "cond F-",
		`lyr.Cond(props.On, float32(0.1), 2)`:                           "cond FF",
		`lyr.Cond(props.On, Published, Draft)`:                          "cond --", // String method: a Stringer write, not folded
		`lyr.Cond(props.On, Slug("s&t"), Slug("u"))`:                    "cond FF",
		`lyr.Cond(props.On, Slug("s<"), Slug("u"))`:                     "cond FF",
		`lyr.Cond[any](props.On, 1, "x")`:                               "", // explicit instantiation
		`lyr.Cond(props.On, lyr.Some("o"), lyr.None[string]())`:         "", // an Opt: lowered as an Opt value
		`Cond(props.On, "a", "b")`:                                      "", // the component's own Cond
		`lyr.Cond(props.On, lyr.HTML("<i>h</i>"), lyr.HTML("<u>"))`:     "cond FF",
		`lyr.Cond(props.On, "on", "off")`:                               "cond FF", // a piece
		`lyr.Cond(props.On, props.N, 7)`:                                "cond -F",
		`lyr.Cond(props.On, "<a>", props.Name)`:                         "cond F-",
		`lyr.Cond(props.On, "a b", props.Link)`:                         "cond F-",
		`lyr.Cond(props.On, ":x", "")`:                                  "cond FF",
		`strings.ToUpper(lyr.Cond(props.On, "a", "b"))`:                 "",
		`lyr.Cond(props.On, "x", "y") + "!"`:                            "",
		`"a<b\"c"`:                                                      "fold",
		`3`:                                                             "fold",
		`0.1`:                                                           "fold",
		`1e21`:                                                          "fold",
		`-2`:                                                            "fold",
		`true`:                                                          "fold",
		`false`:                                                         "fold",
		`"color: red"`:                                                  "fold",
		`"javascript:x()"`:                                              "fold",
		`label`:                                                         "fold",
		`Published`:                                                     "",
		`"Like"`:                                                        "fold",
		`2.5`:                                                           "fold",
		`1e-7`:                                                          "fold",
		`"a" + "b"`:                                                     "fold",
		`'x'`:                                                           "fold",
		`" "`:                                                           "fold",
		`"x<y"`:                                                         "fold",
		`math.MaxInt8`:                                                  "fold ref",
		`math.Pi`:                                                       "fold ref",
		`lyr.If(props.On, math.MaxInt16)`:                               "if ref",
		`lyr.Cond(props.On, math.E, 1)`:                                 "cond FF",
		`len(arr)`:                                                      "fold ref",
	} {
		g, ok := got[code]
		if !ok {
			t.Errorf("no call site %s", code)
			continue
		}
		if g != want {
			t.Errorf("%s: %q, want %q", code, g, want)
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

// condBenchSrc is a page in the shape of the profiled nyth Home: a list
// whose items pick constant classes, ARIA values and labels with lyr.Cond.
const condBenchSrc = `---
type item struct {
	Title string
	On    bool
}

var items []item
for i := range 50 {
	items = append(items, item{"Book", i%3 == 0})
}
---
<ul>
  {for _, it := range items}
    <li class={` + "`card ${lyr.Cond(it.On, \"card--active\", \"card--idle\")}`" + `} aria-pressed={lyr.Cond(it.On, "true", "false")} title={lyr.Cond(it.On, "Remove from library", "Add to library")}>{it.Title}: {lyr.Cond(it.On, "Saved to your library", "Save for later")}</li>
  {/for}
</ul>
`

const condBenchTestSrc = `package alloc

import (
	"bytes"
	"testing"

	page "example.com/blog/lyr_gen/decl/pages/index"
	_ "example.com/blog/lyr_gen/impl/pages/index"
	"github.com/orbit-z/lyr"
)

func BenchmarkRender(b *testing.B) {
	var buf bytes.Buffer
	w := lyr.NewWriter(&buf)
	ctx := &lyr.Ctx{}
	b.ReportAllocs()
	for b.Loop() {
		buf.Reset()
		if err := page.Render(ctx, w, page.Props{}, lyr.Slots{}); err != nil {
			b.Fatal(err)
		}
	}
}
`

// TestCondBench measures the lyr.Cond lowering (PLAN.md §2.6): the same
// page rendered with Cond lowered (constants folded at build time) and
// with the lowering stripped (the typed write of the call's result, the
// escaper scanning each constant at run time), same HTML. Timing only, so
// it runs with LYR_COND_BENCH=1.
func TestCondBench(t *testing.T) {
	if os.Getenv("LYR_COND_BENCH") == "" {
		t.Skip("set LYR_COND_BENCH=1 to run")
	}
	srcs := map[string]string{"src/pages/index.lyr": condBenchSrc}
	m := newGenModule(t)
	p := plPipeline(t, m, srcs, Options{Module: testModule})
	pf := p.files[0]
	stripped := TypedWrites{}
	lowered := 0
	for k, w := range pf.typed.Writes {
		if w.Cond != nil {
			w.Cond = nil
			lowered++
		}
		stripped[k] = w
	}
	if lowered != 4 {
		t.Fatalf("%d lowered sites, want 4", lowered)
	}
	in := *pf.in
	in.Typed = stripped
	for _, c := range []struct {
		name string
		out  *Output
	}{{"lowered", pf.typed.Output}, {"stripped", Generate(&in)}} {
		mm := newGenModule(t)
		mm.writeOutput(c.out)
		mm.write("alloc/alloc_test.go", condBenchTestSrc)
		out, err := mm.goCmd("test", "-run=^$", "-bench=.", "-count=5", "./alloc")
		if err != nil {
			t.Fatalf("go test: %v\n%s", err, out)
		}
		for _, l := range strings.Split(out, "\n") {
			if strings.HasPrefix(l, "BenchmarkRender") {
				t.Logf("%s: %s", c.name, l)
			}
		}
	}
}
