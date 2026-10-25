package orbit

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"math"
	"strings"
	"testing"
	"time"
)

// render runs f on a fresh Writer and returns the output and sticky error.
func render(f func(w *Writer)) (string, error) {
	var buf bytes.Buffer
	w := NewWriter(&buf)
	f(w)
	return buf.String(), w.Err()
}

type (
	slug   string
	count  int
	flag   bool
	tags   []string
	markup HTML
	myURL  URL
	point  struct{ X, Y int }
)

// ptrStringer has String on the pointer receiver only.
type ptrStringer struct{ N int }

func (p *ptrStringer) String() string { return fmt.Sprintf("ptr<%d>", p.N) }

// nilSafe handles a nil receiver.
type nilSafe struct{}

func (n *nilSafe) String() string {
	if n == nil {
		return "none"
	}
	return "some"
}

// status is a named int with a String method.
type status int

func (s status) String() string { return "status-" + strconvItoa(int(s)) }

func strconvItoa(n int) string { return fmt.Sprint(n) }

// myErr is an error type whose Error method dereferences its receiver.
type myErr struct{ msg string }

func (e *myErr) Error() string { return e.msg }

func TestExpr(t *testing.T) {
	var nilPtrStringer *ptrStringer
	var nilNilSafe *nilSafe
	var nilErr *myErr
	tests := []struct {
		name string
		v    any
		want string
	}{
		{"nil", nil, ""},
		{"HTML raw", HTML("<b>hi</b>"), "<b>hi</b>"},
		{"string", "a < b", "a &lt; b"},
		{"string all escapes", "&<>\"'+\x00", "&amp;&lt;&gt;&#34;&#39;+\uFFFD"},
		{"string unicode", "héllo ★", "héllo ★"},
		{"URL", URL("/a?b=1&c=2"), "/a?b=1&amp;c=2"},
		{"URL javascript is text", URL("javascript:x"), "javascript:x"},
		{"bytes", []byte("<hi>"), "&lt;hi&gt;"},
		{"int", 42, "42"},
		{"int8", int8(-3), "-3"},
		{"int16", int16(300), "300"},
		{"int32", int32(-7), "-7"},
		{"int64 min", int64(math.MinInt64), "-9223372036854775808"},
		{"rune", 'a', "97"},
		{"byte", byte(65), "65"},
		{"uint", uint(7), "7"},
		{"uint16", uint16(7), "7"},
		{"uint32", uint32(7), "7"},
		{"uint64 max", uint64(math.MaxUint64), "18446744073709551615"},
		{"uintptr", uintptr(9), "9"},
		{"float 0.1", 0.1, "0.1"},
		{"float 2.0", 2.0, "2"},
		{"float 1e6", 1e6, "1e+06"},
		{"float 1e-7", 1e-7, "1e-07"},
		{"float NaN", math.NaN(), "NaN"},
		{"float +Inf", math.Inf(1), "+Inf"},
		{"float32 0.1", float32(0.1), "0.1"},
		{"float32 1/3", float32(1.0 / 3), "0.33333334"},
		{"bool true", true, "true"},
		{"bool false", false, "false"},
		{"Renderer", Renderer(func(w *Writer) error { w.Raw("<r>"); return nil }), "<r>"},
		{"func", func(w *Writer) error { w.String("<f>"); return nil }, "&lt;f&gt;"},
		{"nil Renderer", Renderer(nil), ""},
		{"error", errors.New("x < y"), "x &lt; y"},
		{"nil *T error", error(nilErr), "&lt;nil&gt;"},
		{"Stringer", time.Second, "1s"},
		{"named int Stringer", status(2), "status-2"},
		{"pointer Stringer", &ptrStringer{3}, "ptr&lt;3&gt;"},
		{"nil *T Stringer that panics", nilPtrStringer, "&lt;nil&gt;"},
		{"nil *T Stringer that handles nil", nilNilSafe, "none"},
		{"T with String on *T", ptrStringer{4}, "{4}"},
		{"named string", slug("a<b"), "a&lt;b"},
		{"named int", count(3), "3"},
		{"named bool", flag(true), "true"},
		{"named HTML is escaped", markup("<b>"), "&lt;b&gt;"},
		{"named URL", myURL("/a&b"), "/a&amp;b"},
		{"named slice uses fmt", tags{"a", "b"}, "[a b]"},
		{"[]string", []string{"a", "<b>"}, "a&lt;b&gt;"},
		{"[]HTML", []HTML{"<i>", "<b>"}, "<i><b>"},
		{"[]int", []int{1, 2, 3}, "123"},
		{"[]float32", []float32{0.1, 0.2}, "0.10.2"},
		{"[]any", []any{1, "<", HTML("<i>"), nil, true, 0.5}, "1&lt;<i>true0.5"},
		{"nested slices", [][]string{{"a", "b"}, {"<"}}, "ab&lt;"},
		{"deeply nested", []any{[]any{[]int{1}, []any{"x", []HTML{"<y>"}}}}, "1x<y>"},
		{"[]Renderer", []Renderer{
			func(w *Writer) error { w.Raw("1"); return nil },
			func(w *Writer) error { w.Raw("2"); return nil },
		}, "12"},
		{"[]error", []error{errors.New("a"), errors.New("<")}, "a&lt;"},
		{"[]Stringer", []fmt.Stringer{time.Second, time.Minute}, "1s1m0s"},
		{"slice of named", []slug{"a", "&"}, "a&amp;"},
		{"array uses fmt", [2]int{1, 2}, "[1 2]"},
		{"struct", point{1, 2}, "{1 2}"},
		{"map", map[string]int{"a": 1}, "map[a:1]"},
		{"complex", complex(1, 2), "(1+2i)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := render(func(w *Writer) { w.Expr(tt.v) })
			if err != nil {
				t.Fatalf("Err() = %v", err)
			}
			if got != tt.want {
				t.Errorf("Expr(%#v) = %q, want %q", tt.v, got, tt.want)
			}
		})
	}
}

