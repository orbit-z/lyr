package cache

import (
	"math/bits"
	"slices"
	"sync"
	"sync/atomic"
	"time"
)

// The engine shared by MemoryProvider and Store: a sharded map with CLOCK
// eviction, entry and byte bounds per shard, lazy expiry and a tag reverse
// index (a port of nyth's memoryCache, PLAN.md §4h).

// DefaultMaxEntries is the entry bound used when MaxEntries is 0 (Astro's
// memoryCache max).
const DefaultMaxEntries = 1000

const (
	maxShards = 64
	// minPerShard is the smallest entry bound a shard gets: a small cache
	// uses fewer shards, so its bound stays close to exact.
	minPerShard = 8
)

// entry is a stored value. Its fields change only under the shard's write
// lock, except accessed.
type entry[V any] struct {
	key      string
	hash     uint64
	val      V
	size     int64
	expires  int64    // Unix nanoseconds: the entry is gone at this instant
	tags     []string // owned; replaced, never changed, so readers may keep it
	accessed atomic.Bool
	// The CLOCK ring: a circular list from the shard's hand (the oldest)
	// round to hand.prev (the youngest).
	prev, next *entry[V]
}

type shard[V any] struct {
	mu    sync.RWMutex
	m     map[uint64]*entry[V]
	hand  *entry[V]
	bytes int64
	tags  map[string]map[*entry[V]]struct{}
	_     [64]byte // keeps hot shards' locks off each other's cache lines
}

type engine[V any] struct {
	shards     []shard[V]
	shift      uint  // hash >> shift is the shard index
	maxEntries int   // per shard
	maxBytes   int64 // per shard; 0: no byte bound
	now        func() time.Time
}

// newEngine builds an engine. shards is a power of two up to 64, or 0 to
// choose from maxEntries.
func newEngine[V any](maxEntries int, maxBytes int64, now func() time.Time, shards int) *engine[V] {
	if maxEntries < 0 {
		panic("cache: MaxEntries must not be negative")
	}
	if maxBytes < 0 {
		panic("cache: MaxBytes must not be negative")
	}
	if maxEntries == 0 {
		maxEntries = DefaultMaxEntries
	}
	if now == nil {
		now = time.Now
	}
	if shards == 0 {
		shards = maxShards
		for shards > 1 && maxEntries/shards < minPerShard {
			shards /= 2
		}
	}
	g := &engine[V]{
		shards:     make([]shard[V], shards),
		shift:      uint(64 - bits.TrailingZeros(uint(shards))),
		maxEntries: (maxEntries + shards - 1) / shards,
		now:        now,
	}
	if maxBytes > 0 {
		g.maxBytes = (maxBytes + int64(shards) - 1) / int64(shards)
	}
	for i := range g.shards {
		g.shards[i].m = make(map[uint64]*entry[V])
		g.shards[i].tags = make(map[string]map[*entry[V]]struct{})
	}
	return g
}

func (g *engine[V]) shard(h uint64) *shard[V] {
	// A shift of 64 (one shard) gives 0.
	return &g.shards[h>>g.shift]
}

// get returns the value and tags stored under key, marking the entry used.
// It takes only the shard's read lock, unless the entry has expired.
func (g *engine[V]) get(h uint64, key string) (v V, tags []string, ok bool) {
	s := g.shard(h)
	s.mu.RLock()
	e := s.m[h]
	if e == nil || e.key != key {
		s.mu.RUnlock()
		return v, nil, false
	}
	// nyth's rule: an entry is gone at its expiry instant, not a tick after.
	if g.now().UnixNano() >= e.expires {
		s.mu.RUnlock()
		g.expire(s, h, e)
		return v, nil, false
	}
	if !e.accessed.Load() { // no write, so no shared cache line, on a hot key
		e.accessed.Store(true)
	}
	v, tags = e.val, e.tags
	s.mu.RUnlock()
	return v, tags, true
}

// expire drops e if it's still stored under h and expired.
func (g *engine[V]) expire(s *shard[V], h uint64, e *entry[V]) {
	s.mu.Lock()
	if s.m[h] == e && g.now().UnixNano() >= e.expires {
		s.remove(e)
	}
	s.mu.Unlock()
}

