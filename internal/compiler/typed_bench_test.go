package compiler

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// tyBenchUnits is a synthetic project shaped like spike/typecheck's gensite:
// n components whose Props use internal/posts, each impl importing three
// other decl packages, with about 30 mixed-type call sites, a loop and a
// slot closure.
func tyBenchUnits(t testing.TB, n int) []Unit {
	exprs := []string{"props.Post.Title", "props.N", "props.Tags", "props.Status", "props.Price", "props.On",
		"href(props.Post)", "len(props.Tags)", "props.N + 1", `"lit"`, "42", "props.Post.Slug", "props.Price * 2",
		"strings.ToUpper(props.Post.Title)", "props.Status.String()", "props.Tags[0]", "props.On && props.N > 0",
		"float32(props.Price)", "uint8(props.N)", "props.Post", "nil", "props.Err", "props.HTML"}
	attrs := [][2]string{{"href", "href(props.Post)"}, {"data-on", "props.On"}, {"title", "props.Post.Title"},
		{"data-n", "props.N"}, {"class", `"card"`}}
	var units []Unit
	for i := range n {
		var body strings.Builder
		for _, e := range exprs {
			fmt.Fprintf(&body, "\tlyr__w.Raw(\"<p>\")\n\tlyr__w.Expr(\n%s,\n\t)\n", e)
		}
		for _, a := range attrs {
			fmt.Fprintf(&body, "\tlyr__w.Attr(%q,\n%s,\n\t)\n", a[0], a[1])
		}
		body.WriteString("\tfor _, tag := range props.Tags {\n\t\tlyr__w.Expr(\ntag,\n\t)\n\t}\n")
		var imports strings.Builder
		imports.WriteString("\t\"strings\"\n\t\"" + tyPostsPath + "\"\n")
		body.WriteString("\tif props.N < 0 {\n")
		for _, d := range []int{1, 7, 13} {
			j := (i + d) % n
			fmt.Fprintf(&imports, "\tC%03d \"%s/lyr_gen/decl/components/c%03d\"\n", j, testModule, j)
			fmt.Fprintf(&body, "\t\tif lyr__err := C%03d.Render(lyr__ctx, lyr__w, C%03d.Props{N: props.N}, lyr__rt.Slots{\n"+
				"\t\t\tDefault: func(lyr__w *lyr__rt.Writer) error { lyr__w.Expr(\nprops.Post.Title,\n); return lyr__w.Err() },\n"+
				"\t\t}); lyr__err != nil {\n\t\t\treturn lyr__err\n\t\t}\n", j, j)
		}
		body.WriteString("\t}\n")
		units = append(units, tyFixture(t, tyComp{
			lyr:         fmt.Sprintf("src/components/C%03d.lyr", i),
			declImports: "\tlyr \"" + RuntimePath + "\"\n\t\"" + tyPostsPath + "\"\n",
			declBody: "\ntype Props struct {\n\tPost   posts.Post\n\tN      int\n\tTags   []string\n\tStatus posts.Status\n" +
				"\tPrice  float64\n\tOn     bool\n\tErr    error\n\tHTML   lyr.HTML\n}\n",
			implImports: imports.String(),
			implTop:     "\nfunc href(p posts.Post) string { return \"/p/\" + string(p.Slug) }\n",
			body:        body.String(),
		}))
	}
	return units
}

func tyBenchModule(b *testing.B) *genModule {
	m := newGenModule(b)
	m.write("internal/posts/posts.go", tyPostsSrc)
	return m
}

// BenchmarkCheckFull is a full typed pass over 50 components (100
// packages) with the real GoListLoader, go list included (the spike's
// srcdecl engine: 38 ms at s50, 46 ms at s200 on a Ryzen 7 9700X).
func BenchmarkCheckFull(b *testing.B) {
	m := tyBenchModule(b)
	units := tyBenchUnits(b, 50)
	g := newTyGen(units...)
	ctx := context.Background()
	sites := 0
	for _, u := range units {
		sites += len(u.Pass1.CallSites)
	}
	for b.Loop() {
		c := NewChecker(tyGoListLoader(m))
		c.generate = g.generate
		res, err := c.Check(ctx, units)
		if err != nil {
			b.Fatal(err)
		}
		if len(res.Warnings) > 0 || !res.Units[0].Checked {
			b.Fatalf("not checked: %v", res.Warnings)
		}
	}
	b.ReportMetric(float64(sites), "sites")
}

// BenchmarkCheckFullNoGoList is the same check with export data already
// loaded: the go/types part alone.
func BenchmarkCheckFullNoGoList(b *testing.B) {
	m := tyBenchModule(b)
	units := tyBenchUnits(b, 50)
	g := newTyGen(units...)
	ctx := context.Background()
	c := NewChecker(tyGoListLoader(m))
	c.generate = g.generate
	if _, err := c.Check(ctx, units); err != nil {
		b.Fatal(err)
	}
	for b.Loop() {
		// Changing every unit's decl re-checks everything, without a Load.
		res, err := c.Update(ctx, units, nil)
		if err != nil {
			b.Fatal(err)
		}
		if len(res.Units) != len(units) {
			b.Fatalf("%d units", len(res.Units))
		}
		c.state.units = map[string]*tyUnit{} // so the next Update sees new decls
	}
}

// BenchmarkCheckHotBody is lyr dev's body edit: one impl package
// re-checked by the resident checker (spike: about 0.3 ms).
func BenchmarkCheckHotBody(b *testing.B) {
	m := tyBenchModule(b)
	units := tyBenchUnits(b, 50)
	g := newTyGen(units...)
	ctx := context.Background()
	c := NewChecker(tyGoListLoader(m))
	c.generate = g.generate
	if _, err := c.Check(ctx, units); err != nil {
		b.Fatal(err)
	}
	changed := []Unit{units[17]}
	for b.Loop() {
		res, err := c.Update(ctx, changed, nil)
		if err != nil {
			b.Fatal(err)
		}
		if len(res.Units) != 1 || !res.Units[0].Checked {
			b.Fatal("not one checked unit")
		}
	}
}
