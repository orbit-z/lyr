package cache

import (
	"context"
	"strings"
	"time"

	"github.com/orbit-z/lyr"
)

// MemoryOptions configure NewMemory.
type MemoryOptions struct {
	// MaxEntries bounds the number of stored responses (Vary markers and
	// variants count as one each). 0 means DefaultMaxEntries.
	MaxEntries int
	// MaxBytes, when positive, bounds the bytes held: bodies, headers, keys
	// and tags, plus a fixed overhead per entry. A response bigger than
	// MaxBytes divided by the number of shards is not stored.
	MaxBytes int64
	// Query says which query parameters are part of a key; Lyr reads it
	// once, in NewSite. Giving both Include and Exclude panics in NewMemory.
	Query lyr.CacheQuery
	// Now is the clock for expiry; nil means time.Now. Tests set it.
	Now func() time.Time
	// OnInvalidate, when set, is called by Invalidate after the local
	// purge, so an app can forward the purge to its other instances (which
	// apply it with InvalidateLocal). Its error is Invalidate's.
	OnInvalidate func(ctx context.Context, o lyr.InvalidateOptions) error
}

// MemoryProvider is an in-process route cache, a lyr.CacheStore. Make
// one with NewMemory and pass it to lyr.WithCache. It's safe for concurrent
// use.
type MemoryProvider struct {
	g            *engine[*lyr.CachedResponse]
	query        lyr.CacheQuery
	onInvalidate func(context.Context, lyr.InvalidateOptions) error
}

var _ lyr.CacheStore = (*MemoryProvider)(nil)

// entryOverhead approximates a stored response's own memory beyond its
// body, headers, key and tags: the entry, its map slot and index sets.
const entryOverhead = 256

// pathTag is the index tag of a request path: the NUL keeps it apart from
// any tag a page sets.
const pathTag = "\x00path:"

// NewMemory returns an in-process route cache. Responses are kept until
// Stored + MaxAge + SWR (Lyr decides whether one is fresh or stale), and
// evicted least recently used first, approximately (CLOCK, per shard),
// when a bound is reached. It panics if o.Query sets both Include and
// Exclude, or if a bound is negative.
func NewMemory(o MemoryOptions) *MemoryProvider {
	if len(o.Query.Include) > 0 && len(o.Query.Exclude) > 0 {
		panic("cache: MemoryOptions.Query sets both Include and Exclude; use one or the other")
	}
	return newMemory(o, 0)
}

func newMemory(o MemoryOptions, shards int) *MemoryProvider {
	return &MemoryProvider{
		g:            newEngine[*lyr.CachedResponse](o.MaxEntries, o.MaxBytes, o.Now, shards),
		query:        o.Query,
		onInvalidate: o.OnInvalidate,
	}
}

// Get returns the response stored under key, or nil. It doesn't allocate.
func (m *MemoryProvider) Get(key lyr.CacheKey) *lyr.CachedResponse {
	e, _, _ := m.g.get(key.Hash(), key.String())
	return e
}

// Put stores e under key, replacing the response stored there before. A
// response with no lifetime (MaxAge + SWR not positive), or one bigger than
// the byte budget, isn't stored, and the earlier one is dropped.
func (m *MemoryProvider) Put(key lyr.CacheKey, e *lyr.CachedResponse) {
	m.put(key.Hash(), key.String(), e)
}

// put is Put with the key's parts (tests can't make a lyr.CacheKey).
func (m *MemoryProvider) put(h uint64, k string, e *lyr.CachedResponse) {
	life := e.MaxAge + e.SWR
	if life <= 0 {
		m.g.delete(h, k)
		return
	}
	tags := make([]string, 0, len(e.Tags)+1)
	tags = append(tags, e.Tags...)
	tags = append(tags, pathTag+e.Path)
	m.g.set(h, k, strings.Clone, e, responseSize(k, e), e.Stored.Add(life).UnixNano(), tags)
}

// responseSize approximates the memory e holds.
func responseSize(key string, e *lyr.CachedResponse) int64 {
	n := len(e.Body) + len(key) + len(e.Path) + entryOverhead
	for name, vs := range e.Header {
		n += len(name)
		for _, v := range vs {
			n += len(v)
		}
	}
	for _, t := range e.Tags {
		n += len(t)
	}
	for _, v := range e.Vary {
		n += len(v)
	}
	return int64(n)
}

// Invalidate drops the responses o selects with InvalidateLocal, then calls
// OnInvalidate, if set, and returns its error.
func (m *MemoryProvider) Invalidate(ctx context.Context, o lyr.InvalidateOptions) error {
	m.InvalidateLocal(o)
	if m.onInvalidate != nil {
		return m.onInvalidate(ctx, o)
	}
	return nil
}

// InvalidateLocal drops every response carrying one of o.Tags, and every
// response whose Path is o.Path (all its hosts, queries, variants and its
// Vary marker), without calling OnInvalidate. Use it to apply a purge
// received from another instance.
func (m *MemoryProvider) InvalidateLocal(o lyr.InvalidateOptions) {
	m.g.invalidateTags(o.Tags...)
	if o.Path != "" {
		m.g.invalidateTags(pathTag + o.Path)
	}
}

// CacheQuery returns MemoryOptions.Query; Lyr calls it in NewSite.
func (m *MemoryProvider) CacheQuery() lyr.CacheQuery { return m.query }

// Clear drops every stored response, without calling OnInvalidate.
func (m *MemoryProvider) Clear() { m.g.clear() }

// Len returns the number of stored responses, expired ones not yet dropped
// included.
func (m *MemoryProvider) Len() int { return m.g.len() }

// Bytes returns the approximate bytes held (see MemoryOptions.MaxBytes).
func (m *MemoryProvider) Bytes() int64 { return m.g.bytes() }
