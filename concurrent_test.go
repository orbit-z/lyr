package lyr

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// The tests of server:concurrent (PLAN.md §3.6). The components are written
// the way codegen writes them: Enter/Leave, typed writes, slot closures
// that rebind their Ctx with CtxOf, and each call site either a direct call
// or Concurrent, depending on mark.

// cProps drives the test component cComp.
type cProps struct {
	Name   string
	Sleep  time.Duration
	Err    error // returned after the output
	Panic  any
	Status int
	Header string // "Key: value", set with ctx.Header().Set
	Head   bool   // write a </head> marker
	// Kids are rendered inside, each marked if Mark is set.
	Kids []cProps
	Mark bool
	// Depth recurses into cComp itself (<Self>) Depth more times.
	Depth int
}

var cRunning, cMaxRunning atomic.Int32

func cComp(c *Ctx, w *Writer, p cProps, s Slots) error {
	if err := w.Enter("src/components/C.lyr"); err != nil {
		return err
	}
	defer w.Leave()
	n := cRunning.Add(1)
	for {
		m := cMaxRunning.Load()
		if n <= m || cMaxRunning.CompareAndSwap(m, n) {
			break
		}
	}
	defer cRunning.Add(-1)
	if p.Sleep > 0 {
		time.Sleep(p.Sleep)
	}
	if p.Panic != nil {
		panic(p.Panic)
	}
	if p.Status != 0 {
		c.SetStatus(p.Status)
	}
	if k, v, ok := strings.Cut(p.Header, ": "); ok {
		c.Header().Set(k, v)
	}
	w.Raw("<c")
	if p.Name != "" {
		w.Raw(" " + p.Name)
	}
	w.Raw(">")
	if p.Head {
		w.HeadEnd()
		w.Raw("</head>")
	}
	w.Render(s.Default)
	for _, k := range p.Kids {
		if err := call(p.Mark, c, w, cComp, k, Slots{}); err != nil {
			return err
		}
	}
	if p.Depth > 0 {
		q := p
		q.Depth--
		q.Name = ""
		if err := call(p.Mark, c, w, cComp, q, Slots{}); err != nil {
			return err
		}
	}
	w.Raw("</c>")
	if p.Err != nil {
		return p.Err
	}
	return w.Err()
}

// call is a call site: Concurrent when marked, a plain call otherwise.
func call[P any](mark bool, c *Ctx, w *Writer, render func(*Ctx, *Writer, P, Slots) error, p P, s Slots) error {
	if mark {
		return Concurrent(c, w, render, p, s)
	}
	return render(c, w, p, s)
}

// trackForks counts the child Writers taken from and returned to the
// pools while the test runs.
func trackForks(t *testing.T) *atomic.Int64 {
	t.Helper()
	var n atomic.Int64
	testForkHook = func(d int) { n.Add(int64(d)) }
	t.Cleanup(func() { testForkHook = nil })
	return &n
}

// cPage is a page in the shape of the spec's example: a layout with a
// </head>, marked components, slot content forwarded into one, a lazy
// marker around a hole and a loop.
func cPage(mark bool) func(*Ctx, *Writer) error {
	return func(c *Ctx, w *Writer) error {
		w.Raw("<!DOCTYPE html><html><head><title>t</title>")
		w.HeadEnd()
		w.Raw("</head><body>")
		if err := call(mark, c, w, cComp, cProps{Name: "a", Sleep: 3 * time.Millisecond}, Slots{}); err != nil {
			return err
		}
		if err := call(mark, c, w, cComp, cProps{Name: "b", Sleep: time.Millisecond, Mark: mark,
			Kids: []cProps{{Name: "b1", Sleep: 2 * time.Millisecond}, {Name: "b2"}}}, Slots{}); err != nil {
			return err
		}
		x := "captured"
		// Slot content that calls a component: it runs in b's goroutine.
		slot := func(lyr__w *Writer) error {
			lyr__ctx := CtxOf(lyr__w, c)
			lyr__w.Raw("<slot " + x + ">")
			if err := cComp(lyr__ctx, lyr__w, cProps{Name: "inslot"}, Slots{}); err != nil {
				return err
			}
			lyr__w.Raw("</slot>")
			return lyr__w.Err()
		}
		if err := call(mark, c, w, cComp, cProps{Name: "s", Sleep: time.Millisecond}, Slots{Default: slot}); err != nil {
			return err
		}
		w.Raw(`<lyr-lazy component="C-1" client="idle">`)
		if err := call(mark, c, w, cComp, cProps{Name: "lazy"}, Slots{}); err != nil {
			return err
		}
		w.Raw(`</lyr-lazy>`)
		for i := range 40 {
			if err := call(mark, c, w, cComp, cProps{Name: "l" + strconv.Itoa(i)}, Slots{}); err != nil {
				return err
			}
		}
		w.Raw("<footer></footer></body></html>")
		return nil
	}
}

