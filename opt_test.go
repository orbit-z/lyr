package orbit

import (
	"bytes"
	"cmp"
	"encoding/json"
	"fmt"
	"testing"
)

func TestOptBasics(t *testing.T) {
	if v, ok := Some("").Get(); v != "" || !ok {
		t.Errorf("Some(\"\").Get() = %q, %v", v, ok)
	}
	if v, ok := None[int]().Get(); v != 0 || ok {
		t.Errorf("None.Get() = %v, %v", v, ok)
	}
	if Some(2).Or(5) != 2 || None[int]().Or(5) != 5 {
		t.Error("Or")
	}
	if !Some(0).IsSome() || Some(0).IsZero() || None[int]().IsSome() || !None[int]().IsZero() {
		t.Error("IsSome/IsZero")
	}
	if NonZero("") != None[string]() || NonZero("x") != Some("x") || NonZero(0) != (Opt[int]{}) {
		t.Error("NonZero")
	}
	if If(true, "a") != Some("a") || If(false, "a") != None[string]() {
		t.Error("If")
	}
	if got := cmp.Or(None[string](), Some(""), Some("b")); got != Some("") {
		t.Errorf("cmp.Or = %#v", got)
	}
	if fmt.Sprint(Some("a")) != "a" || fmt.Sprint(None[string]()) != "" || fmt.Sprint(Some(3)) != "3" {
		t.Error("String")
	}
	var o Opt[string]
	if Wrap(o, "x") != Some("x") || WrapIf(o, If[bool], false, "x") != o || WrapIf(o, If[bool], true, "x") != Some("x") {
		t.Error("Wrap/WrapIf")
	}
	type slug string
	if OptOf(Opt[slug]{}, slug("s")) != Some(slug("s")) || OptOf(Opt[slug]{}, Some(slug("t"))) != Some(slug("t")) || OptOf(Opt[slug]{}, 3).IsSome() || OptOf(Opt[slug]{}, "u") != Some(slug("u")) || OptOf(Opt[slug]{}, If(true, "v")) != Some(slug("v")) || OptOf(Opt[slug]{}, If(false, "v")).IsSome() || OptOf(Opt[float64]{}, 2) != Some(2.0) {
		t.Error("OptOf")
	}
}

type optFlag bool
type optSlug string

// TestOptWrites: Attr, Expr, Spread, SpreadList, SpreadRest (through
// RestAny and RestOpt) and A write an Opt as its value, or nothing when
// none.
func TestOptWrites(t *testing.T) {
	cases := []struct {
		name     string
		some     any // the Opt holding v
		none     any
		v        any
		attrName string
	}{
		{"string", Some(`a"<&'b`), None[string](), `a"<&'b`, "title"},
		{"url filter", Some("javascript:x"), None[string](), "javascript:x", "href"},
		{"trusted url", Some(URL("javascript:ok()")), None[URL](), URL("javascript:ok()"), "href"},
		{"css", Some(CSS("color: red")), None[CSS](), CSS("color: red"), "style"},
		{"html", Some(HTML("<b>")), None[HTML](), HTML("<b>"), "title"},
		{"json", Some(JSON([]int{1})), None[JSONValue](), JSON([]int{1}), "data-x"},
		{"bool presence", Some(true), None[bool](), true, "hidden"},
		{"bool false", Some(false), None[bool](), false, "hidden"},
		{"bool aria", Some(false), None[bool](), false, "aria-pressed"},
		{"named bool", Some(optFlag(true)), None[optFlag](), optFlag(true), "data-flag"},
		{"int", Some(-1), None[int](), -1, "tabindex"},
		{"int8", Some(int8(-3)), None[int8](), int8(-3), "data-n"},
		{"uint16", Some(uint16(9)), None[uint16](), uint16(9), "data-n"},
		{"float32", Some(float32(0.1)), None[float32](), float32(0.1), "data-f"},
		{"float64", Some(1.5), None[float64](), 1.5, "data-f"},
		{"named string", Some(optSlug("s&t")), None[optSlug](), optSlug("s&t"), "data-slug"},
		{"any", Some[any](7), None[any](), 7, "data-any"},
		{"nested", Some(Some("in")), None[Opt[string]](), Some("in"), "title"},
		{"nested none", Some(None[string]()), None[Opt[string]](), None[string](), "title"},
	}
	for _, c := range cases {
		check := func(what string, f func(w *Writer, v any)) {
			t.Helper()
			want, _ := render(func(w *Writer) { f(w, c.v) })
			got, _ := render(func(w *Writer) { f(w, c.some) })
			if got != want {
				t.Errorf("%s %s: some writes %q, want %q", c.name, what, got, want)
			}
			if got, _ := render(func(w *Writer) { f(w, c.none) }); got != "" {
				t.Errorf("%s %s: none writes %q", c.name, what, got)
			}
		}
		check("Attr", func(w *Writer, v any) { w.Attr(c.attrName, v) })
		check("Expr", func(w *Writer, v any) { w.Expr(v) })
		check("Spread", func(w *Writer, v any) { w.Spread(Attrs{c.attrName: v}) })
		check("SpreadList", func(w *Writer, v any) { w.SpreadList(AttrList{A(c.attrName, v)}) })
		check("RestAny", func(w *Writer, v any) {
			r := Rest("", "", RestAny("", c.attrName, v))
			w.SpreadRest(&r, true)
		})
		check("RestOf", func(w *Writer, v any) {
			r := RestOf(Attrs{c.attrName: v})
			w.SpreadRest(&r, true)
		})
		// The concrete A, and RestOpt, against Attr of the value.
		want, _ := render(func(w *Writer) { w.Attr(c.attrName, c.v) })
		for what, f := range map[string]func(w *Writer){
			"A":       func(w *Writer) { w.SpreadList(AttrList{aOf(c.attrName, c.some)}) },
			"RestOpt": func(w *Writer) { r := Rest("", "", restOptOf("", c.attrName, c.some)); w.SpreadRest(&r, true) },
		} {
			if got, _ := render(f); got != want {
				t.Errorf("%s %s: %q, want %q", c.name, what, got, want)
			}
		}
		if got, _ := render(func(w *Writer) { w.SpreadList(AttrList{aOf(c.attrName, c.none)}) }); got != "" {
			t.Errorf("%s A none: %q", c.name, got)
		}
	}
}

