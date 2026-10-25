package orbit

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
)

func TestAcquireCtx(t *testing.T) {
	r := httptest.NewRequest("GET", "/blog/hello?x=1", nil)
	site, _ := url.Parse("https://example.com")
	params := Params{"slug": "hello"}
	c := acquireCtx(r, params, site, true)
	defer releaseCtx(c)
	if c.Request != r || c.URL != r.URL || c.Site != site || c.Params["slug"] != "hello" || !c.isDev() {
		t.Errorf("acquireCtx fields = %+v", c)
	}
	if c.responseStatus() != http.StatusOK {
		t.Errorf("default status = %d, want 200", c.responseStatus())
	}
	if c.responseHeader() != nil || c.cachedLocals() != nil {
		t.Error("fresh Ctx has headers or locals")
	}

	n := acquireCtx(nil, nil, nil, false)
	defer releaseCtx(n)
	if n.Request != nil || n.URL != nil {
		t.Error("acquireCtx(nil) set Request or URL")
	}
}

func TestSetStatusAndHeader(t *testing.T) {
	c := acquireCtx(httptest.NewRequest("GET", "/", nil), nil, nil, false)
	defer releaseCtx(c)
	c.SetStatus(http.StatusTeapot)
	if got := c.responseStatus(); got != http.StatusTeapot {
		t.Errorf("status = %d, want 418", got)
	}
	h := c.Header()
	if h == nil {
		t.Fatal("Header() = nil")
	}
	h.Set("Content-Type", "text/plain")
	if c.Header().Get("Content-Type") != "text/plain" {
		t.Error("Header() returned a different map on the second call")
	}
	if got := c.responseHeader().Get("Content-Type"); got != "text/plain" {
		t.Errorf("responseHeader Content-Type = %q", got)
	}
}

func TestRedirect(t *testing.T) {
	c := acquireCtx(httptest.NewRequest("GET", "/", nil), nil, nil, false)
	defer releaseCtx(c)
	for _, code := range []int{300, 301, 302, 303, 307, 308} {
		err := c.Redirect("/login?next=%2F", code)
		var re *RedirectError
		if !errors.As(err, &re) {
			t.Fatalf("Redirect(%d) = %T, want *RedirectError", code, err)
		}
		if re.Location != "/login?next=%2F" || re.Code != code {
			t.Errorf("Redirect(%d) = %+v", code, re)
		}
	}
	if got := c.Redirect("/a", 302).Error(); got != "orbit: redirect 302 to /a" {
		t.Errorf("Error() = %q", got)
	}
}

func TestRedirectNon3xxPanics(t *testing.T) {
	c := acquireCtx(httptest.NewRequest("GET", "/", nil), nil, nil, false)
	defer releaseCtx(c)
	for _, code := range []int{0, 200, 299, 400, 404, 500} {
		func() {
			defer func() {
				msg, _ := recover().(string)
				if !strings.Contains(msg, "3xx") {
					t.Errorf("Redirect(%d): panic = %q, want a 3xx message", code, msg)
				}
			}()
			c.Redirect("/x", code)
		}()
	}
}

func TestReleaseCtxResets(t *testing.T) {
	r := WithLocals(httptest.NewRequest("GET", "/", nil), &testLocals{Theme: "dark"})
	site, _ := url.Parse("https://example.com")
	c := acquireCtx(r, Params{"id": "1"}, site, true)
	c.SetStatus(404)
	c.Header().Set("X-Secret", "1")
	LocalsOf[testLocals](c)
	releaseCtx(c)

	if c.Request != nil || c.URL != nil || c.Params != nil || c.Site != nil || c.locals != nil || c.status != 0 || c.dev {
		t.Errorf("released Ctx not reset: %+v", c)
	}
	if len(c.header) != 0 {
		t.Errorf("released Ctx keeps headers: %v", c.header)
	}

	// Whatever the pool hands back next carries nothing from before.
	n := acquireCtx(httptest.NewRequest("GET", "/", nil), nil, nil, false)
	defer releaseCtx(n)
	if n.responseHeader() != nil || n.responseStatus() != 200 || n.Params != nil || n.Site != nil || n.isDev() {
		t.Errorf("reused Ctx leaks state: %+v", n)
	}
	if l := LocalsOf[testLocals](n); l.Theme != "" {
		t.Errorf("reused Ctx leaks locals: %+v", l)
	}
	// A different locals type is fine on a reused Ctx.
	n2 := acquireCtx(httptest.NewRequest("GET", "/", nil), nil, nil, false)
	defer releaseCtx(n2)
	LocalsOf[otherLocals](n2)
}

func TestCtxPoolConcurrent(t *testing.T) {
	var wg sync.WaitGroup
	for g := range 8 {
		wg.Go(func() {
			for i := range 1000 {
				r := httptest.NewRequest("GET", "/", nil)
				if i%2 == 0 {
					r, _ = EnsureLocals[testLocals](r)
				}
				c := acquireCtx(r, Params{"g": "x"}, nil, g%2 == 0)
				if c.responseHeader() != nil || c.status != 0 || c.cachedLocals() != nil {
					t.Error("acquired Ctx carries state")
				}
				l := LocalsOf[testLocals](c)
				if l.RequestID != "" {
					t.Error("locals leaked between requests")
				}
				l.RequestID = "set"
				c.SetStatus(201)
				c.Header().Set("X-G", "1")
				releaseCtx(c)
			}
		})
	}
	wg.Wait()
}

func TestAcquireReleaseAllocs(t *testing.T) {
	r := WithLocals(httptest.NewRequest("GET", "/", nil), &testLocals{})
	params := Params{"slug": "a"}
	releaseCtx(acquireCtx(r, params, nil, false)) // warm the pool
	allocs := testing.AllocsPerRun(1000, func() {
		c := acquireCtx(r, params, nil, false)
		LocalsOf[testLocals](c)
		c.SetStatus(200)
		releaseCtx(c)
	})
	if allocs != 0 {
		t.Errorf("acquireCtx/releaseCtx: %v allocs, want 0", allocs)
	}
}

func BenchmarkAcquireRelease(b *testing.B) {
	r := WithLocals(httptest.NewRequest("GET", "/", nil), &testLocals{})
	b.ReportAllocs()
	for b.Loop() {
		c := acquireCtx(r, nil, nil, false)
		LocalsOf[testLocals](c)
		releaseCtx(c)
	}
}

func BenchmarkAcquireReleaseHeader(b *testing.B) {
	r := httptest.NewRequest("GET", "/", nil)
	b.ReportAllocs()
	for b.Loop() {
		c := acquireCtx(r, nil, nil, false)
		c.Header()["Cache-Control"] = nil
		releaseCtx(c)
	}
}