// TestTypedWritesMatchExpr checks that each typed write gives the same bytes
// as Expr, as codegen relies on.
func TestTypedWritesMatchExpr(t *testing.T) {
	tests := []struct {
		name  string
		typed func(w *Writer)
		v     any
	}{
		{"String", func(w *Writer) { w.String("a<&>\"'") }, "a<&>\"'"},
		{"String named", func(w *Writer) { w.String(string(slug("a<b"))) }, slug("a<b")},
		{"Int", func(w *Writer) { w.Int(-42) }, -42},
		{"Int named", func(w *Writer) { w.Int(int64(count(7))) }, count(7)},
		{"Int rune", func(w *Writer) { w.Int('a') }, 'a'},
		{"Uint", func(w *Writer) { w.Uint(42) }, uint(42)},
		{"Uint byte", func(w *Writer) { w.Uint(uint64(byte(65))) }, byte(65)},
		{"Float", func(w *Writer) { w.Float(0.1) }, 0.1},
		{"Float big", func(w *Writer) { w.Float(1e21) }, 1e21},
		{"Float32", func(w *Writer) { w.Float32(0.1) }, float32(0.1)},
		{"Bool", func(w *Writer) { w.Bool(true) }, true},
		{"Bool named", func(w *Writer) { w.Bool(bool(flag(false))) }, flag(false)},
		{"HTML", func(w *Writer) { w.HTML("<b>") }, HTML("<b>")},
		{"Bytes", func(w *Writer) { w.Bytes([]byte("<x>")) }, []byte("<x>")},
		{"Error", func(w *Writer) { w.Error(errors.New("<e>")) }, errors.New("<e>")},
		{"Error nil", func(w *Writer) { w.Error(nil) }, nil},
		{"Stringer", func(w *Writer) { w.Stringer(status(1)) }, status(1)},
		{"Stringer nil", func(w *Writer) { w.Stringer(nil) }, nil},
		{"Render", func(w *Writer) { w.Render(func(w *Writer) error { w.Raw("<r>"); return nil }) },
			Renderer(func(w *Writer) error { w.Raw("<r>"); return nil })},
		{"Render nil", func(w *Writer) { w.Render(nil) }, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			typed, _ := render(tt.typed)
			generic, _ := render(func(w *Writer) { w.Expr(tt.v) })
			if typed != generic {
				t.Errorf("typed = %q, Expr = %q", typed, generic)
			}
			if fmtOut := fmt.Sprint(tt.v); tt.v != nil && !isFunc(tt.v) {
				// Expr prints the same text as fmt.Sprint, escaped.
				var b bytes.Buffer
				escapeString(&b, fmtOut)
				switch tt.v.(type) {
				case HTML, []byte:
					return
				}
				if b.String() != generic {
					t.Errorf("Expr = %q, escaped fmt.Sprint = %q", generic, b.String())
				}
			}
		})
	}
}

