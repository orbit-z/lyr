package lyr

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestIfCond(t *testing.T) {
	if v, ok := If(true, "page").Get(); v != "page" || !ok {
		t.Errorf("If(true) = %v, %v", v, ok)
	}
	if v, ok := If(false, "page").Get(); v != "" || ok {
		t.Errorf("If(false) = %v, %v", v, ok)
	}
	var err error
	if v, ok := If(true, err).Get(); v != nil || !ok { // some(a nil interface value)
		t.Errorf("If(true, nil error) = %v, %v", v, ok)
	}
	if Cond(true, "a", "b") != "a" || Cond(false, 1, 2) != 2 {
		t.Error("Cond")
	}
}

// TestIfMatchesTypedWrites: Attr and Expr of an If result write what the
// lowered typed writes (an if around the pieces) write.
func TestIfMatchesTypedWrites(t *testing.T) {
	s := `a"<&'b`
	for _, c := range []bool{true, false} {
		tests := []struct {
			name           string
			unlowered, low func(w *Writer)
		}{
			{"string", func(w *Writer) { w.Attr("rel", If(c, s)) }, func(w *Writer) {
				if c {
					w.Raw(` rel="`)
					w.AttrValue(s)
					w.Raw(`"`)
				}
			}},
			{"url", func(w *Writer) { w.Attr("href", If(c, "javascript:x")) }, func(w *Writer) {
				if c {
					w.Raw(` href="`)
					w.URL("javascript:x")
					w.Raw(`"`)
				}
			}},
			{"int", func(w *Writer) { w.Attr("tabindex", If(c, -1)) }, func(w *Writer) {
				if c {
					w.Raw(` tabindex="-1"`)
				}
			}},
			{"bool", func(w *Writer) { w.Attr("hidden", If(c, true)) }, func(w *Writer) {
				if c {
					w.AttrBool("hidden", true)
				}
			}},
			{"aria bool", func(w *Writer) { w.Attr("aria-pressed", If(c, false)) }, func(w *Writer) {
				if c {
					w.Raw(` aria-pressed="`)
					w.Bool(false)
					w.Raw(`"`)
				}
			}},
			{"json", func(w *Writer) { w.Attr("data-x", If(c, JSON([]int{1}))) }, func(w *Writer) {
				if c {
					w.Raw(` data-x='`)
					w.JSONAttr("x.lyr:1:1", JSON([]int{1}))
					w.Raw(`'`)
				}
			}},
			{"content", func(w *Writer) { w.Expr(If(c, s)) }, func(w *Writer) {
				if c {
					w.String(s)
				}
			}},
		}
		for _, tt := range tests {
			got, _ := render(tt.unlowered)
			want, _ := render(tt.low)
			if got != want {
				t.Errorf("%s (%v): Attr/Expr %q, lowered %q", tt.name, c, got, want)
			}
		}
	}
}

// TestIfAllocs: PLAN.md §2.6's cost table. If returns an Opt (a struct),
// so passing it unlowered to Attr boxes it: 1 allocation, the untyped
// path's cost ("Typed optional values", Cost); the lowered forms stay 0.
func TestIfAllocs(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf)
	s := string([]byte("noopener")) // not a constant
	c := true
	for _, tt := range []struct {
		name string
		want float64
		f    func()
	}{
		{"constant", 1, func() { w.Attr("aria-current", If(c, "page")) }},
		{"dynamic false", 1, func() { w.Attr("rel", If(!c, s)) }},
		{"int constant", 1, func() { w.Attr("tabindex", If(c, -1)) }},
		{"bool", 1, func() { w.Attr("aria-pressed", If(c, false)) }},
		{"lowered", 0, func() {
			if c {
				w.Raw(` rel="`)
				w.AttrValue(s)
				w.Raw(`"`)
			}
		}},
		{"cond content", 0, func() { w.String(Cond(c, s, "x")) }},
	} {
		if n := testing.AllocsPerRun(100, func() { buf.Reset(); tt.f() }); n != tt.want {
			t.Errorf("%s: %v allocs, want %v", tt.name, n, tt.want)
		}
	}
}

