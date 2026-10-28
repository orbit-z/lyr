package cache

import (
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// Ports of nyth's memoryCache.test.ts, on a one-shard Store[string] so the
// bounds are exact, with a clock the test moves by hand.

var start = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newClock() *fakeClock { return &fakeClock{t: start} }

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

const ttl = 30 * time.Second

func tier(maxEntries int, maxBytes int64) (*Store[string], *fakeClock) {
	c := newClock()
	return newStore(StoreOptions[string]{MaxEntries: maxEntries, MaxBytes: maxBytes, Now: c.Now}, 1), c
}

func get(s *Store[string], key string) string {
	v, ok := s.Get(key)
	if !ok {
		return "<miss>"
	}
	return v
}

func wantGet(t *testing.T, s *Store[string], key, want string) {
	t.Helper()
	if got := get(s, key); got != want {
		t.Errorf("Get(%q) = %q, want %q", key, got, want)
	}
}

const miss = "<miss>"

func TestStorage(t *testing.T) {
	s, _ := tier(100, 0)
	s.Set("a", "1", ttl)
	wantGet(t, s, "a", "1")
	wantGet(t, s, "b", miss)

	s.Set("b", "2", ttl)
	s.Delete("a")
	wantGet(t, s, "a", miss)
	wantGet(t, s, "b", "2")
	s.Clear()
	wantGet(t, s, "b", miss)
	if s.Len() != 0 || s.Bytes() != 0 || s.g.tagCount() != 0 {
		t.Errorf("after Clear: %d entries, %d bytes, %d tags", s.Len(), s.Bytes(), s.g.tagCount())
	}
}

func TestNonPositiveTTL(t *testing.T) {
	s, _ := tier(100, 0)
	s.Set("a", "1", 0)
	s.Set("b", "1", -time.Second)
	if s.Len() != 0 {
		t.Fatalf("Len = %d, want 0", s.Len())
	}
	s.Set("c", "1", ttl)
	s.Set("c", "2", 0) // drops the earlier value
	wantGet(t, s, "c", miss)
	if s.Len() != 0 || s.Bytes() != 0 {
		t.Errorf("Len %d, Bytes %d, want 0, 0", s.Len(), s.Bytes())
	}
}

func TestNegativeBoundsPanic(t *testing.T) {
	for _, o := range []StoreOptions[string]{{MaxEntries: -1}, {MaxBytes: -1}} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("NewStore(%+v) didn't panic", o)
				}
			}()
			NewStore(o)
		}()
	}
}

func TestExpiry(t *testing.T) {
	s, c := tier(100, 0)
	s.Set("a", "v", ttl)
	c.Advance(29 * time.Second)
	wantGet(t, s, "a", "v")
	c.Advance(time.Second - 1)
	wantGet(t, s, "a", "v")
	// nyth's rule: gone at exactly its expiry instant.
	c.Advance(1)
	wantGet(t, s, "a", miss)
	// The read freed the slot, the bytes and the tags.
	if s.Len() != 0 || s.Bytes() != 0 {
		t.Errorf("after expiry: Len %d, Bytes %d", s.Len(), s.Bytes())
	}
}

func TestEvictsInInsertionOrder(t *testing.T) {
	s, _ := tier(2, 0)
	s.Set("a", "1", ttl)
	s.Set("b", "2", ttl)
	s.Set("c", "3", ttl)
	wantGet(t, s, "a", miss)
	wantGet(t, s, "b", "2")
	wantGet(t, s, "c", "3")
}

func TestClockSecondChance(t *testing.T) {
	s, _ := tier(2, 0)
	s.Set("a", "1", ttl)
	s.Set("b", "2", ttl)
	// Reading a gives it a second chance: b goes instead.
	wantGet(t, s, "a", "1")
	s.Set("c", "3", ttl)
	wantGet(t, s, "b", miss)
	wantGet(t, s, "a", "1")
	wantGet(t, s, "c", "3")

	// Both a and c are now marked: the hand clears them in turn and the
	// oldest of the two, a, goes.
	s.Set("d", "4", ttl)
	wantGet(t, s, "a", miss)
	wantGet(t, s, "c", "3")
	wantGet(t, s, "d", "4")
}

