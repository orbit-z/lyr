package orbit

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/bertrandkhe/orbit/internal/islandcrypto"
)

const commentsID = "Comments-9b1e44a2"

type commentsProps struct {
	PostID int
	Mode   string // "", "notfound", "error", "panic", "redirect", "cache", "locals"
}

type islandLocals struct{ User string }

// comments is the deferred component of these tests.
func comments(c *Ctx, w *Writer, p commentsProps, s Slots) error {
	switch p.Mode {
	case "notfound":
		return ErrNotFound
	case "error":
		return errors.New("db down")
	case "panic":
		panic("boom")
	case "redirect":
		c.Header().Set("X-Before", "1")
		return c.Redirect("/login", http.StatusFound)
	case "cache":
		c.Header().Set("Cache-Control", "public, max-age=60")
	case "locals":
		cookie, _ := c.Request.Cookie("session")
		fmt.Fprintf(w.buf, "user=%s cookie=%v url=%s referer=%s param=%q",
			LocalsOf[islandLocals](c).User, cookie != nil && cookie.Value == "s1",
			c.URL.Path, c.Request.Referer(), c.Param("slug"))
		return nil
	}
	w.Raw(`<ul class="comments">`)
	w.Int(int64(p.PostID))
	w.Raw(`</ul>`)
	return nil
}

func otherIsland(c *Ctx, w *Writer, p struct{}, s Slots) error {
	w.Raw("other")
	return nil
}

func fallbackLoading(w *Writer) error {
	w.Raw("<p>Loading…</p>")
	return nil
}

// deferPage renders a page with one deferred Comments island; the props
// come from the query (?mode=, ?id=).
func deferPage(c *Ctx, w *Writer) error {
	w.Raw(`<!DOCTYPE html><html><head><title>Post</title>`)
	w.HeadEnd()
	w.Raw(`</head><body>`)
	p := commentsProps{PostID: 42, Mode: c.URL.Query().Get("mode")}
	if err := Defer(c, w, commentsID, p, fallbackLoading); err != nil {
		return err
	}
	w.Raw(`</body></html>`)
	return nil
}

// headPage is a page with a </head> marker.
func headPage(body string) func(*Ctx, *Writer) error {
	return func(c *Ctx, w *Writer) error {
		w.Raw("<html><head>")
		w.HeadEnd()
		w.Raw("</head><body>" + body + "</body></html>")
		return nil
	}
}

var islandTable = []Island{
	NewIsland(commentsID, "src/components/Comments.orbit", comments),
	NewIsland("Other-00000000", "src/components/Other.orbit", otherIsland),
}

func islandRoutes() []Route {
	return []Route{
		{Pattern: "/post", File: "src/pages/post.orbit", Render: deferPage, Islands: true,
			Assets: []Asset{{Kind: "css", URL: "/_orbit/post-1234.css"}}},
		{Pattern: "/plain", File: "src/pages/plain.orbit", Render: headPage("plain"),
			Assets: []Asset{{Kind: "css", URL: "/_orbit/plain-1234.css"}}},
		{Pattern: "/lazy", File: "src/pages/lazy.orbit", Render: headPage("lazy"),
			Assets: []Asset{{Kind: "lazy", ID: "Counter-1c2d3e4f", URL: "/_orbit/Counter-ABCD.js"}, {Kind: "lazy", ID: "Counter-1c2d3e4f", URL: "/_orbit/Inner-EFGH.js"}, {Kind: "lazy", ID: "Counter-1c2d3e4f", URL: "/_orbit/Counter-ABCD.js"}, {Kind: "lazy", ID: "x</script>", URL: "/_orbit/x.js"}}},
	}
}

func islandSite(t testing.TB, opts ...Option) *Site {
	t.Helper()
	if tt, ok := t.(*testing.T); ok {
		tt.Setenv("ORBIT_STATIC", "")
		tt.Setenv("ORBIT_PUBLIC", "")
	}
	return NewSite(islandRoutes(), islandTable, append([]Option{WithStaticDir(t.TempDir())}, opts...)...)
}

var placeholderRe = regexp.MustCompile(`<orbit-island src="(/_orbit/island/([0-9a-f]{16})/([^"]+))" props="([^"]*)">`)