// TestTrueFalseAttrs: a bool writes "true"/"false" on aria-* and HTML's
// true/false enumerated attributes, and keeps the presence rule (bare or
// omitted) everywhere else, through Attr, Spread, SpreadRest and JSON.
func TestTrueFalseAttrs(t *testing.T) {
	tf := []string{"aria-pressed", "ARIA-Hidden", "aria-x", "contenteditable", "Draggable", "spellcheck", "writingsuggestions"}
	presence := []string{"hidden", "disabled", "popover", "download", "data-x", "translate", "autocomplete", "autocorrect", "aria", "aria-", "open", "inert"}
	check := func(name string, b bool, want string) {
		t.Helper()
		got, _ := render(func(w *Writer) { w.Attr(name, b) })
		if got != want {
			t.Errorf("Attr(%q, %v) = %q, want %q", name, b, got, want)
		}
		if got, _ := render(func(w *Writer) { w.Attr(name, flag(b)) }); got != want {
			t.Errorf("Attr(%q, flag(%v)) = %q, want %q", name, b, got, want)
		}
		if got, _ := render(func(w *Writer) { w.Spread(Attrs{name: b}) }); got != want {
			t.Errorf("Spread(%q: %v) = %q, want %q", name, b, got, want)
		}
		r := Rest("", "", RestBool("", name, b))
		if got, _ := render(func(w *Writer) { w.SpreadRest(&r, false) }); got != want {
			t.Errorf("RestBool(%q, %v) = %q, want %q", name, b, got, want)
		}
		r = RestOf(Attrs{name: b})
		if got, _ := render(func(w *Writer) { w.SpreadRest(&r, false) }); got != want {
			t.Errorf("RestOf(%q: %v) = %q, want %q", name, b, got, want)
		}
		r = Rest("", "", RestAny("", name, b))
		if got, _ := render(func(w *Writer) { w.SpreadRest(&r, false) }); got != want {
			t.Errorf("RestAny(%q, %v) = %q, want %q", name, b, got, want)
		}
		// Through JSON (server:defer), and Get.
		js, err := json.Marshal(Rest("", "", RestBool("", name, b)))
		if err != nil {
			t.Fatal(err)
		}
		var back RestAttrs
		if err := json.Unmarshal(js, &back); err != nil {
			t.Fatal(err)
		}
		if got, _ := render(func(w *Writer) { w.SpreadRest(&back, false) }); got != want {
			t.Errorf("JSON round trip of %q: %v = %q (%s), want %q", name, b, got, js, want)
		}
	}
	for _, name := range tf {
		check(name, true, " "+name+`="true"`)
		check(name, false, " "+name+`="false"`)
		if v, ok := RestOf(Attrs{name: false}).Get(name); !ok || v != "false" {
			t.Errorf("Get(%q) = %q, %v", name, v, ok)
		}
	}
	for _, name := range presence {
		check(name, true, " "+name)
		check(name, false, "")
	}
	// AttrBool keeps the presence rule on every attribute.
	if got, _ := render(func(w *Writer) { w.AttrBool("aria-pressed", true) }); got != " aria-pressed" {
		t.Errorf("AttrBool = %q", got)
	}
	// A string is written as is.
	if got, _ := render(func(w *Writer) { w.Attr("hidden", "until-found") }); got != ` hidden="until-found"` {
		t.Errorf("hidden string = %q", got)
	}
}

// BenchmarkIf compares a lowered lyr.If (an if around pieces) with the
// unlowered call through Attr, for a dynamic string, both conditions.
func BenchmarkIf(b *testing.B) {
	var buf bytes.Buffer
	w := NewWriter(&buf)
	s := string([]byte("noopener"))
	for _, c := range []bool{true, false} {
		name := map[bool]string{true: "true", false: "false"}[c]
		b.Run("lowered/"+name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				buf.Reset()
				if c, v := c, s; c {
					w.Raw(` rel="`)
					w.String(v)
					w.Raw(`"`)
				}
			}
		})
		b.Run("attr/"+name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				buf.Reset()
				w.Attr("rel", If(c, s))
			}
		})
	}
}
