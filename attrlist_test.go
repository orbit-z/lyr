package orbit

import (
	"encoding/json"
	"math"
	"reflect"
	"strings"
	"testing"
)

// TestAttrListMatchesAttr: every value kind through A writes what Attr
// writes for the same value, and Value returns it.
func TestAttrListMatchesAttr(t *testing.T) {
	type c struct {
		name string
		p    AttrPair
		v    any
	}
	tests := []c{
		{"title", A("title", `a"<&'b`), `a"<&'b`},
		{"href", A("href", "javascript:x"), "javascript:x"},
		{"href", A("href", " JavaScript:x"), " JavaScript:x"},
		{"href", A("href", "/ok?a=1&b"), "/ok?a=1&b"},
		{"href", A("href", URL("javascript:ok")), URL("javascript:ok")},
		{"src", A("src", HTML("javascript:x")), HTML("javascript:x")},
		{"title", A("title", HTML("<b>")), HTML("<b>")},
		{"style", A("style", CSS(`content:"&"`)), CSS(`content:"&"`)},
		{"data-x", A("data-x", JSON(map[string]any{"a": "<'"})), JSON(map[string]any{"a": "<'"})},
		{"hidden", A("hidden", true), true},
		{"hidden", A("hidden", false), false},
		{"aria-hidden", A("aria-hidden", true), true},
		{"aria-hidden", A("aria-hidden", false), false},
		{"draggable", A("draggable", false), false},
		{"n", A("n", -42), -42},
		{"n", A("n", int8(-8)), int8(-8)},
		{"n", A("n", int16(-16)), int16(-16)},
		{"n", A("n", int32(-32)), int32(-32)},
		{"n", A("n", int64(math.MinInt64)), int64(math.MinInt64)},
		{"n", A("n", uint(7)), uint(7)},
		{"n", A("n", uint8(255)), uint8(255)},
		{"n", A("n", uint16(65535)), uint16(65535)},
		{"n", A("n", uint32(1<<32-1)), uint32(1<<32 - 1)},
		{"n", A("n", uint64(1<<63)), uint64(1 << 63)},
		{"n", A("n", uintptr(9)), uintptr(9)},
		{"f", A("f", 0.1), 0.1},
		{"f", A("f", 1e6), 1e6},
		{"f", A("f", math.Inf(1)), math.Inf(1)},
		{"f", A("f", float32(0.1)), float32(0.1)},
		{"x", A[any]("x", nil), nil},
		{"x", A("x", flag(true)), flag(true)},
		{"x", A("x", flag(false)), flag(false)},
		{"x", A("x", slug("a&b")), slug("a&b")},
		{"x", A("x", []int{1, 2}), []int{1, 2}},
		{"x", A("x", fakeStringer("s<")), fakeStringer("s<")},
		{"aria-current", A("aria-current", If(true, "page")), "page"},
		{"aria-current", A("aria-current", If(false, "page")), nil},
	}
	for _, tt := range tests {
		want, _ := render(func(w *Writer) { w.Attr(tt.name, tt.v) })
		got, err := render(func(w *Writer) { w.SpreadList(AttrList{tt.p}) })
		if err != nil || got != want {
			t.Errorf("%s=%#v: got %q, %v; Attr writes %q", tt.name, tt.v, got, err, want)
		}
		if tt.p.Name() != tt.name {
			t.Errorf("Name() = %q", tt.p.Name())
		}
		if v := tt.p.Value(); !reflect.DeepEqual(v, tt.v) {
			t.Errorf("%s: Value() = %#v, want %#v", tt.name, v, tt.v)
		}
	}
}