// placeholder renders /post?mode=… on h and returns the island URL path
// and the token.
func placeholder(t testing.TB, h http.Handler, mode string) (src, token string) {
	t.Helper()
	rec := do(h, "GET", "/post?mode="+mode)
	m := placeholderRe.FindStringSubmatch(rec.Body.String())
	if m == nil {
		t.Fatalf("no placeholder in %q", rec.Body)
	}
	return m[1], m[4]
}

func TestDeferPlaceholder(t *testing.T) {
	t.Setenv("ORBIT_KEY", "")
	s := islandSite(t)
	rec := do(s.Handler(), "GET", "/post")
	body := rec.Body.String()
	tok := testKeyring().sealer.Seal(commentsID, testBuildID, []byte(`{"PostID":42,"Mode":""}`))
	want := `<body><orbit-island src="/_orbit/island/0000000000000000/Comments-9b1e44a2" props="` + tok + `"><p>Loading…</p></orbit-island></body>`
	if !strings.Contains(body, want) {
		t.Errorf("page = %s\nwant it to contain %s", body, want)
	}
	// Deterministic across renders.
	if again := do(s.Handler(), "GET", "/post").Body.String(); again != body {
		t.Errorf("second render differs:\n%s\n%s", body, again)
	}
}

func TestDeferNoFallbackAndMarshalError(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf)
	c := &Ctx{keys: testKeyring()}
	if err := Defer(c, w, "X-1", struct{ A int }{1}, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(buf.String(), `<orbit-island src="/_orbit/island/0000000000000000/X-1" props="`) || !strings.HasSuffix(buf.String(), `"></orbit-island>`) {
		t.Errorf("placeholder = %s", buf.String())
	}
	buf.Reset()
	w = NewWriter(&buf)
	err := Defer(c, w, "X-1", struct{ F func() }{}, nil)
	if err == nil || !strings.Contains(err.Error(), "<X server:defer>") || w.Err() != err {
		t.Errorf("Defer of a func prop: err = %v, w.Err = %v", err, w.Err())
	}
}

func TestIslandEndpoint(t *testing.T) {
	t.Setenv("ORBIT_KEY", "")
	s := islandSite(t)
	h := s.Handler()
	src, tok := placeholder(t, h, "")

	t.Run("GET", func(t *testing.T) {
		rec := do(h, "GET", src+"?p="+tok)
		if rec.Code != 200 || rec.Body.String() != `<ul class="comments">42</ul>` {
			t.Fatalf("got %d %q", rec.Code, rec.Body)
		}
		for k, v := range map[string]string{
			"Content-Type":   "text/html; charset=utf-8",
			"Content-Length": "28",
			"Cache-Control":  "private, no-store",
			"X-Robots-Tag":   "noindex",
		} {
			if got := rec.Header().Get(k); got != v {
				t.Errorf("%s = %q, want %q", k, got, v)
			}
		}
	})
	t.Run("HEAD", func(t *testing.T) {
		rec := do(h, "HEAD", src+"?p="+tok)
		if rec.Code != 200 || rec.Body.Len() != 0 || rec.Header().Get("Content-Length") != "28" {
			t.Errorf("got %d %q, Content-Length %q", rec.Code, rec.Body, rec.Header().Get("Content-Length"))
		}
	})
	t.Run("POST", func(t *testing.T) {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("POST", src, strings.NewReader(tok)))
		if rec.Code != 200 || rec.Body.String() != `<ul class="comments">42</ul>` {
			t.Errorf("got %d %q", rec.Code, rec.Body)
		}
	})
	t.Run("POST too large", func(t *testing.T) {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("POST", src, strings.NewReader(strings.Repeat("A", maxIslandBody+1))))
		if rec.Code != http.StatusRequestEntityTooLarge || rec.Body.Len() != 0 {
			t.Errorf("got %d %q, want an empty 413", rec.Code, rec.Body)
		}
	})
	t.Run("PUT", func(t *testing.T) {
		rec := do(h, "PUT", src+"?p="+tok)
		if rec.Code != 405 || rec.Header().Get("Allow") != "GET, HEAD, POST" {
			t.Errorf("got %d, Allow %q", rec.Code, rec.Header().Get("Allow"))
		}
	})

	raw, _ := base64.RawURLEncoding.DecodeString(tok)
	raw[len(raw)/2] ^= 1
	flipped := base64.RawURLEncoding.EncodeToString(raw)
	otherKey, _ := islandcrypto.New(bytes.Repeat([]byte{7}, 32))
	foreign := otherKey.Seal(commentsID, testBuildID, []byte(`{"PostID":42}`))
	badJSON := s.keys.sealer.Seal(commentsID, testBuildID, []byte(`{"PostID":"x"}`))
	notJSON := s.keys.sealer.Seal(commentsID, testBuildID, []byte(`nope`))
	for _, tt := range []struct {
		name, target string
		code         int
	}{
		{"flipped byte", src + "?p=" + flipped, 400},
		{"missing token", src, 400},
		{"empty token", src + "?p=", 400},
		{"other component", "/_orbit/island/0000000000000000/Other-00000000?p=" + tok, 400},
		{"other build ID", "/_orbit/island/1111111111111111/" + commentsID + "?p=" + tok, 400},
		{"other key", src + "?p=" + foreign, 400},
		{"props of the wrong type", src + "?p=" + badJSON, 400},
		{"props not JSON", src + "?p=" + notJSON, 400},
		{"unknown component", "/_orbit/island/0000000000000000/Nope-00000000?p=" + tok, 404},
		{"no component", "/_orbit/island/0000000000000000", 404},
		{"no build ID", "/_orbit/island//" + commentsID + "?p=" + tok, 404},
		{"extra segment", src + "/x?p=" + tok, 404},
	} {
		rec := do(h, "GET", tt.target)
		if rec.Code != tt.code || rec.Body.Len() != 0 {
			t.Errorf("%s: got %d %q, want an empty %d", tt.name, rec.Code, rec.Body, tt.code)
		}
	}
}

