package orbit

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type testLocals struct {
	RequestID string
	Theme     string
}

type otherLocals struct {
	User string
}

func TestLocalsFromRequest(t *testing.T) {
	attached := &testLocals{RequestID: "a"}
	tests := []struct {
		name string
		req  *http.Request
		want *testLocals
	}{
		{"none attached", httptest.NewRequest("GET", "/", nil), nil},
		{"attached", WithLocals(httptest.NewRequest("GET", "/", nil), attached), attached},
		{"other type attached", WithLocals(httptest.NewRequest("GET", "/", nil), &otherLocals{}), nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := LocalsFromRequest[testLocals](tt.req); got != tt.want {
				t.Errorf("LocalsFromRequest = %p, want %p", got, tt.want)
			}
		})
	}
}

func TestEnsureLocals(t *testing.T) {
	t.Run("attaches a new value", func(t *testing.T) {
		r := httptest.NewRequest("GET", "/", nil)
		r2, l := EnsureLocals[testLocals](r)
		if l == nil {
			t.Fatal("EnsureLocals returned nil")
		}
		if r2 == r {
			t.Error("EnsureLocals returned the same request; want a copy carrying the locals")
		}
		if got := LocalsFromRequest[testLocals](r2); got != l {
			t.Errorf("LocalsFromRequest = %p, want %p", got, l)
		}
		if got := LocalsFromRequest[testLocals](r); got != nil {
			t.Error("the original request was modified")
		}
	})
	t.Run("returns the attached value and the same request", func(t *testing.T) {
		attached := &testLocals{}
		r := WithLocals(httptest.NewRequest("GET", "/", nil), attached)
		r2, l := EnsureLocals[testLocals](r)
		if r2 != r {
			t.Error("EnsureLocals returned a new request; want r unchanged")
		}
		if l != attached {
			t.Errorf("EnsureLocals = %p, want %p", l, attached)
		}
	})
}

func TestWithLocalsShadowsDownstream(t *testing.T) {
	first := &testLocals{Theme: "first"}
	second := &testLocals{Theme: "second"}
	upstream := WithLocals(httptest.NewRequest("GET", "/", nil), first)
	downstream := WithLocals(upstream, second)
	if got := LocalsFromRequest[testLocals](downstream); got != second {
		t.Errorf("downstream sees %v, want second", got)
	}
	if got := LocalsFromRequest[testLocals](upstream); got != first {
		t.Errorf("upstream sees %v, want first", got)
	}
}

func TestLocalsTypeIsolation(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r, a := EnsureLocals[testLocals](r)
	r, b := EnsureLocals[otherLocals](r)
	a.Theme = "dark"
	b.User = "ada"
	if got := LocalsFromRequest[testLocals](r); got != a || got.Theme != "dark" {
		t.Errorf("testLocals = %+v, want %+v", got, a)
	}
	if got := LocalsFromRequest[otherLocals](r); got != b || got.User != "ada" {
		t.Errorf("otherLocals = %+v, want %+v", got, b)
	}
	// Named types over the same underlying type are distinct keys too.
	type aliasLike testLocals
	if got := LocalsFromRequest[aliasLike](r); got != nil {
		t.Errorf("LocalsFromRequest[aliasLike] = %+v, want nil", got)
	}
}

func TestLocalsOf(t *testing.T) {
	attached := &testLocals{Theme: "dark"}
	tests := []struct {
		name     string
		req      *http.Request
		want     *testLocals // nil: expect a fresh zero value
		wantZero bool
	}{
		{"attached to request", WithLocals(httptest.NewRequest("GET", "/", nil), attached), attached, false},
		{"none attached", httptest.NewRequest("GET", "/", nil), nil, true},
		{"nil request", nil, nil, true},
		{"other type attached", WithLocals(httptest.NewRequest("GET", "/", nil), &otherLocals{User: "x"}), nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := acquireCtx(tt.req, nil, false)
			defer releaseCtx(c)
			l := LocalsOf[testLocals](c)
			if l == nil {
				t.Fatal("LocalsOf returned nil")
			}
			if tt.want != nil && l != tt.want {
				t.Errorf("LocalsOf = %p, want %p", l, tt.want)
			}
			if tt.wantZero && *l != (testLocals{}) {
				t.Errorf("LocalsOf = %+v, want zero", *l)
			}
			if again := LocalsOf[testLocals](c); again != l {
				t.Errorf("second LocalsOf = %p, want cached %p", again, l)
			}
		})
	}
}

