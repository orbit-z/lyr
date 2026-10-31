package cache

import (
	"context"
	"errors"
	"fmt"
	"hash/maphash"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/orbit-z/lyr"
)

// StoreOptions configure NewStore.
type StoreOptions[V any] struct {
	// MaxEntries bounds the number of values held. 0 means
	// DefaultMaxEntries.
	MaxEntries int
	// MaxBytes, when positive, bounds the bytes held, as measured by Size
	// plus each key's length. A value bigger than MaxBytes divided by the
	// number of shards is not stored.
	MaxBytes int64
	// Size returns the size of a value in bytes, for MaxBytes and Bytes.
	// When nil, a string, []byte or lyr.HTML counts its length and any
	// other value counts 0 (only its key is counted).
	Size func(V) int64
	// Now is the clock for expiry; nil means time.Now. Tests set it.
	Now func() time.Time
}

// Store is an in-process cache of values of type V, with a time to live
// and tags per value, bounded by entry count and optionally bytes: a data
// or fragment cache. Make one with NewStore. It's safe for concurrent use.
//
// Values are stored as given and every hit returns the same value: don't
// change a value once it's stored (or one you got from the store), such as
// the elements of a slice or the fields behind a pointer.
type Store[V any] struct {
	g    *engine[V]
	seed maphash.Seed
	size func(V) int64

	mu      sync.Mutex
	flights map[string]*flight[V]
	// gen is bumped by every Delete, InvalidateTags and Clear: a load
	// that started before one doesn't store its result.
	gen atomic.Uint64
}

// flight is one running load that concurrent Load calls wait for.
type flight[V any] struct {
	done chan struct{}
	// Set before done is closed.
	val  V
	tags []string // the first caller's, stored with val
	err  error
}

// ErrLoadPanicked is wrapped by the error Load returns to the callers that
// waited on a load which panicked.
var ErrLoadPanicked = errors.New("cache: load panicked")

// NewStore returns an empty Store. It panics if a bound is negative.
func NewStore[V any](o StoreOptions[V]) *Store[V] {
	return newStore(o, 0)
}

func newStore[V any](o StoreOptions[V], shards int) *Store[V] {
	size := o.Size
	if size == nil {
		size = defaultSize[V]
	}
	return &Store[V]{
		g:       newEngine[V](o.MaxEntries, o.MaxBytes, o.Now, shards),
		seed:    maphash.MakeSeed(),
		size:    size,
		flights: make(map[string]*flight[V]),
	}
}

func defaultSize[V any](v V) int64 {
	switch v := any(v).(type) {
	case string:
		return int64(len(v))
	case []byte:
		return int64(len(v))
	case lyr.HTML:
		return int64(len(v))
	}
	return 0
}

func identity(s string) string { return s }

// Get returns the value stored under key, and whether there was one that
// hasn't expired. It doesn't allocate.
func (s *Store[V]) Get(key string) (V, bool) {
	v, _, ok := s.g.get(maphash.String(s.seed, key), key)
	return v, ok
}

// Set stores v under key for ttl, with tags, replacing the value stored
// there before. A ttl that isn't positive, or a value bigger than the byte
// budget, stores nothing and drops the earlier value.
func (s *Store[V]) Set(key string, v V, ttl time.Duration, tags ...string) {
	s.set(maphash.String(s.seed, key), key, v, ttl, slices.Clone(tags))
}

// set stores v; tags are owned by the store from then on.
func (s *Store[V]) set(h uint64, key string, v V, ttl time.Duration, tags []string) {
	if ttl <= 0 {
		s.g.delete(h, key)
		return
	}
	if len(tags) == 0 {
		tags = nil
	}
	size := int64(len(key)) + s.size(v)
	s.g.set(h, key, identity, v, size, s.g.now().Add(ttl).UnixNano(), tags)
}

// Load returns the value stored under key, with its tags. On a miss it
// calls load once, however many callers ask for key meanwhile, and stores
// its result for ttl with tags (nothing is stored when ttl isn't positive,
// or when a Delete, InvalidateTags or Clear ran during the load). The
// returned tags are the stored ones on a hit, and the first caller's tags
// after a load, so a fragment cache can re-apply them to ctx.Cache();
// don't change them.
//
// The first caller to miss calls load itself, on its goroutine, with
// context.WithoutCancel(ctx), and so waits for it whatever happens to ctx:
// load may use that caller's *lyr.Ctx, as lyr.RenderHTML does. The
// other callers wait under their own ctx: when it's done, Load returns
// ctx.Err() and the load goes on. A load's error is returned to every
// caller waiting on it, and nothing is stored. A panic in load goes on in
// the first caller, and the others get an error wrapping ErrLoadPanicked.
func (s *Store[V]) Load(ctx context.Context, key string, ttl time.Duration, tags []string, load func(context.Context) (V, error)) (V, []string, error) {
	h := maphash.String(s.seed, key)
	if v, t, ok := s.g.get(h, key); ok {
		return v, t, nil
	}
	s.mu.Lock()
	f := s.flights[key]
	if f == nil {
		// Checked again under the lock: a load may have stored it since.
		if v, t, ok := s.g.get(h, key); ok {
			s.mu.Unlock()
			return v, t, nil
		}
		f = &flight[V]{done: make(chan struct{})}
		if len(tags) > 0 {
			f.tags = slices.Clone(tags)
		}
		s.flights[key] = f
		gen := s.gen.Load()
		s.mu.Unlock()
		s.run(context.WithoutCancel(ctx), f, h, key, ttl, gen, load)
		if f.err != nil {
			return f.val, nil, f.err
		}
		return f.val, f.tags, nil
	}
	s.mu.Unlock()
	select {
	case <-f.done:
	case <-ctx.Done():
		var zero V
		return zero, nil, ctx.Err()
	}
	if f.err != nil {
		var zero V
		return zero, nil, f.err
	}
	return f.val, f.tags, nil
}

// run calls load for a flight, stores its result and wakes the callers
// waiting on it. A panic in load isn't recovered: the waiters get an error
// and the panic goes on.
func (s *Store[V]) run(ctx context.Context, f *flight[V], h uint64, key string, ttl time.Duration, gen uint64, load func(context.Context) (V, error)) {
	returned := false
	defer func() {
		if !returned {
			// A panic, or runtime.Goexit.
			var zero V
			f.val, f.err = zero, fmt.Errorf("%w (key %q)", ErrLoadPanicked, key)
		}
		s.mu.Lock()
		if f.err == nil && s.gen.Load() == gen {
			s.set(h, key, f.val, ttl, f.tags)
		}
		delete(s.flights, key)
		s.mu.Unlock()
		close(f.done)
	}()
	f.val, f.err = load(ctx)
	if f.err != nil {
		var zero V
		f.val = zero
	}
	returned = true
}

// Delete drops the value stored under key, if any.
func (s *Store[V]) Delete(key string) {
	s.gen.Add(1)
	s.g.delete(maphash.String(s.seed, key), key)
}

// InvalidateTags drops every value carrying any of tags.
func (s *Store[V]) InvalidateTags(tags ...string) {
	s.gen.Add(1)
	s.g.invalidateTags(tags...)
}

// Clear drops every value.
func (s *Store[V]) Clear() {
	s.gen.Add(1)
	s.g.clear()
}

// Len returns the number of values held, expired ones not yet dropped
// included.
func (s *Store[V]) Len() int { return s.g.len() }

// Bytes returns the bytes held, as measured by StoreOptions.Size plus each
// key's length.
func (s *Store[V]) Bytes() int64 { return s.g.bytes() }