func TestIslandErrors(t *testing.T) {
	t.Setenv("ORBIT_KEY", "")
	for _, dev := range []bool{false, true} {
		t.Run(fmt.Sprint("dev=", dev), func(t *testing.T) {
			if dev {
				t.Setenv("ORBIT_DEV", "1")
			} else {
				t.Setenv("ORBIT_DEV", "")
			}
			logs := captureLog(t)
			h := islandSite(t).Handler()
			get := func(mode string) *httptest.ResponseRecorder {
				src, tok := placeholder(t, h, mode)
				return do(h, "GET", src+"?p="+tok)
			}
			if rec := get("notfound"); rec.Code != 404 || rec.Body.Len() != 0 {
				t.Errorf("ErrNotFound: got %d %q, want an empty 404", rec.Code, rec.Body)
			}
			for _, mode := range []string{"error", "panic"} {
				rec := get(mode)
				wantBody := ""
				if dev {
					wantBody = map[string]string{"error": "db down\n", "panic": "panic: boom\n"}[mode]
				}
				if rec.Code != 500 || rec.Body.String() != wantBody {
					t.Errorf("%s: got %d %q, want 500 %q", mode, rec.Code, rec.Body, wantBody)
				}
			}
			if !strings.Contains(logs.String(), "db down") || !strings.Contains(logs.String(), "panic: boom") {
				t.Errorf("log = %q", logs)
			}
			rec := get("redirect")
			if rec.Code != 302 || rec.Header().Get("Location") != "/login" || rec.Header().Get("X-Before") != "1" {
				t.Errorf("redirect: got %d, Location %q", rec.Code, rec.Header().Get("Location"))
			}
			if rec := get("cache"); rec.Code != 200 || rec.Header().Get("Cache-Control") != "public, max-age=60" {
				t.Errorf("own Cache-Control: got %d %q", rec.Code, rec.Header().Get("Cache-Control"))
			}
		})
	}
}