func cSite(t *testing.T, routes []Route, opts ...Option) http.Handler {
	t.Helper()
	return testSite(t, routes, opts...).Handler()
}

func TestConcurrentSameOutput(t *testing.T) {
	forks := trackForks(t)
	assets := []Asset{{Kind: "css", URL: "/_lyr/x.css"}}
	h := cSite(t, []Route{
		{Pattern: "/plain", File: "src/pages/plain.lyr", Render: cPage(false), Assets: assets},
		{Pattern: "/marked", File: "src/pages/marked.lyr", Render: cPage(true), Assets: assets},
	}, WithConcurrencyLimit(4))
	want := do(h, "GET", "/plain")
	if want.Code != 200 || !strings.Contains(want.Body.String(), `<link rel="stylesheet" href="/_lyr/x.css"></head>`) {
		t.Fatalf("plain: %d %q", want.Code, want.Body)
	}
	cMaxRunning.Store(0)
	for range 20 {
		got := do(h, "GET", "/marked")
		if got.Code != 200 || got.Body.String() != want.Body.String() {
			t.Fatalf("marked:\n got %d %q\nwant %q", got.Code, got.Body, want.Body)
		}
		if got.Header().Get("Content-Length") != strconv.Itoa(want.Body.Len()) {
			t.Fatalf("Content-Length %q", got.Header().Get("Content-Length"))
		}
	}
	if n := forks.Load(); n != 0 {
		t.Errorf("%d child writers not returned to the pools", n)
	}
	// The limit counts goroutines across nesting; b's kids run inline or
	// forked, and unmarked calls on a goroutine add to its own count only.
	if m := cMaxRunning.Load(); m > 4+1+1 {
		t.Errorf("max running components %d, want at most the limit plus the page's and an inline one", m)
	}
}

func TestConcurrentLimit(t *testing.T) {
	forks := trackForks(t)
	var started atomic.Int32
	loop := func(c *Ctx, w *Writer) error {
		for i := range 30 {
			if err := Concurrent(c, w, func(c *Ctx, w *Writer, i int, s Slots) error {
				started.Add(1)
				time.Sleep(time.Millisecond)
				w.Int(int64(i))
				w.Raw(",")
				return nil
			}, i, Slots{}); err != nil {
				return err
			}
		}
		return nil
	}
	var want strings.Builder
	for i := range 30 {
		want.WriteString(strconv.Itoa(i) + ",")
	}
	for _, limit := range []int{0, 1, 3, 16, -1} {
		h := cSite(t, []Route{{Pattern: "/", File: "src/pages/index.lyr", Render: loop}}, WithConcurrencyLimit(limit))
		started.Store(0)
		if got := do(h, "GET", "/"); got.Body.String() != want.String() {
			t.Errorf("limit %d: %q", limit, got.Body)
		}
		if started.Load() != 30 {
			t.Errorf("limit %d: %d rendered", limit, started.Load())
		}
	}
	if n := forks.Load(); n != 0 {
		t.Errorf("%d child writers not returned", n)
	}
}

