package orbit

import (
	"bytes"
	"encoding/json"
	"math"
	"strings"
	"testing"
)

// TestSpreadRestMatchesAttr: each dynamic attribute kind writes what Attr
// writes for the same value (escaping, URL filtering, false and nil).
func TestSpreadRestMatchesAttr(t *testing.T) {
	tests := []struct {
		name string
		a    RestAttr
		v    any
	}{
		{"title", RestString("", "title", `a"<&'b`), `a"<&'b`},
		{"href", RestStringURL("", "href", "javascript:x"), "javascript:x"},
		{"href", RestStringURL("", "href", "/ok?a=1&b"), "/ok?a=1&b"},
		{"href", RestURL("", "href", "javascript:ok"), URL("javascript:ok")},
		{"hidden", RestBool("", "hidden", true), true},
		{"hidden", RestBool("", "hidden", false), false},
		{"n", RestInt("", "n", -42), -42},
		{"n", RestUint("", "n", 1<<63), uint64(1 << 63)},
		{"f", RestFloat("", "f", 0.1), 0.1},
		{"f", RestFloat("", "f", 1e6), 1e6},
		{"f", RestFloat32("", "f", 0.1), float32(0.1)},
		{"data-x", RestJSON("", "data-x", "", JSON(map[string]any{"a": "<'"})), JSON(map[string]any{"a": "<'"})},
		{"x", RestAny("", "x", nil), nil},
		{"x", RestAny("", "x", flag(true)), flag(true)},
		{"x", RestAny("", "x", flag(false)), flag(false)},
		{"src", RestAny("", "src", "javascript:x"), "javascript:x"},
		{"x", RestAny("", "x", []int{1, 2}), []int{1, 2}},
	}
	for _, tt := range tests {
		r := Rest("", "", tt.a)
		got, err := render(func(w *Writer) { w.SpreadRest(&r, false) })
		if err != nil {
			t.Fatal(err)
		}
		want, _ := render(func(w *Writer) { w.Attr(tt.name, tt.v) })
		if got != want {
			t.Errorf("%s %v: SpreadRest = %q, Attr = %q", tt.name, tt.v, got, want)
		}
	}
}

func TestSpreadRestOrder(t *testing.T) {
	r := Rest("mt-2  x", ` required data-o-5c7d1e2f`,
		RestString(` name="shelf"`, "data-shelf", "s-81"),
		RestBool(` a="1"`, "off", false))
	got, _ := render(func(w *Writer) {
		w.Raw("<button")
		w.SpreadRest(&r, false)
		w.Raw(` class="btn`)
		w.RestClass(&r)
		w.Raw(`">`)
	})
	want := `<button name="shelf" data-shelf="s-81" a="1" required data-o-5c7d1e2f class="btn mt-2 x">`
	if got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
	got, _ = render(func(w *Writer) { w.SpreadRest(&r, true) })
	if want := ` name="shelf" data-shelf="s-81" a="1" required data-o-5c7d1e2f class="mt-2 x"`; got != want {
		t.Errorf("withClass: got %s, want %s", got, want)
	}
	empty := Rest("", "")
	got, _ = render(func(w *Writer) { w.SpreadRest(&empty, true) })
	if got != "" {
		t.Errorf("empty: %q", got)
	}
}

func TestRestOf(t *testing.T) {
	r := RestOf(Attrs{"b": 1, "a": "x", "class": "c1", "href": "javascript:x", "off": false, "z": nil})
	got, err := render(func(w *Writer) { w.SpreadRest(&r, true) })
	if err != nil {
		t.Fatal(err)
	}
	if want := ` a="x" b="1" href="about:invalid#orbit" class="c1"`; got != want {
		t.Errorf("got %s, want %s", got, want)
	}
	bad := RestOf(Attrs{"a b": 1})
	if _, err := render(func(w *Writer) { w.SpreadRest(&bad, false) }); err == nil || !strings.Contains(err.Error(), `invalid attribute name "a b"`) {
		t.Errorf("err = %v", err)
	}
}