func isFunc(v any) bool {
	switch v.(type) {
	case Renderer, func(*Writer) error:
		return true
	}
	return false
}

func TestRendererErrorPropagates(t *testing.T) {
	boom := errors.New("boom")
	inner := Renderer(func(w *Writer) error { w.Raw("in"); return boom })
	outer := Renderer(func(w *Writer) error {
		w.Raw("<a>")
		w.Expr(inner)
		w.Raw("never")
		return w.Err()
	})
	got, err := render(func(w *Writer) {
		w.Render(outer)
		w.Raw("after")
	})
	if !errors.Is(err, boom) {
		t.Fatalf("Err() = %v, want boom", err)
	}
	if got != "<a>in" {
		t.Errorf("output = %q, want %q", got, "<a>in")
	}
}

func TestStickyError(t *testing.T) {
	first := errors.New("first")
	got, err := render(func(w *Writer) {
		w.Raw("ok")
		w.Render(func(*Writer) error { return first })
		w.Render(func(*Writer) error { return errors.New("second") })
		w.Raw("x")
		w.String("x")
		w.Int(1)
		w.Uint(1)
		w.Float(1)
		w.Float32(1)
		w.Bool(true)
		w.HTML("x")
		w.Bytes([]byte("x"))
		w.Error(errors.New("x"))
		w.Stringer(time.Second)
		w.Expr("x")
		w.JSON("x")
		w.Attr("a", "x")
		w.AttrString("a", "x")
		w.AttrURL("a", "x")
		w.AttrBool("a", true)
		w.Spread(Attrs{"a": "x"})
		if e := w.Enter("C"); e != first {
			t.Errorf("Enter after error = %v, want first", e)
		}
	})
	if err != first {
		t.Errorf("Err() = %v, want first", err)
	}
	if got != "ok" {
		t.Errorf("output = %q, want %q", got, "ok")
	}
}

func TestJSON(t *testing.T) {
	tests := []struct {
		name string
		v    any
		want string
	}{
		{"script breakout", "</script><!--", `"\u003c/script\u003e\u003c!--"`},
		{"amp and separators", "a&b\u2028\u2029", `"a\u0026b\u2028\u2029"`},
		{"map", map[string]any{"tags": []string{"go", "<b>"}}, `{"tags":["go","\u003cb\u003e"]}`},
		{"struct tags", struct {
			Name string `json:"name"`
		}{"x"}, `{"name":"x"}`},
		{"Marshaler output escaped", json.RawMessage(`"</script>"`), `"\u003c/script\u003e"`},
		{"nil", nil, "null"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := render(func(w *Writer) { w.JSON(tt.v) })
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Errorf("JSON = %s, want %s", got, tt.want)
			}
			if strings.ContainsAny(got, "<>&\u2028\u2029") {
				t.Errorf("JSON output has an unescaped character: %s", got)
			}
		})
	}

	got, err := render(func(w *Writer) { w.Raw("a"); w.JSON(make(chan int)); w.Raw("b") })
	var ute *json.UnsupportedTypeError
	if !errors.As(err, &ute) || !strings.HasPrefix(err.Error(), "orbit: set:json: ") {
		t.Errorf("JSON(chan) error = %v", err)
	}
	if got != "a" {
		t.Errorf("output after JSON error = %q, want %q", got, "a")
	}
}