func TestConcurrentLimitRunning(t *testing.T) {
	var running, maxRunning atomic.Int32
	loop := func(c *Ctx, w *Writer) error {
		for i := range 12 {
			if err := Concurrent(c, w, func(c *Ctx, w *Writer, i int, s Slots) error {
				n := running.Add(1)
				defer running.Add(-1)
				for {
					m := maxRunning.Load()
					if n <= m || maxRunning.CompareAndSwap(m, n) {
						break
					}
				}
				time.Sleep(2 * time.Millisecond)
				return nil
			}, i, Slots{}); err != nil {
				return err
			}
		}
		return nil
	}
	h := cSite(t, []Route{{Pattern: "/", File: "src/pages/index.lyr", Render: loop}}, WithConcurrencyLimit(3))
	do(h, "GET", "/")
	// 3 goroutines, plus one rendering inline on the page's goroutine.
	if m := maxRunning.Load(); m > 4 || m < 2 {
		t.Errorf("max running %d, want 2..4", m)
	}
}

func TestConcurrentHead(t *testing.T) {
	forks := trackForks(t)
	assets := []Asset{{Kind: "css", URL: "/_lyr/x.css"}}
	const tags = `<link rel="stylesheet" href="/_lyr/x.css">`
	pages := map[string]func(mark bool) func(*Ctx, *Writer) error{
		// The </head> comes before any hole: written inline.
		"before": func(mark bool) func(*Ctx, *Writer) error {
			return func(c *Ctx, w *Writer) error {
				w.Raw("<html><head>")
				w.HeadEnd()
				w.Raw("</head><body>")
				if err := call(mark, c, w, cComp, cProps{Name: "a", Sleep: time.Millisecond}, Slots{}); err != nil {
					return err
				}
				w.Raw("</body></html>")
				return nil
			}
		},
		// The layout itself is marked: its </head> is in a child.
		"inside": func(mark bool) func(*Ctx, *Writer) error {
			return func(c *Ctx, w *Writer) error {
				w.Raw("<!DOCTYPE html>")
				if err := call(mark, c, w, cComp, cProps{Name: "layout", Head: true, Sleep: time.Millisecond, Mark: mark,
					Kids: []cProps{{Name: "k", Head: true}}}, Slots{}); err != nil {
					return err
				}
				w.Raw("<p>after</p>")
				return nil
			}
		},
		// A marked component in the head, then the page's </head>.
		"hole-then-head": func(mark bool) func(*Ctx, *Writer) error {
			return func(c *Ctx, w *Writer) error {
				w.Raw("<html><head>")
				if err := call(mark, c, w, cComp, cProps{Name: "meta", Sleep: time.Millisecond}, Slots{}); err != nil {
					return err
				}
				w.HeadEnd()
				w.Raw("</head><body>")
				if err := call(mark, c, w, cComp, cProps{Name: "a"}, Slots{}); err != nil {
					return err
				}
				w.Raw("</body></html>")
				return nil
			}
		},
		// No </head> at all: after the doctype.
		"none": func(mark bool) func(*Ctx, *Writer) error {
			return func(c *Ctx, w *Writer) error {
				w.Raw("<!DOCTYPE html><p>")
				if err := call(mark, c, w, cComp, cProps{Name: "a", Sleep: time.Millisecond}, Slots{}); err != nil {
					return err
				}
				w.Raw("</p>")
				return nil
			}
		},
	}
	for name, p := range pages {
		t.Run(name, func(t *testing.T) {
			h := cSite(t, []Route{
				{Pattern: "/plain", File: "src/pages/plain.lyr", Render: p(false), Assets: assets},
				{Pattern: "/marked", File: "src/pages/marked.lyr", Render: p(true), Assets: assets},
			})
			want := do(h, "GET", "/plain").Body.String()
			if strings.Count(want, tags) != 1 {
				t.Fatalf("plain: %q", want)
			}
			if got := do(h, "GET", "/marked").Body.String(); got != want {
				t.Errorf("marked:\n got %q\nwant %q", got, want)
			}
		})
	}
	if n := forks.Load(); n != 0 {
		t.Errorf("%d child writers not returned", n)
	}
}