// TestSpreadListOrder: list order, not sorted; Attrs stays sorted.
func TestSpreadListOrder(t *testing.T) {
	l := AttrList{A("data-theme", "dark"), A("data-theme-pref", "auto"), A("data-dark-theme", "dark"), A("data-day-theme", "mist"), A("data-theme-store", "browser")}
	got, err := render(func(w *Writer) { SpreadOf(w, "", l) })
	want := ` data-theme="dark" data-theme-pref="auto" data-dark-theme="dark" data-day-theme="mist" data-theme-store="browser"`
	if err != nil || got != want {
		t.Errorf("list: got %s, %v\nwant %s", got, err, want)
	}
	m := Attrs{"b": 1, "a": "x"}
	got, _ = render(func(w *Writer) { SpreadOf(w, "", m) })
	if want := ` a="x" b="1"`; got != want {
		t.Errorf("attrs: got %s, want %s", got, want)
	}
	// nil and empty lists write nothing.
	for _, l := range []AttrList{nil, {}} {
		if got, err := render(func(w *Writer) { SpreadOf(w, "p:1:1", l) }); got != "" || err != nil {
			t.Errorf("%#v: %q %v", l, got, err)
		}
	}
	// append, slices: a plain slice.
	l2 := append(AttrList{A("id", "x")}, A("role", "alert"))
	if got, _ := render(func(w *Writer) { w.SpreadList(l2) }); got != ` id="x" role="alert"` {
		t.Errorf("append: %s", got)
	}
}

func TestSpreadListErrors(t *testing.T) {
	tests := []struct {
		l   AttrList
		err string
	}{
		{AttrList{A("a b", 1)}, `src/x.orbit:2:7: invalid attribute name "a b" in spread`},
		{AttrList{A("ok", 1), A("", 1)}, `src/x.orbit:2:7: invalid attribute name "" in spread`},
		{AttrList{A("role", "a"), A("id", 1), A("role", "b")}, `src/x.orbit:2:7: attribute "role" twice in spread`},
		{AttrList{A("Role", "a"), A("rOLE", "b")}, `src/x.orbit:2:7: attribute "rOLE" twice in spread`},
	}
	for _, tt := range tests {
		got, err := render(func(w *Writer) {
			w.Raw("<p")
			SpreadOf(w, "src/x.orbit:2:7", tt.l)
		})
		if err == nil || err.Error() != tt.err {
			t.Errorf("err = %v, want %s", err, tt.err)
		}
		if got != "<p" {
			t.Errorf("wrote %q", got)
		}
	}
	// SpreadList: Orbit's prefix, no position. Attrs through SpreadOf:
	// the position.
	if _, err := render(func(w *Writer) { w.SpreadList(AttrList{A("a b", 1)}) }); err == nil || err.Error() != `orbit: invalid attribute name "a b" in spread` {
		t.Errorf("SpreadList: %v", err)
	}
	if _, err := render(func(w *Writer) { SpreadOf(w, "src/x.orbit:2:7", Attrs{"a b": 1}) }); err == nil || err.Error() != `src/x.orbit:2:7: invalid attribute name "a b" in spread` {
		t.Errorf("SpreadOf(Attrs): %v", err)
	}
}