func TestAttr(t *testing.T) {
	const bad = ` href="about:invalid#orbit"`
	tests := []struct {
		name  string
		attr  string
		v     any
		want  string
		attrs string
	}{
		{"true is bare", "disabled", true, " disabled", ""},
		{"false omitted", "disabled", false, "", ""},
		{"nil omitted", "title", nil, "", ""},
		{"named bool true", "data-on", flag(true), " data-on", ""},
		{"named bool false", "data-on", flag(false), "", ""},
		{"string", "title", `a"b<c>&'`, ` title="a&#34;b&lt;c&gt;&amp;&#39;"`, ""},
		{"empty string", "alt", "", ` alt=""`, ""},
		{"HTML is escaped", "title", HTML("<b>"), ` title="&lt;b&gt;"`, ""},
		{"int", "n", 3, ` n="3"`, ""},
		{"int64", "n", int64(-3), ` n="-3"`, ""},
		{"int8", "n", int8(3), ` n="3"`, ""},
		{"uint", "n", uint(3), ` n="3"`, ""},
		{"float", "n", 0.5, ` n="0.5"`, ""},
		{"float32", "n", float32(0.1), ` n="0.1"`, ""},
		{"bytes", "n", []byte("<"), ` n="&lt;"`, ""},
		{"Stringer", "n", time.Second, ` n="1s"`, ""},
		{"error", "n", errors.New(`"x"`), ` n="&#34;x&#34;"`, ""},
		{"slice concatenated", "class", []string{"a", " b<"}, ` class="a b&lt;"`, ""},
		{"nested slice", "class", []any{"a", []string{" ", "b"}, 1}, ` class="a b1"`, ""},
		{"named string", "title", slug("<"), ` title="&lt;"`, ""},
		{"Renderer output is escaped", "title", Renderer(func(w *Writer) error { w.Raw("<b>"); return nil }),
			` title="&lt;b&gt;"`, ""},
		{"struct", "title", point{1, 2}, ` title="{1 2}"`, ""},

		{"javascript", "href", "javascript:alert(1)", bad, ""},
		{"mixed case and space", "href", " JaVaScRiPt:alert(1)", bad, ""},
		{"leading control char", "href", "\x01javascript:", bad, ""},
		{"leading DEL and newline", "href", "\x7f\n javascript:x", bad, ""},
		{"tab inside scheme", "href", "java\tscript:x", bad, ""},
		{"newline inside scheme", "href", "javas\ncript:x", bad, ""},
		{"vbscript", "href", "VBScript:x", bad, ""},
		{"uppercase attr name", "HREF", "javascript:x", ` HREF="about:invalid#orbit"`, ""},
		{"src", "src", "javascript:x", ` src="about:invalid#orbit"`, ""},
		{"xlink:href", "xlink:href", "javascript:x", ` xlink:href="about:invalid#orbit"`, ""},
		{"formaction", "formaction", "javascript:x", ` formaction="about:invalid#orbit"`, ""},
		{"HTML in href filtered", "href", HTML("javascript:x"), bad, ""},
		{"named string filtered", "href", slug("javascript:x"), bad, ""},
		{"named URL filtered", "href", myURL("javascript:x"), bad, ""},
		{"Stringer filtered", "href", fakeStringer("javascript:x"), bad, ""},
		{"slice filtered as a whole", "href", []string{"java", "script:x"}, bad, ""},
		{"orbit.URL passes", "href", URL("javascript:ok()"), ` href="javascript:ok()"`, ""},
		{"orbit.URL still escaped", "href", URL(`/a?b=1&c="2"`), ` href="/a?b=1&amp;c=&#34;2&#34;"`, ""},
		{"safe URL", "href", "https://example.com/?a=1&b=2", ` href="https://example.com/?a=1&amp;b=2"`, ""},
		{"relative URL", "href", "/javascript:x", ` href="/javascript:x"`, ""},
		{"not a scheme", "href", "javascript", ` href="javascript"`, ""},
		{"not a URL attr", "title", "javascript:x", ` title="javascript:x"`, ""},
		{"data-href not a URL attr", "data-href", "javascript:x", ` data-href="javascript:x"`, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := render(func(w *Writer) { w.Attr(tt.attr, tt.v) })
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Errorf("Attr(%q, %#v) = %q, want %q", tt.attr, tt.v, got, tt.want)
			}
			// Spread gives the same output for a single entry.
			got, err = render(func(w *Writer) { w.Spread(Attrs{tt.attr: tt.v}) })
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Errorf("Spread({%q: %#v}) = %q, want %q", tt.attr, tt.v, got, tt.want)
			}
		})
	}
}

type fakeStringer string

func (f fakeStringer) String() string { return string(f) }