func TestConcurrentErrors(t *testing.T) {
	forks := trackForks(t)
	errA, errB, errPage := errors.New("a failed"), errors.New("b failed"), errors.New("page failed")
	var log *bytes.Buffer
	tests := []struct {
		name string
		page func(*Ctx, *Writer) error
		want error
	}{
		{"later fails first in time", func(c *Ctx, w *Writer) error {
			if err := Concurrent(c, w, cComp, cProps{Sleep: 5 * time.Millisecond, Err: errA}, Slots{}); err != nil {
				return err
			}
			return Concurrent(c, w, cComp, cProps{Err: errB}, Slots{})
		}, errA},
		{"parent fails after a hole", func(c *Ctx, w *Writer) error {
			if err := Concurrent(c, w, cComp, cProps{Sleep: 2 * time.Millisecond, Err: errA}, Slots{}); err != nil {
				return err
			}
			return errPage
		}, errA},
		{"parent fails, holes succeed", func(c *Ctx, w *Writer) error {
			if err := Concurrent(c, w, cComp, cProps{Sleep: 2 * time.Millisecond}, Slots{}); err != nil {
				return err
			}
			return errPage
		}, errPage},
		{"nested: inner hole before outer error", func(c *Ctx, w *Writer) error {
			return Concurrent(c, w, cComp, cProps{Err: errA, Mark: true, Kids: []cProps{{Sleep: 2 * time.Millisecond, Err: errB}}}, Slots{})
		}, errB},
		{"write error in a child", func(c *Ctx, w *Writer) error {
			if err := Concurrent(c, w, func(c *Ctx, w *Writer, _ int, s Slots) error {
				w.Spread(Attrs{"bad name": 1})
				return w.Err()
			}, 0, Slots{}); err != nil {
				return err
			}
			return nil
		}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got error
			s := testSite(t, []Route{{Pattern: "/", File: "src/pages/index.lyr", Render: tt.page}},
				WithErrorHandler(func(w http.ResponseWriter, r *http.Request, err error) bool {
					got = err
					w.WriteHeader(599)
					return true
				}))
			log = captureLog(t)
			rec := do(s.Handler(), "GET", "/")
			if rec.Code != 599 {
				t.Fatalf("status %d", rec.Code)
			}
			if tt.want != nil && !errors.Is(got, tt.want) {
				t.Errorf("error %v, want %v", got, tt.want)
			}
			if tt.want == nil && (got == nil || !strings.Contains(got.Error(), "bad name")) {
				t.Errorf("error %v", got)
			}
		})
	}
	_ = log
	if n := forks.Load(); n != 0 {
		t.Errorf("%d child writers not returned", n)
	}
}

func TestConcurrentControlFlow(t *testing.T) {
	forks := trackForks(t)
	child := func(mode string) func(*Ctx, *Writer, string, Slots) error {
		return func(c *Ctx, w *Writer, _ string, s Slots) error {
			time.Sleep(time.Millisecond)
			switch mode {
			case "redirect":
				c.Header().Set("Set-Cookie", "a=1")
				return c.Redirect("/login", http.StatusSeeOther)
			case "notfound":
				return ErrNotFound
			}
			return nil
		}
	}
	page := func(mode string) func(*Ctx, *Writer) error {
		return func(c *Ctx, w *Writer) error {
			w.Raw("<p>")
			if err := Concurrent(c, w, child(mode), "", Slots{}); err != nil {
				return err
			}
			w.Raw("</p>")
			return nil
		}
	}
	h := cSite(t, []Route{
		{Pattern: "/r", File: "src/pages/r.lyr", Render: page("redirect")},
		{Pattern: "/n", File: "src/pages/n.lyr", Render: page("notfound")},
		{Pattern: "404", File: "src/pages/404.lyr", Render: func(c *Ctx, w *Writer) error {
			w.Raw("not here")
			return nil
		}},
	})
	rec := do(h, "GET", "/r")
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/login" || rec.Header().Get("Set-Cookie") != "a=1" {
		t.Errorf("redirect: %d %v", rec.Code, rec.Header())
	}
	rec = do(h, "GET", "/n")
	if rec.Code != 404 || rec.Body.String() != "not here" {
		t.Errorf("not found: %d %q", rec.Code, rec.Body)
	}

	// On an island request.
	islandOf := func(c *Ctx, w *Writer, p commentsProps, s Slots) error {
		if err := Concurrent(c, w, comments, p, Slots{}); err != nil {
			return err
		}
		return Concurrent(c, w, comments, commentsProps{PostID: 7}, Slots{})
	}
	s := NewSite(islandRoutes(), []Island{NewIsland(commentsID, "src/components/Comments.lyr", islandOf)}, WithStaticDir(t.TempDir()))
	ih := s.Handler()
	for mode, want := range map[string]int{"redirect": http.StatusFound, "notfound": 404, "": 200, "cache": 200} {
		src, token := placeholder(t, ih, mode)
		rec := do(ih, "GET", src+"?p="+token)
		if rec.Code != want {
			t.Errorf("island %q: %d %q", mode, rec.Code, rec.Body)
		}
		switch mode {
		case "":
			if rec.Body.String() != `<ul class="comments">42</ul><ul class="comments">7</ul>` {
				t.Errorf("island body %q", rec.Body)
			}
		case "redirect":
			if rec.Header().Get("X-Before") != "1" || rec.Header().Get("Location") != "/login" {
				t.Errorf("island redirect headers %v", rec.Header())
			}
		case "cache":
			if rec.Header().Get("Cache-Control") != "public, max-age=60" {
				t.Errorf("island Cache-Control %q", rec.Header().Get("Cache-Control"))
			}
		}
	}
	if n := forks.Load(); n != 0 {
		t.Errorf("%d child writers not returned", n)
	}
}