func TestIslandMiddleware(t *testing.T) {
	t.Setenv("ORBIT_KEY", "")
	s := islandSite(t)
	outer := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r, l := EnsureLocals[islandLocals](r)
			l.User = "ada"
			w.Header().Set("X-Outer", "1")
			next.ServeHTTP(w, r)
		})
	}
	h := outer(s.Handler())
	src, tok := placeholder(t, h, "locals")
	req := httptest.NewRequest("GET", src+"?p="+tok, nil)
	req.Header.Set("Referer", "http://example.com/post?mode=locals")
	req.AddCookie(&http.Cookie{Name: "session", Value: "s1"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	want := `user=ada cookie=true url=` + src + ` referer=http://example.com/post?mode=locals param=""`
	if rec.Code != 200 || rec.Body.String() != want {
		t.Errorf("got %d %q\nwant %q", rec.Code, rec.Body, want)
	}
	if rec.Header().Get("X-Outer") != "1" {
		t.Error("the outer middleware's header is missing")
	}
}

func TestIslandKeys(t *testing.T) {
	key1 := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32))
	key2 := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{2}, 32))
	opens := func(page, island *Site) bool {
		src, tok := placeholder(t, page.Handler(), "")
		return do(island.Handler(), "GET", src+"?p="+tok).Code == 200
	}

	t.Setenv("ORBIT_KEY", key1)
	a, b := islandSite(t), islandSite(t)
	if !opens(a, b) || !opens(b, a) {
		t.Error("two sites with the same ORBIT_KEY don't open each other's tokens")
	}
	srcA, tokA := placeholder(t, a.Handler(), "")
	if srcB, tokB := placeholder(t, b.Handler(), ""); srcA != srcB || tokA != tokB {
		t.Error("two instances with the same key and build ID give different URLs")
	}
	t.Setenv("ORBIT_KEY", key2)
	c := islandSite(t)
	if opens(a, c) {
		t.Error("a site with another ORBIT_KEY opened the token")
	}
	if !opens(islandSite(t, WithKey(bytes.Repeat([]byte{1}, 32))), a) {
		t.Error("WithKey doesn't override ORBIT_KEY")
	}

	// A second build: same key, another build ID.
	t.Setenv("ORBIT_KEY", key1)
	build2 := islandSite(t)
	build2.keys = &keyring{sealer: build2.keys.sealer, buildID: "1111111111111111"}
	src2, tok2 := placeholder(t, build2.Handler(), "")
	if src2 == srcA || tok2 == tokA {
		t.Error("another build ID gives the same URL")
	}
	if !opens(build2, a) || !opens(a, build2) {
		t.Error("builds that share ORBIT_KEY don't open each other's tokens")
	}

	// Without ORBIT_KEY, two builds have different embedded keys.
	t.Setenv("ORBIT_KEY", "")
	p1, p2 := islandSite(t), islandSite(t)
	p1.keys = &keyring{sealer: mustSealer(bytes.Repeat([]byte{3}, 32)), buildID: "2222222222222222"}
	p2.keys = &keyring{sealer: mustSealer(bytes.Repeat([]byte{4}, 32)), buildID: "3333333333333333"}
	if opens(p1, p2) {
		t.Error("separate builds without ORBIT_KEY opened each other's tokens")
	}
}

func TestIslandKeyInvalid(t *testing.T) {
	for _, tt := range []struct{ env, want string }{
		{"not base64!", "ORBIT_KEY isn't valid standard base64"},
		{base64.StdEncoding.EncodeToString(make([]byte, 16)), "ORBIT_KEY decodes to 16 bytes"},
		{base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0xff}, 32)), "ORBIT_KEY isn't valid standard base64"},
	} {
		t.Setenv("ORBIT_KEY", tt.env)
		msg := panicMessage(func() { islandSite(t) })
		if !strings.Contains(msg, tt.want) || !strings.Contains(msg, "orbit create-key") {
			t.Errorf("ORBIT_KEY=%q: panic %q, want %q", tt.env, msg, tt.want)
		}
		if strings.Contains(msg, tt.env) {
			t.Errorf("the panic message %q shows the key", msg)
		}
	}
	if msg := panicMessage(func() { WithKey(make([]byte, 31)) }); !strings.Contains(msg, "32 bytes") {
		t.Errorf("WithKey(31 bytes): panic %q", msg)
	}
	t.Setenv("ORBIT_KEY", "")
	if msg := panicMessage(func() {
		NewSite(nil, []Island{islandTable[0], islandTable[0]})
	}); !strings.Contains(msg, "registered twice") {
		t.Errorf("duplicate island: panic %q", msg)
	}
}

func panicMessage(f func()) (msg string) {
	defer func() { msg = fmt.Sprint(recover()) }()
	f()
	return ""
}