func TestOverwriteKeepsTheSlot(t *testing.T) {
	s, _ := tier(2, 0)
	s.Set("a", "1", ttl)
	s.Set("b", "2", ttl)
	s.Set("a", "11", ttl)
	if s.Len() != 2 {
		t.Fatalf("Len = %d, want 2", s.Len())
	}
	wantGet(t, s, "a", "11")
	wantGet(t, s, "b", "2")
	// An overwrite makes the key the youngest (nyth). The reads above
	// marked both entries, so check it on a fresh store.
	s2, _ := tier(2, 0)
	s2.Set("a", "1", ttl)
	s2.Set("b", "2", ttl)
	s2.Set("a", "11", ttl)
	s2.Set("c", "3", ttl)
	wantGet(t, s2, "b", miss)
	wantGet(t, s2, "a", "11")
}

func TestBoundBounds(t *testing.T) {
	s, _ := tier(3, 0)
	for i := range 1000 {
		s.Set("k"+strconv.Itoa(i), strconv.Itoa(i), ttl)
	}
	if s.Len() != 3 {
		t.Errorf("Len = %d, want 3", s.Len())
	}
	wantGet(t, s, "k999", "999")
	wantGet(t, s, "k0", miss)
}

func TestShardedBoundsAreApproximate(t *testing.T) {
	s := NewStore(StoreOptions[string]{MaxEntries: 1000})
	if len(s.g.shards) != 64 || s.g.maxEntries != 16 {
		t.Fatalf("%d shards of %d, want 64 of 16", len(s.g.shards), s.g.maxEntries)
	}
	for i := range 10_000 {
		s.Set("k"+strconv.Itoa(i), "v", ttl)
	}
	if n := s.Len(); n > 64*16 || n < 900 {
		t.Errorf("Len = %d, want about 1000 and at most 1024", n)
	}
	if small := NewStore(StoreOptions[string]{MaxEntries: 2}); len(small.g.shards) != 1 {
		t.Errorf("MaxEntries 2: %d shards, want 1", len(small.g.shards))
	}
}

func TestTags(t *testing.T) {
	s, _ := tier(100, 0)
	s.Set("a", "1", ttl, "user:alice")
	s.Set("b", "2", ttl, "user:bob")
	s.Set("c", "3", ttl)
	s.InvalidateTags("user:alice")
	wantGet(t, s, "a", miss)
	wantGet(t, s, "b", "2")
	wantGet(t, s, "c", "3")

	s.Set("x", "1", ttl, "book:dcc", "user:alice")
	s.Set("y", "2", ttl, "book:dcc", "user:alice")
	s.InvalidateTags("book:dcc")
	wantGet(t, s, "x", miss)
	wantGet(t, s, "y", miss)
	s.Set("x", "1", ttl, "book:dcc", "user:alice")
	s.InvalidateTags("user:alice")
	wantGet(t, s, "x", miss)

	s.InvalidateTags("never-used")
	wantGet(t, s, "b", "2")
}

func TestTagIndexDoesNotOutliveEntries(t *testing.T) {
	s, c := tier(1, 0)
	s.Set("a", "1", ttl, "t", "u")
	if n := s.g.tagCount(); n != 2 {
		t.Fatalf("tagCount = %d, want 2", n)
	}
	s.Delete("a")
	if n := s.g.tagCount(); n != 0 {
		t.Errorf("after delete: tagCount = %d, want 0", n)
	}
	// Eviction prunes.
	s.Set("a", "1", ttl, "evicted")
	s.Set("b", "2", ttl, "expired")
	if n := s.g.tagCount(); n != 1 {
		t.Errorf("after eviction: tagCount = %d, want 1", n)
	}
	// Expiry prunes.
	c.Advance(ttl)
	s.Get("b")
	if n := s.g.tagCount(); n != 0 {
		t.Errorf("after expiry: tagCount = %d, want 0", n)
	}
	// Invalidation prunes.
	s.Set("c", "3", ttl, "invalidated", "other")
	s.InvalidateTags("invalidated")
	if n := s.g.tagCount(); n != 0 {
		t.Errorf("after invalidation: tagCount = %d, want 0", n)
	}
	// An overwrite with other tags drops the ones that left.
	s.Set("d", "4", ttl, "old", "kept")
	s.Set("d", "5", ttl, "kept", "new")
	if n := s.g.tagCount(); n != 2 {
		t.Errorf("after overwrite: tagCount = %d, want 2", n)
	}
	s.InvalidateTags("old")
	wantGet(t, s, "d", "5")
	s.InvalidateTags("new")
	wantGet(t, s, "d", miss)
	if n := s.g.tagCount(); n != 0 {
		t.Errorf("at the end: tagCount = %d, want 0", n)
	}
	// A deleted key's tag doesn't resurrect or drop a later value.
	s.Set("e", "1", ttl, "t")
	s.Delete("e")
	s.InvalidateTags("t")
	s.Set("e", "2", ttl)
	wantGet(t, s, "e", "2")
}