func TestConcurrentStatusHeaderMerge(t *testing.T) {
	forks := trackForks(t)
	page := func(c *Ctx, w *Writer) error {
		c.Header().Set("X-Page", "page")
		c.Header().Set("X-Shared", "page")
		c.SetStatus(201)
		// a (marked, slow) sets 202 and X-Shared: a; b (unmarked, after it)
		// sets X-Shared: b; c (marked, last) sets X-Last.
		if err := Concurrent(c, w, cComp, cProps{Sleep: 3 * time.Millisecond, Status: 202, Header: "X-Shared: a"}, Slots{}); err != nil {
			return err
		}
		if err := cComp(c, w, cProps{Header: "X-Shared: b"}, Slots{}); err != nil {
			return err
		}
		return Concurrent(c, w, cComp, cProps{Header: "X-Last: c", Mark: true, Kids: []cProps{{Status: 203}}}, Slots{})
	}
	h := cSite(t, []Route{{Pattern: "/", File: "src/pages/index.lyr", Render: page}})
	rec := do(h, "GET", "/")
	// The marked components' writes apply at the join, in output order,
	// after the page's own (and the unmarked b's).
	if rec.Code != 203 || rec.Header().Get("X-Page") != "page" || rec.Header().Get("X-Shared") != "a" || rec.Header().Get("X-Last") != "c" {
		t.Errorf("got %d %v", rec.Code, rec.Header())
	}
	if n := forks.Load(); n != 0 {
		t.Errorf("%d child writers not returned", n)
	}
}

func TestConcurrentDepthGuard(t *testing.T) {
	forks := trackForks(t)
	page := func(c *Ctx, w *Writer) error {
		return Concurrent(c, w, cComp, cProps{Mark: true, Depth: 2000}, Slots{})
	}
	var got error
	s := testSite(t, []Route{{Pattern: "/", File: "src/pages/index.lyr", Render: page}},
		WithErrorHandler(func(w http.ResponseWriter, r *http.Request, err error) bool {
			got = err
			w.WriteHeader(500)
			return true
		}))
	do(s.Handler(), "GET", "/")
	if got == nil || !strings.Contains(got.Error(), "render depth exceeded 1000: src/components/C.lyr > src/components/C.lyr") {
		t.Errorf("error %v", got)
	}
	if n := forks.Load(); n != 0 {
		t.Errorf("%d child writers not returned", n)
	}
}

func TestConcurrentNewWriterInline(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf)
	c := acquireCtx(httptest.NewRequest("GET", "/", nil), nil, false)
	defer releaseCtx(c)
	done := false
	err := Concurrent(c, w, func(c *Ctx, w *Writer, _ int, s Slots) error {
		time.Sleep(time.Millisecond)
		w.Raw("inline")
		done = true
		return nil
	}, 0, Slots{})
	if err != nil || !done || buf.String() != "inline" {
		t.Errorf("err %v, done %v, %q", err, done, buf.String())
	}
	// Its error is the call's.
	errX := errors.New("x")
	if err := Concurrent(c, w, cComp, cProps{Err: errX}, Slots{}); err != errX {
		t.Errorf("inline error %v", err)
	}
	// A failed writer starts nothing.
	w.setErr(errX)
	if err := Concurrent(c, w, cComp, cProps{Name: "no"}, Slots{}); err != errX || strings.Contains(buf.String(), "no") {
		t.Errorf("after failure: %v %q", err, buf.String())
	}
}

