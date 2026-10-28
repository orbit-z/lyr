package compiler

import (
	"reflect"
	"strings"
	"testing"
)

// TestClassListTyped: the typed pass picks Class/Classes for the items of
// a string kind and []string (named ones converted), and reports every
// other type at the item.
func TestClassListTyped(t *testing.T) {
	const page = `---
import Card "../components/Card.orbit"

type Cls string

type Tags []string

s := "a"
var c Cls = "b"
tags := Tags{"c"}
n := 3
var v any = "x"
---
<div class:list={s, c, tags, []string{"d"}, n, nil, v, "e": n > 1}>x</div>
<div class={n} class:list={"f"}>y</div>
<Card class:list={s, tags, n} />
`
	const card = `---
type Props struct{ Class string }
---
<p class={props.Class}>z</p>
`
	p := plFront(map[string]string{"src/pages/index.orbit": page, "src/components/Card.orbit": card}, Options{Module: testModule})
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
		for _, cs := range pf.pass1.CallSites {
			if cs.Kind == SiteClass || cs.Kind == SiteClassProp {
				kinds[pf.path+" "+cs.Code.Text] = pf.typed.Writes[cs.Key]
			}
		}
	}
	want := []string{
		"src/pages/index.orbit:14:45: class:list items must be a string or []string, not int",
		"src/pages/index.orbit:14:48: class:list items must be a string or []string, not untyped nil",
		"src/pages/index.orbit:14:53: class:list items must be a string or []string, not any",
		"src/pages/index.orbit:15:13: class={…} next to class:list must be a string or []string, not int",
		"src/pages/index.orbit:16:28: class:list items must be a string or []string, not int",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("got:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	for k, w := range map[string]TypedWrite{
		"src/pages/index.orbit s":             {Kind: WriteClass},
		"src/pages/index.orbit c":             {Kind: WriteClass, Conv: "string"},
		"src/pages/index.orbit tags":          {Kind: WriteClasses, Conv: "[]string"},
		`src/pages/index.orbit []string{"d"}`: {Kind: WriteClasses},
		"src/pages/index.orbit n":             {},
	} {
		if g := kinds[k]; !reflect.DeepEqual(g, w) {
			t.Errorf("%s: %+v, want %+v", k, g, w)
		}
	}
}

func TestClassListUnknownProp(t *testing.T) {
	p := plFront(map[string]string{
		"src/pages/index.orbit":     "---\nimport Card \"../components/Card.orbit\"\n---\n<Card class:list={\"a\"} />\n",
		"src/components/Card.orbit": "---\ntype Props struct{ Title string }\n---\n<p>{props.Title}</p>\n",
	}, Options{Module: testModule})
	got := strings.Join(p.diagnostics(), "\n")
	want := `src/pages/index.orbit:4:7: class:list sets the class prop, which <Card> doesn't have (props: title)`
	if got != want {
		t.Errorf("got %s\nwant %s", got, want)
	}
}
