package lyr

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// Tests of lyr.All (PLAN.md §4f).

// loadCtx returns a Ctx for a GET of target.
func loadCtx(target string) *Ctx {
	return &Ctx{Request: httptest.NewRequest("GET", target, nil)}
}

func sleepLoad(d time.Duration, done *atomic.Int32) func(context.Context) error {
	return func(context.Context) error {
		time.Sleep(d)
		done.Add(1)
		return nil
	}
}

// failLoad returns err after d.
func failLoad(d time.Duration, err error) func(context.Context) error {
	return func(context.Context) error {
		time.Sleep(d)
		return err
	}
}

// waitLoad waits for its context (or a second) and returns its error.
func waitLoad(seen *atomic.Int32) func(context.Context) error {
	return func(rc context.Context) error {
		select {
		case <-rc.Done():
			seen.Add(1)
			return rc.Err()
		case <-time.After(time.Second):
			return nil
		}
	}
}

func TestAllConcurrent(t *testing.T) {
	const d = 10 * time.Millisecond
	best := time.Hour
	for range 5 {
		var done atomic.Int32
		start := time.Now()
		if err := All(loadCtx("/"), sleepLoad(d, &done), sleepLoad(d, &done), sleepLoad(d, &done)); err != nil {
			t.Fatal(err)
		}
		best = min(best, time.Since(start))
		if done.Load() != 3 {
			t.Fatalf("%d loads ran, want 3", done.Load())
		}
	}
	if best >= 2*d {
		t.Errorf("3 loads sleeping %v took %v, want well under %v", d, best, 2*d)
	}
}

func TestAllWaitsForEveryLoad(t *testing.T) {
	// A slow load that ignores its context still finishes before All
	// returns, after an error too.
	var done atomic.Int32
	boom := errors.New("boom")
	err := All(loadCtx("/"), failLoad(0, boom), sleepLoad(20*time.Millisecond, &done))
	if !errors.Is(err, boom) || done.Load() != 1 {
		t.Errorf("err %v, done %d", err, done.Load())
	}
	err = All(loadCtx("/"), sleepLoad(20*time.Millisecond, &done), failLoad(0, boom))
	if !errors.Is(err, boom) || done.Load() != 2 {
		t.Errorf("inline slow load: err %v, done %d", err, done.Load())
	}
}

func TestAllFirstErrorInTime(t *testing.T) {
	early, late := errors.New("early"), errors.New("late")
	// The later-in-order load fails first and wins.
	if err := All(loadCtx("/"), failLoad(30*time.Millisecond, late), failLoad(0, early), failLoad(15*time.Millisecond, late)); err != early {
		t.Errorf("err = %v, want early", err)
	}
	// The context.Canceled the failure causes doesn't win.
	var seen atomic.Int32
	if err := All(loadCtx("/"), waitLoad(&seen), failLoad(5*time.Millisecond, early), waitLoad(&seen)); err != early || seen.Load() != 2 {
		t.Errorf("err = %v, %d loads cancelled", err, seen.Load())
	}
}

func TestAllCancels(t *testing.T) {
	// A load failing after 5 ms next to one that honors its context
	// returns long before the second's 1 s.
	var seen atomic.Int32
	start := time.Now()
	err := All(loadCtx("/"), waitLoad(&seen), failLoad(5*time.Millisecond, errUnavailable))
	if el := time.Since(start); el > 500*time.Millisecond || err != errUnavailable || seen.Load() != 1 {
		t.Errorf("took %v, err %v, cancelled %d", el, err, seen.Load())
	}

	// The context derives from the request's and is cancelled once All
	// returns, on success too.
	type key struct{}
	c := loadCtx("/")
	c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), key{}, "v"))
	var got [2]context.Context
	err = All(c,
		func(rc context.Context) error { got[0] = rc; return nil },
		func(rc context.Context) error { got[1] = rc; return nil })
	if err != nil {
		t.Fatal(err)
	}
	for i, rc := range got {
		if rc.Value(key{}) != "v" || rc.Err() != context.Canceled {
			t.Errorf("load %d: value %v, err %v", i, rc.Value(key{}), rc.Err())
		}
	}

	// A cancelled request reaches every load.
	rctx, cancel := context.WithCancel(context.Background())
	cancel()
	c.Request = c.Request.WithContext(rctx)
	if err := All(c, waitLoad(&seen), waitLoad(&seen)); err != context.Canceled {
		t.Errorf("cancelled request: err %v", err)
	}
}

