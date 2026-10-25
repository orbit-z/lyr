package orbit

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

type likeProps struct {
	Slug  string
	Likes int
}

// likeButton stands in for a generated component's Render: it reads the
// request's locals and writes a fragment.
func likeButton(c *Ctx, w *Writer, p likeProps, s Slots) error {
	if err := w.Enter("src/components/LikeButton.orbit"); err != nil {
		return err
	}
	defer w.Leave()
	locals := LocalsOf[testLocals](c)
	w.Raw(`<button hx-post="/blog/`)
	w.String(p.Slug)
	w.Raw(`/like" data-theme="`)
	w.String(locals.Theme)
	w.Raw(`">`)
	w.Int(int64(p.Likes))
	w.Raw(` likes</button>`)
	return nil
}

func TestRenderComponent(t *testing.T) {
	r := httptest.NewRequest("POST", "/blog/hello/like", nil)
	r, l := EnsureLocals[testLocals](r)
	l.Theme = "dark"
	rec := httptest.NewRecorder()
	if err := RenderComponent(rec, r, likeButton, likeProps{Slug: "a<b", Likes: 3}); err != nil {
		t.Fatal(err)
	}
	want := `<button hx-post="/blog/a&lt;b/like" data-theme="dark">3 likes</button>`
	if rec.Code != 200 || rec.Body.String() != want {
		t.Errorf("got %d %q, want 200 %q", rec.Code, rec.Body, want)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/html; charset=utf-8" {
		t.Errorf("Content-Type %q", ct)
	}
	if cl := rec.Header().Get("Content-Length"); cl != strconv.Itoa(len(want)) {
		t.Errorf("Content-Length %q, want %d", cl, len(want))
	}
}

func TestRenderComponentNoLocals(t *testing.T) {
	rec := httptest.NewRecorder()
	if err := RenderComponent(rec, httptest.NewRequest("GET", "/", nil), likeButton, likeProps{Slug: "x"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rec.Body.String(), `data-theme=""`) {
		t.Errorf("got %q", rec.Body)
	}
}

func TestRenderComponentStatusAndHeaders(t *testing.T) {
	render := func(c *Ctx, w *Writer, p likeProps, s Slots) error {
		c.SetStatus(http.StatusCreated)
		c.Header().Set("HX-Trigger", "liked")
		c.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Raw("ok")
		return nil
	}
	rec := httptest.NewRecorder()
	if err := RenderComponent(rec, httptest.NewRequest("POST", "/", nil), render, likeProps{}); err != nil {
		t.Fatal(err)
	}
	if rec.Code != 201 || rec.Header().Get("HX-Trigger") != "liked" || rec.Header().Get("Content-Type") != "text/plain; charset=utf-8" || rec.Body.String() != "ok" {
		t.Errorf("got %d %v %q", rec.Code, rec.Header(), rec.Body)
	}
}

func TestRenderComponentHead(t *testing.T) {
	rec := httptest.NewRecorder()
	if err := RenderComponent(rec, httptest.NewRequest("HEAD", "/", nil), likeButton, likeProps{Likes: 1}); err != nil {
		t.Fatal(err)
	}
	if rec.Code != 200 || rec.Body.Len() != 0 || rec.Header().Get("Content-Length") == "0" {
		t.Errorf("got %d %v %q", rec.Code, rec.Header(), rec.Body)
	}
}

func TestRenderComponentErrors(t *testing.T) {
	boom := errors.New("boom")
	tests := []struct {
		name   string
		render func(*Ctx, *Writer, likeProps, Slots) error
		check  func(error) bool
	}{
		{"returned error", func(c *Ctx, w *Writer, p likeProps, s Slots) error {
			c.Header().Set("X-Partial", "1")
			c.SetStatus(418)
			w.Raw("<p>partial")
			return boom
		}, func(err error) bool { return errors.Is(err, boom) }},
		{"writer error", func(c *Ctx, w *Writer, p likeProps, s Slots) error {
			w.Raw("<p>partial")
			w.setErr(boom)
			return nil
		}, func(err error) bool { return errors.Is(err, boom) }},
		{"redirect", func(c *Ctx, w *Writer, p likeProps, s Slots) error {
			return c.Redirect("/login", 303)
		}, func(err error) bool {
			var re *RedirectError
			return errors.As(err, &re) && re.Location == "/login"
		}},
		{"panic", func(c *Ctx, w *Writer, p likeProps, s Slots) error {
			w.Raw("<p>partial")
			panic("kaboom")
		}, func(err error) bool {
			var pe *panicError
			return errors.As(err, &pe) && strings.Contains(err.Error(), "panic: kaboom")
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			err := RenderComponent(rec, httptest.NewRequest("GET", "/", nil), tt.render, likeProps{})
			if err == nil || !tt.check(err) {
				t.Errorf("err = %v", err)
			}
			if rec.Body.Len() != 0 || len(rec.Header()) != 0 || rec.Code != 200 || rec.Flushed {
				t.Errorf("something was written: %d %v %q", rec.Code, rec.Header(), rec.Body)
			}
		})
	}
}

func TestRenderComponentAbortHandler(t *testing.T) {
	defer func() {
		if p := recover(); p != http.ErrAbortHandler {
			t.Errorf("recovered %v, want http.ErrAbortHandler", p)
		}
	}()
	render := func(c *Ctx, w *Writer, p likeProps, s Slots) error { panic(http.ErrAbortHandler) }
	RenderComponent(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil), render, likeProps{})
}

func TestRenderComponentAllocs(t *testing.T) {
	if testing.Short() || raceEnabled {
		t.Skip("allocation counts (not under -short or -race)")
	}
	fw := newFakeResponseWriter()
	r := httptest.NewRequest("POST", "/blog/hello/like", nil)
	r, _ = EnsureLocals[testLocals](r)
	props := likeProps{Slug: "hello", Likes: 42}
	allocs := testing.AllocsPerRun(1000, func() {
		fw.reset()
		if err := RenderComponent(fw, r, likeButton, props); err != nil {
			t.Fatal(err)
		}
	})
	if fw.status != 200 || fw.n == 0 {
		t.Fatalf("fake writer got %d, %d bytes", fw.status, fw.n)
	}
	// The one allocation is the Content-Length value.
	if allocs != 1 {
		t.Errorf("RenderComponent: %v allocs, want 1 (Content-Length)", allocs)
	}
}
