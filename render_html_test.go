package orbit

import (
	"bytes"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
)

// rhCard is a component with class:list, a client:* marker (static Raw) and
// a server:defer placeholder.
func rhCard(c *Ctx, w *Writer, p struct{ Title string }, s Slots) error {
	if err := w.Enter("src/components/Card.orbit"); err != nil {
		return err
	}
	defer w.Leave()
	w.Raw(`<article`)
	w.ClassStart()
	w.Class("card")
	w.ClassAny([]string{"featured"})
	w.ClassEnd()
	w.Raw(`><h2>`)
	w.String(p.Title)
	w.Raw(`</h2><orbit-lazy id="Counter-1c2d3e4f" strategy="visible"><button>0</button></orbit-lazy>`)
	if err := Defer(c, w, commentsID, commentsProps{PostID: 7}, fallbackLoading); err != nil {
		return err
	}
	w.Render(s.Default)
	w.Raw(`</article>`)
	return w.Err()
}

func rhContent(c *Ctx) Renderer {
	return func(w *Writer) error {
		return rhCard(c, w, struct{ Title string }{"a <b>"}, Slots{Default: func(w *Writer) error {
			w.Raw("<p>")
			w.String("slot & text")
			w.Raw("</p>")
			return nil
		}})
	}
}

// rhCtx returns a Ctx paired with a page Writer, as servePage pairs them.
func rhCtx(t *testing.T) (*Ctx, *Writer, *bytes.Buffer) {
	t.Helper()
	c := acquireCtx(httptest.NewRequest("GET", "/", nil), nil, false)
	c.keys = testKeyring()
	var buf bytes.Buffer
	w := NewWriter(&buf)
	c.w = w
	t.Cleanup(func() { releaseCtx(c) })
	return c, w, &buf
}

func TestRenderHTMLSameAsInPlace(t *testing.T) {
	c, w, buf := rhCtx(t)
	w.Render(rhContent(c))
	if w.Err() != nil {
		t.Fatal(w.Err())
	}
	inPlace := buf.String()
	got, err := RenderHTML(c, rhContent(c))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != inPlace || !strings.Contains(inPlace, `<orbit-island src=`) || !strings.Contains(inPlace, `class="card featured"`) {
		t.Errorf("captured:\n%s\nin place:\n%s", got, inPlace)
	}
	if c.w != w {
		t.Error("ctx's writer not restored")
	}
	if got, err := RenderHTML(c, nil); got != "" || err != nil {
		t.Errorf("nil renderer: %q, %v", got, err)
	}
	// A Ctx with no writer (built by hand) and a nil Ctx work too.
	for _, cc := range []*Ctx{{}, nil} {
		got, err := RenderHTML(cc, func(w *Writer) error { w.String("<x>"); return nil })
		if got != "&lt;x&gt;" || err != nil {
			t.Errorf("ctx %v: %q, %v", cc, got, err)
		}
	}
}

// rhRecurse renders itself through a capture, forever.
func rhRecurse(c *Ctx, w *Writer, p struct{}, s Slots) error {
	if err := w.Enter("src/components/Recurse.orbit"); err != nil {
		return err
	}
	defer w.Leave()
	html, err := RenderHTML(c, func(w *Writer) error { return rhRecurse(c, w, p, Slots{}) })
	if err != nil {
		return err
	}
	w.HTML(html)
	return nil
}

func TestRenderHTMLDepth(t *testing.T) {
	c, w, _ := rhCtx(t)
	if err := w.Enter("src/pages/index.orbit"); err != nil {
		t.Fatal(err)
	}
	err := rhRecurse(c, w, struct{}{}, Slots{})
	want := "orbit: render depth exceeded 1000: src/pages/index.orbit > src/components/Recurse.orbit > "
	if err == nil || !strings.HasPrefix(err.Error(), want) {
		t.Fatalf("err = %v, want prefix %q", err, want)
	}
	if c.w != w || len(w.stack) != 1 || w.Err() != nil {
		t.Errorf("after the error: writer restored %v, stack %v, sticky %v", c.w == w, w.stack, w.Err())
	}
}

