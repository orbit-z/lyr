package dev

import (
	"bytes"
	"compress/gzip"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"testing/synctest"
)

// handlerTransport answers proxied requests with a handler, in memory.
type handlerTransport struct{ h http.Handler }

func (t handlerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	rec := httptest.NewRecorder()
	t.h.ServeHTTP(rec, r)
	resp := rec.Result()
	resp.Request = r
	return resp, nil
}

// testServer is a server whose app is h, idle.
func testServer(t *testing.T, h http.Handler) (*server, *bytes.Buffer) {
	var stderr bytes.Buffer
	s := newServer(t.TempDir(), &stderr)
	s.roundTrip = handlerTransport{h}
	s.app = &app{addr: "127.0.0.1:1", done: make(chan struct{})}
	s.setIdle()
	return s, &stderr
}

func get(s http.Handler, path string, header ...string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("GET", path, nil)
	for i := 0; i+1 < len(header); i += 2 {
		r.Header.Set(header[i], header[i+1])
	}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	return w
}

// gzipMiddleware compresses responses for clients that accept gzip, as a
// user's middleware would.
func gzipMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			next.ServeHTTP(w, r)
			return
		}
		rec := httptest.NewRecorder()
		next.ServeHTTP(rec, r)
		for k, v := range rec.Header() {
			w.Header()[k] = v
		}
		var b bytes.Buffer
		zw := gzip.NewWriter(&b)
		zw.Write(rec.Body.Bytes())
		zw.Close()
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Set("Content-Length", strconv.Itoa(b.Len()))
		w.WriteHeader(rec.Code)
		w.Write(b.Bytes())
	})
}

func TestInject(t *testing.T) {
	const page = "<!DOCTYPE html><html><body><p>hi</p></BODY></html>"
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Length", strconv.Itoa(len(page)))
		io.WriteString(w, page)
	})
	mux.HandleFunc("/_lyr/island/0000000000000000/x", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		io.WriteString(w, "<section>fragment</section>")
	})
	mux.HandleFunc("/robots.txt", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "User-agent: *\n</body>")
	})
	mux.HandleFunc("/always-gzip", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Header().Set("Content-Encoding", "gzip")
		zw := gzip.NewWriter(w)
		io.WriteString(zw, page)
		zw.Close()
	})
	s, stderr := testServer(t, gzipMiddleware(mux))

	// The browser accepts gzip, but the proxy removes Accept-Encoding, so
	// the user's gzip middleware leaves the page alone and it gets the
	// script.
	w := get(s, "/", "Accept-Encoding", "gzip, br")
	want := "<!DOCTYPE html><html><body><p>hi</p>" + reloadTag + "</BODY></html>"
	if w.Body.String() != want {
		t.Errorf("page body:\n%s\nwant\n%s", w.Body, want)
	}
	if got := w.Header().Get("Content-Length"); got != strconv.Itoa(len(want)) {
		t.Errorf("Content-Length %s, want %d", got, len(want))
	}
	if w.Header().Get("Content-Encoding") != "" {
		t.Errorf("Content-Encoding %q", w.Header().Get("Content-Encoding"))
	}

	// A fragment has no </body>: untouched.
	if w := get(s, "/_lyr/island/0000000000000000/x"); w.Body.String() != "<section>fragment</section>" {
		t.Errorf("fragment: %q", w.Body)
	}
	// Not HTML: untouched.
	if w := get(s, "/robots.txt"); w.Body.String() != "User-agent: *\n</body>" {
		t.Errorf("robots.txt: %q", w.Body)
	}
	// Still compressed: passed through, with one warning.
	for range 2 {
		w := get(s, "/always-gzip")
		zr, err := gzip.NewReader(w.Body)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(zr)
		if string(body) != page {
			t.Errorf("compressed page changed: %q", body)
		}
	}
	if n := strings.Count(stderr.String(), "Content-Encoding: gzip"); n != 1 {
		t.Errorf("%d warnings, want 1:\n%s", n, stderr)
	}
}

// TestRequestsWait checks that requests wait while a rebuild is in
// progress, and that /__lyr/* never waits.
func TestRequestsWait(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s, _ := testServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, "new app")
		}))
		s.setBusy()
		done := make(chan *httptest.ResponseRecorder)
		go func() { done <- get(s, "/") }()
		synctest.Wait()
		select {
		case <-done:
			t.Fatal("request finished during the rebuild")
		default:
		}
		if w := get(s, reloadPath); w.Code != 200 || !bytes.Equal(w.Body.Bytes(), reloadJS) {
			t.Errorf("reload.js during a rebuild: %d", w.Code)
		}
		s.setIdle()
		w := <-done
		if w.Code != 200 || w.Body.String() != "new app" {
			t.Errorf("after the rebuild: %d %q", w.Code, w.Body)
		}
	})
}

