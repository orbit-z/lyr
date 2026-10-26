package orbittest

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/orbit-z/orbit"
)

// fakeTB records failures. Fatal stops the calling goroutine, as testing
// does, so run code that may call it through run.
type fakeTB struct {
	testing.TB
	mu     sync.Mutex
	failed bool
	fatal  bool
	msgs   []string
}

func (f *fakeTB) Helper() {}

func (f *fakeTB) record(fatal bool, msg string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failed = true
	f.fatal = f.fatal || fatal
	f.msgs = append(f.msgs, msg)
}

func (f *fakeTB) Error(args ...any)                 { f.record(false, fmt.Sprint(args...)) }
func (f *fakeTB) Errorf(format string, args ...any) { f.record(false, fmt.Sprintf(format, args...)) }
func (f *fakeTB) Fatal(args ...any)                 { f.record(true, fmt.Sprint(args...)); runtime.Goexit() }
func (f *fakeTB) Fatalf(format string, args ...any) {
	f.record(true, fmt.Sprintf(format, args...))
	runtime.Goexit()
}
func (f *fakeTB) Failed() bool { return f.failed }

func (f *fakeTB) output() string { return strings.Join(f.msgs, "\n") }

// run calls fn with a fakeTB on its own goroutine and returns the fakeTB.
func run(t *testing.T, fn func(tb testing.TB)) *fakeTB {
	t.Helper()
	f := &fakeTB{TB: t}
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn(f)
	}()
	<-done
	return f
}

type props struct{ Name string }

// hello is a hand-written component: escaped props, slots, a param, locals
// and the request.
func hello(ctx *orbit.Ctx, w *orbit.Writer, p props, s orbit.Slots) error {
	w.Raw("<p>")
	w.String(p.Name)
	w.Raw("</p>")
	if d := s.Default; d != nil {
		w.Raw("<main>")
		w.Render(d)
		w.Raw("</main>")
	}
	if f := s.Get("footer"); f != nil {
		w.Raw("<footer>")
		w.Render(f)
		w.Raw("</footer>")
	}
	return nil
}

func TestRenderPropsAndSlots(t *testing.T) {
	got := Render(t, hello, props{Name: "<Ada>"})
	if want := "<p>&lt;Ada&gt;</p>"; got != want {
		t.Errorf("no options: got %q, want %q", got, want)
	}
	got = Render(t, hello, props{Name: "x"},
		WithSlot("default", "<em>one</em>"),
		WithSlot("footer", "<small>old</small>"),
		WithSlot("footer", "<small>new</small>"))
	if want := "<p>x</p><main><em>one</em></main><footer><small>new</small></footer>"; got != want {
		t.Errorf("slots: got %q, want %q", got, want)
	}
}

func TestRenderParams(t *testing.T) {
	page := func(ctx *orbit.Ctx, w *orbit.Writer, _ struct{}, _ orbit.Slots) error {
		w.String(ctx.Param("slug") + "|" + ctx.Param("cat") + "|" + ctx.Param("none"))
		return nil
	}
	got := Render(t, page, struct{}{},
		WithParam("slug", "first"), WithParam("cat", "books"), WithParam("slug", "hello"))
	if want := "hello|books|"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	// Params don't leak into the next render through the pooled Ctx.
	if got := Render(t, page, struct{}{}); got != "||" {
		t.Errorf("second render: got %q, want %q", got, "||")
	}
}

type locals struct{ User string }

func TestRenderLocals(t *testing.T) {
	c := func(ctx *orbit.Ctx, w *orbit.Writer, _ struct{}, _ orbit.Slots) error {
		w.String("user=" + orbit.LocalsOf[locals](ctx).User)
		return nil
	}
	if got := Render(t, c, struct{}{}); got != "user=" {
		t.Errorf("without locals: got %q", got)
	}
	if got := Render(t, c, struct{}{}, WithLocals(&locals{User: "ada"})); got != "user=ada" {
		t.Errorf("with locals: got %q", got)
	}
	// WithLocals applies to the request from WithRequest, whatever the order.
	r, _ := http.NewRequest("GET", "/x", nil)
	if got := Render(t, c, struct{}{}, WithLocals(&locals{User: "bo"}), WithRequest(r)); got != "user=bo" {
		t.Errorf("with locals and request: got %q", got)
	}
}

func TestRenderRequest(t *testing.T) {
	c := func(ctx *orbit.Ctx, w *orbit.Writer, _ struct{}, _ orbit.Slots) error {
		r := ctx.Request
		ck, _ := r.Cookie("theme")
		theme := ""
		if ck != nil {
			theme = ck.Value
		}
		w.String(r.Method + " " + ctx.URL.Path + " q=" + r.URL.Query().Get("q") + " theme=" + theme)
		return nil
	}
	if got, want := Render(t, c, struct{}{}), "GET / q= theme="; got != want {
		t.Errorf("default request: got %q, want %q", got, want)
	}
	r, _ := http.NewRequest("POST", "/search?q=go", nil)
	r.AddCookie(&http.Cookie{Name: "theme", Value: "dark"})
	if got, want := Render(t, c, struct{}{}, WithRequest(r)), "POST /search q=go theme=dark"; got != want {
		t.Errorf("WithRequest: got %q, want %q", got, want)
	}
}

