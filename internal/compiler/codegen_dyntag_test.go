package compiler

import (
	"os"
	"strings"
	"testing"
)

// TestDynTagAnalyze: a capitalized tag is a dynamic tag when it names a
// variable or constant in scope at the tag (PLAN.md §2.8), decided from
// the body file's object resolution.
func TestDynTagAnalyze(t *testing.T) {
	cases := []struct {
		src  string
		want []string // the dynamic tag names, in order
	}{
		{"---\nTag := \"a\"\n---\n<Tag>x</Tag>\n", []string{"Tag"}},
		{"---\nvar Tag = \"a\"\n---\n<Tag />\n", []string{"Tag"}},
		{"---\nconst Tag = \"a\"\n---\n<Tag />\n", []string{"Tag"}},
		{"---\nconst (\n\tA = \"a\"\n\tB = \"b\"\n)\n---\n<B /><A />\n", []string{"B", "A"}},
		{"---\ntags := []string{\"a\"}\n---\n{for _, T := range tags}<T />{/for}\n", []string{"T"}},
		{"---\nm := map[string]string{}\n---\n{if T, ok := m[\"k\"]; ok}<T />{/if}\n", []string{"T"}},
		{"---\nvar x any\n---\n{switch T := x.(type)}{case string}<T />{/switch}\n", []string{"T"}},
		// Out of scope at the tag: a loop variable after its loop.
		{"---\ntags := []string{\"a\"}\n---\n{for _, T := range tags}{/for}<T />\n", nil},
		// Not variables: a type, a func, an unresolved name, <Self>.
		{"---\ntype Tag string\n---\n<Tag />\n", nil},
		{"---\nfunc Tag() string { return \"a\" }\n---\n<Tag />\n", nil},
		{"<Crad />\n", nil},
		{"<Self />\n", nil},
		// A variable declared inside a function literal isn't in scope.
		{"---\nf := func() { Tag := \"a\"; _ = Tag }\n_ = f\n---\n<Tag />\n", nil},
		// Nested.
		{"---\nA, B := \"div\", \"span\"\n---\n<A><B>x</B></A>\n", []string{"A", "B"}},
	}
	for _, c := range cases {
		f := Parse(c.src, Strict)
		a := Analyze(f, FileInfo{Path: "src/components/X.lyr"})
		var got []string
		for _, s := range a.DynTags {
			got = append(got, c.src[s.Start:s.End])
		}
		if strings.Join(got, ",") != strings.Join(c.want, ",") {
			t.Errorf("%q: dynamic tags %v, want %v", c.src, got, c.want)
		}
		n := 0
		Walk(f.Template, func(n2 Node) bool {
			if e, ok := n2.(*Element); ok && e.Dynamic {
				n++
			}
			return true
		})
		if n != len(c.want) {
			t.Errorf("%q: %d dynamic Elements, want %d", c.src, n, len(c.want))
		}
		// Analyzing the same File again gives the same answer.
		if a2 := Analyze(f, FileInfo{Path: "src/components/X.lyr"}); len(a2.DynTags) != len(a.DynTags) {
			t.Errorf("%q: second Analyze: %d dynamic tags, want %d", c.src, len(a2.DynTags), len(a.DynTags))
		}
	}
}