func payload(n int) string { return strings.Repeat("x", n) }

func TestByteBudget(t *testing.T) {
	t.Run("evicts on bytes before the entry count", func(t *testing.T) {
		s, _ := tier(100, 310)
		// Each entry is 1 key byte + 102 value bytes.
		s.Set("a", payload(102), ttl)
		s.Set("b", payload(102), ttl)
		s.Set("c", payload(102), ttl)
		s.Set("d", payload(102), ttl)
		wantGet(t, s, "a", miss)
		if s.Len() != 3 || s.Bytes() != 309 {
			t.Errorf("Len %d, Bytes %d, want 3, 309", s.Len(), s.Bytes())
		}
	})
	t.Run("respects the CLOCK order", func(t *testing.T) {
		s, _ := tier(100, 300)
		s.Set("a", payload(102), ttl)
		s.Set("b", payload(102), ttl)
		s.Get("a")
		s.Set("c", payload(102), ttl)
		wantGet(t, s, "b", miss)
		wantGet(t, s, "a", payload(102))
	})
	t.Run("the entry count binds when tighter", func(t *testing.T) {
		s, _ := tier(2, 1_000_000)
		s.Set("a", "1", ttl)
		s.Set("b", "2", ttl)
		s.Set("c", "3", ttl)
		wantGet(t, s, "a", miss)
		if s.Len() != 2 {
			t.Errorf("Len = %d, want 2", s.Len())
		}
	})
	t.Run("a value bigger than the budget is refused, not admitted", func(t *testing.T) {
		s, _ := tier(100, 200)
		s.Set("small", "1", ttl)
		s.Set("huge", payload(500), ttl)
		wantGet(t, s, "huge", miss)
		wantGet(t, s, "small", "1")
		if s.Len() != 1 {
			t.Errorf("Len = %d, want 1", s.Len())
		}
	})
	t.Run("a refused value drops the one it replaces", func(t *testing.T) {
		s, _ := tier(100, 200)
		s.Set("k", "1", ttl, "t")
		s.Set("k", payload(500), ttl)
		wantGet(t, s, "k", miss)
		if s.Len() != 0 || s.Bytes() != 0 || s.g.tagCount() != 0 {
			t.Errorf("Len %d, Bytes %d, tags %d, want 0", s.Len(), s.Bytes(), s.g.tagCount())
		}
	})
	t.Run("counts bytes, not characters", func(t *testing.T) {
		s, _ := tier(100, 11)
		s.Set("a", strings.Repeat("é", 5), ttl) // 1 + 10 bytes
		wantGet(t, s, "a", strings.Repeat("é", 5))
		s.Set("b", strings.Repeat("é", 6), ttl)
		wantGet(t, s, "b", miss)
	})
	t.Run("no byte bound: only the count", func(t *testing.T) {
		s, _ := tier(3, 0)
		for i := range 10 {
			s.Set("k"+strconv.Itoa(i), payload(10_000), ttl)
		}
		if s.Len() != 3 {
			t.Errorf("Len = %d, want 3", s.Len())
		}
	})
}