func TestAttrRendererError(t *testing.T) {
	boom := errors.New("boom")
	got, err := render(func(w *Writer) {
		w.Raw("<a")
		w.Attr("title", Renderer(func(w *Writer) error { w.Raw("x"); return boom }))
		w.Raw(">")
	})
	if err != boom {
		t.Errorf("Err() = %v, want boom", err)
	}
	if got != "<a" {
		t.Errorf("output = %q, want %q", got, "<a")
	}
}

func TestTypedAttrs(t *testing.T) {
	tests := []struct {
		name string
		f    func(w *Writer)
		want string
	}{
		{"AttrString", func(w *Writer) { w.AttrString("title", `<"x">`) }, ` title="&lt;&#34;x&#34;&gt;"`},
		{"AttrString no filter", func(w *Writer) { w.AttrString("href", "javascript:x") }, ` href="javascript:x"`},
		{"AttrURL safe", func(w *Writer) { w.AttrURL("href", "/a&b") }, ` href="/a&amp;b"`},
		{"AttrURL filtered", func(w *Writer) { w.AttrURL("href", " JaVaScRiPt:alert(1)") }, ` href="about:invalid#orbit"`},
		{"AttrURL control", func(w *Writer) { w.AttrURL("src", "\x01javascript:") }, ` src="about:invalid#orbit"`},
		{"AttrBool true", func(w *Writer) { w.AttrBool("hidden", true) }, " hidden"},
		{"AttrBool false", func(w *Writer) { w.AttrBool("hidden", false) }, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := render(tt.f)
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSpread(t *testing.T) {
	got, err := render(func(w *Writer) {
		w.Raw("<a")
		w.Spread(Attrs{
			"title":      "t",
			"b":          1,
			"a":          "x",
			"hidden":     true,
			"disabled":   false,
			"href":       "javascript:x",
			"_x":         nil,
			"data-y.z":   "<",
			":c":         URL("javascript:ok"),
			"xlink:href": "javascript:x",
		})
		w.Raw(">")
	})
	if err != nil {
		t.Fatal(err)
	}
	want := `<a :c="javascript:ok" a="x" b="1" data-y.z="&lt;" hidden href="about:invalid#orbit" title="t" xlink:href="about:invalid#orbit">`
	if got != want {
		t.Errorf("Spread =\n%s\nwant\n%s", got, want)
	}

	// Many entries (more than the stack array) stay sorted.
	big := Attrs{}
	for i := range 40 {
		big[fmt.Sprintf("a%02d", i)] = i
	}
	got, _ = render(func(w *Writer) { w.Spread(big) })
	if !strings.HasPrefix(got, ` a00="0" a01="1" a02="2"`) || !strings.HasSuffix(got, ` a39="39"`) {
		t.Errorf("big Spread = %q", got)
	}

	if got, err := render(func(w *Writer) { w.Spread(nil) }); got != "" || err != nil {
		t.Errorf("Spread(nil) = %q, %v", got, err)
	}
}

func TestSpreadInvalidName(t *testing.T) {
	for _, name := range []string{"", "a b", `a"`, "a'", "a>", "a/", "a=b", "1a", "-a", ".a", "a<", "é", "a\x00"} {
		t.Run(fmt.Sprintf("%q", name), func(t *testing.T) {
			got, err := render(func(w *Writer) {
				w.Raw("<a")
				w.Spread(Attrs{"ok": "1", name: "x"})
				w.Raw(">")
			})
			if err == nil {
				t.Fatal("no error")
			}
			want := fmt.Sprintf("orbit: invalid attribute name %q in spread", name)
			if err.Error() != want {
				t.Errorf("error = %q, want %q", err, want)
			}
			if got != "<a" {
				t.Errorf("output = %q, want %q (nothing written)", got, "<a")
			}
		})
	}
	for _, name := range []string{"a", "_", ":", "data-x", "x.y", "xlink:href", "A9", "a-b_c:d.e"} {
		if !validAttrName(name) {
			t.Errorf("validAttrName(%q) = false", name)
		}
	}
}

func TestDepthGuard(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf)
	entered := 0
	var a, b func() error
	a = func() error {
		if err := w.Enter("src/components/A.orbit"); err != nil {
			return err
		}
		defer w.Leave()
		entered++
		return b()
	}
	b = func() error {
		if err := w.Enter("src/components/B.orbit"); err != nil {
			return err
		}
		defer w.Leave()
		entered++
		return a()
	}
	err := a()
	if err == nil {
		t.Fatal("no error from runaway recursion")
	}
	if entered != maxDepth {
		t.Errorf("entered %d components, want %d", entered, maxDepth)
	}
	const prefix = "orbit: render depth exceeded 1000: src/components/A.orbit > src/components/B.orbit > src/components/A.orbit > "
	if msg := err.Error(); !strings.HasPrefix(msg, prefix) || !strings.HasSuffix(msg, " > …") {
		t.Errorf("error = %q", msg)
	}
	if w.Err() != err {
		t.Errorf("Err() = %v, want the depth error", w.Err())
	}
	if len(w.stack) != 0 {
		t.Errorf("stack not unwound: %d entries", len(w.stack))
	}

	// A depth of exactly 1000 is fine.
	w = NewWriter(&buf)
	for i := range maxDepth {
		if err := w.Enter("C"); err != nil {
			t.Fatalf("Enter #%d: %v", i+1, err)
		}
	}
	for range maxDepth {
		w.Leave()
	}
	if w.Err() != nil {
		t.Fatal(w.Err())
	}
}

func TestHeadEndFirstOnly(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf)
	if w.headOffset() != -1 {
		t.Fatalf("headOffset = %d, want -1", w.headOffset())
	}
	w.Raw("<head>")
	w.HeadEnd()
	w.Raw("</head><template><head>")
	w.HeadEnd()
	if w.headOffset() != 6 {
		t.Errorf("headOffset = %d, want 6", w.headOffset())
	}
}

func TestInjectHead(t *testing.T) {
	assets := []Asset{
		{Kind: "css", URL: "/_orbit/a.css"},
		{Kind: "js", URL: "/_orbit/b.js?x=1&y=2"},
		{Kind: "lazy", URL: "/_orbit/lazy.js", ID: "c1"},
		{Kind: "css", URL: "/_orbit/c.css"},
	}
	const tags = `<link rel="stylesheet" href="/_orbit/a.css">` +
		`<script type="module" src="/_orbit/b.js?x=1&amp;y=2"></script>` +
		`<link rel="stylesheet" href="/_orbit/c.css">`
	tests := []struct {
		name   string
		write  func(w *Writer)
		assets []Asset
		want   string
	}{
		{"marker", func(w *Writer) {
			w.Raw("<!DOCTYPE html><html><head><title>x</title>")
			w.HeadEnd()
			w.Raw("</head><body></head></body></html>")
		}, assets, "<!DOCTYPE html><html><head><title>x</title>" + tags + "</head><body></head></body></html>"},
		{"marker at start", func(w *Writer) {
			w.HeadEnd()
			w.Raw("</head>")
		}, assets, tags + "</head>"},
		{"doctype, no marker", func(w *Writer) { w.Raw("<!doctype html>\n<p>hi</p>") }, assets,
			"<!doctype html>" + tags + "\n<p>hi</p>"},
		{"doctype after whitespace", func(w *Writer) { w.Raw("\n  <!DocType html>\n<p>") }, assets,
			"\n  <!DocType html>" + tags + "\n<p>"},
		{"neither", func(w *Writer) { w.Raw("<p>hi</p>") }, assets, tags + "<p>hi</p>"},
		{"empty page", func(w *Writer) {}, assets, tags},
		{"unterminated doctype", func(w *Writer) { w.Raw("<!DOCTYPE html") }, assets, tags + "<!DOCTYPE html"},
		{"no tag assets", func(w *Writer) { w.Raw("<p>") }, []Asset{{Kind: "lazy", URL: "/x.js", ID: "a"}}, "<p>"},
		{"no assets", func(w *Writer) { w.Raw("<p>") }, nil, "<p>"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			buf := getBuffer()
			defer putBuffer(buf)
			w := NewWriter(buf)
			w.tags = headTags("", tt.assets)
			tt.write(w)
			w.finishHead()
			if got := buf.String(); got != tt.want {
				t.Errorf("got\n%s\nwant\n%s", got, tt.want)
			}
		})
	}
}