func TestRestMerge(t *testing.T) {
	inner := Rest("caller", ` name="x" data-o-p`, RestString(` id="i"`, "title", "t"))
	// <Input {...props.Attrs} name="y" /> in a scoped wrapper: the tag wins.
	own := Rest("", ` name="y" data-o-f`)
	m := RestMerge(own, inner)
	got, _ := render(func(w *Writer) { w.SpreadRest(&m, true) })
	if want := ` name="y" data-o-f id="i" title="t" data-o-p class="caller"`; got != want {
		t.Errorf("merge: got\n%s\nwant\n%s", got, want)
	}
	// Only the scope attribute: no allocation, the same output order.
	scoped := RestMerge(Rest("", ` data-o-f`), inner)
	got, _ = render(func(w *Writer) { w.SpreadRest(&scoped, true) })
	if want := ` id="i" title="t" name="x" data-o-p data-o-f class="caller"`; got != want {
		t.Errorf("scoped: got\n%s\nwant\n%s", got, want)
	}
	if n := testing.AllocsPerRun(100, func() { scoped = RestMerge(Rest("", ` data-o-f`), inner) }); n != 0 {
		t.Errorf("scoped merge: %v allocs", n)
	}
	// An Attrs merges without allocating; the tag's class wins.
	am := Attrs{"name": "z", "rows": 3, "class": "m"}
	ma := RestMerge(Rest("own", ` name="y"`), am)
	got, _ = render(func(w *Writer) { w.SpreadRest(&ma, true) })
	if want := ` name="y" rows="3" class="own"`; got != want {
		t.Errorf("attrs: got %s, want %s", got, want)
	}
	if n := testing.AllocsPerRun(100, func() { ma = RestMerge(Rest("own", ` name="y"`), am) }); n != 0 {
		t.Errorf("attrs merge: %v allocs", n)
	}
	// Twice: the earlier spread wins over the later. (The first merge
	// takes the fast path: the tag's static attributes go after.)
	two := RestMerge(RestMerge(Rest("", ` a="1"`), Rest("", ` b="2"`)), RestOf(Attrs{"b": "3", "c": "4", "class": "k"}))
	got, _ = render(func(w *Writer) { w.SpreadRest(&two, true) })
	if want := ` b="2" a="1" c="4" class="k"`; got != want {
		t.Errorf("two: got %s, want %s", got, want)
	}
	if f := RestFrom(inner); f.tail != inner.tail || len(f.dyn) != 1 {
		t.Errorf("RestFrom(RestAttrs) = %+v", f)
	}
	if f := RestFrom(am); f.m == nil {
		t.Errorf("RestFrom(Attrs) = %+v", f)
	}
}

func TestRestHasGet(t *testing.T) {
	r := RestMerge(Rest("a b", ` name="s&amp;h" required`,
		RestStringURL(` x="1"`, "href", "javascript:x"),
		RestBool("", "off", false),
		RestInt("", "n", 7)), Attrs{"m": "v", "name": "no"})
	tests := []struct {
		name, want string
		ok         bool
	}{
		{"name", "s&h", true},
		{"NAME", "s&h", true},
		{"required", "", true},
		{"x", "1", true},
		{"href", "about:invalid#orbit", true},
		{"off", "", false},
		{"n", "7", true},
		{"m", "v", true},
		{"class", "a b", true},
		{"missing", "", false},
	}
	for _, tt := range tests {
		got, ok := r.Get(tt.name)
		if got != tt.want || ok != tt.ok {
			t.Errorf("Get(%q) = %q, %v; want %q, %v", tt.name, got, ok, tt.want, tt.ok)
		}
		if r.Has(tt.name) != tt.ok {
			t.Errorf("Has(%q) = %v", tt.name, !tt.ok)
		}
	}
	var zero RestAttrs
	if zero.Has("class") {
		t.Error("zero has class")
	}
	if n := testing.AllocsPerRun(100, func() { _ = r.Has("required") }); n != 0 {
		t.Errorf("Has: %v allocs", n)
	}
}

