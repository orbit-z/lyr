package lyr

import (
	"encoding/json"
	"errors"
	"html"
	"io"
	"math"
	"strings"
	"testing"
	"time"
)

func TestJSONAttr(t *testing.T) {
	tests := []struct {
		name string
		v    any
		want string
	}{
		{"single quote", "it's", `"it&#39;s"`},
		{"double quote", `say "hi"`, `"say \"hi\""`},
		{"html", "</script><b>&", `"\u003c/script\u003e\u003cb\u003e\u0026"`},
		{"separators", "a\u2028b\u2029", `"a\u2028b\u2029"`},
		{"invalid utf-8", "a\xffb", "\"a\ufffdb\""},
		{"raw message with quote and <", json.RawMessage(`{"a":"'<"}`), `{"a":"&#39;\u003c"}`},
		{"struct", struct {
			ID   int64    `json:"id"`
			Tags []string `json:"tags"`
		}{42, nil}, `{"id":42,"tags":null}`},
		{"false", false, "false"},
		{"nil", nil, "null"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := render(func(w *Writer) { w.JSONAttr("x.lyr:1:1", JSON(tt.v)) })
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Errorf("JSONAttr = %s, want %s", got, tt.want)
			}
			// The fallback writes the same bytes, with the name and quotes.
			attr, err := render(func(w *Writer) { w.Attr("data-x", JSON(tt.v)) })
			if err != nil {
				t.Fatal(err)
			}
			if want := " data-x='" + tt.want + "'"; attr != want {
				t.Errorf("Attr = %s, want %s", attr, want)
			}
			spread, _ := render(func(w *Writer) { w.Spread(Attrs{"data-x": JSON(tt.v)}) })
			if spread != attr {
				t.Errorf("Spread = %s, want %s", spread, attr)
			}
		})
	}
}

func TestJSONValueZero(t *testing.T) {
	got, _ := render(func(w *Writer) { w.Attr("data-x", JSONValue{}) })
	if got != " data-x='null'" {
		t.Errorf("zero JSONValue = %s", got)
	}
	b, err := json.Marshal(struct{ V JSONValue }{JSON([]int{1})})
	if err != nil || string(b) != `{"V":[1]}` {
		t.Errorf("Marshal(JSONValue field) = %s, %v", b, err)
	}
}

func TestJSONContent(t *testing.T) {
	got, err := render(func(w *Writer) { w.Expr(JSON(map[string]string{"a": "it's <b>"})) })
	if err != nil {
		t.Fatal(err)
	}
	if want := `{&quot;a&quot;:&quot;it&#39;s \u003cb\u003e&quot;}`; got != want {
		t.Errorf("Expr(JSON) = %s, want %s", got, want)
	}
	// set:json unwraps a JSONValue.
	got, _ = render(func(w *Writer) { w.JSON("", JSON([]string{"</script>"})) })
	if want := `["\u003c/script\u003e"]`; got != want {
		t.Errorf("JSON(JSONValue) = %s, want %s", got, want)
	}
}

func TestJSONErrors(t *testing.T) {
	tests := []struct {
		name string
		f    func(w *Writer)
		want string
	}{
		{"JSONAttr", func(w *Writer) { w.JSONAttr("src/x.lyr:9:24", JSON(math.NaN())) },
			"src/x.lyr:9:24: lyr.JSON: json: unsupported value: NaN"},
		{"set:json", func(w *Writer) { w.JSON("src/x.lyr:3:40", math.Inf(1)) },
			"src/x.lyr:3:40: set:json: json: unsupported value: +Inf"},
		{"Attr", func(w *Writer) { w.Attr("data-x", JSON(math.NaN())) },
			"lyr: lyr.JSON: json: unsupported value: NaN"},
		{"Expr", func(w *Writer) { w.Expr(JSON(func() {})) },
			"lyr: lyr.JSON: json: unsupported type: func()"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := render(func(w *Writer) { w.Raw("a"); tt.f(w); w.Raw("b") })
			if err == nil || err.Error() != tt.want {
				t.Fatalf("error = %v, want %s", err, tt.want)
			}
			var uve *json.UnsupportedValueError
			var ute *json.UnsupportedTypeError
			if !errors.As(err, &uve) && !errors.As(err, &ute) {
				t.Errorf("error %v doesn't wrap the encoding/json error", err)
			}
			if got != "a" {
				t.Errorf("output = %q, want %q (truncated, then sticky)", got, "a")
			}
		})
	}
	// A positioned error passes through ErrorAt unchanged.
	_, err := render(func(w *Writer) { w.JSONAttr("src/x.lyr:9:24", JSON(math.NaN())) })
	if ErrorAt("src/y.lyr:1:1", err) != err {
		t.Error("ErrorAt re-wrapped a positioned JSON error")
	}
}

