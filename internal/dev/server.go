package dev

import (
	"bytes"
	"context"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"sync"
)

//go:embed reload.js
var reloadJS []byte

// reloadTag is the script tag the proxy adds before </body>.
const reloadTag = `<script src="/__lyr/reload.js" async></script>`

// Paths the proxy answers itself (PLAN.md §7): everything else is the
// user's server's.
const (
	lyrPrefix  = "/__lyr/"
	eventsPath = lyrPrefix + "events"
	reloadPath = lyrPrefix + "reload.js"
)

// server is the dev proxy: it serves /__lyr/*, makes requests wait while
// a rebuild is in progress, shows the error overlay for HTML requests while
// the last build failed, and proxies everything else to the running app,
// adding the reload script to HTML pages.
type server struct {
	root   string
	stderr io.Writer

	mu      sync.Mutex
	busy    bool
	idle    chan struct{} // closed when busy is false
	app     *app          // the running app; nil when none
	failure *failure      // the last build's (or the app's) failure

	hub       hub
	transport *http.Transport
	proxy     *httputil.ReverseProxy
	// roundTrip is the proxy's transport; tests replace it.
	roundTrip http.RoundTripper

	warnEncoding sync.Once
}

// targetKey is the request context key of the app address a proxied
// request goes to.
type targetKey struct{}

func newServer(root string, stderr io.Writer) *server {
	s := &server{root: root, stderr: stderr, busy: true, idle: make(chan struct{})}
	s.transport = &http.Transport{
		Proxy:               nil,
		DialContext:         (&net.Dialer{}).DialContext,
		MaxIdleConnsPerHost: 16,
		// Compression is the app's business: the proxy asks for none
		// (Accept-Encoding is removed) and passes through what it gets.
		DisableCompression: true,
	}
	s.roundTrip = s.transport
	s.proxy = &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			addr, _ := pr.In.Context().Value(targetKey{}).(string)
			pr.SetURL(&url.URL{Scheme: "http", Host: addr})
			pr.Out.Host = pr.In.Host // the app sees the Host the browser sent
			// Uncompressed responses, so the reload script can be added
			// even under a gzip middleware (PLAN.md §7).
			pr.Out.Header.Del("Accept-Encoding")
		},
		Transport:      retryTransport{s},
		FlushInterval:  -1,
		ModifyResponse: s.inject,
		ErrorHandler:   s.proxyError,
		ErrorLog:       log.New(io.Discard, "", 0),
	}
	return s
}

// setBusy makes requests wait until setIdle.
func (s *server) setBusy() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.busy {
		s.busy = true
		s.idle = make(chan struct{})
	}
}

// setIdle releases the waiting requests.
func (s *server) setIdle() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.busy {
		s.busy = false
		close(s.idle)
	}
}

// wait blocks while a rebuild is in progress. It reports false when ctx
// ended first.
func (s *server) wait(ctx context.Context) bool {
	s.mu.Lock()
	ch := s.idle
	s.mu.Unlock()
	select {
	case <-ch:
		return true
	case <-ctx.Done():
		return false
	}
}

// state returns the running app and the current failure.
func (s *server) state() (*app, *failure) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.app, s.failure
}

func (s *server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, lyrPrefix) {
		s.serveLyr(w, r)
		return
	}
	if !s.wait(r.Context()) {
		return
	}
	a, f := s.state()
	if f != nil && wantsHTML(r) {
		s.serveOverlay(w, r, f)
		return
	}
	if a == nil {
		if f == nil {
			f = &failure{Title: "No server running", Text: "lyr dev has no running server; see the terminal."}
		}
		if wantsHTML(r) {
			s.serveOverlay(w, r, f)
			return
		}
		http.Error(w, "lyr dev: "+f.Title+"\n\n"+f.Text, http.StatusServiceUnavailable)
		return
	}
	s.proxy.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), targetKey{}, a.addr)))
}

// wantsHTML reports whether r is a page request: a GET or HEAD that
// accepts text/html, as a browser's navigation does.
func wantsHTML(r *http.Request) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	return strings.Contains(r.Header.Get("Accept"), "text/html")
}

func (s *server) serveOverlay(w http.ResponseWriter, r *http.Request, f *failure) {
	body := overlayHTML(s.root, f)
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	h.Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(http.StatusInternalServerError)
	if r.Method != http.MethodHead {
		w.Write(body)
	}
}

// serveLyr answers /__lyr/*.
func (s *server) serveLyr(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case eventsPath:
		s.serveEvents(w, r)
	case reloadPath:
		h := w.Header()
		h.Set("Content-Type", "text/javascript; charset=utf-8")
		h.Set("Cache-Control", "no-cache")
		http.ServeContent(w, r, "reload.js", startTime, bytes.NewReader(reloadJS))
	default:
		http.NotFound(w, r)
	}
}