// aOf calls A with the Opt's static type, as Go code would.
func aOf(name string, o any) AttrPair {
	switch o := o.(type) {
	case Opt[string]:
		return A(name, o)
	case Opt[URL]:
		return A(name, o)
	case Opt[CSS]:
		return A(name, o)
	case Opt[HTML]:
		return A(name, o)
	case Opt[JSONValue]:
		return A(name, o)
	case Opt[bool]:
		return A(name, o)
	case Opt[int]:
		return A(name, o)
	case Opt[int8]:
		return A(name, o)
	case Opt[uint16]:
		return A(name, o)
	case Opt[float32]:
		return A(name, o)
	case Opt[float64]:
		return A(name, o)
	case Opt[optFlag]:
		return A(name, o)
	case Opt[optSlug]:
		return A(name, o)
	case Opt[any]:
		return A(name, o)
	case Opt[Opt[string]]:
		return A(name, o)
	}
	panic(fmt.Sprintf("aOf %T", o))
}

// restOptOf calls RestOpt (RestOptURL in a URL attribute) with the Opt's
// static type, as generated code would.
func restOptOf(pre, name string, o any) RestAttr {
	f := func(o any) RestAttr { panic(fmt.Sprintf("restOptOf %T", o)) }
	url := isURLAttr(name)
	pick := func(a, b RestAttr) RestAttr {
		if url {
			return b
		}
		return a
	}
	switch o := o.(type) {
	case Opt[string]:
		return pick(RestOpt(pre, name, o), RestOptURL(pre, name, o))
	case Opt[URL]:
		return pick(RestOpt(pre, name, o), RestOptURL(pre, name, o))
	case Opt[CSS]:
		return pick(RestOpt(pre, name, o), RestOptURL(pre, name, o))
	case Opt[HTML]:
		return pick(RestOpt(pre, name, o), RestOptURL(pre, name, o))
	case Opt[JSONValue]:
		return pick(RestOpt(pre, name, o), RestOptURL(pre, name, o))
	case Opt[bool]:
		return pick(RestOpt(pre, name, o), RestOptURL(pre, name, o))
	case Opt[int]:
		return pick(RestOpt(pre, name, o), RestOptURL(pre, name, o))
	case Opt[int8]:
		return pick(RestOpt(pre, name, o), RestOptURL(pre, name, o))
	case Opt[uint16]:
		return pick(RestOpt(pre, name, o), RestOptURL(pre, name, o))
	case Opt[float32]:
		return pick(RestOpt(pre, name, o), RestOptURL(pre, name, o))
	case Opt[float64]:
		return pick(RestOpt(pre, name, o), RestOptURL(pre, name, o))
	case Opt[optFlag]:
		return pick(RestOpt(pre, name, o), RestOptURL(pre, name, o))
	case Opt[optSlug]:
		return pick(RestOpt(pre, name, o), RestOptURL(pre, name, o))
	case Opt[any]:
		return pick(RestOpt(pre, name, o), RestOptURL(pre, name, o))
	case Opt[Opt[string]]:
		return pick(RestOpt(pre, name, o), RestOptURL(pre, name, o))
	}
	return f(o)
}

func TestOptClass(t *testing.T) {
	got, _ := render(func(w *Writer) {
		w.ClassStart()
		w.ClassAny(Some("a"))
		w.ClassAny(None[string]())
		w.ClassAny(Some([]string{"b", "c"}))
		w.ClassEnd()
	})
	if got != ` class="a b c"` {
		t.Errorf("ClassAny: %q", got)
	}
	if ClassString(Some(optSlug("x"))) != "x" || ClassString(None[string]()) != "" {
		t.Error("ClassString")
	}
}