// jsonPost is the typical value of PLAN.md §5c's encoding cost.
type jsonPost struct {
	ID        int64       `json:"id"`
	Slug      string      `json:"slug"`
	Title     string      `json:"title"`
	Tags      []string    `json:"tags"`
	Published time.Time   `json:"published"`
	Likes     int         `json:"likes"`
	Author    *jsonAuthor `json:"author,omitempty"`
}

type jsonAuthor struct {
	Name string `json:"name"`
	URL  string `json:"url,omitempty"`
}

var testPost = jsonPost{ID: 42, Slug: "hello", Title: "Hello <world>", Tags: []string{"go"},
	Published: time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC), Likes: 7, Author: &jsonAuthor{Name: "Ada"}}

// TestJSONAttrAllocs: JSONAttr adds no allocation beyond encoding/json's own
// (PLAN.md §5c Cost): 0 for a pointer, the same as an Encode to io.Discard
// for a value.
func TestJSONAttrAllocs(t *testing.T) {
	if raceEnabled {
		t.Skip("allocation counts aren't meaningful under -race")
	}
	w, buf := benchWriter()
	w.JSONAttr("", JSON(&testPost)) // creates the reused encoder
	ptr := JSON(&testPost)
	if n := testing.AllocsPerRun(1000, func() { buf.Reset(); w.JSONAttr("", ptr) }); n != 0 {
		t.Errorf("JSONAttr(pointer) allocs = %v, want 0", n)
	}
	val := JSON(testPost)
	enc := json.NewEncoder(io.Discard)
	base := testing.AllocsPerRun(1000, func() { enc.Encode(val.v) })
	if n := testing.AllocsPerRun(1000, func() { buf.Reset(); w.JSONAttr("", val) }); n != base {
		t.Errorf("JSONAttr(value) allocs = %v, want %v (encoding/json's own)", n, base)
	}
	if n := testing.AllocsPerRun(1000, func() { buf.Reset(); w.JSON("", ptr.v) }); n != 0 {
		t.Errorf("JSON(pointer) allocs = %v, want 0", n)
	}
}

// FuzzJSONAttr: the attribute value can't be broken out of, and the
// browser's attribute decoding gives back the exact JSON text.
func FuzzJSONAttr(f *testing.F) {
	for _, s := range []string{"", "it's", `"<&>'`, "  ", "a\xffb", "&#39;", "</script>"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		for _, v := range []any{s, map[string]any{s: []string{s}}, json.RawMessage(s)} {
			want, merr := json.Marshal(v)
			got, err := render(func(w *Writer) { w.JSONAttr("", JSON(v)) })
			if (merr == nil) != (err == nil) {
				t.Fatalf("JSONAttr(%q) error = %v, json.Marshal error = %v", s, err, merr)
			}
			if err != nil {
				continue
			}
			if strings.ContainsAny(got, `'<>`) {
				t.Fatalf("JSONAttr(%q) = %s: an unescaped ', < or >", s, got)
			}
			if dec := html.UnescapeString(got); dec != string(want) {
				t.Fatalf("decode(JSONAttr(%q)) = %s, want %s", s, dec, want)
			}
		}
	})
}
