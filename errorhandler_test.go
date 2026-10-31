package lyr

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The app's typed errors in these tests.
type accessRefused struct{ next string }

func (e *accessRefused) Error() string { return "access refused" }

var errUnavailable = errors.New("catalog: unavailable")

// ehInner is a nested component that fails with the error named by
// ?fail=; ehOuter renders it, so the error crosses two components.
func ehInner(c *Ctx, w *Writer, p struct{}, s Slots) error {
	if err := w.Enter("src/components/Inner.lyr"); err != nil {
		return err
	}
	defer w.Leave()
	var err error
	switch c.URL.Query().Get("fail") {
	case "refused":
		err = &accessRefused{next: "/fiction/x"}
	case "unavailable":
		err = errUnavailable
	case "joined":
		err = errors.Join(errors.New("other"), &accessRefused{next: "/joined"})
	case "unknown":
		err = errors.New("unknown failure")
	case "panic":
		panic(&accessRefused{next: "/panicked"})
	case "panicstring":
		panic("plain panic")
	case "notfound":
		return ErrNotFound
	case "redirect":
		return c.Redirect("/elsewhere", http.StatusFound)
	}
	return ErrorAt("src/components/Inner.lyr:4:3", err)
}

func ehOuter(c *Ctx, w *Writer, p struct{}, s Slots) error {
	if err := w.Enter("src/components/Outer.lyr"); err != nil {
		return err
	}
	defer w.Leave()
	w.Raw("<section>")
	if err := ehInner(c, w, struct{}{}, Slots{}); err != nil {
		return err
	}
	w.Raw("</section>")
	return w.Err()
}

func ehPage(c *Ctx, w *Writer) error {
	c.Header().Set("X-Page", "1")
	w.Raw("<!DOCTYPE html><html><head>")
	w.HeadEnd()
	w.Raw("</head><body>partial")
	if err := ehOuter(c, w, struct{}{}, Slots{}); err != nil {
		return ErrorAt("src/pages/book.lyr:9:2", err)
	}
	w.Raw("</body></html>")
	return nil
}

// answerError is the handler of the PLAN §4c example.
func answerError(calls *[]error) func(w http.ResponseWriter, r *http.Request, err error) bool {
	return func(w http.ResponseWriter, r *http.Request, err error) bool {
		*calls = append(*calls, err)
		var refused *accessRefused
		switch {
		case errors.As(err, &refused):
			http.Redirect(w, r, "/signin?next="+refused.next, http.StatusSeeOther)
		case errors.Is(err, errUnavailable):
			w.Header().Set("Retry-After", "60")
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
		case strings.Contains(err.Error(), "blocked"):
			http.Error(w, "blocked", http.StatusForbidden)
		default:
			return false
		}
		return true
	}
}

func ehRoutes() []Route {
	return []Route{
		{Pattern: "/book", File: "src/pages/book.lyr", Render: ehPage,
			Assets: []Asset{{Kind: "css", URL: "/_lyr/book-1234.css"}}},
		{Pattern: "404", File: "src/pages/404.lyr", Render: func(c *Ctx, w *Writer) error {
			if c.URL.Query().Get("fail404") != "" {
				return errors.New("404 page blocked")
			}
			w.Raw("not found page")
			return nil
		}},
	}
}