func TestBufferPool(t *testing.T) {
	b := getBuffer()
	b.WriteString("leftover")
	putBuffer(b)
	if b := getBuffer(); b.Len() != 0 {
		t.Errorf("pooled buffer not reset: %q", b.String())
	}
	big := bytes.NewBuffer(make([]byte, 0, maxPooledBuffer+1))
	putBuffer(big) // dropped; must not panic
}

func TestWriterReset(t *testing.T) {
	var a, b bytes.Buffer
	w := NewWriter(&a)
	w.Enter("X")
	w.HeadEnd()
	w.Render(func(*Writer) error { return errors.New("x") })
	w.reset(&b)
	if w.Err() != nil || w.headOffset() != -1 || len(w.stack) != 0 {
		t.Fatalf("reset left state: err=%v head=%d stack=%v", w.Err(), w.headOffset(), w.stack)
	}
	w.Raw("y")
	if b.String() != "y" {
		t.Errorf("reset writer wrote %q", b.String())
	}
}

func TestZeroAllocs(t *testing.T) {
	buf := bytes.NewBuffer(make([]byte, 0, 1<<16))
	w := NewWriter(buf)
	const clean = "Hello, world. This string needs no escaping at all."
	cleanBytes := []byte(clean)
	attrs := Attrs{"id": "main", "class": "card", "hidden": true}
	tests := []struct {
		name string
		f    func()
	}{
		{"Raw", func() { w.Raw(clean) }},
		{"String", func() { w.String(clean) }},
		{"String escaped", func() { w.String("a < b & c") }},
		{"AttrString", func() { w.AttrString("title", clean) }},
		{"AttrURL", func() { w.AttrURL("href", "/blog/hello") }},
		{"AttrBool", func() { w.AttrBool("disabled", true) }},
		{"Bytes", func() { w.Bytes(cleanBytes) }},
		{"Int", func() { w.Int(123456) }},
		{"Uint", func() { w.Uint(123456) }},
		{"Float", func() { w.Float(3.25) }},
		{"Float32", func() { w.Float32(0.1) }},
		{"Bool", func() { w.Bool(true) }},
		{"HTML", func() { w.HTML("<b>x</b>") }},
		{"Expr string", func() { w.Expr(clean) }},
		{"Attr string", func() { w.Attr("title", clean) }},
		{"Spread", func() { w.Spread(attrs) }},
		{"Enter/Leave", func() { w.Enter("C"); w.Leave() }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			allocs := testing.AllocsPerRun(100, func() {
				buf.Reset()
				tt.f()
			})
			if allocs != 0 {
				t.Errorf("%v allocs per run, want 0", allocs)
			}
		})
	}
}