// TestRetryDuringSwap: a request that finds the old app gone (connection
// refused) waits for the swap and goes to the new app.
func TestRetryDuringSwap(t *testing.T) {
	newApp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "new app")
	}))
	defer newApp.Close()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	oldAddr := l.Addr().String()
	l.Close() // the old app is gone

	s := newServer(t.TempDir(), io.Discard)
	s.app = &app{addr: oldAddr, done: make(chan struct{})}
	s.setIdle()
	// The swap happens while the request is refused.
	s.roundTrip = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host == oldAddr {
			s.mu.Lock()
			s.app = &app{addr: strings.TrimPrefix(newApp.URL, "http://"), done: make(chan struct{})}
			s.mu.Unlock()
		}
		return s.transport.RoundTrip(r)
	})
	w := get(s, "/")
	if w.Code != 200 || w.Body.String() != "new app" {
		t.Errorf("%d %q", w.Code, w.Body)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestOverlay(t *testing.T) {
	s, _ := testServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "old app")
	}))
	src := "---\ntype Props struct{ Post string }\n---\n<article>\n\t<h2>{props.Psot}</h2>\n</article>\n"
	if err := os.MkdirAll(filepath.Join(s.root, "src/components"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.root, "src/components/Card.lyr"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	s.failure = compileFailure("# example.com/site/lyr_gen/impl/components/card\nsrc/components/Card.lyr:5:13: props.Psot undefined (type Props has no field or method Psot, but does have field Post)\n")

	w := get(s, "/", "Accept", "text/html,application/xhtml+xml")
	body := w.Body.String()
	if w.Code != 500 || w.Header().Get("Content-Type") != "text/html; charset=utf-8" {
		t.Errorf("overlay: %d %s", w.Code, w.Header().Get("Content-Type"))
	}
	for _, want := range []string{
		`<span class="pos">src/components/Card.lyr:5:13</span>: props.Psot undefined`,
		`&lt;h2&gt;{props.Psot}&lt;/h2&gt;`, // the code frame
		"&gt; 5 │ ",
		reloadTag,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("overlay has no %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "# example.com") {
		t.Errorf("overlay shows the go build package header")
	}
	// The caret is under column 13, the P (the tab counts as 4 columns).
	if !strings.Contains(body, `<span class="caret">`+strings.Repeat(" ", 4+11)+"^</span>") {
		t.Errorf("caret misplaced:\n%s", body)
	}
	// Not a page request: the old app answers.
	if w := get(s, "/api/x", "Accept", "application/json"); w.Body.String() != "old app" {
		t.Errorf("API request during a failure: %q", w.Body)
	}
}

func TestStartFailureOverlay(t *testing.T) {
	f := &failure{Title: "Your server failed to start", Text: "your server exited before it accepted connections: exit status 1", Output: "2026/10/25 db: connection <refused>\n"}
	body := string(overlayHTML(t.TempDir(), f))
	if !strings.Contains(body, "db: connection &lt;refused&gt;") || !strings.Contains(body, "exit status 1") {
		t.Errorf("overlay:\n%s", body)
	}
}

func TestEvents(t *testing.T) {
	s, _ := testServer(t, http.NotFoundHandler())
	ts := httptest.NewServer(s)
	defer ts.Close()
	resp, err := http.Get(ts.URL + eventsPath)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("Content-Type %q", ct)
	}
	buf := make([]byte, 64)
	n, _ := resp.Body.Read(buf) // the comment line
	if !strings.HasPrefix(string(buf[:n]), ":") {
		t.Errorf("first bytes %q", buf[:n])
	}
	s.hub.broadcast()
	var got []byte
	for !bytes.Contains(got, []byte("\n\n")) {
		n, err := resp.Body.Read(buf)
		got = append(got, buf[:n]...)
		if err != nil {
			break
		}
	}
	if string(got) != "event: reload\ndata: {}\n\n" {
		t.Errorf("event %q", got)
	}
}

func TestListenLoopback(t *testing.T) {
	ls, port, err := listen(false, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range ls {
		defer l.Close()
		ip := l.Addr().(*net.TCPAddr).IP
		if !ip.IsLoopback() {
			t.Errorf("without --host, listening on %v", l.Addr())
		}
	}
	if port == 0 || len(ls) == 0 {
		t.Fatalf("port %d, %d listeners", port, len(ls))
	}

	// A taken port: the next one is used.
	ls2, port2, err := listen(false, ls[0].Addr().(*net.TCPAddr).Port)
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range ls2 {
		defer l.Close()
	}
	if port2 == ls[0].Addr().(*net.TCPAddr).Port {
		t.Errorf("port %d reused", port2)
	}

	lh, _, err := listen(true, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer lh[0].Close()
	if ip := lh[0].Addr().(*net.TCPAddr).IP; !ip.IsUnspecified() {
		t.Errorf("with --host, listening on %v", lh[0].Addr())
	}
}

func TestAppEnv(t *testing.T) {
	has := func(env []string, kv string) bool {
		for _, e := range env {
			if e == kv {
				return true
			}
		}
		return false
	}
	env := appEnv([]string{"PATH=/bin"}, nil)
	for _, kv := range []string{"LYR_DEV=1", "LYR_STATIC=.lyr/dev", "LYR_PUBLIC=public"} {
		if !has(env, kv) {
			t.Errorf("no %s in %v", kv, env)
		}
	}
	var key string
	for _, e := range env {
		if v, ok := strings.CutPrefix(e, "LYR_KEY="); ok {
			key = v
		}
	}
	if len(key) != 44 {
		t.Errorf("session key %q", key)
	}
	// The user's own key wins.
	env = appEnv([]string{"LYR_KEY=mine"}, nil)
	n := 0
	for _, e := range env {
		if strings.HasPrefix(e, "LYR_KEY=") {
			n++
		}
	}
	if n != 1 || !has(env, "LYR_KEY=mine") {
		t.Errorf("user key: %v", env)
	}
}