// set stores v under key until expires (Unix nanoseconds). key and tags
// are owned by the engine from then on (a key already stored is kept).
// keyOf returns the key to own: it's called only for a new entry. A value
// bigger than the shard's byte budget is refused, and the value stored
// under key before is dropped; set reports whether v was stored.
func (g *engine[V]) set(h uint64, key string, keyOf func(string) string, v V, size, expires int64, tags []string) bool {
	s := g.shard(h)
	s.mu.Lock()
	defer s.mu.Unlock()
	old := s.m[h]
	if old != nil && old.key != key {
		s.remove(old) // a hash collision: the newer key wins the slot
		old = nil
	}
	if g.maxBytes > 0 && size > g.maxBytes {
		if old != nil {
			s.remove(old)
		}
		return false
	}
	var oldTags []string
	newSlot := 1
	if old != nil {
		// Out of the ring and the totals, but it keeps its map slot.
		s.unlink(old)
		s.bytes -= old.size
		oldTags = old.tags
		for _, t := range oldTags {
			if !slices.Contains(tags, t) {
				s.untag(t, old)
			}
		}
		newSlot = 0
	}
	now := g.now().UnixNano()
	for s.hand != nil && (len(s.m)+newSlot > g.maxEntries || g.maxBytes > 0 && s.bytes+size > g.maxBytes) {
		victim := s.hand
		if victim.accessed.Load() && now < victim.expires {
			// Second chance: it becomes the youngest.
			victim.accessed.Store(false)
			s.hand = victim.next
			continue
		}
		s.remove(victim)
	}
	e := old
	if e == nil {
		e = &entry[V]{key: keyOf(key), hash: h}
		s.m[h] = e
	}
	e.val, e.size, e.expires, e.tags = v, size, expires, tags
	e.accessed.Store(false)
	s.link(e)
	s.bytes += size
	for _, t := range tags {
		if slices.Contains(oldTags, t) {
			continue
		}
		set := s.tags[t]
		if set == nil {
			set = make(map[*entry[V]]struct{}, 1)
			s.tags[t] = set
		}
		set[e] = struct{}{}
	}
	return true
}

// delete drops the entry stored under key, if any.
func (g *engine[V]) delete(h uint64, key string) {
	s := g.shard(h)
	s.mu.Lock()
	if e := s.m[h]; e != nil && e.key == key {
		s.remove(e)
	}
	s.mu.Unlock()
}

// invalidateTags drops every entry carrying any of tags.
func (g *engine[V]) invalidateTags(tags ...string) {
	if len(tags) == 0 {
		return
	}
	for i := range g.shards {
		s := &g.shards[i]
		s.mu.Lock()
		for _, t := range tags {
			set := s.tags[t]
			if set == nil {
				continue
			}
			// Detached first, so the removals below don't touch it.
			delete(s.tags, t)
			for e := range set {
				s.remove(e)
			}
		}
		s.mu.Unlock()
	}
}

func (g *engine[V]) clear() {
	for i := range g.shards {
		s := &g.shards[i]
		s.mu.Lock()
		clear(s.m)
		clear(s.tags)
		s.hand, s.bytes = nil, 0
		s.mu.Unlock()
	}
}

// len returns the number of entries held, expired ones not yet dropped
// included.
func (g *engine[V]) len() int {
	n := 0
	for i := range g.shards {
		s := &g.shards[i]
		s.mu.RLock()
		n += len(s.m)
		s.mu.RUnlock()
	}
	return n
}

func (g *engine[V]) bytes() int64 {
	var n int64
	for i := range g.shards {
		s := &g.shards[i]
		s.mu.RLock()
		n += s.bytes
		s.mu.RUnlock()
	}
	return n
}

// tagCount returns the number of distinct tags in the index (for tests:
// nyth's invariant that no empty tag set is left behind).
func (g *engine[V]) tagCount() int {
	n := 0
	for i := range g.shards {
		s := &g.shards[i]
		s.mu.RLock()
		n += len(s.tags)
		s.mu.RUnlock()
	}
	return n
}

// remove drops e from the map, the ring, the totals and the tag index.
func (s *shard[V]) remove(e *entry[V]) {
	delete(s.m, e.hash)
	s.unlink(e)
	s.bytes -= e.size
	for _, t := range e.tags {
		s.untag(t, e)
	}
}

// untag removes e from tag t's set, and the set when it's empty (else the
// index would grow one empty set per tag ever used).
func (s *shard[V]) untag(t string, e *entry[V]) {
	set := s.tags[t]
	if set == nil {
		return
	}
	delete(set, e)
	if len(set) == 0 {
		delete(s.tags, t)
	}
}

// link inserts e as the youngest entry, just behind the hand.
func (s *shard[V]) link(e *entry[V]) {
	if s.hand == nil {
		e.prev, e.next = e, e
		s.hand = e
		return
	}
	last := s.hand.prev
	e.prev, e.next = last, s.hand
	last.next = e
	s.hand.prev = e
}

// unlink removes e from the ring; e must be in it, or have nil links.
func (s *shard[V]) unlink(e *entry[V]) {
	if e.next == nil {
		return
	}
	if e.next == e {
		s.hand = nil
	} else {
		e.prev.next = e.next
		e.next.prev = e.prev
		if s.hand == e {
			s.hand = e.next
		}
	}
	e.prev, e.next = nil, nil
}