func TestErrorHandler(t *testing.T) {
	for _, dev := range []string{"", "1"} {
		t.Run("LYR_DEV="+dev, func(t *testing.T) {
			t.Setenv("LYR_DEV", dev)
			var calls []error
			s := testSite(t, ehRoutes(), WithErrorHandler(answerError(&calls)))
			h := s.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("X-MW", "1")
				s.NotFound().ServeHTTP(w, r)
			}))
			mw := func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("X-Before", "1")
					next.ServeHTTP(w, r)
				})
			}
			h = mw(h)
			tests := []struct {
				method, target string
				code           int
				location, body string
				header         string // a header the answer must have
			}{
				{"GET", "/book?fail=refused", 303, "/signin?next=/fiction/x", "", ""},
				{"GET", "/book?fail=unavailable", 503, "", "unavailable\n", "Retry-After"},
				{"GET", "/book?fail=joined", 303, "/signin?next=/joined", "", ""},
				{"GET", "/book?fail=panic", 303, "/signin?next=/panicked", "", ""},
				{"HEAD", "/book?fail=unavailable", 503, "", "unavailable\n", "Retry-After"}, // net/http drops the body
				{"GET", "/missing?fail404=1", 403, "", "blocked\n", ""},
			}
			for _, tt := range tests {
				logs := captureLog(t)
				calls = calls[:0]
				rec := do(h, tt.method, tt.target)
				if rec.Code != tt.code || rec.Header().Get("Location") != tt.location || (tt.code != 303 && rec.Body.String() != tt.body) {
					t.Errorf("%s %s: got %d %q %q", tt.method, tt.target, rec.Code, rec.Header().Get("Location"), rec.Body)
				}
				if rec.Header().Get("X-Page") != "" || strings.Contains(rec.Body.String(), "partial") {
					t.Errorf("%s: the page's headers or output leaked: %v %q", tt.target, rec.Header(), rec.Body)
				}
				if rec.Header().Get("X-Before") != "1" {
					t.Errorf("%s: middleware header dropped", tt.target)
				}
				if tt.header != "" && rec.Header().Get(tt.header) == "" {
					t.Errorf("%s: no %s header", tt.target, tt.header)
				}
				if logs.Len() != 0 {
					t.Errorf("%s: handled error logged: %q", tt.target, logs)
				}
				if len(calls) != 1 {
					t.Errorf("%s: handler called %d times", tt.target, len(calls))
				}
			}

			// The error text starts with the innermost return's position.
			calls = calls[:0]
			do(h, "GET", "/book?fail=unavailable")
			if got := calls[0].Error(); got != "src/components/Inner.lyr:4:3: catalog: unavailable" {
				t.Errorf("handler's err = %q", got)
			}

			// false: the built-in 500 and its log line.
			for _, fail := range []string{"unknown", "panicstring"} {
				logs := captureLog(t)
				rec := do(h, "GET", "/book?fail="+fail)
				if rec.Code != 500 || !strings.Contains(rec.Body.String(), "500 Internal Server Error") ||
					strings.Count(logs.String(), "\n") != 1 || !strings.HasPrefix(logs.String(), "lyr: GET /book: ") {
					t.Errorf("%s: got %d %q, log %q", fail, rec.Code, rec.Body, logs)
				}
				if (dev == "1") != strings.Contains(rec.Body.String(), "<pre>") {
					t.Errorf("%s: dev error page = %q", fail, rec.Body)
				}
			}

			// Control flow never reaches the handler.
			calls = calls[:0]
			if rec := do(h, "GET", "/book?fail=notfound"); rec.Code != 404 || rec.Body.String() != "not found page" {
				t.Errorf("ErrNotFound: got %d %q", rec.Code, rec.Body)
			}
			if rec := do(h, "GET", "/book?fail=redirect"); rec.Code != 302 || rec.Header().Get("Location") != "/elsewhere" || rec.Header().Get("X-Page") != "1" {
				t.Errorf("redirect: got %d %v", rec.Code, rec.Header())
			}
			if len(calls) != 0 {
				t.Errorf("control flow reached the handler: %v", calls)
			}
		})
	}
}

func TestErrorHandlerErrorIdentity(t *testing.T) {
	var got error
	s := testSite(t, ehRoutes(), WithErrorHandler(func(w http.ResponseWriter, r *http.Request, err error) bool {
		got = err
		return true
	}))
	for _, fail := range []string{"refused", "joined", "panic"} {
		got = nil
		do(s.Handler(), "GET", "/book?fail="+fail)
		var refused *accessRefused
		if !errors.As(got, &refused) {
			t.Errorf("%s: errors.As can't find *accessRefused in %v", fail, got)
		}
	}
	// A panicked non-error unwraps to nil.
	do(s.Handler(), "GET", "/book?fail=panicstring")
	if errors.Unwrap(got) != nil || !strings.Contains(got.Error(), "panic: plain panic") {
		t.Errorf("string panic: %v, Unwrap %v", got, errors.Unwrap(got))
	}
}

func TestErrorHandlerLastWins(t *testing.T) {
	first := func(w http.ResponseWriter, r *http.Request, err error) bool {
		http.Error(w, "first", http.StatusTeapot)
		return true
	}
	second := func(w http.ResponseWriter, r *http.Request, err error) bool {
		http.Error(w, "second", http.StatusBadGateway)
		return true
	}
	s := testSite(t, ehRoutes(), WithErrorHandler(first), WithErrorHandler(second))
	if rec := do(s.Handler(), "GET", "/book?fail=unknown"); rec.Code != 502 || rec.Body.String() != "second\n" {
		t.Errorf("got %d %q, want the last handler's answer", rec.Code, rec.Body)
	}
}