func TestIslandTestKey(t *testing.T) {
	t.Setenv("ORBIT_KEY", "")
	s := islandSite(t)
	if s.keys != testKeyring() || s.keys.buildID != testBuildID {
		t.Errorf("under go test: keys %p (build ID %q), want the fixed test keyring", s.keys, s.keys.buildID)
	}
	// orbittest.Render's path uses the fixed key too, even with ORBIT_KEY.
	t.Setenv("ORBIT_KEY", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{9}, 32)))
	html, err := testRender(httptest.NewRequest("GET", "/", nil), nil, nil, func(c, w any) error {
		return Defer(c.(*Ctx), w.(*Writer), commentsID, commentsProps{PostID: 1}, nil)
	})
	tok := testKeyring().sealer.Seal(commentsID, testBuildID, []byte(`{"PostID":1,"Mode":""}`))
	if err != nil || !strings.Contains(html, `/0000000000000000/`+commentsID+`" props="`+tok+`"`) {
		t.Errorf("testRender = %q, %v", html, err)
	}
	if s2 := islandSite(t); s2.keys == testKeyring() {
		t.Error("ORBIT_KEY doesn't override the test key")
	}
}

func TestRenderComponentDefer(t *testing.T) {
	t.Setenv("ORBIT_KEY", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{5}, 32)))
	s := islandSite(t)
	frag := func(c *Ctx, w *Writer, p commentsProps, _ Slots) error {
		return Defer(c, w, commentsID, p, nil)
	}
	rec := httptest.NewRecorder()
	if err := RenderComponent(rec, httptest.NewRequest("GET", "/like", nil), frag, commentsProps{PostID: 3}); err != nil {
		t.Fatal(err)
	}
	m := placeholderRe.FindStringSubmatch(rec.Body.String())
	if m == nil {
		t.Fatalf("no placeholder in %q", rec.Body)
	}
	if got := do(s.Handler(), "GET", m[1]+"?p="+m[4]); got.Code != 200 || got.Body.String() != `<ul class="comments">3</ul>` {
		t.Errorf("fragment's island: got %d %q", got.Code, got.Body)
	}
}

// escLT and escGT are the \u escapes of < and > in JSON.
const escLT, escGT = "\\u003c", "\\u003e"

func TestLoaderInjection(t *testing.T) {
	t.Setenv("ORBIT_KEY", "")
	for _, dev := range []bool{false, true} {
		t.Run(fmt.Sprint("dev=", dev), func(t *testing.T) {
			if dev {
				t.Setenv("ORBIT_DEV", "1")
			} else {
				t.Setenv("ORBIT_DEV", "")
			}
			h := islandSite(t).Handler()
			devJSON := fmt.Sprint(dev)

			post := do(h, "GET", "/post").Body.String()
			wantPost := `<title>Post</title>` + loaderStyle +
				`<script type="application/json" id="orbit-loader">{"dev":` + devJSON + `,"lazy":{}}</script>` +
				`<script type="module">` + loaderIslandsJS + `</script>` +
				`<link rel="stylesheet" href="/_orbit/post-1234.css"></head>`
			if !strings.Contains(post, wantPost) {
				t.Errorf("/post = %s\nwant it to contain %s", post, wantPost)
			}
			if strings.Contains(post, loaderLazyJS) {
				t.Error("/post has the lazy loader")
			}

			lazy := do(h, "GET", "/lazy").Body.String()
			wantLazy := `<head>` + loaderStyle +
				`<script type="application/json" id="orbit-loader">{"dev":` + devJSON + `,"lazy":{"Counter-1c2d3e4f":["/_orbit/Counter-ABCD.js","/_orbit/Inner-EFGH.js"],"x` + escLT + `/script` + escGT + `":["/_orbit/x.js"]}}</script>` +
				`<script type="module">` + loaderLazyJS + `</script></head>`
			if lazy != `<html>`+wantLazy+`<body>lazy</body></html>` {
				t.Errorf("/lazy = %s\nwant %s", lazy, wantLazy)
			}

			plain := do(h, "GET", "/plain").Body.String()
			if plain != `<html><head><link rel="stylesheet" href="/_orbit/plain-1234.css"></head><body>plain</body></html>` {
				t.Errorf("/plain = %s", plain)
			}
		})
	}
}