func TestAttrListJSON(t *testing.T) {
	l := AttrList{A("data-z", "1"), A("href", URL("javascript:ok")), A("hidden", true), A("off", false), A("aria-x", false), A("n", 3), A[any]("nil", nil), A("data-a", `"<`)}
	b, err := json.Marshal(l)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"data-z":"1","href":"javascript:ok","hidden":true,"aria-x":"false","n":"3","data-a":"\"\u003c"}`
	if string(b) != want {
		t.Errorf("Marshal = %s\nwant %s", b, want)
	}
	var back AttrList
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	got, _ := render(func(w *Writer) { w.SpreadList(back) })
	// The order is kept; the URL lost its trust.
	if want := ` data-z="1" href="about:invalid#orbit" hidden aria-x="false" n="3" data-a="&quot;&lt;"`; got != want {
		t.Errorf("round trip = %s\nwant %s", got, want)
	}
	for _, bad := range []string{`[]`, `{"a b":"x"}`, `{"a":1}`, `{"a":"1","A":"2"}`} {
		if err := json.Unmarshal([]byte(bad), &back); err == nil {
			t.Errorf("Unmarshal(%s): no error", bad)
		}
	}
	if err := json.Unmarshal([]byte(`null`), &back); err != nil || back != nil {
		t.Errorf("null: %v %#v", err, back)
	}
	if _, err := json.Marshal(AttrList{A("a", 1), A("A", 2)}); err == nil {
		t.Error("Marshal of a duplicate: no error")
	}
	type props struct{ Attrs AttrList }
	b, _ = json.Marshal(props{})
	if string(b) != `{"Attrs":null}` {
		t.Errorf("zero = %s", b)
	}
}

// TestRestAttrList: RestOf(list) and a merge into RestAttrs keep the list
// order; the call site's attributes win.
func TestRestAttrList(t *testing.T) {
	l := AttrList{A("b", 1), A("a", "x"), A("class", "c1"), A("href", "javascript:x"), A("off", false)}
	r := RestOf(l)
	got, err := render(func(w *Writer) { w.SpreadRest(&r, true) })
	if want := ` b="1" a="x" href="about:invalid#orbit" class="c1"`; err != nil || got != want {
		t.Errorf("RestOf: got %s, %v; want %s", got, err, want)
	}
	if v, ok := r.Get("A"); !ok || v != "x" {
		t.Errorf("Get(A) = %q, %v", v, ok)
	}
	if r.Has("off") || !r.Has("href") || r.Class() != "c1" {
		t.Errorf("Has/Class: %v %v %q", r.Has("off"), r.Has("href"), r.Class())
	}
	m := RestMerge(Rest("own", ` a="y"`), l)
	got, _ = render(func(w *Writer) { w.SpreadRest(&m, true) })
	if want := ` a="y" b="1" href="about:invalid#orbit" class="own"`; got != want {
		t.Errorf("merge: got %s, want %s", got, want)
	}
	if n := testing.AllocsPerRun(100, func() { m = RestMerge(Rest("own", ` a="y"`), l) }); n != 0 {
		t.Errorf("merge: %v allocs", n)
	}
	if f := RestFrom(l); len(f.l) != len(l) {
		t.Errorf("RestFrom(AttrList) = %+v", f)
	}
	// After an Attrs: a second part.
	two := RestMerge(RestMerge(Rest("", ` a="1"`), Attrs{"c": 3}), AttrList{A("c", 4), A("d", 5)})
	got, _ = render(func(w *Writer) { w.SpreadRest(&two, true) })
	if want := ` a="1" c="3" d="5"`; got != want {
		t.Errorf("two: got %s, want %s", got, want)
	}
	// Duplicates are a render error.
	dup := RestOf(AttrList{A("x", 1), A("X", 2)})
	if _, err := render(func(w *Writer) { w.SpreadRest(&dup, false) }); err == nil || !strings.Contains(err.Error(), `attribute "X" twice in spread`) {
		t.Errorf("dup: %v", err)
	}
	// JSON keeps the order.
	b, err := json.Marshal(RestMerge(Rest("k", ` z="0"`), l))
	if want := `{"class":"k","z":"0","b":"1","a":"x","href":"about:invalid#orbit"}`; err != nil || string(b) != want {
		t.Errorf("JSON = %s, %v\nwant %s", b, err, want)
	}
	// Existing calls of RestOf with an Attrs still infer.
	_ = RestOf(Attrs{"a": 1})
}

// TestAttrListAllocs: a list of strings, ints and bools built in place
// costs no allocation.
func TestAttrListAllocs(t *testing.T) {
	w := NewWriter(getBuffer())
	theme, n, on := "dark", 3, true
	allocs := testing.AllocsPerRun(1000, func() {
		w.buf.Reset()
		SpreadOf(w, "src/x.orbit:1:1", AttrList{A("data-theme", theme), A("data-n", n), A("hidden", on), A("data-f", 0.5), A("href", theme)})
	})
	if allocs != 0 || w.Err() != nil {
		t.Errorf("%v allocations, err %v; want 0", allocs, w.Err())
	}
}

// BenchmarkSpreadList measures PLAN.md §2.9's cost table: five dynamic
// string attributes as a map, a list built in place, a list from a helper,
// and written one by one.
func BenchmarkSpreadList(b *testing.B) {
	w := NewWriter(getBuffer())
	type theme struct{ A, B, C, D, E string }
	th := theme{"dark", "auto", "dark", "mist", "browser"}
	b.Run("attrs", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			w.buf.Reset()
			SpreadOf(w, "", Attrs{"data-theme": th.A, "data-theme-pref": th.B, "data-dark-theme": th.C, "data-day-theme": th.D, "data-theme-store": th.E})
		}
	})
	b.Run("list", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			w.buf.Reset()
			SpreadOf(w, "", AttrList{A("data-theme", th.A), A("data-theme-pref", th.B), A("data-dark-theme", th.C), A("data-day-theme", th.D), A("data-theme-store", th.E)})
		}
	})
	b.Run("list-helper", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			w.buf.Reset()
			SpreadOf(w, "", themeList(th.A, th.B, th.C, th.D, th.E))
		}
	})
	b.Run("one-by-one", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			w.buf.Reset()
			w.AttrString("data-theme", th.A)
			w.AttrString("data-theme-pref", th.B)
			w.AttrString("data-dark-theme", th.C)
			w.AttrString("data-day-theme", th.D)
			w.AttrString("data-theme-store", th.E)
		}
	})
}

// TestAttrListIsland: a deferred component's AttrList prop crosses the
// island request as JSON and renders the same attributes, in order.
func TestAttrListIsland(t *testing.T) {
	t.Setenv("ORBIT_KEY", "")
	t.Setenv("ORBIT_STATIC", "")
	t.Setenv("ORBIT_PUBLIC", "")
	type listProps struct{ List AttrList }
	const id = "Box-0a1b2c3d"
	box := func(c *Ctx, w *Writer, p listProps, s Slots) error {
		w.Raw("<div")
		SpreadOf(w, "src/components/Box.orbit:4:6", p.List)
		w.Raw(">box</div>")
		return nil
	}
	list := AttrList{A("data-z", "z"), A("data-n", 2), A("hidden", true), A("data-off", false), A("data-a", `"<`)}
	want, _ := render(func(w *Writer) { box(nil, w, listProps{list}, Slots{}) })
	page := func(c *Ctx, w *Writer) error { return Defer(c, w, id, listProps{list}, nil) }
	s := NewSite([]Route{{Pattern: "/p", File: "src/pages/p.orbit", Render: page, Islands: true}},
		[]Island{NewIsland(id, "src/components/Box.orbit", box)}, WithStaticDir(t.TempDir()))
	h := s.Handler()
	m := placeholderRe.FindStringSubmatch(do(h, "GET", "/p").Body.String())
	if m == nil {
		t.Fatal("no placeholder")
	}
	rec := do(h, "GET", m[1]+"?p="+m[4])
	if rec.Code != 200 || rec.Body.String() != want {
		t.Errorf("got %d\n%s\nwant\n%s", rec.Code, rec.Body, want)
	}
}

// TestIsURLAttr: the switch in isURLAttr is exactly urlAttrs, in any ASCII
// case.
func TestIsURLAttr(t *testing.T) {
	for _, a := range urlAttrs {
		for _, n := range []string{a, strings.ToUpper(a), strings.ToUpper(a[:1]) + a[1:]} {
			if !isURLAttr(n) {
				t.Errorf("isURLAttr(%q) = false", n)
			}
		}
	}
	for _, n := range []string{"", "s", "sr", "srcs", "class", "data-href", "xlink:hre", "hrefs", "backgrounds", "ſrc", "hreF\x00"} {
		if isURLAttr(n) {
			t.Errorf("isURLAttr(%q) = true", n)
		}
	}
}

//go:noinline
func themeList(a, b, c, d, e string) AttrList {
	return AttrList{A("data-theme", a), A("data-theme-pref", b), A("data-dark-theme", c), A("data-day-theme", d), A("data-theme-store", e)}
}