func TestErrorHandlerIsland(t *testing.T) {
	t.Setenv("LYR_KEY", "")
	var gotURL, gotReferer string
	var gotHead http.Header
	s := islandSite(t, WithErrorHandler(func(w http.ResponseWriter, r *http.Request, err error) bool {
		gotURL, gotReferer = r.URL.Path, r.Referer()
		gotHead = w.Header().Clone()
		if strings.Contains(err.Error(), "boom") {
			return false
		}
		w.Header().Set("Cache-Control", "no-cache")
		http.Error(w, "handled: "+err.Error(), http.StatusServiceUnavailable)
		return true
	}))
	h := s.Handler()
	src, tok := placeholder(t, h, "error")
	r := httptest.NewRequest("GET", src+"?p="+tok, nil)
	r.Header.Set("Referer", "https://example.com/post")
	rec := httptest.NewRecorder()
	logs := captureLog(t)
	h.ServeHTTP(rec, r)
	if rec.Code != 503 || rec.Body.String() != "handled: db down\n" || logs.Len() != 0 {
		t.Errorf("got %d %q, log %q", rec.Code, rec.Body, logs)
	}
	if gotURL != src || gotReferer != "https://example.com/post" {
		t.Errorf("handler saw %q, Referer %q", gotURL, gotReferer)
	}
	if gotHead.Get("Cache-Control") != "private, no-store" || gotHead.Get("X-Robots-Tag") != "noindex" {
		t.Errorf("default island headers before the handler: %v", gotHead)
	}
	if rec.Header().Get("Cache-Control") != "no-cache" || rec.Header().Get("X-Robots-Tag") != "noindex" {
		t.Errorf("answer headers: %v", rec.Header())
	}

	// false: the island's own 500 and log line.
	src, tok = placeholder(t, h, "panic")
	if rec := do(h, "GET", src+"?p="+tok); rec.Code != 500 || !strings.Contains(logs.String(), "panic: boom") {
		t.Errorf("unhandled panic: got %d, log %q", rec.Code, logs)
	}

	// The endpoint's own 400 and 413 aren't render errors.
	gotURL = ""
	if rec := do(h, "GET", src+"?p=bad"); rec.Code != 400 {
		t.Errorf("bad token: got %d", rec.Code)
	}
	big := httptest.NewRequest("POST", src, strings.NewReader(strings.Repeat("x", maxIslandBody+1)))
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, big)
	if rec.Code != 413 || gotURL != "" {
		t.Errorf("too big: got %d, handler saw %q", rec.Code, gotURL)
	}
	// Props that don't decode into the component's Props.
	if rec := do(h, "GET", "/_lyr/island/0000000000000000/"+commentsID+"?p="+sealProps(t, s, `{"PostID":"x"}`)); rec.Code != 400 || gotURL != "" {
		t.Errorf("bad props: got %d, handler saw %q", rec.Code, gotURL)
	}
}

// sealProps seals raw JSON props for the Comments island with s's key.
func sealProps(t *testing.T, s *Site, props string) string {
	t.Helper()
	return string(sealerOf(s.keys).AppendSeal(nil, commentsID, s.keys.buildID, []byte(props)))
}

func TestServeError(t *testing.T) {
	var calls []error
	s := testSite(t, ehRoutes(), WithErrorHandler(answerError(&calls)))
	serve := func(err error) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		s.ServeError(rec, httptest.NewRequest("GET", "/fragment", nil), err)
		return rec
	}
	if rec := serve(fmt.Errorf("x: %w", &accessRefused{next: "/n"})); rec.Code != 303 || rec.Header().Get("Location") != "/signin?next=/n" {
		t.Errorf("handled: got %d %v", rec.Code, rec.Header())
	}
	if rec := serve(&RedirectError{Location: "/r", Code: 307}); rec.Code != 307 || rec.Header().Get("Location") != "/r" {
		t.Errorf("redirect: got %d %v", rec.Code, rec.Header())
	}
	if rec := serve(ErrNotFound); rec.Code != 404 || rec.Body.String() != "not found page" {
		t.Errorf("not found: got %d %q", rec.Code, rec.Body)
	}
	logs := captureLog(t)
	if rec := serve(errors.New("other")); rec.Code != 500 || logs.String() != "lyr: GET /fragment: other\n" {
		t.Errorf("unhandled: got %d, log %q", rec.Code, logs)
	}
	if rec := serve(nil); rec.Code != 200 || rec.Body.Len() != 0 || len(rec.Header()) != 0 {
		t.Errorf("nil: got %d %q", rec.Code, rec.Body)
	}
	// With RenderComponent, as the docs show.
	rec := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/book?fail=unavailable", nil)
	if err := RenderComponent(rec, r, ehOuter, struct{}{}); err != nil {
		s.ServeError(rec, r, err)
	}
	if rec.Code != 503 || rec.Header().Get("Retry-After") != "60" {
		t.Errorf("RenderComponent + ServeError: got %d %v", rec.Code, rec.Header())
	}
}