func TestAllControlFlow(t *testing.T) {
	// ErrNotFound and a redirect are returned as is and win over the
	// context.Canceled they cause.
	c := loadCtx("/")
	redirect := c.Redirect("/elsewhere", http.StatusFound)
	for _, want := range []error{ErrNotFound, redirect} {
		var seen atomic.Int32
		loads := lyrLoads(want, nil)
		if err := All(c, loads[0], loads[1], waitLoad(&seen)); err != want || seen.Load() != 1 {
			t.Errorf("err = %v (%T), want %v", err, err, want)
		}
		if err := ErrorAt("src/pages/load.lyr:5:1", All(c, loads[1])); err != want {
			t.Errorf("one load: err = %v, want %v", err, want)
		}
	}
}

func TestAllErrorPosition(t *testing.T) {
	refused := &accessRefused{next: "/x"}
	for _, n := range []int{1, 2} { // one load inline, or two
		for _, tt := range []struct {
			err  error
			want string
		}{
			{errUnavailable, "src/pages/load.lyr:20: catalog: unavailable"},
			{refused, "src/pages/load.lyr:20: access refused"},
			// An error that already carries a position is kept.
			{ErrorAt("src/components/C.lyr:3:4", errUnavailable), "src/components/C.lyr:3:4: catalog: unavailable"},
		} {
			loads := lyrLoads(tt.err, nil)
			err := All(loadCtx("/"), loads[2-n:]...)
			// The frontmatter's return wraps it with ErrorAt: unchanged.
			err = ErrorAt("src/pages/load.lyr:30:2", err)
			if err == nil || err.Error() != tt.want {
				t.Errorf("%d loads: err = %v, want %q", n, err, tt.want)
				continue
			}
			var ar *accessRefused
			if errors.Is(tt.err, errUnavailable) != errors.Is(err, errUnavailable) || (tt.err == refused) != (errors.As(err, &ar) && ar == refused) {
				t.Errorf("%d loads: %v: error identity lost", n, err)
			}
		}
	}
	// A load outside a .lyr file (a method value, Go code) gets no
	// position: the return's ErrorAt adds one.
	if err := All(loadCtx("/"), failLoad(0, errUnavailable), sleepLoad(0, new(atomic.Int32))); err != errUnavailable {
		t.Errorf("non-.lyr load: err = %v", err)
	}
}

// recoverAll calls All and returns what it panicked with.
func recoverAll(c *Ctx, loads ...func(context.Context) error) (p any) {
	defer func() { p = recover() }()
	All(c, loads...)
	return nil
}

func TestAllPanics(t *testing.T) {
	boom := errors.New("boom")
	loads := lyrLoads(nil, "from load")
	var done atomic.Int32
	slow := sleepLoad(20*time.Millisecond, &done)
	for _, tt := range []struct {
		name  string
		loads []func(context.Context) error
	}{
		{"goroutine", []func(context.Context) error{slow, loads[1]}},
		{"inline", []func(context.Context) error{loads[1], slow}},
		{"after an error", []func(context.Context) error{failLoad(0, boom), failLoad(10*time.Millisecond, nil), slow, loads[1]}},
		{"one load", loads[1:]},
	} {
		before := done.Load()
		p := recoverAll(loadCtx("/"), tt.loads...)
		pe, ok := p.(*panicError)
		if tt.name == "one load" {
			// Inline, without recovery: the page's own recovery positions it.
			if p != "from load" {
				t.Errorf("%s: panicked with %#v", tt.name, p)
			}
			continue
		}
		if !ok || pe.Error() != "src/pages/load.lyr:22: panic: from load" {
			t.Errorf("%s: panicked with %#v", tt.name, p)
		}
		if done.Load() != before+1 {
			t.Errorf("%s: the slow load wasn't waited for", tt.name)
		}
	}

	// A panic cancels the other loads.
	var seen atomic.Int32
	start := time.Now()
	if p := recoverAll(loadCtx("/"), waitLoad(&seen), loads[1]); p == nil || seen.Load() != 1 || time.Since(start) > 500*time.Millisecond {
		t.Errorf("panic didn't cancel: %v, %d", p, seen.Load())
	}

	// http.ErrAbortHandler is re-panicked as is.
	abort := lyrLoads(nil, http.ErrAbortHandler)
	if p := recoverAll(loadCtx("/"), slow, abort[1]); p != http.ErrAbortHandler {
		t.Errorf("abort: panicked with %#v", p)
	}
}

