package benchgo

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/bertrandkhe/orbit"

	"github.com/bertrandkhe/orbit/bench/go/data"
	"github.com/bertrandkhe/orbit/bench/go/htmltpl"
	"github.com/bertrandkhe/orbit/bench/go/orbit_gen/app"
	"github.com/bertrandkhe/orbit/bench/go/orbit_gen/decl/pages/cards"
	"github.com/bertrandkhe/orbit/bench/go/templpage"
)

// The three engines render the same cards page into a reused buffer.

func renderOrbit(c *orbit.Ctx, w *orbit.Writer) error {
	return cards.Render(c, w, cards.Props{}, orbit.Slots{})
}

func renderTempl(buf *bytes.Buffer) error {
	return templpage.Cards(data.Items).Render(context.Background(), buf)
}

var htmlPage = &htmltpl.Page{Title: data.PageTitle, Nav: data.Nav, Items: data.Items}

func renderHTMLTemplate(buf *bytes.Buffer) error {
	return htmltpl.Render(buf, htmlPage)
}

var (
	betweenTags = regexp.MustCompile(`>\s+`)
	beforeTags  = regexp.MustCompile(`\s+<`)
	spaces      = regexp.MustCompile(`\s+`)
	doctype     = regexp.MustCompile(`(?i)^<!doctype html>`)
)

// normalize removes whitespace next to tags, collapses the rest and
// uppercases the doctype (templ writes <!doctype html>), so outputs that
// differ only in formatting compare equal.
func normalize(s string) string {
	s = betweenTags.ReplaceAllString(s, ">")
	s = beforeTags.ReplaceAllString(s, "<")
	s = strings.TrimSpace(spaces.ReplaceAllString(s, " "))
	return doctype.ReplaceAllString(s, "<!DOCTYPE html>")
}

func render(t *testing.T) (orbitHTML, templHTML, stdHTML string) {
	t.Helper()
	var buf bytes.Buffer
	if err := renderOrbit(new(orbit.Ctx), orbit.NewWriter(&buf)); err != nil {
		t.Fatal(err)
	}
	orbitHTML = buf.String()
	buf.Reset()
	if err := renderTempl(&buf); err != nil {
		t.Fatal(err)
	}
	templHTML = buf.String()
	buf.Reset()
	if err := renderHTMLTemplate(&buf); err != nil {
		t.Fatal(err)
	}
	return orbitHTML, templHTML, buf.String()
}

// TestEquivalent checks that the engines produce the same page, modulo
// whitespace, so the benchmarks compare the same work.
func TestEquivalent(t *testing.T) {
	o, tp, h := render(t)
	o, tp, h = normalize(o), normalize(tp), normalize(h)
	if n := strings.Count(o, `<article class="card"`); n != data.NumCards {
		t.Fatalf("orbit rendered %d cards, want %d", n, data.NumCards)
	}
	for _, want := range []string{
		"<title>500 cards &amp; more</title>",
		`<article class="card" data-id="0" data-featured><h2><a href="/items/0">Card number 0</a></h2>`,
		`<p class="body">Tom &amp; Jerry &lt;7&gt; chase each other &gt; twice.</p>`,
		`<span class="count">37</span><div class="extra"><em>#1</em></div>`,
	} {
		if !strings.Contains(o, want) {
			t.Errorf("orbit output lacks %q", want)
		}
	}
	for name, got := range map[string]string{"templ": tp, "html/template": h} {
		if got != o {
			i := 0
			for i < len(got) && i < len(o) && got[i] == o[i] {
				i++
			}
			lo := max(0, i-80)
			t.Errorf("%s differs from orbit at byte %d:\n orbit: %q\n %s: %q", name, i,
				o[lo:min(len(o), i+80)], name, got[lo:min(len(got), i+80)])
		}
	}
}

// TestServe checks the page served through Site.Middleware.
func TestServe(t *testing.T) {
	h := app.New().Middleware(http.NotFoundHandler())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/cards", nil))
	o, _, _ := render(t)
	if rec.Code != 200 || rec.Body.String() != o {
		t.Fatalf("GET /cards: %d, body equal to render: %v", rec.Code, rec.Body.String() == o)
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/post/hello-world", nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "<title>hello-world</title>") {
		t.Fatalf("GET /post/hello-world: %d\n%s", rec.Code, rec.Body)
	}
}

// Pure render: each engine writes the page into a reused buffer.

func BenchmarkRender(b *testing.B) {
	b.Run("engine=orbit", func(b *testing.B) {
		var buf bytes.Buffer
		w := orbit.NewWriter(&buf)
		c := new(orbit.Ctx)
		b.ReportAllocs()
		for b.Loop() {
			buf.Reset()
			if err := renderOrbit(c, w); err != nil {
				b.Fatal(err)
			}
		}
		b.SetBytes(int64(buf.Len()))
	})
	b.Run("engine=templ", func(b *testing.B) {
		var buf bytes.Buffer
		b.ReportAllocs()
		for b.Loop() {
			buf.Reset()
			if err := renderTempl(&buf); err != nil {
				b.Fatal(err)
			}
		}
		b.SetBytes(int64(buf.Len()))
	})
	b.Run("engine=html-template", func(b *testing.B) {
		var buf bytes.Buffer
		b.ReportAllocs()
		for b.Loop() {
			buf.Reset()
			if err := renderHTMLTemplate(&buf); err != nil {
				b.Fatal(err)
			}
		}
		b.SetBytes(int64(buf.Len()))
	})
}

// fakeResponseWriter is a reusable http.ResponseWriter that discards the
// body, so the serve benchmarks measure Orbit's handler, not net/http.
type fakeResponseWriter struct {
	h      http.Header
	status int
	n      int
}

func (f *fakeResponseWriter) Header() http.Header  { return f.h }
func (f *fakeResponseWriter) WriteHeader(code int) { f.status = code }
func (f *fakeResponseWriter) Write(b []byte) (int, error) {
	f.n += len(b)
	return len(b), nil
}

// BenchmarkServe serves pages through Site.Middleware: routing, the pooled
// Ctx, Writer and buffer, rendering, headers and the body write.
func BenchmarkServe(b *testing.B) {
	h := app.New().Middleware(http.NotFoundHandler())
	for _, path := range []string{"/cards", "/post/hello-world"} {
		b.Run("path="+path, func(b *testing.B) {
			r := httptest.NewRequest("GET", path, nil)
			f := &fakeResponseWriter{h: make(http.Header, 8)}
			b.ReportAllocs()
			for b.Loop() {
				clear(f.h)
				f.n = 0
				h.ServeHTTP(f, r)
				if f.status != 200 {
					b.Fatalf("status %d", f.status)
				}
			}
			b.SetBytes(int64(f.n))
		})
	}
}