func TestLoaderConstants(t *testing.T) {
	for name, js := range map[string]string{"loaderIslandsJS": loaderIslandsJS, "loaderLazyJS": loaderLazyJS} {
		if strings.Contains(strings.ToLower(js), "</script") || strings.Contains(js, "<!--") {
			t.Errorf("%s can't be inlined in a <script>", name)
		}
	}
}

func TestQueryValue(t *testing.T) {
	for _, tt := range []struct{ raw, want string }{
		{"", ""},
		{"p=abc", "abc"},
		{"x=1&p=abc&p=def", "abc"},
		{"pp=1&p=a%2Db", "a-b"},
		{"p=a+b", "a b"},
		{"p=%zz", ""},
		{"p", ""},
	} {
		if got := queryValue(tt.raw, "p"); got != tt.want {
			t.Errorf("queryValue(%q) = %q, want %q", tt.raw, got, tt.want)
		}
	}
}

// Benchmarks (PLAN.md §13): Defer (JSON + seal), Open, and the whole island
// endpoint (open + decode + render + write).

type benchProps struct{ Text string }

func benchPropsOf(n int) benchProps {
	// {"Text":"…"} is n bytes of JSON.
	return benchProps{Text: strings.Repeat("a", n-len(`{"Text":""}`))}
}

func benchComponent(c *Ctx, w *Writer, p benchProps, s Slots) error {
	w.Raw(`<div class="comments">`)
	w.Int(int64(len(p.Text)))
	w.Raw(`</div>`)
	return nil
}

var benchSizes = []struct {
	name string
	n    int
}{{"128B", 128}, {"1KB", 1 << 10}, {"8KB", 8 << 10}}

func BenchmarkDefer(b *testing.B) {
	c := &Ctx{keys: testKeyring()}
	buf := bytes.NewBuffer(make([]byte, 0, 64<<10))
	w := NewWriter(buf)
	for _, sz := range benchSizes {
		p := benchPropsOf(sz.n)
		b.Run(sz.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				buf.Reset()
				if err := Defer(c, w, commentsID, p, fallbackLoading); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkIslandOpen(b *testing.B) {
	kr := testKeyring()
	for _, sz := range benchSizes {
		tok := kr.sealer.Seal(commentsID, testBuildID, []byte(fmt.Sprintf(`{"Text":"%s"}`, benchPropsOf(sz.n).Text)))
		b.Run(sz.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, err := kr.sealer.Open(commentsID, testBuildID, tok); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkIslandEndpoint(b *testing.B) {
	os.Setenv("ORBIT_STATIC", "")
	s := NewSite(nil, []Island{NewIsland(commentsID, "c.orbit", benchComponent)}, WithStaticDir(b.TempDir()))
	h := s.Handler()
	for _, sz := range benchSizes {
		data := fmt.Sprintf(`{"Text":"%s"}`, benchPropsOf(sz.n).Text)
		target := "/_orbit/island/" + s.keys.buildID + "/" + commentsID + "?p=" + s.keys.sealer.Seal(commentsID, s.keys.buildID, []byte(data))
		req := httptest.NewRequest("GET", target, nil)
		w := newFakeResponseWriter()
		b.Run(sz.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				w.reset()
				h.ServeHTTP(w, req)
				if w.status != 200 {
					b.Fatal(w.status)
				}
			}
		})
	}
}

// BenchmarkServeIslandsPage serves a page with a server:defer placeholder
// and the injected loader.
func BenchmarkServeIslandsPage(b *testing.B) {
	os.Setenv("ORBIT_STATIC", "")
	s := NewSite(islandRoutes(), islandTable, WithStaticDir(b.TempDir()))
	h := s.Handler()
	req := httptest.NewRequest("GET", "/post", nil)
	w := newFakeResponseWriter()
	b.ReportAllocs()
	for b.Loop() {
		w.reset()
		h.ServeHTTP(w, req)
	}
}

func BenchmarkServeLazyPage(b *testing.B) {
	os.Setenv("ORBIT_STATIC", "")
	s := NewSite(islandRoutes(), islandTable, WithStaticDir(b.TempDir()))
	h := s.Handler()
	req := httptest.NewRequest("GET", "/lazy", nil)
	w := newFakeResponseWriter()
	b.ReportAllocs()
	for b.Loop() {
		w.reset()
		h.ServeHTTP(w, req)
	}
}
