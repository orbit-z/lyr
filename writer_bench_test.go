package orbit

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

const (
	benchClean   = "A typical card title with no special characters in it"
	benchEscaped = `Tom & Jerry's "<great>" adventure`
)

// benchWriter returns a Writer over a pre-grown buffer, reset when full so
// the benchmark measures writes rather than growth.
func benchWriter() (*Writer, *bytes.Buffer) {
	buf := bytes.NewBuffer(make([]byte, 0, 1<<20))
	return NewWriter(buf), buf
}

func bench(b *testing.B, f func(w *Writer)) {
	w, buf := benchWriter()
	b.ReportAllocs()
	for b.Loop() {
		if buf.Len() > 1<<19 {
			buf.Reset()
		}
		f(w)
	}
}

func BenchmarkRaw(b *testing.B)           { bench(b, func(w *Writer) { w.Raw(benchClean) }) }
func BenchmarkStringClean(b *testing.B)   { bench(b, func(w *Writer) { w.String(benchClean) }) }
func BenchmarkStringEscaped(b *testing.B) { bench(b, func(w *Writer) { w.String(benchEscaped) }) }
func BenchmarkInt(b *testing.B)           { bench(b, func(w *Writer) { w.Int(1234567) }) }
func BenchmarkFloat(b *testing.B)         { bench(b, func(w *Writer) { w.Float(3.14159) }) }
func BenchmarkFloat32(b *testing.B)       { bench(b, func(w *Writer) { w.Float32(0.1) }) }
func BenchmarkAttrString(b *testing.B) {
	bench(b, func(w *Writer) { w.AttrString("title", benchClean) })
}
func BenchmarkAttrURL(b *testing.B) {
	bench(b, func(w *Writer) { w.AttrURL("href", "/blog/hello-world") })
}
func BenchmarkURL(b *testing.B) {
	bench(b, func(w *Writer) { w.Raw(` href="`); w.URL("/blog/hello-world"); w.Raw(`"`) })
}
func BenchmarkExprString(b *testing.B)   { bench(b, func(w *Writer) { w.Expr(benchClean) }) }
func BenchmarkExprInt(b *testing.B)      { bench(b, func(w *Writer) { w.Expr(42) }) }
func BenchmarkExprStringer(b *testing.B) { bench(b, func(w *Writer) { w.Expr(time.Second) }) }
func BenchmarkExprSprint(b *testing.B)   { bench(b, func(w *Writer) { w.Expr(slug(benchClean)) }) }

func BenchmarkExprSlice(b *testing.B) {
	v := []string{"a", "b", "c", "d"}
	bench(b, func(w *Writer) { w.Expr(v) })
}

func BenchmarkExprReflectSlice(b *testing.B) {
	v := []slug{"a", "b", "c", "d"}
	bench(b, func(w *Writer) { w.Expr(v) })
}

func BenchmarkAttr(b *testing.B) { bench(b, func(w *Writer) { w.Attr("href", "/blog/hello") }) }

func BenchmarkSpread(b *testing.B) {
	a := Attrs{"id": "main", "class": "card", "hidden": true, "href": "/x", "data-n": 3}
	bench(b, func(w *Writer) { w.Spread(a) })
}

func BenchmarkJSON(b *testing.B) {
	v := map[string]any{"tags": []string{"go", "web"}, "n": 3}
	bench(b, func(w *Writer) { w.JSON("", v) })
}

func BenchmarkEnterLeave(b *testing.B) {
	bench(b, func(w *Writer) {
		if w.Enter("src/components/Card.orbit") == nil {
			w.Leave()
		}
	})
}

// BenchmarkCard renders a small component the way generated code does.
func BenchmarkCard(b *testing.B) {
	title, href := "Hello, world", "/blog/hello-world"
	bench(b, func(w *Writer) {
		if w.Enter("src/components/Card.orbit") != nil {
			return
		}
		w.Raw(`<article class="card" data-o-3fa2c1d0`)
		w.AttrBool("data-featured", true)
		w.Raw(">\n  <a data-o-3fa2c1d0")
		w.AttrURL("href", href)
		w.Raw(">")
		w.String(title)
		w.Raw("</a>\n  <span>")
		w.Int(42)
		w.Raw(" views</span>\n</article>")
		w.Leave()
	})
}

func BenchmarkEscapeLongClean(b *testing.B) {
	s := strings.Repeat(benchClean, 100)
	b.SetBytes(int64(len(s)))
	bench(b, func(w *Writer) { w.String(s) })
}

func BenchmarkEscapeLongEscaped(b *testing.B) {
	s := strings.Repeat(benchEscaped, 100)
	b.SetBytes(int64(len(s)))
	bench(b, func(w *Writer) { w.String(s) })
}

// BenchmarkInjectHead inserts the head tags of a page without </head>
// after its doctype, which moves the whole page.
func BenchmarkInjectHead(b *testing.B) {
	page := "<!DOCTYPE html><html><body>" + strings.Repeat("<p>hi</p>", 1000) + "</body></html>"
	tags := headTags("", []Asset{{Kind: "css", URL: "/_orbit/a.css"}, {Kind: "js", URL: "/_orbit/b.js"}})
	b.ReportAllocs()
	for b.Loop() {
		buf := getBuffer()
		buf.WriteString(page)
		injectHead(buf, tags)
		putBuffer(buf)
	}
}

// BenchmarkJSONAttr: PLAN.md §5c's encoding cost, for a small and a typical
// value, passed by value and by pointer. Run with GOMAXPROCS=1.
func BenchmarkJSONAttr(b *testing.B) {
	small := struct {
		ID    int  `json:"id"`
		Liked bool `json:"liked"`
	}{7, true}
	for _, c := range []struct {
		name string
		v    JSONValue
	}{
		{"small", JSON(small)},
		{"small-ptr", JSON(&small)},
		{"typical", JSON(testPost)},
		{"typical-ptr", JSON(&testPost)},
	} {
		b.Run(c.name, func(b *testing.B) {
			bench(b, func(w *Writer) { w.Raw(` data-post='`); w.JSONAttr("x.orbit:1:1", c.v); w.Raw(`'`) })
		})
	}
	// The same small value as two typed attributes, for comparison.
	b.Run("small-typed-attrs", func(b *testing.B) {
		bench(b, func(w *Writer) {
			w.Raw(` data-id="`)
			w.Int(int64(small.ID))
			w.Raw(`"`)
			w.AttrBool("data-liked", small.Liked)
		})
	})
}