// serveEvents is the server-sent event stream: a "reload" event after
// every rebuild.
func (s *server) serveEvents(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	ch := s.hub.subscribe()
	defer s.hub.unsubscribe(ch)
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	io.WriteString(w, ": lyr dev\n\n")
	fl.Flush()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ch:
			if _, err := io.WriteString(w, "event: reload\ndata: {}\n\n"); err != nil {
				return
			}
			fl.Flush()
		}
	}
}

// inject adds the reload script before the last </body> of an
// uncompressed text/html response, and recomputes Content-Length. Other
// responses, and HTML without </body> (island fragments), pass through.
func (s *server) inject(resp *http.Response) error {
	if resp.Request.Method == http.MethodHead || resp.Body == nil || resp.Body == http.NoBody {
		return nil
	}
	mt, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if mt != "text/html" {
		return nil
	}
	if ce := resp.Header.Get("Content-Encoding"); ce != "" && !strings.EqualFold(ce, "identity") {
		s.warnEncoding.Do(func() {
			fmt.Fprintf(s.stderr, "lyr dev: %s %s came back with Content-Encoding: %s even though the proxy removes Accept-Encoding, so it has no live-reload script; make your compression middleware check Accept-Encoding\n", resp.Request.Method, resp.Request.URL.Path, ce)
		})
		return nil
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return err
	}
	if i := lastIndexFold(body, "</body>"); i >= 0 {
		out := make([]byte, 0, len(body)+len(reloadTag))
		out = append(append(append(out, body[:i]...), reloadTag...), body[i:]...)
		body = out
	}
	resp.Body = io.NopCloser(bytes.NewReader(body))
	resp.ContentLength = int64(len(body))
	resp.Header.Set("Content-Length", strconv.Itoa(len(body)))
	resp.TransferEncoding = nil
	return nil
}

// lastIndexFold is the index of the last ASCII case-insensitive match of
// the lower-case needle in b, or -1.
func lastIndexFold(b []byte, needle string) int {
	for i := len(b) - len(needle); i >= 0; i-- {
		if b[i] == '<' && bytes.EqualFold(b[i:i+len(needle)], []byte(needle)) {
			return i
		}
	}
	return -1
}

// proxyError answers a request the app couldn't take.
func (s *server) proxyError(w http.ResponseWriter, r *http.Request, err error) {
	if r.Context().Err() != nil {
		return // the browser went away
	}
	f := &failure{Title: "Your server didn't answer", Text: err.Error()}
	if wantsHTML(r) {
		body := overlayHTML(s.root, f)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		w.WriteHeader(http.StatusBadGateway)
		w.Write(body)
		return
	}
	http.Error(w, "lyr dev: your server didn't answer: "+err.Error(), http.StatusBadGateway)
}

// retryTransport sends a proxied request to the app. When the connection
// is refused because the app it was meant for was being replaced, it waits
// for the rebuild and sends it to the new app: such a request was never
// sent, so it's safe to repeat.
type retryTransport struct{ s *server }

func (t retryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.s.roundTrip.RoundTrip(req)
	if err == nil || !isDialError(err) || (req.Body != nil && req.Body != http.NoBody && req.GetBody == nil) {
		return resp, err
	}
	if !t.s.wait(req.Context()) {
		return nil, err
	}
	a, _ := t.s.state()
	if a == nil || a.addr == req.URL.Host {
		return nil, err
	}
	retry := req.Clone(req.Context())
	retry.URL.Host = a.addr
	if req.GetBody != nil {
		if retry.Body, err = req.GetBody(); err != nil {
			return nil, err
		}
	}
	return t.s.roundTrip.RoundTrip(retry)
}

func isDialError(err error) bool {
	var op *net.OpError
	return errors.As(err, &op) && op.Op == "dial"
}

// hub fans reload events out to the /__lyr/events subscribers.
type hub struct {
	mu   sync.Mutex
	subs map[chan struct{}]bool
}

func (h *hub) subscribe() chan struct{} {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.subs == nil {
		h.subs = map[chan struct{}]bool{}
	}
	ch := make(chan struct{}, 1)
	h.subs[ch] = true
	return ch
}

func (h *hub) unsubscribe(ch chan struct{}) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.subs, ch)
}

// broadcast sends a reload to every subscriber (at most one pending each).
func (h *hub) broadcast() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

// subscribers is the number of connected event streams.
func (h *hub) subscribers() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.subs)
}
