package compiler

import (
	"context"
	"go/types"
	"strings"
	"testing"
	"time"
)

const tyJSONComments = `---
import (
	"time"
	"unsafe"

	"github.com/orbit-z/orbit"
)

type point struct{ X, Y int }

type Node struct {
	Next *Node
	Kids []Node
	V    int
}

type Inner struct {
	F    chan bool
	ok   func()
	Good int
}

type Slug string

type Props struct {
	PostID   int
	OnLoad   func()
	Render   orbit.Renderer
	Slots    orbit.Slots
	Ch       chan int
	P        unsafe.Pointer
	C        complex128
	Any      any
	Err      error
	ByPoint  map[point]int
	Fs       []func()
	Inner    Inner
	At       time.Time
	Skipped  func() ` + "`json:\"-\"`" + `
	Tree     *Node
	ByID     map[int]string
	ByName   map[string][]Slug
	Raw      []byte
	Arr      [2]*int
	Depth    int
}
---
<p>{props.PostID}</p>
{if props.Depth > 0}<Self server:defer depth={props.Depth - 1} />{/if}
`

const tyJSONPage = `---
import Comments "../components/Comments.orbit"
import Good "../components/Good.orbit"
---
<main>
  <Comments server:defer postID={1}><p slot="fallback">Loading…</p></Comments>
  <Good server:defer name="x" data-n={1} />
  <Good />
</main>
`

const tyJSONGood = `---
import "time"

type Props struct {
	At   time.Time
	N    int
	Skip func() ` + "`json:\"-\"`" + `
	M    map[int]float64
	// PLAN.md §2.5: RestAttrs's JSON methods pass the check.
	Attrs orbit.RestAttrs
}
---
<p {...props.Attrs}>{props.N}</p>
`

func TestDeferPropsJSONCheck(t *testing.T) {
	p := plFront(map[string]string{
		"src/components/Comments.orbit": tyJSONComments,
		"src/components/Good.orbit":     tyJSONGood,
		"src/pages/index.orbit":         tyJSONPage,
	}, Options{Module: testModule})
	if ds := p.diagnostics(); len(ds) > 0 {
		t.Fatalf("front diagnostics:\n%s", strings.Join(ds, "\n"))
	}
	p.typedPass(t, &tyTestLoader{})
	var got []string
	for _, pf := range p.files {
		if !pf.typed.Checked {
			t.Fatalf("%s not checked (warnings %v)", pf.path, p.warnings)
		}
		for _, d := range pf.typed.Diagnostics {
			got = append(got, d.Format(pf.path, pf.f.Lines))
		}
	}
	const tail = ", which can't be sent to a deferred component; props must be JSON-serializable"
	var want []string
	for _, site := range []struct{ pos, tag string }{
		{"src/components/Comments.orbit:49:27", "Self"},
		{"src/pages/index.orbit:6:13", "Comments"},
	} {
		for _, f := range []string{
			"Props.OnLoad has type func()",
			"Props.Render has type orbit.Renderer",
			"Props.Slots.Default has type orbit.Renderer",
			"Props.Ch has type chan int",
			"Props.P has type unsafe.Pointer",
			"Props.C has type complex128",
			"Props.Any has type any",
			"Props.Err has type error",
			"Props.ByPoint has type map[point]int",
			"Props.Fs[] has type func()",
			"Props.Inner.F has type chan bool",
		} {
			pos := site.pos
			want = append(want, pos+": <"+site.tag+" server:defer>: "+f+tail)
		}
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("got:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// TestDeferPropsNotChecked: a package with type errors keeps its
// fallbacks and gets no JSON check (Defer's json.Marshal reports it at run
// time).
func TestDeferPropsNotChecked(t *testing.T) {
	p := plFront(map[string]string{
		"src/components/Good.orbit": "---\ntype Props struct{ F func() }\n---\n<p>{undefinedName}</p>\n{if false}<Self server:defer />{/if}\n",
	}, Options{Module: testModule})
	p.typedPass(t, &tyTestLoader{})
	if pf := p.files[0]; pf.typed.Checked || len(pf.typed.Diagnostics) > 0 {
		t.Errorf("checked %v, diagnostics %v", pf.typed.Checked, pf.typed.Diagnostics)
	}
}

func TestJSONWalkerSelfCoded(t *testing.T) {
	timePkg, err := tyStd().Import("time")
	if err != nil {
		t.Fatal(err)
	}
	tt := timePkg.Scope().Lookup("Time").Type()
	if !selfCoded(tt) {
		t.Error("time.Time isn't self-coded")
	}
	if selfCoded(types.Universe.Lookup("error").Type()) {
		t.Error("error is self-coded")
	}
	w := &jsonWalker{stack: map[*types.Named]bool{}}
	if p := w.check(types.NewMap(tt, types.Typ[types.Int]), "Props.M"); p != nil {
		t.Errorf("map[time.Time]int: %v", p)
	}
	_ = time.Time{}
}

// BenchmarkDeferPropsCheck measures the JSON check of one deferred call
// site with a large Props (20 fields, nested and recursive types): the cost
// it adds to pass 2.
func BenchmarkDeferPropsCheck(b *testing.B) {
	p := plFront(map[string]string{
		"src/components/Comments.orbit": tyJSONComments,
		"src/components/Good.orbit":     tyJSONGood,
		"src/pages/index.orbit":         tyJSONPage,
	}, Options{Module: testModule})
	units := make([]Unit, len(p.files))
	for i, pf := range p.files {
		units[i] = Unit{Input: pf.in, Pass1: pf.pass1}
	}
	c := NewChecker(&tyTestLoader{})
	if _, err := c.Check(context.Background(), units); err != nil {
		b.Fatal(err)
	}
	d := c.state.decls[testModule+"/orbit_gen/decl/components/comments"]
	if d == nil || d.pkg == nil {
		b.Fatal("no Comments decl package")
	}
	props := d.pkg.Scope().Lookup("Props").Type()
	site := p.files[2].pass1.DeferSites[0]
	b.ReportAllocs()
	for b.Loop() {
		if len(deferPropsErrors(&site, props)) != 11 {
			b.Fatal("wrong error count")
		}
	}
}