func TestBytesAccounted(t *testing.T) {
	s, c := tier(3, 0)
	s.Set("deleted", "1", ttl)
	s.Delete("deleted")
	s.Set("expired", "1", ttl)
	c.Advance(ttl + time.Second)
	s.Get("expired")
	s.Set("invalidated", "1", ttl, "t")
	s.InvalidateTags("t")
	if s.Bytes() != 0 {
		t.Fatalf("Bytes = %d, want 0", s.Bytes())
	}
	s.Set("ow", "1", ttl)
	s.Set("ow", "22", ttl)
	if s.Bytes() != 4 {
		t.Errorf("after overwrite: Bytes = %d, want 4", s.Bytes())
	}
	s.Set("dropped", "1", 0)
	if s.Bytes() != 4 {
		t.Errorf("after ttl 0: Bytes = %d, want 4", s.Bytes())
	}
	for i := range 50 {
		s.Set("k"+strconv.Itoa(i+10), payload(100), ttl)
	}
	if s.Bytes() != 3*103 {
		t.Errorf("after evictions: Bytes = %d, want %d", s.Bytes(), 3*103)
	}
	s.Clear()
	if s.Bytes() != 0 {
		t.Errorf("after Clear: Bytes = %d", s.Bytes())
	}
}

func TestSizeFunc(t *testing.T) {
	s := newStore(StoreOptions[[]int]{MaxBytes: 100, Size: func(v []int) int64 { return int64(8 * len(v)) }}, 1)
	s.Set("k", make([]int, 10), ttl)
	if s.Bytes() != 81 {
		t.Errorf("Bytes = %d, want 81", s.Bytes())
	}
	s.Set("k", make([]int, 20), ttl)
	if _, ok := s.Get("k"); ok || s.Len() != 0 {
		t.Errorf("an oversize value was stored")
	}
	// Without Size, other types count only their key.
	o := NewStore(StoreOptions[int]{})
	o.Set("key", 1, ttl)
	if o.Bytes() != 3 {
		t.Errorf("default size: Bytes = %d, want 3", o.Bytes())
	}
}

func TestHashCollisionIsAMiss(t *testing.T) {
	g := newEngine[string](10, 0, nil, 1)
	exp := time.Now().Add(time.Hour).UnixNano()
	g.set(42, "a", identity, "A", 1, exp, []string{"t"})
	if _, _, ok := g.get(42, "b"); ok {
		t.Error("a different key with the same hash was a hit")
	}
	// The newer key takes the slot, and the old one's tags go with it.
	g.set(42, "b", identity, "B", 1, exp, nil)
	if _, _, ok := g.get(42, "a"); ok {
		t.Error("the replaced key is still a hit")
	}
	if v, _, _ := g.get(42, "b"); v != "B" {
		t.Errorf("get(b) = %q", v)
	}
	if g.len() != 1 || g.tagCount() != 0 || g.bytes() != 1 {
		t.Errorf("len %d, tags %d, bytes %d", g.len(), g.tagCount(), g.bytes())
	}
}

func TestIsolation(t *testing.T) {
	a, b := NewStore(StoreOptions[string]{}), NewStore(StoreOptions[string]{})
	a.Set("k", "1", ttl)
	if _, ok := b.Get("k"); ok {
		t.Error("two stores share an entry")
	}
}

// TestConcurrentEngine runs every operation at once, for -race.
func TestConcurrentEngine(t *testing.T) {
	s := NewStore(StoreOptions[string]{MaxEntries: 64, MaxBytes: 4096})
	var wg sync.WaitGroup
	for w := range 8 {
		wg.Go(func() {
			for i := range 2000 {
				k := "k" + strconv.Itoa((i*7+w)%100)
				switch i % 7 {
				case 0:
					s.Delete(k)
				case 1:
					s.InvalidateTags("t" + strconv.Itoa(i%5))
				default:
					s.Set(k, payload(i%50), time.Minute, "t"+strconv.Itoa(i%5))
					s.Get(k)
				}
			}
		})
	}
	wg.Wait()
	if s.Len() > 64 {
		t.Errorf("Len = %d", s.Len())
	}
	var n int
	for i := range s.g.shards {
		sh := &s.g.shards[i]
		for _, e := range sh.m {
			n += int(e.size)
		}
	}
	if int64(n) != s.Bytes() {
		t.Errorf("Bytes = %d, entries sum to %d", s.Bytes(), n)
	}
}