func TestAllNested(t *testing.T) {
	var done atomic.Int32
	// Concurrent: 2d; in order: 4d. The 3d bound leaves d of slack for a
	// loaded machine.
	const d = 30 * time.Millisecond
	c := loadCtx("/")
	var book, author, reviews string
	start := time.Now()
	err := All(c,
		func(rc context.Context) error {
			time.Sleep(d)
			book = "b"
			// A fan-out after the book.
			return All(c,
				func(context.Context) error { time.Sleep(d); author = book + "-a"; return nil },
				func(context.Context) error { time.Sleep(d); reviews = book + "-r"; return nil })
		},
		sleepLoad(d, &done))
	if el := time.Since(start); err != nil || author != "b-a" || reviews != "b-r" || done.Load() != 1 || el >= 3*d {
		t.Errorf("err %v, %q %q, took %v", err, author, reviews, el)
	}

	// Stages: two calls in a row.
	var first, second string
	if err := All(c, func(context.Context) error { first = "1"; return nil }, sleepLoad(0, &done)); err != nil {
		t.Fatal(err)
	}
	if err := All(c, func(context.Context) error { second = first + "2"; return nil }, sleepLoad(0, &done)); err != nil || second != "12" {
		t.Errorf("stages: %v %q", err, second)
	}

	// A nested All's error and panic keep the inner load's position.
	loads := lyrLoads(errUnavailable, nil)
	err = All(c, sleepLoad(0, &done), func(context.Context) error { return All(c, loads...) })
	if err == nil || err.Error() != "src/pages/load.lyr:20: catalog: unavailable" {
		t.Errorf("nested error: %v", err)
	}
	loads = lyrLoads(nil, "inner")
	p := recoverAll(c, sleepLoad(0, &done), func(context.Context) error { return All(c, loads...) })
	if pe, ok := p.(*panicError); !ok || pe.Error() != "src/pages/load.lyr:22: panic: inner" {
		t.Errorf("nested panic: %#v", p)
	}
}

func TestAllZeroAndOne(t *testing.T) {
	if err := All(loadCtx("/")); err != nil {
		t.Errorf("zero loads: %v", err)
	}
	if err := All(nil); err != nil {
		t.Errorf("zero loads, nil Ctx: %v", err)
	}
	// One load runs inline with the request's own context.
	c := loadCtx("/")
	var got context.Context
	if err := All(c, func(rc context.Context) error { got = rc; return nil }); err != nil || got != c.Request.Context() {
		t.Errorf("one load: err %v, context %v", err, got)
	}
	// A nil Ctx (a hand-built test) uses context.Background.
	var bg [2]context.Context
	if err := All(nil, func(rc context.Context) error { bg[0] = rc; return nil }); err != nil || bg[0] != context.Background() {
		t.Errorf("nil Ctx, one load: %v %v", err, bg[0])
	}
	err := All(nil,
		func(rc context.Context) error { bg[0] = rc; return nil },
		func(rc context.Context) error { bg[1] = rc; return nil })
	if err != nil || bg[0] == nil || bg[0] != bg[1] {
		t.Errorf("nil Ctx: %v %v", err, bg)
	}
}

// loadPage is a page whose frontmatter calls All with lyrLoads(err, p),
// returning its error through ErrorAt, as generated code does.
func loadPage(err error, p any) func(c *Ctx, w *Writer) error {
	return func(c *Ctx, w *Writer) error {
		if err := All(c, lyrLoads(err, p)...); err != nil {
			return ErrorAt("src/pages/load.lyr:30:2", err)
		}
		w.Raw("ok")
		return nil
	}
}