func TestRenderSameAsWriter(t *testing.T) {
	// Escaping and attribute filtering are the runtime's own.
	c := func(_ *orbit.Ctx, w *orbit.Writer, _ struct{}, _ orbit.Slots) error {
		w.Raw("<a")
		w.Attr("href", "javascript:alert(1)")
		w.Raw(">")
		w.Expr([]any{1, "<", 2.5, true})
		w.Raw("</a>")
		return nil
	}
	got := Render(t, c, struct{}{})
	if want := `<a href="about:invalid#orbit">1&lt;2.5true</a>`; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestRenderErrors(t *testing.T) {
	boom := errors.New("boom")
	tests := []struct {
		name   string
		render func(*orbit.Ctx, *orbit.Writer, struct{}, orbit.Slots) error
		opts   []Option
		want   string
	}{
		{
			name:   "returned error",
			render: func(*orbit.Ctx, *orbit.Writer, struct{}, orbit.Slots) error { return boom },
			want:   "orbittest.Render: boom",
		},
		{
			name: "writer error",
			render: func(_ *orbit.Ctx, w *orbit.Writer, _ struct{}, _ orbit.Slots) error {
				w.Spread(orbit.Attrs{"bad name": 1})
				return nil
			},
			want: "orbittest.Render: ",
		},
		{
			name: "not linked",
			render: func(*orbit.Ctx, *orbit.Writer, struct{}, orbit.Slots) error {
				panic("orbit: src/components/Card.orbit not linked; import example.com/blog/orbit_gen/app")
			},
			want: "orbittest: src/components/Card.orbit not linked; import example.com/blog/orbit_gen/app in this test package",
		},
		{
			name:   "zero option",
			render: hello0,
			opts:   []Option{{}},
			want:   "orbittest.Render: zero Option",
		},
		{
			name:   "nil render",
			render: nil,
			want:   "orbittest.Render: render func is nil",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := run(t, func(tb testing.TB) { Render(tb, tt.render, struct{}{}, tt.opts...) })
			if !f.fatal || !strings.Contains(f.output(), tt.want) {
				t.Errorf("fatal=%v, output %q, want it to contain %q", f.fatal, f.output(), tt.want)
			}
		})
	}
}

func hello0(_ *orbit.Ctx, w *orbit.Writer, _ struct{}, _ orbit.Slots) error {
	w.Raw("hi")
	return nil
}

func TestRenderOtherPanicPropagates(t *testing.T) {
	defer func() {
		if p := recover(); p != "kaboom" {
			t.Errorf("recovered %v, want kaboom", p)
		}
	}()
	Render(t, func(*orbit.Ctx, *orbit.Writer, struct{}, orbit.Slots) error { panic("kaboom") }, struct{}{})
	t.Error("Render returned")
}

// echo answers with what it saw of the request and its locals.
var echo = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	user := ""
	if l := orbit.LocalsFromRequest[locals](r); l != nil {
		user = l.User
	}
	ck, _ := r.Cookie("theme")
	theme := ""
	if ck != nil {
		theme = ck.Value
	}
	fmt.Fprintf(w, "%s %s q=%s user=%s theme=%s ua=%s", r.Method, r.URL.Path, r.URL.Query().Get("q"), user, theme, r.Header.Get("User-Agent"))
})

func TestGet(t *testing.T) {
	if got, want := Get(t, echo, "/a?q=1").Body.String(), "GET /a q=1 user= theme= ua="; got != want {
		t.Errorf("plain: got %q, want %q", got, want)
	}
	r, _ := http.NewRequest("POST", "/other?q=zzz", strings.NewReader("body"))
	r.AddCookie(&http.Cookie{Name: "theme", Value: "dark"})
	r.Header.Set("User-Agent", "probe")
	got := Get(t, echo, "/b?q=2", WithRequest(r), WithLocals(&locals{User: "ada"})).Body.String()
	if want := "GET /b q=2 user=ada theme=dark ua=probe"; got != want {
		t.Errorf("options: got %q, want %q", got, want)
	}
	if r.Method != "POST" || r.URL.Path != "/other" {
		t.Errorf("WithRequest's request was modified: %s %s", r.Method, r.URL)
	}
}

func TestGetNoRedirectFollowing(t *testing.T) {
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/old" {
			http.Redirect(w, r, "/new", http.StatusPermanentRedirect)
			return
		}
		w.Write([]byte("new"))
	})
	rec := Get(t, h, "/old")
	if rec.Code != http.StatusPermanentRedirect || rec.Header().Get("Location") != "/new" {
		t.Errorf("got %d Location %q", rec.Code, rec.Header().Get("Location"))
	}
}