func TestConcurrentInAttrValueInline(t *testing.T) {
	page := func(c *Ctx, w *Writer) error {
		var r Renderer = func(w *Writer) error {
			return Concurrent(CtxOf(w, c), w, cComp, cProps{Name: "x", Sleep: time.Millisecond}, Slots{})
		}
		w.Raw("<p")
		w.Attr("title", []Renderer{r})
		w.Raw(">")
		return nil
	}
	h := cSite(t, []Route{{Pattern: "/", File: "src/pages/index.lyr", Render: page}})
	if got := do(h, "GET", "/").Body.String(); got != `<p title="&lt;c x&gt;&lt;/c&gt;">` {
		t.Errorf("got %q", got)
	}
}

func TestConcurrentLatency(t *testing.T) {
	const d = 20 * time.Millisecond
	page := func(c *Ctx, w *Writer) error {
		for _, n := range []string{"a", "b", "c"} {
			if err := Concurrent(c, w, cComp, cProps{Name: n, Sleep: d}, Slots{}); err != nil {
				return err
			}
		}
		return nil
	}
	h := cSite(t, []Route{{Pattern: "/", File: "src/pages/index.lyr", Render: page}})
	best := time.Hour
	for range 3 {
		start := time.Now()
		rec := do(h, "GET", "/")
		best = min(best, time.Since(start))
		if rec.Body.String() != "<c a></c><c b></c><c c></c>" {
			t.Fatalf("got %q", rec.Body)
		}
	}
	if best >= 2*d {
		t.Errorf("3 components sleeping %v took %v, want well under %v", d, best, 2*d)
	}
}

func TestConcurrentPanics(t *testing.T) {
	forks := trackForks(t)
	page := func(c *Ctx, w *Writer) error {
		w.Raw("<p>")
		if err := Concurrent(c, w, func(c *Ctx, w *Writer, _ int, s Slots) error {
			return lyrPanicker(c, w)
		}, 0, Slots{}); err != nil {
			return err
		}
		return nil
	}
	s := testSite(t, []Route{{Pattern: "/", File: "src/pages/index.lyr", Render: page}})
	log := captureLog(t)
	rec := do(s.Handler(), "GET", "/")
	if rec.Code != 500 || !strings.Contains(log.String(), "lyr: GET /: src/pages/p.lyr:7: panic: from lyr") {
		t.Errorf("got %d, log %q", rec.Code, log)
	}

	abort := func(c *Ctx, w *Writer) error {
		if err := Concurrent(c, w, cComp, cProps{Sleep: time.Millisecond}, Slots{}); err != nil {
			return err
		}
		return Concurrent(c, w, cComp, cProps{Panic: http.ErrAbortHandler}, Slots{})
	}
	s = testSite(t, []Route{{Pattern: "/", File: "src/pages/index.lyr", Render: abort}})
	var p any
	func() {
		defer func() { p = recover() }()
		do(s.Handler(), "GET", "/")
	}()
	if p != http.ErrAbortHandler {
		t.Errorf("recovered %v, want http.ErrAbortHandler", p)
	}
	if n := forks.Load(); n != 0 {
		t.Errorf("%d child writers not returned", n)
	}
}