func TestAllThroughSite(t *testing.T) {
	for _, conc := range []bool{false, true} {
		page := func(err error, p any) func(c *Ctx, w *Writer) error {
			render := loadPage(err, p)
			if !conc {
				return render
			}
			// The same inside a server:concurrent component.
			return func(c *Ctx, w *Writer) error {
				return Concurrent(c, w, func(c *Ctx, w *Writer, _ int, _ Slots) error {
					return render(c, w)
				}, 0, Slots{})
			}
		}
		routes := []Route{
			{Pattern: "/err", File: "src/pages/load.lyr", Render: page(errUnavailable, nil)},
			{Pattern: "/unknown", File: "src/pages/load.lyr", Render: page(errors.New("unknown"), nil)},
			{Pattern: "/notfound", File: "src/pages/load.lyr", Render: page(ErrNotFound, nil)},
			{Pattern: "/redirect", File: "src/pages/load.lyr", Render: page(&RedirectError{Location: "/elsewhere", Code: 302}, nil)},
			{Pattern: "/panic", File: "src/pages/load.lyr", Render: page(nil, "boom")},
		}
		var calls []error
		h := testSite(t, routes, WithErrorHandler(answerError(&calls))).Handler()

		// The §4c hook sees the load's position and the typed error.
		if rec := do(h, "GET", "/err"); rec.Code != 503 || len(calls) != 1 ||
			calls[0].Error() != "src/pages/load.lyr:20: catalog: unavailable" {
			t.Errorf("concurrent=%v: /err: %d %v", conc, rec.Code, calls)
		}
		if rec := do(h, "GET", "/notfound"); rec.Code != 404 {
			t.Errorf("concurrent=%v: /notfound: %d", conc, rec.Code)
		}
		if rec := do(h, "GET", "/redirect"); rec.Code != 302 || rec.Header().Get("Location") != "/elsewhere" {
			t.Errorf("concurrent=%v: /redirect: %d", conc, rec.Code)
		}
		for target, want := range map[string]string{
			"/unknown": "lyr: GET /unknown: src/pages/load.lyr:20: unknown\n",
			"/panic":   "lyr: GET /panic: src/pages/load.lyr:22: panic: boom\n",
		} {
			logs := captureLog(t)
			if rec := do(h, "GET", target); rec.Code != 500 || logs.String() != want {
				t.Errorf("concurrent=%v: %s: %d, log %q, want %q", conc, target, rec.Code, logs, want)
			}
		}
	}
}

func TestAllAllocs(t *testing.T) {
	if raceEnabled {
		t.Skip("allocations differ under -race")
	}
	c := loadCtx("/")
	nop := func(context.Context) error { return nil }
	loads := []func(context.Context) error{nop, nop, nop}
	// The shared state, the derived context (2) and 2 goroutine starts;
	// one load runs inline and allocates nothing.
	if n := testing.AllocsPerRun(200, func() { All(c, loads...) }); n > 5 {
		t.Errorf("All with 3 loads: %v allocs, want ≤ 5", n)
	}
	if n := testing.AllocsPerRun(200, func() { All(c, nop) }); n != 0 {
		t.Errorf("All with 1 load: %v allocs, want 0", n)
	}
}

func BenchmarkAll(b *testing.B) {
	c := loadCtx("/")
	b.Run("3loads", func(b *testing.B) {
		b.ReportAllocs()
		var x, y, z int
		for b.Loop() {
			All(c,
				func(context.Context) error { x++; return nil },
				func(context.Context) error { y++; return nil },
				func(context.Context) error { z++; return nil })
		}
	})
	b.Run("3sequential", func(b *testing.B) {
		b.ReportAllocs()
		var x, y, z int
		for b.Loop() {
			for _, f := range []func(context.Context) error{
				func(context.Context) error { x++; return nil },
				func(context.Context) error { y++; return nil },
				func(context.Context) error { z++; return nil },
			} {
				f(c.Request.Context())
			}
		}
	})
	b.Run("error", func(b *testing.B) {
		b.ReportAllocs()
		loads := lyrLoads(errUnavailable, nil)
		for b.Loop() {
			All(c, loads...)
		}
	})
}