func TestGetErrors(t *testing.T) {
	tests := []struct {
		name string
		h    http.Handler
		path string
		opts []Option
		want string
	}{
		{"WithSlot", echo, "/", []Option{WithSlot("default", "x")}, `orbittest.Get: WithSlot("default", …) doesn't apply to Get: a request has no slots`},
		{"WithParam", echo, "/", []Option{WithParam("slug", "x")}, `orbittest.Get: WithParam("slug", …) doesn't apply to Get: the router sets params from the path`},
		{"bad path", echo, "blog", nil, `orbittest.Get("blog"): the path must start with /`},
		{"nil handler", nil, "/", nil, "orbittest.Get: handler is nil"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := run(t, func(tb testing.TB) { Get(tb, tt.h, tt.path, tt.opts...) })
			if !f.fatal || !strings.Contains(f.output(), tt.want) {
				t.Errorf("fatal=%v, output %q, want it to contain %q", f.fatal, f.output(), tt.want)
			}
		})
	}
}

func TestNormalize(t *testing.T) {
	tests := []struct{ in, want string }{
		{`<link rel="stylesheet" href="/_orbit/index-5RXDJ3BM.css">`, `<link rel="stylesheet" href="/_orbit/index-*.css">`},
		{`<script type="module" src="/_orbit/blog-slug-ABCD2345.js"></script>`, `<script type="module" src="/_orbit/blog-slug-*.js"></script>`},
		{`/_orbit/index-5RXDJ3BM.js.map`, `/_orbit/index-*.js.map`},
		{`url(/_orbit/logo-QWERTY23.svg)`, `url(/_orbit/logo-*.svg)`},
		{`/_orbit/dir/chunk-AAAAAAAA.js`, `/_orbit/dir/chunk-*.js`},
		{`<orbit-island src="/_orbit/island/0123456789abcdef/c1">`, `<orbit-island src="/_orbit/island/*/c1">`},
		// Not hashes: lowercase, wrong length, outside /_orbit/.
		{`/_orbit/index-5rxdj3bm.css /_orbit/a-ABC.css /static/a-5RXDJ3BM.css`, `/_orbit/index-5rxdj3bm.css /_orbit/a-ABC.css /static/a-5RXDJ3BM.css`},
		{`/_orbit/island/0123/c1`, `/_orbit/island/0123/c1`},
	}
	for _, tt := range tests {
		if got := normalize(tt.in); got != tt.want {
			t.Errorf("normalize(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// inTempDir runs the test in a new working directory, as go test runs a
// package in its own directory, and sets -update to upd.
func inTempDir(t *testing.T, upd bool) string {
	dir := t.TempDir()
	t.Chdir(dir)
	old := *update
	*update = upd
	t.Cleanup(func() { *update = old })
	return dir
}

func TestSnapshotRoundTrip(t *testing.T) {
	dir := inTempDir(t, true)
	page := `<head><link rel="stylesheet" href="/_orbit/index-5RXDJ3BM.css"></head><p>hi</p>`
	Snapshot(t, "pages/home", page)
	data, err := os.ReadFile(filepath.Join(dir, "testdata", "pages", "home.golden"))
	if err != nil {
		t.Fatal(err)
	}
	if want := `<head><link rel="stylesheet" href="/_orbit/index-*.css"></head><p>hi</p>`; string(data) != want {
		t.Errorf("golden = %q, want %q", data, want)
	}

	*update = false
	// A new hash (a CSS edit) still matches.
	Snapshot(t, "pages/home", strings.Replace(page, "5RXDJ3BM", "ZZZZ7777", 1))

	f := run(t, func(tb testing.TB) { Snapshot(tb, "pages/home", strings.Replace(page, "hi", "bye", 1)) })
	if !f.failed || f.fatal {
		t.Fatalf("changed output: failed=%v fatal=%v, want a non-fatal failure", f.failed, f.fatal)
	}
	for _, want := range []string{"output differs from testdata/pages/home.golden", "-update", "line 1, column 67", "<p>bye</p>"} {
		if !strings.Contains(f.output(), want) {
			t.Errorf("message %q lacks %q", f.output(), want)
		}
	}
}

func TestSnapshotMissing(t *testing.T) {
	inTempDir(t, false)
	f := run(t, func(tb testing.TB) { Snapshot(tb, "card", "<p></p>") })
	want := "orbittest.Snapshot: no golden file testdata/card.golden; run go test with -update to create it"
	if !f.fatal || f.output() != want {
		t.Errorf("fatal=%v output %q, want %q", f.fatal, f.output(), want)
	}
}

func TestSnapshotBadName(t *testing.T) {
	inTempDir(t, true)
	for _, name := range []string{"", "../x", "a/../../x", "/abs", "a//b", "./a", "a/", `a\b`, "a/.."} {
		f := run(t, func(tb testing.TB) { Snapshot(tb, name, "x") })
		if !f.fatal || !strings.Contains(f.output(), "invalid name") {
			t.Errorf("Snapshot(%q): fatal=%v output %q", name, f.fatal, f.output())
		}
	}
}

func TestDifferenceMultiline(t *testing.T) {
	got := difference("a\nb\nc\n", "a\nb\nX\n")
	if !strings.Contains(got, "line 3, column 1") {
		t.Errorf("difference = %q", got)
	}
	got = difference("abc", "ab")
	if !strings.Contains(got, "(end of file)") && !strings.Contains(got, `got:  "ab"`) {
		t.Errorf("difference = %q", got)
	}
}
