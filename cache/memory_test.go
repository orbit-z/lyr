package cache

import (
	"context"
	"errors"
	"hash/maphash"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/orbit-z/lyr"
)

var testSeed = maphash.MakeSeed()

func mput(m *MemoryProvider, key string, e *lyr.CachedResponse) {
	m.put(maphash.String(testSeed, key), key, e)
}

func mget(m *MemoryProvider, key string) *lyr.CachedResponse {
	e, _, _ := m.g.get(maphash.String(testSeed, key), key)
	return e
}

func response(path string, maxAge, swr time.Duration, stored time.Time, tags ...string) *lyr.CachedResponse {
	return &lyr.CachedResponse{
		Status: 200,
		Header: http.Header{"Content-Type": {"text/html; charset=utf-8"}},
		Body:   []byte("<p>" + path + "</p>"),
		Stored: stored,
		MaxAge: maxAge,
		SWR:    swr,
		Tags:   tags,
		Path:   path,
	}
}

func TestMemoryLifetime(t *testing.T) {
	c := newClock()
	m := newMemory(MemoryOptions{Now: c.Now}, 1)
	e := response("/a", time.Minute, 30*time.Second, c.Now())
	mput(m, "h/a", e)
	if mget(m, "h/a") != e {
		t.Fatal("not stored")
	}
	// Kept while stale: Lyr serves it during SWR.
	c.Advance(time.Minute + 29*time.Second)
	if mget(m, "h/a") != e {
		t.Error("dropped inside SWR")
	}
	// Gone at Stored + MaxAge + SWR exactly.
	c.Advance(time.Second)
	if mget(m, "h/a") != nil {
		t.Error("kept past MaxAge + SWR")
	}
	if m.Len() != 0 || m.Bytes() != 0 || m.g.tagCount() != 0 {
		t.Errorf("Len %d, Bytes %d, tags %d", m.Len(), m.Bytes(), m.g.tagCount())
	}

	// No lifetime: not stored, and the earlier one is dropped.
	mput(m, "h/b", response("/b", time.Minute, 0, c.Now()))
	mput(m, "h/b", response("/b", 0, 0, c.Now()))
	if mget(m, "h/b") != nil || m.Len() != 0 {
		t.Error("a response with no lifetime was kept")
	}
}

func TestMemoryInvalidate(t *testing.T) {
	m := newMemory(MemoryOptions{}, 0)
	now := time.Now()
	marker := response("/blog", time.Hour, 0, now, "posts")
	marker.Body, marker.Vary = nil, []string{"accept-language"}
	mput(m, "h/blog", marker)
	mput(m, "h/blog\x00en", response("/blog", time.Hour, 0, now, "posts"))
	mput(m, "h/blog\x00fr", response("/blog", time.Hour, 0, now, "posts"))
	mput(m, "h/blog?page=2", response("/blog", time.Hour, 0, now, "posts"))
	mput(m, "other.host/blog", response("/blog", time.Hour, 0, now))
	mput(m, "h/blog/hello", response("/blog/hello", time.Hour, 0, now, "posts", "post:hello"))
	mput(m, "h/about", response("/about", time.Hour, 0, now, "pages"))

	m.InvalidateLocal(lyr.InvalidateOptions{Path: "/blog"})
	for _, k := range []string{"h/blog", "h/blog\x00en", "h/blog\x00fr", "h/blog?page=2", "other.host/blog"} {
		if mget(m, k) != nil {
			t.Errorf("Path /blog kept %q", k)
		}
	}
	if mget(m, "h/blog/hello") == nil || mget(m, "h/about") == nil {
		t.Fatal("Path /blog dropped another path")
	}

	m.InvalidateLocal(lyr.InvalidateOptions{Tags: []string{"post:hello", "never-used"}})
	if mget(m, "h/blog/hello") != nil || mget(m, "h/about") == nil {
		t.Error("tag invalidation dropped the wrong entries")
	}
	m.Clear()
	if m.Len() != 0 || m.g.tagCount() != 0 {
		t.Errorf("after Clear: Len %d, tags %d", m.Len(), m.g.tagCount())
	}
}

func TestMemoryPathTagIsOwn(t *testing.T) {
	// A page tag that looks like a path doesn't match Path, and back.
	m := newMemory(MemoryOptions{}, 0)
	mput(m, "h/x", response("/x", time.Hour, 0, time.Now(), "/y"))
	m.InvalidateLocal(lyr.InvalidateOptions{Path: "/y"})
	m.InvalidateLocal(lyr.InvalidateOptions{Tags: []string{"/x"}})
	if mget(m, "h/x") == nil {
		t.Error("path and tag namespaces overlap")
	}
}

func TestMemoryOnInvalidate(t *testing.T) {
	var got []lyr.InvalidateOptions
	fail := errors.New("bus down")
	m := NewMemory(MemoryOptions{OnInvalidate: func(ctx context.Context, o lyr.InvalidateOptions) error {
		got = append(got, o)
		return fail
	}})
	mput(m, "h/a", response("/a", time.Hour, 0, time.Now(), "t"))
	mput(m, "h/b", response("/b", time.Hour, 0, time.Now(), "u"))

	err := m.Invalidate(context.Background(), lyr.InvalidateOptions{Tags: []string{"t"}})
	if !errors.Is(err, fail) {
		t.Errorf("Invalidate returned %v, want OnInvalidate's error", err)
	}
	// The local purge happens first, whatever the hook returns.
	if mget(m, "h/a") != nil {
		t.Error("Invalidate didn't purge locally")
	}
	if len(got) != 1 || !slices.Equal(got[0].Tags, []string{"t"}) {
		t.Errorf("OnInvalidate got %+v", got)
	}

	// InvalidateLocal (a purge from another instance) doesn't call it.
	m.InvalidateLocal(lyr.InvalidateOptions{Tags: []string{"u"}})
	if mget(m, "h/b") != nil || len(got) != 1 {
		t.Errorf("InvalidateLocal: entry kept or hook called (%d calls)", len(got))
	}

	// Without a hook, Invalidate returns nil.
	if err := NewMemory(MemoryOptions{}).Invalidate(context.Background(), lyr.InvalidateOptions{Path: "/"}); err != nil {
		t.Error(err)
	}
}

func TestMemoryQuery(t *testing.T) {
	q := lyr.CacheQuery{Include: []string{"page"}}
	if got := NewMemory(MemoryOptions{Query: q}).CacheQuery(); !slices.Equal(got.Include, q.Include) {
		t.Errorf("CacheQuery = %+v", got)
	}
	defer func() {
		if recover() == nil {
			t.Error("Include and Exclude together didn't panic")
		}
	}()
	NewMemory(MemoryOptions{Query: lyr.CacheQuery{Include: []string{"a"}, Exclude: []string{"b"}}})
}

func TestMemoryBytes(t *testing.T) {
	m := newMemory(MemoryOptions{MaxBytes: 2000}, 1)
	e := response("/a", time.Hour, 0, time.Now(), "t")
	mput(m, "h/a", e)
	if want := responseSize("h/a", e); m.Bytes() != want || want < int64(len(e.Body))+entryOverhead {
		t.Errorf("Bytes = %d, want %d", m.Bytes(), want)
	}
	// A body over the budget isn't stored, and drops the earlier one.
	big := response("/a", time.Hour, 0, time.Now())
	big.Body = make([]byte, 4000)
	mput(m, "h/a", big)
	if mget(m, "h/a") != nil || m.Len() != 0 || m.Bytes() != 0 {
		t.Error("an oversize response was stored or the old one kept")
	}
}