func TestConcurrentEntryPoints(t *testing.T) {
	forks := trackForks(t)
	frag := func(c *Ctx, w *Writer, _ int, s Slots) error {
		if err := Concurrent(c, w, cComp, cProps{Name: "a", Sleep: 2 * time.Millisecond, Header: "X-A: 1"}, Slots{}); err != nil {
			return err
		}
		w.Raw("|")
		return Concurrent(c, w, cComp, cProps{Name: "b"}, Slots{})
	}
	rec := httptest.NewRecorder()
	if err := RenderComponent(rec, httptest.NewRequest("GET", "/", nil), frag, 0); err != nil {
		t.Fatal(err)
	}
	if rec.Body.String() != "<c a></c>|<c b></c>" || rec.Header().Get("X-A") != "1" || rec.Header().Get("Content-Length") != "19" {
		t.Errorf("RenderComponent: %q %v", rec.Body, rec.Header())
	}
	errA := errors.New("a")
	failing := func(c *Ctx, w *Writer, _ int, s Slots) error {
		return Concurrent(c, w, cComp, cProps{Err: errA}, Slots{})
	}
	if err := RenderComponent(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil), failing, 0); !errors.Is(err, errA) {
		t.Errorf("RenderComponent error %v", err)
	}

	// lyrtest's bridge.
	html, err := testRender(httptest.NewRequest("GET", "/", nil), nil, nil, func(c, w any) error {
		return frag(c.(*Ctx), w.(*Writer), 0, Slots{})
	})
	if err != nil || html != "<c a></c>|<c b></c>" {
		t.Errorf("testRender: %q %v", html, err)
	}

	// A RenderHTML capture joins its own holes; inside a marked component
	// too, and </head> in a capture's child is still an error.
	page := func(c *Ctx, w *Writer) error {
		inner := func(c *Ctx, w *Writer, _ int, s Slots) error {
			h, err := RenderHTML(c, func(w *Writer) error {
				return frag(CtxOf(w, c), w, 0, Slots{})
			})
			if err != nil {
				return err
			}
			w.Raw("[" + string(h) + "]")
			return nil
		}
		if err := inner(c, w, 0, Slots{}); err != nil {
			return err
		}
		return Concurrent(c, w, inner, 0, Slots{})
	}
	h := cSite(t, []Route{
		{Pattern: "/", File: "src/pages/index.lyr", Render: page},
		{Pattern: "/head", File: "src/pages/head.lyr", Render: func(c *Ctx, w *Writer) error {
			_, err := RenderHTML(c, func(w *Writer) error {
				return Concurrent(CtxOf(w, c), w, cComp, cProps{Head: true}, Slots{})
			})
			return err
		}},
	})
	rec = do(h, "GET", "/")
	if rec.Body.String() != "[<c a></c>|<c b></c>][<c a></c>|<c b></c>]" || rec.Header().Get("X-A") != "1" {
		t.Errorf("RenderHTML: %q %v", rec.Body, rec.Header())
	}
	log := captureLog(t)
	if rec := do(h, "GET", "/head"); rec.Code != 500 || !strings.Contains(log.String(), "</head> can't be inside lyr.RenderHTML") {
		t.Errorf("head in capture: %d %q", rec.Code, log)
	}
	if n := forks.Load(); n != 0 {
		t.Errorf("%d child writers not returned", n)
	}
}

// TestConcurrentStress runs marked pages from many goroutines, for -race.
func TestConcurrentStress(t *testing.T) {
	h := cSite(t, []Route{
		{Pattern: "/plain", File: "src/pages/plain.lyr", Render: cPage(false)},
		{Pattern: "/marked", File: "src/pages/marked.lyr", Render: cPage(true)},
	})
	want := do(h, "GET", "/plain").Body.String()
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 5 {
				if got := do(h, "GET", "/marked").Body.String(); got != want {
					t.Errorf("got %q", got)
					return
				}
			}
		})
	}
	wg.Wait()
}

func TestConcurrentServeAllocs(t *testing.T) {
	if testing.Short() || raceEnabled {
		t.Skip("allocation counts (not under -short or -race)")
	}
	small := func(c *Ctx, w *Writer, n int, s Slots) error {
		w.Raw("<p>")
		w.Int(int64(n))
		w.Raw("</p>")
		return nil
	}
	page := func(c *Ctx, w *Writer) error {
		w.Raw("<html><head>")
		w.HeadEnd()
		w.Raw("</head><body>")
		if err := Concurrent(c, w, small, 1, Slots{}); err != nil {
			return err
		}
		w.Raw("</body></html>")
		return nil
	}
	s := testSite(t, []Route{{Pattern: "/", File: "src/pages/index.lyr", Render: page}})
	h := s.Handler()
	fw := newFakeResponseWriter()
	r := httptest.NewRequest("GET", "/", nil)
	allocs := testing.AllocsPerRun(1000, func() {
		fw.reset()
		h.ServeHTTP(fw, r)
	})
	// Content-Length, plus the goroutine's arguments.
	if allocs > 2 {
		t.Errorf("a page with one marked component: %v allocs, want 2", allocs)
	}
	t.Logf("%v allocs", allocs)
}

