package compiler

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

// TestTypedReemit generates the card case again with typed writes (as part
// C's pass 2 would choose them), checks the call sites keep their keys and
// order, and that the typed code builds, vets and renders the same bytes.
func TestTypedReemit(t *testing.T) {
	c := loadCase(t, "testdata/codegen/card")
	res := generateCase(t, c)
	choose := map[string]TypedWrite{
		"label":                             {Kind: WriteString},
		"n":                                 {Kind: WriteInt, Conv: "int64"},
		"props.Featured":                    {Kind: WriteAttrBool},
		"props.PostID":                      {Kind: WriteAttrInt, Conv: "int64"},
		"props.Href":                        {Kind: WriteAttrURL},
		"l.text":                            {Kind: WriteString},
		"l.href":                            {Kind: WriteAttrURL},
		`"<escaped>"`:                       {Kind: WriteString},
		`"javascript:img"`:                  {Kind: WriteAttrURL},
		`orbit.URL("javascript:trusted()")`: {Kind: WriteAttrString, Conv: "string"},
	}
	m := newGenModule(t)
	for _, r := range res {
		typed := TypedWrites{}
		for _, cs := range r.out.CallSites {
			if tw, ok := choose[cs.Code.Text]; ok {
				typed[cs.Key] = tw
			}
		}
		in := *r.in
		in.Typed = typed
		out := Generate(&in)
		if len(out.CallSites) != len(r.out.CallSites) {
			t.Fatalf("%s: %d call sites in pass 2, %d in pass 1", r.path, len(out.CallSites), len(r.out.CallSites))
		}
		for i, cs := range out.CallSites {
			if cs.Key != r.out.CallSites[i].Key {
				t.Errorf("%s: site %d key %d, pass 1 %d", r.path, i, cs.Key, r.out.CallSites[i].Key)
			}
			if string(out.Impl.Content[cs.Arg.Start:cs.Arg.End]) != cs.Code.Text {
				t.Errorf("%s: site %d Arg doesn't hold its code", r.path, i)
			}
			call := string(out.Impl.Content[cs.Call.Start:cs.Call.End])
			if tw, ok := typed[cs.Key]; ok && !strings.HasPrefix(call, NameW+"."+tw.Kind.Method()+"(") {
				t.Errorf("%s: site %d call %q, want %s", r.path, i, call, tw.Kind.Method())
			}
		}
		checkOutput(t, &cgResult{path: r.path, f: r.f, a: r.a, in: &in, out: out})
		if bytes.Equal(out.Impl.Content, r.out.Impl.Content) && len(typed) > 0 {
			t.Errorf("%s: typed writes changed nothing", r.path)
		}
		if r.path == "src/components/Card.orbit" {
			golden(t, "testdata/codegen/card/components_card.render.go.typed.golden", string(out.Impl.Content))
		}
		m.writeOutput(out)
	}
	m.buildAndVet()
	mainSrc, err := os.ReadFile("testdata/codegen/card/main.go.txt")
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile("testdata/codegen/card/want.html")
	if err != nil {
		t.Fatal(err)
	}
	if got := m.run(string(mainSrc)); got != string(want) {
		t.Errorf("typed output differs from the fallback:\n--- typed\n%s\n--- fallback\n%s", got, want)
	}
}

// TestTypedSkip checks WriteSkip emits nothing and keeps the site.
func TestTypedSkip(t *testing.T) {
	src := "<p>{nil}</p>"
	f, a := cgAnalyze(t, "src/components/X.orbit", src, Strict)
	in := &Input{File: f, Analysis: a, Options: Options{Module: testModule}}
	p1 := Generate(in)
	in.Typed = TypedWrites{p1.CallSites[0].Key: {Kind: WriteSkip}}
	p2 := Generate(in)
	if len(p2.CallSites) != 1 || bytes.Contains(p2.Impl.Content, []byte("Expr(")) || !bytes.Contains(p2.Impl.Content, []byte(`Raw("<p></p>")`)) {
		t.Errorf("skip: %d sites\n%s", len(p2.CallSites), p2.Impl.Content)
	}
}