func TestLocalsOfTypeMismatchPanics(t *testing.T) {
	c := acquireCtx(httptest.NewRequest("GET", "/", nil), nil, false)
	defer releaseCtx(c)
	LocalsOf[testLocals](c)
	defer func() {
		msg, _ := recover().(string)
		if !strings.Contains(msg, "*orbit.otherLocals") || !strings.Contains(msg, "*orbit.testLocals") {
			t.Errorf("panic = %q, want both type names", msg)
		}
	}()
	LocalsOf[otherLocals](c)
	t.Error("LocalsOf with a different type didn't panic")
}

// TestMiddlewareChain mirrors docs/middleware.md: two middlewares call
// EnsureLocals, the second fills the value in, and the page (LocalsOf) and
// the outer middleware both see it.
func TestMiddlewareChain(t *testing.T) {
	var outer, inner, page *testLocals
	var outerAfter string
	pageHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c := acquireCtx(r, nil, false)
		defer releaseCtx(c)
		page = LocalsOf[testLocals](c)
		if page.RequestID != "req-1" || page.Theme != "dark" {
			t.Errorf("page sees %+v", *page)
		}
	})
	requestInfo := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r2, l := EnsureLocals[testLocals](r)
			if r2 != r {
				t.Error("inner EnsureLocals returned a new request")
			}
			inner = l
			l.RequestID = "req-1"
			l.Theme = "dark"
			next.ServeHTTP(w, r2)
		})
	}
	logging := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r, l := EnsureLocals[testLocals](r)
			outer = l
			next.ServeHTTP(w, r)
			outerAfter = l.RequestID
		})
	}
	h := logging(requestInfo(pageHandler))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/account", nil))
	if outer == nil || outer != inner || inner != page {
		t.Errorf("pointers differ: outer %p, inner %p, page %p", outer, inner, page)
	}
	if outerAfter != "req-1" {
		t.Errorf("outer middleware reads RequestID %q after next, want req-1", outerAfter)
	}
}

func TestLocalsOfAllocs(t *testing.T) {
	r := WithLocals(httptest.NewRequest("GET", "/", nil), &testLocals{})
	c := acquireCtx(r, nil, false)
	defer releaseCtx(c)
	allocs := testing.AllocsPerRun(100, func() {
		c.locals = nil // force the request-context lookup each time
		LocalsOf[testLocals](c)
	})
	if allocs != 0 {
		t.Errorf("LocalsOf with attached locals: %v allocs, want 0", allocs)
	}
	if got := LocalsFromRequest[testLocals](r); got == nil {
		t.Fatal("no locals")
	}
	allocs = testing.AllocsPerRun(100, func() { LocalsFromRequest[testLocals](r) })
	if allocs != 0 {
		t.Errorf("LocalsFromRequest: %v allocs, want 0", allocs)
	}
}

func BenchmarkLocalsOfAttached(b *testing.B) {
	r := WithLocals(httptest.NewRequest("GET", "/", nil), &testLocals{})
	c := acquireCtx(r, nil, false)
	defer releaseCtx(c)
	b.ReportAllocs()
	for b.Loop() {
		c.locals = nil
		LocalsOf[testLocals](c)
	}
}

func BenchmarkLocalsOfCached(b *testing.B) {
	c := acquireCtx(httptest.NewRequest("GET", "/", nil), nil, false)
	defer releaseCtx(c)
	LocalsOf[testLocals](c)
	b.ReportAllocs()
	for b.Loop() {
		LocalsOf[testLocals](c)
	}
}

func BenchmarkEnsureLocalsAttached(b *testing.B) {
	r, _ := EnsureLocals[testLocals](httptest.NewRequest("GET", "/", nil))
	b.ReportAllocs()
	for b.Loop() {
		EnsureLocals[testLocals](r)
	}
}