// benchCard is a small component, like a card in a list.
func benchCard(c *Ctx, w *Writer, n int, s Slots) error {
	if err := w.Enter("src/components/Card.lyr"); err != nil {
		return err
	}
	defer w.Leave()
	w.Raw(`<article class="card"><h2>Card `)
	w.Int(int64(n))
	w.Raw(`</h2>`)
	w.Render(s.Default)
	w.Raw(`</article>`)
	return nil
}

// BenchmarkConcurrent serves pages of n small cards, in order and marked,
// through Site.Handler with a reused ResponseWriter (PLAN.md §3.6, Cost).
func BenchmarkConcurrent(b *testing.B) {
	b.Setenv("LYR_STATIC", "")
	b.Setenv("LYR_DEV", "")
	page := func(n int, mark bool, filler string) func(*Ctx, *Writer) error {
		return func(c *Ctx, w *Writer) error {
			w.Raw("<!DOCTYPE html><html><head><title>t</title>")
			w.HeadEnd()
			w.Raw("</head><body>" + filler)
			for i := range n {
				if err := call(mark, c, w, benchCard, i, Slots{}); err != nil {
					return err
				}
			}
			w.Raw(filler + "</body></html>")
			return nil
		}
	}
	big := strings.Repeat("<p>Lorem ipsum dolor sit amet, consectetur adipiscing elit.</p>", 270) // ~17 KB, twice
	cases := []struct {
		name   string
		n      int
		filler string
		opts   []Option
	}{
		{"cards=3", 3, "", nil},
		{"cards=20", 20, "", nil},
		{"cards=500", 500, "", nil},
		{"cards=500/nolimit", 500, "", []Option{WithConcurrencyLimit(-1)}},
		{"page=34KB/cards=2", 2, big, nil},
	}
	for _, tc := range cases {
		for _, mark := range []bool{false, true} {
			name := tc.name + "/inorder"
			if mark {
				name = tc.name + "/concurrent"
			}
			b.Run(name, func(b *testing.B) {
				s := NewSite([]Route{{Pattern: "/", File: "src/pages/index.lyr", Render: page(tc.n, mark, tc.filler),
					Assets: []Asset{{Kind: "css", URL: "/_lyr/x.css"}}}}, nil, append([]Option{WithStaticDir(b.TempDir())}, tc.opts...)...)
				h := s.Handler()
				fw := newFakeResponseWriter()
				r := httptest.NewRequest("GET", "/", nil)
				b.ReportAllocs()
				for b.Loop() {
					fw.reset()
					h.ServeHTTP(fw, r)
				}
			})
		}
	}
}

// BenchmarkCtxOf renders 500 components with capturing slot content, with
// and without the CtxOf rebinding that codegen puts at the start of slot
// content that calls a component.
func BenchmarkCtxOf(b *testing.B) {
	inner := func(c *Ctx, w *Writer, n int, s Slots) error {
		w.Int(int64(n))
		return nil
	}
	for _, rebind := range []bool{false, true} {
		b.Run("ctxof="+strconv.FormatBool(rebind), func(b *testing.B) {
			var buf bytes.Buffer
			w := NewWriter(&buf)
			c := acquireCtx(httptest.NewRequest("GET", "/", nil), nil, false)
			defer releaseCtx(c)
			b.ReportAllocs()
			for b.Loop() {
				buf.Reset()
				for i := range 500 {
					if err := benchCard(c, w, i, Slots{Default: func(lyr__w *Writer) error {
						lyr__ctx := c
						if rebind {
							lyr__ctx = CtxOf(lyr__w, c)
						}
						return inner(lyr__ctx, lyr__w, i, Slots{})
					}}); err != nil {
						b.Fatal(err)
					}
				}
			}
		})
	}
}