// FuzzEscape checks that escaped text and attribute values never contain a
// raw <, >, ", ' or a bare &, and that unescaping gives back the input.
func FuzzEscape(f *testing.F) {
	for _, s := range []string{"", "plain", `<script>alert("x")</script>`, "a&amp;b", "\x00", "'+'", "é★\xff", `"><img src=x>`} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		var a, b bytes.Buffer
		escapeString(&a, s)
		escapeBytes(&b, []byte(s))
		out := a.String()
		if out != b.String() {
			t.Fatalf("escapeString = %q, escapeBytes = %q", out, b.String())
		}
		checkEscaped(t, out)
		if want := strings.ReplaceAll(s, "\x00", "\uFFFD"); html.UnescapeString(out) != want {
			t.Fatalf("unescape(%q) = %q, want %q", out, html.UnescapeString(out), want)
		}

		attr, _ := render(func(w *Writer) { w.AttrString("x", s) })
		if want := ` x="` + out + `"`; attr != want {
			t.Fatalf("AttrString = %q, want %q", attr, want)
		}
		expr, _ := render(func(w *Writer) { w.Attr("x", []any{s, []byte(s)}) })
		checkEscaped(t, strings.TrimSuffix(strings.TrimPrefix(expr, ` x="`), `"`))
	})
}

func checkEscaped(t *testing.T, out string) {
	t.Helper()
	if strings.ContainsAny(out, "<>\"'") {
		t.Fatalf("unescaped character in %q", out)
	}
	for i := 0; i < len(out); i++ {
		if out[i] != '&' {
			continue
		}
		rest := out[i:]
		ok := false
		for _, e := range []string{"&amp;", "&lt;", "&gt;", "&#34;", "&#39;"} {
			if strings.HasPrefix(rest, e) {
				ok = true
				break
			}
		}
		if !ok {
			t.Fatalf("bare & at %d in %q", i, out)
		}
	}
}