func TestOptJSON(t *testing.T) {
	type S struct {
		A Opt[string]
		B Opt[int]  `json:",omitzero"`
		C Opt[*int] `json:"c"`
		D Opt[[]string]
	}
	b, err := json.Marshal(S{A: Some("x"), C: Some[*int](nil), D: None[[]string]()})
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `{"A":"x","c":null,"D":null}` {
		t.Errorf("Marshal: %s", b)
	}
	var s S
	if err := json.Unmarshal([]byte(`{"A":null,"B":3,"c":null,"D":["a"]}`), &s); err != nil {
		t.Fatal(err)
	}
	if s.A.IsSome() || s.B != Some(3) || s.C.IsSome() || !s.D.IsSome() || s.D.Or(nil)[0] != "a" {
		t.Errorf("Unmarshal: %#v", s)
	}
	// C was some(nil pointer): encoded null, it came back none (the
	// documented lossy edge).
	s.A = Some("keep")
	if err := json.Unmarshal([]byte(`{"A":"y"}`), &s); err != nil || s.A != Some("y") {
		t.Errorf("Unmarshal into a set Opt: %v %#v", err, s.A)
	}
	if err := json.Unmarshal([]byte(`{"B":"no"}`), &s); err == nil {
		t.Error("Unmarshal of a wrong type: no error")
	}
	// A RestAttrs holding Opt values encodes them as written.
	r := Rest("", "", RestAny("", "rel", Some("x")), RestAny("", "title", None[string]()), RestOpt("", "hidden", Some(true)), RestOpt("", "data-n", None[int]()))
	b, err = json.Marshal(r)
	if err != nil || string(b) != `{"rel":"x","hidden":true}` {
		t.Errorf("RestAttrs JSON: %s %v", b, err)
	}
	m := RestOf(Attrs{"rel": Some("y"), "hidden": Some(true), "x": None[int]()})
	b, err = json.Marshal(m)
	if err != nil || string(b) != `{"hidden":true,"rel":"y"}` {
		t.Errorf("RestOf JSON: %s %v", b, err)
	}
	if v, ok := m.Get("rel"); v != "y" || !ok {
		t.Errorf("Get: %q %v", v, ok)
	}
	if m.Has("x") {
		t.Error("Has(none)")
	}
}

// TestOptAllocs: the runtime rows of the cost table ("Typed optional
// values", Cost).
func TestOptAllocs(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf)
	s := string([]byte("noopener")) // not a constant
	o := Some(s)
	var none Opt[string]
	n := 7
	var sink Opt[string]
	var pair AttrPair
	var ra RestAttr
	for _, tt := range []struct {
		name string
		want float64
		f    func()
	}{
		{"lowered", 0, func() {
			if v, ok := o.Get(); ok {
				w.Raw(` rel="`)
				w.AttrValue(v)
				w.Raw(`"`)
			}
		}},
		{"NonZero", 0, func() { sink = NonZero(s) }},
		{"Wrap", 0, func() { sink = Wrap(sink, s) }},
		{"OptOf T", 0, func() { sink = OptOf(sink, s) }},
		{"OptOf Opt", 0, func() { sink = OptOf(sink, o) }},
		{"A string", 0, func() { pair = A("rel", o) }},
		{"A int", 0, func() { pair = A("tabindex", Some(n)) }},
		{"A bool", 0, func() { pair = A("hidden", Some(true)) }},
		{"A URL", 0, func() { pair = A("href", Some(URL(s))) }},
		{"A none", 0, func() { pair = A("rel", none) }},
		{"SpreadList", 0, func() { w.SpreadList(AttrList{A("rel", o), A("tabindex", Some(n))}) }},
		{"RestOpt", 0, func() { ra = RestOpt("", "rel", o) }},
		{"RestOpt int", 0, func() { ra = RestOpt("", "tabindex", Some(n)) }},
		{"Attr some", 1, func() { w.Attr("rel", o) }},
		{"Expr some", 1, func() { w.Expr(o) }},
	} {
		if got := testing.AllocsPerRun(100, func() { buf.Reset(); tt.f() }); got != tt.want {
			t.Errorf("%s: %v allocs, want %v", tt.name, got, tt.want)
		}
	}
	_, _, _ = sink, pair, ra
}

// TestSetHTML: pass 1's set:html (PLAN.md §2.6): an HTML and a set
// Opt[HTML] are written raw, none writes nothing, and a string (which the
// build refuses) is a positioned render error, never written.
func TestSetHTML(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf)
	SetHTML(w, "x.orbit:1:2", HTML("<b>a</b>"))
	SetHTML(w, "x.orbit:1:3", Some(HTML("<i>b</i>")))
	SetHTML(w, "x.orbit:1:4", None[HTML]())
	if got := buf.String(); got != "<b>a</b><i>b</i>" || w.Err() != nil {
		t.Errorf("got %q, %v", got, w.Err())
	}
	SetHTML(w, "x.orbit:2:5", "<script>")
	const want = "x.orbit:2:5: set:html needs an orbit.HTML (or an orbit.Opt[orbit.HTML]), not string; it was skipped (see the warning above)"
	if err := w.Err(); err == nil || err.Error() != want {
		t.Errorf("error %v, want %s", err, want)
	}
	if got := buf.String(); got != "<b>a</b><i>b</i>" {
		t.Errorf("the string was written: %q", got)
	}
}