// TestDynTagErrors: the Lyr errors of dynamic tags, each at its .lyr
// position.
func TestDynTagErrors(t *testing.T) {
	srcs := map[string]string{
		"src/components/Card.lyr": "<p>card</p>\n",
		"src/pages/index.lyr": `---
import Card "../components/Card.lyr"

Card := "div"
Tag := "section"
---
<Card />
<Crad />
<Tag server:defer />
<Tag client:load />
<Tag server:concurrent />
<Tag is:inline />
<Tag set:json={1} />
<Tag set:html={"x"}>child</Tag>
<Tag set:text={"x"} />
`,
	}
	p := plFront(srcs, Options{Module: testModule})
	want := []string{
		"src/pages/index.lyr:4:1: Card is both a component import and a variable; rename one",
		"src/pages/index.lyr:8:2: unknown component <Crad>: no .lyr import with this alias (or declare a variable Crad for a dynamic tag)",
		"src/pages/index.lyr:9:6: server:defer only works on .lyr components",
		"src/pages/index.lyr:10:6: client:load only works on .lyr components; move the <script> into a component",
		"src/pages/index.lyr:11:6: server:concurrent only works on .lyr components",
		"src/pages/index.lyr:12:6: is:inline only works on <script> and <style>",
		`src/pages/index.lyr:13:6: set:json only works on <script type="application/json">`,
		"src/pages/index.lyr:14:21: an element with set:html can't have children; the directive sets its content",
	}
	got := p.diagnostics()
	sortStrings(got)
	sortStrings(want)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("got:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	// The component reading's errors are gone for a dynamic tag, and
	// stay for a component.
	p = plFront(map[string]string{
		"src/components/Card.lyr": "<p>card</p>\n",
		"src/pages/index.lyr":     "---\nimport Card \"../components/Card.lyr\"\n---\n<Card set:html={\"x\"} server:defer=\"1\" />\n",
	}, Options{Module: testModule})
	want = []string{
		"src/pages/index.lyr:4:7: set:html only works on HTML elements and <Fragment>",
		"src/pages/index.lyr:4:22: server:defer takes no value",
	}
	if got := p.diagnostics(); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("component: got:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func sortStrings(s []string) {
	for i := range s {
		for j := i + 1; j < len(s); j++ {
			if s[j] < s[i] {
				s[i], s[j] = s[j], s[i]
			}
		}
	}
}

// TestDynTagTolerant: in Tolerant mode, the end tag's name is an exact
// segment too (_ = Tag), so that a rename of the variable edits both tags.
func TestDynTagTolerant(t *testing.T) {
	src := "---\nTag := \"a\"\n---\n<Tag href=\"/\">x</Tag>\n"
	f := Parse(src, Tolerant)
	a := Analyze(f, FileInfo{Path: "src/components/X.lyr"})
	out := Generate(&Input{File: f, Analysis: a, Index: ComponentIndex{}, Options: Options{Module: testModule, Mode: Tolerant}})
	var spans []int
	for _, s := range out.Impl.Map.Segments {
		if s.Kind == SegExact && src[s.LyrStart:s.LyrEnd] == "Tag" {
			spans = append(spans, s.LyrStart)
		}
	}
	start, end := strings.Index(src, "<Tag")+1, strings.Index(src, "</Tag")+2
	// The start tag and the end tag.
	if len(spans) != 2 || spans[0] != start || spans[1] != end {
		t.Errorf("exact Tag segments at %v, want [%d %d]\n%s", spans, start, end, out.Impl.Content)
	}
	if !strings.Contains(string(out.Impl.Content), "_ = /*line") {
		t.Errorf("no _ = Tag in tolerant output:\n%s", out.Impl.Content)
	}
	// Strict mode has no such line.
	f = Parse(src, Strict)
	a = Analyze(f, FileInfo{Path: "src/components/X.lyr"})
	out = Generate(&Input{File: f, Analysis: a, Index: ComponentIndex{}, Options: Options{Module: testModule}})
	if strings.Contains(string(out.Impl.Content), "_ = /*line") {
		t.Errorf("_ = Tag in strict output:\n%s", out.Impl.Content)
	}
}

// dynAllocTestSrc renders the dyntag case's allocs page (a concatenated
// dynamic tag name, Button's <a>/<button>, Choice's optional wrapper both
// ways, an in-page AttrList) and prints its allocations per render.
const dynAllocTestSrc = `package alloc

import (
	"bytes"
	"testing"

	_ "example.com/blog/lyr_gen/impl/components/button"
	_ "example.com/blog/lyr_gen/impl/components/choice"
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

func BenchmarkRender(b *testing.B) {
	var buf bytes.Buffer
	w := lyr.NewWriter(&buf)
	ctx := &lyr.Ctx{}
	b.ReportAllocs()
	for b.Loop() {
		buf.Reset()
		allocs.Render(ctx, w, allocs.Props{}, lyr.Slots{})
	}
}
`

// TestDynTagDirectAllocs: the dyntag case's allocs page renders with 0
// allocations in both flavors: dynamic tags (a concatenated name included)
// and an AttrList built in the page cost none (PLAN.md §2.8, §2.9).
func TestDynTagDirectAllocs(t *testing.T) {
	c := loadCase(t, "testdata/codegen/dyntag")
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
	m.write("alloc/alloc_test.go", dynAllocTestSrc)
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
	if os.Getenv("LYR_DYNTAG_BENCH") != "" {
		for _, args := range [][]string{{"-overlay=" + OverlayFile}, nil} {
			out, err := m.goCmd(append(append([]string{"test", "-run=^$", "-bench=.", "-benchtime=200000x", "-count=5"}, args...), "./alloc")...)
			if err != nil {
				t.Fatal(err, out)
			}
			t.Logf("%v:\n%s", args, out)
		}
	}
}
