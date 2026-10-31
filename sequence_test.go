package lyr

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// tag returns a middleware that records name before and after next.
func tag(name string, log *[]string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			*log = append(*log, name+">")
			next.ServeHTTP(w, r)
			*log = append(*log, "<"+name)
		})
	}
}

func TestSequence(t *testing.T) {
	tests := []struct {
		name  string
		names []string
		want  string
	}{
		{"none", nil, "h"},
		{"one", []string{"a"}, "a> h <a"},
		{"three", []string{"a", "b", "c"}, "a> b> c> h <c <b <a"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var log []string
			var mws []func(http.Handler) http.Handler
			for _, n := range tt.names {
				mws = append(mws, tag(n, &log))
			}
			h := Sequence(mws...)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				log = append(log, "h")
			}))
			h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
			if got := strings.Join(log, " "); got != tt.want {
				t.Errorf("order = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSequenceNested(t *testing.T) {
	var log []string
	inner := Sequence(tag("b", &log), tag("c", &log))
	h := Sequence(tag("a", &log), inner)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		log = append(log, "h")
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
	if got, want := strings.Join(log, " "), "a> b> c> h <c <b <a"; got != want {
		t.Errorf("order = %q, want %q", got, want)
	}
}

// The caller's slice may change after Sequence returns.
func TestSequenceCopiesArgs(t *testing.T) {
	var log []string
	mws := []func(http.Handler) http.Handler{tag("a", &log)}
	seq := Sequence(mws...)
	mws[0] = tag("x", &log)
	seq(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})).
		ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
	if got, want := strings.Join(log, " "), "a> <a"; got != want {
		t.Errorf("order = %q, want %q", got, want)
	}
}

func TestSequenceNilPanics(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil || !strings.Contains(r.(string), "index 1") {
			t.Fatalf("recover() = %v, want a panic naming index 1", r)
		}
	}()
	var log []string
	Sequence(tag("a", &log), nil)
}

// Sequence adds no per-request cost: the composed handler is the outermost
// middleware's own handler.
func TestSequenceNoRequestAllocs(t *testing.T) {
	pass := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { next.ServeHTTP(w, r) })
	}
	h := Sequence(pass, pass, pass)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/", nil)
	if n := testing.AllocsPerRun(100, func() { h.ServeHTTP(w, r) }); n != 0 {
		t.Errorf("allocs per request = %v, want 0", n)
	}
}