func TestRenderHTMLNested(t *testing.T) {
	c, w, buf := rhCtx(t)
	w.Raw("page ")
	var inner *Writer
	outer, err := RenderHTML(c, func(ow *Writer) error {
		ow.Raw("<ul>")
		if err := ow.Enter("src/components/List.orbit"); err != nil {
			return err
		}
		defer ow.Leave()
		for i := range 2 {
			card, err := RenderHTML(c, func(cw *Writer) error {
				inner = c.w
				if len(cw.stack) != 1 || cw.stack[0] != "src/components/List.orbit" {
					t.Errorf("nested capture's stack = %v", cw.stack)
				}
				cw.Raw("<li>")
				cw.Int(int64(i))
				cw.Raw("</li>")
				return nil
			})
			if err != nil {
				return err
			}
			if c.w != ow {
				t.Error("inner capture didn't restore the outer capture's writer")
			}
			ow.HTML(card)
		}
		ow.Raw("</ul>")
		return nil
	})
	if err != nil || outer != "<ul><li>0</li><li>1</li></ul>" {
		t.Errorf("got %q, %v", outer, err)
	}
	if inner == nil || inner == w || c.w != w || buf.String() != "page " {
		t.Errorf("writer pointer: inner %p page %p now %p; page buf %q", inner, w, c.w, buf)
	}
}

func TestRenderHTMLError(t *testing.T) {
	c, w, buf := rhCtx(t)
	w.Raw("before ")
	boom := errors.New("boom")
	for name, r := range map[string]Renderer{
		"returned": func(w *Writer) error { w.Raw("partial"); return boom },
		"sticky":   func(w *Writer) error { w.Raw("partial"); w.setErr(boom); return nil },
	} {
		got, err := RenderHTML(c, r)
		if got != "" || !errors.Is(err, boom) {
			t.Errorf("%s: got %q, %v", name, got, err)
		}
	}
	if buf.String() != "before " || w.Err() != nil || c.w != w {
		t.Errorf("page writer touched: %q, %v", buf, w.Err())
	}
	// A panic isn't recovered, but the writer pointer is restored.
	func() {
		defer func() {
			if recover() == nil {
				t.Error("panic was recovered")
			}
		}()
		RenderHTML(c, func(w *Writer) error { panic("kaboom") })
	}()
	if c.w != w {
		t.Error("ctx's writer not restored after a panic")
	}
}

func TestRenderHTMLHeadEnd(t *testing.T) {
	var captureErr error
	routes := []Route{{Pattern: "/", File: "src/pages/index.orbit",
		Assets: []Asset{{Kind: "css", URL: "/_orbit/index-1234.css"}},
		Render: func(c *Ctx, w *Writer) error {
			_, captureErr = RenderHTML(c, func(w *Writer) error {
				w.Raw("<head>")
				w.HeadEnd()
				w.Raw("</head>")
				return nil
			})
			w.Raw("<html><head>")
			w.HeadEnd()
			w.Raw("</head><body></body></html>")
			return nil
		}}}
	rec := do(testSite(t, routes).Handler(), "GET", "/")
	if captureErr == nil || captureErr.Error() != "orbit: </head> can't be inside orbit.RenderHTML; keep the document head outside the captured content" {
		t.Errorf("capture err = %v", captureErr)
	}
	want := `<html><head><link rel="stylesheet" href="/_orbit/index-1234.css"></head><body></body></html>`
	if rec.Code != 200 || rec.Body.String() != want {
		t.Errorf("got %d %q, want %q", rec.Code, rec.Body, want)
	}
}

func TestRenderHTMLAllocs(t *testing.T) {
	if testing.Short() || raceEnabled {
		t.Skip("allocation counts (not under -short or -race)")
	}
	c, w, buf := rhCtx(t)
	content := strings.Repeat("<li>static</li>", 128)
	slot := Renderer(func(w *Writer) error { w.Raw(content); return nil })
	allocs := testing.AllocsPerRun(1000, func() {
		html, err := RenderHTML(c, slot)
		if err != nil || len(html) != len(content) {
			t.Fatal(html, err)
		}
	})
	if allocs != 1 {
		t.Errorf("a miss: %v allocs, want 1 (the string)", allocs)
	}
	cache := map[string]HTML{"k": HTML(content)}
	allocs = testing.AllocsPerRun(1000, func() {
		buf.Reset()
		w.HTML(cache["k"])
	})
	if allocs != 0 {
		t.Errorf("a hit: %v allocs, want 0", allocs)
	}
}

func BenchmarkRenderHTML(b *testing.B) {
	c := acquireCtx(httptest.NewRequest("GET", "/", nil), nil, false)
	defer releaseCtx(c)
	var buf bytes.Buffer
	c.w = NewWriter(&buf)
	content := strings.Repeat("<li>static</li>", 136) // about 2 KB
	slot := Renderer(func(w *Writer) error { w.Raw(content); return nil })
	b.ReportAllocs()
	for b.Loop() {
		if _, err := RenderHTML(c, slot); err != nil {
			b.Fatal(err)
		}
	}
}