func TestRestJSON(t *testing.T) {
	r := Rest("mt-2", ` name="a&quot;b" required data-o-x`,
		RestURL(` id="i"`, "href", "javascript:ok"),
		RestBool("", "off", false),
		RestFloat("", "f", 0.5))
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"class":"mt-2","id":"i","href":"javascript:ok","f":"0.5","name":"a\"b","required":true,"data-o-x":true}`
	if string(b) != want {
		t.Errorf("Marshal = %s\nwant %s", b, want)
	}
	var back RestAttrs
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	got, _ := render(func(w *Writer) { w.SpreadRest(&back, true) })
	// The URL lost its trust: filtered after the round trip.
	if want := ` id="i" href="about:invalid#orbit" f="0.5" name="a&#34;b" required data-o-x class="mt-2"`; got != want {
		t.Errorf("round trip = %s\nwant %s", got, want)
	}
	for _, bad := range []string{`[]`, `{"a b":"x"}`, `{"a":1}`} {
		if err := json.Unmarshal([]byte(bad), &back); err == nil {
			t.Errorf("Unmarshal(%s): no error", bad)
		}
	}
	if err := json.Unmarshal([]byte(`null`), &back); err != nil || !back.empty() {
		t.Errorf("null: %v %+v", err, back)
	}
	type props struct{ Attrs RestAttrs }
	b, _ = json.Marshal(props{})
	if string(b) != `{"Attrs":{}}` {
		t.Errorf("zero = %s", b)
	}
}

// TestRestAllocs: an all-static rest allocates nothing; a dynamic one
// allocates nothing when it doesn't escape (direct calls, orbit build).
func TestRestAllocs(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf)
	id := "s-81"
	n := 3
	spread := func(p restProps) {
		w.Raw("<button")
		w.SpreadRest(&p.Attrs, false)
		w.Raw(` class="btn`)
		w.RestClass(&p.Attrs)
		w.Raw(`">`)
	}
	static := testing.AllocsPerRun(1000, func() {
		buf.Reset()
		spread(restProps{Attrs: Rest("mt-2", ` name="shelf" required`)})
	})
	dynamic := testing.AllocsPerRun(1000, func() {
		buf.Reset()
		spread(restProps{Attrs: Rest("mt-2", ` required`,
			RestString(` name="shelf"`, "data-shelf", id), RestInt("", "rows", int64(n)),
			RestFloat("", "f", math.Pi), RestStringURL("", "href", id))})
	})
	if static != 0 || dynamic != 0 {
		t.Errorf("allocs: static %v, dynamic %v; want 0", static, dynamic)
	}
}

type restProps struct {
	Variant string
	Attrs   RestAttrs
}

func BenchmarkSpreadRest(b *testing.B) {
	var buf bytes.Buffer
	w := NewWriter(&buf)
	id := "s-81"
	b.Run("static", func(b *testing.B) {
		for b.Loop() {
			buf.Reset()
			r := Rest("mt-2", ` name="shelf" required data-o-5c7d1e2f`)
			w.SpreadRest(&r, true)
		}
	})
	b.Run("dynamic", func(b *testing.B) {
		for b.Loop() {
			buf.Reset()
			r := Rest("mt-2", ` required data-o-5c7d1e2f`, RestString(` name="shelf"`, "data-shelf", id))
			w.SpreadRest(&r, true)
		}
	})
}

// TestRestIsland: a deferred component's RestAttrs crosses the island
// request as JSON and renders the attributes.
func TestRestIsland(t *testing.T) {
	t.Setenv("ORBIT_KEY", "")
	t.Setenv("ORBIT_STATIC", "")
	t.Setenv("ORBIT_PUBLIC", "")
	const id = "Button-0a1b2c3d"
	button := func(c *Ctx, w *Writer, p restProps, s Slots) error {
		w.Raw("<button")
		w.SpreadRest(&p.Attrs, false)
		w.Raw(` class="btn`)
		w.RestClass(&p.Attrs)
		w.Raw(`">`)
		w.String(p.Variant)
		w.Raw("</button>")
		return nil
	}
	page := func(c *Ctx, w *Writer) error {
		shelf := "s-81"
		return Defer(c, w, id, restProps{Variant: "quiet", Attrs: Rest("mt-2", ` required data-o-5c7d1e2f`,
			RestString(` name="shelf"`, "data-shelf", shelf), RestURL("", "href", "javascript:x"))}, nil)
	}
	s := NewSite([]Route{{Pattern: "/p", File: "src/pages/p.orbit", Render: page, Islands: true}},
		[]Island{NewIsland(id, "src/components/Button.orbit", button)}, WithStaticDir(t.TempDir()))
	h := s.Handler()
	m := placeholderRe.FindStringSubmatch(do(h, "GET", "/p").Body.String())
	if m == nil {
		t.Fatal("no placeholder")
	}
	rec := do(h, "GET", m[1]+"?p="+m[4])
	want := `<button name="shelf" data-shelf="s-81" href="about:invalid#orbit" required data-o-5c7d1e2f class="btn mt-2">quiet</button>`
	if rec.Code != 200 || rec.Body.String() != want {
		t.Errorf("got %d\n%s\nwant\n%s", rec.Code, rec.Body, want)
	}
}
