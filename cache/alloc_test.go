package cache

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/orbit-z/orbit"
)

func TestGetAllocs(t *testing.T) {
	if testing.Short() || raceEnabled {
		t.Skip("allocation counts (not under -short or -race)")
	}
	m := Memory(MemoryOptions{})
	// The zero CacheKey is the only one a test outside package orbit can
	// make: its hash and string are those of the key "" with hash 0.
	var key orbit.CacheKey
	e := response("/", time.Hour, time.Hour, time.Now(), "posts", "home")
	m.Put(key, e)
	if m.Get(key) != e {
		t.Fatal("Put then Get missed")
	}
	if n := testing.AllocsPerRun(1000, func() { m.Get(key) }); n != 0 {
		t.Errorf("MemoryProvider.Get hit: %v allocs, want 0", n)
	}

	s := New(Options[orbit.HTML]{})
	s.Set("sidebar:v1", "<nav></nav>", time.Hour, "posts")
	if n := testing.AllocsPerRun(1000, func() { s.Get("sidebar:v1") }); n != 0 {
		t.Errorf("Store.Get hit: %v allocs, want 0", n)
	}
	if n := testing.AllocsPerRun(1000, func() { s.Get("absent") }); n != 0 {
		t.Errorf("Store.Get miss: %v allocs, want 0", n)
	}
}

func BenchmarkMemoryGetHit(b *testing.B) {
	m := Memory(MemoryOptions{})
	var key orbit.CacheKey
	m.Put(key, response("/", time.Hour, 0, time.Now()))
	b.ReportAllocs()
	for b.Loop() {
		if m.Get(key) == nil {
			b.Fatal("miss")
		}
	}
}

func BenchmarkMemoryGetMiss(b *testing.B) {
	m := Memory(MemoryOptions{})
	var key orbit.CacheKey
	b.ReportAllocs()
	for b.Loop() {
		m.Get(key)
	}
}

// BenchmarkMemoryGetHitParallel reads one hot key from every thread: the
// case CLOCK's read lock is for (run with -cpu 16).
func BenchmarkMemoryGetHitParallel(b *testing.B) {
	m := Memory(MemoryOptions{})
	var key orbit.CacheKey
	m.Put(key, response("/", time.Hour, 0, time.Now()))
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if m.Get(key) == nil {
				b.Error("miss")
				return
			}
		}
	})
}

// BenchmarkMemoryPut stores a response under rotating keys in a full
// cache, so every Put evicts (the key copy and entry are the allocations).
func BenchmarkMemoryPut(b *testing.B) {
	m := newMemory(MemoryOptions{}, 0)
	keys := make([]string, 4096)
	for i := range keys {
		keys[i] = "example.com/blog/post-" + strconv.Itoa(i)
	}
	e := response("/blog/post", time.Hour, time.Hour, time.Now(), "posts", "home")
	b.ReportAllocs()
	i := 0
	for b.Loop() {
		mput(m, keys[i%len(keys)], e)
		i++
	}
}

func BenchmarkStoreGetHit(b *testing.B) {
	s := New(Options[orbit.HTML]{})
	s.Set("sidebar:v1", "<nav></nav>", time.Hour)
	b.ReportAllocs()
	for b.Loop() {
		if _, ok := s.Get("sidebar:v1"); !ok {
			b.Fatal("miss")
		}
	}
}

func BenchmarkStoreGetMiss(b *testing.B) {
	s := New(Options[orbit.HTML]{})
	b.ReportAllocs()
	for b.Loop() {
		s.Get("sidebar:v1")
	}
}

func BenchmarkStoreGetHitParallel(b *testing.B) {
	s := New(Options[orbit.HTML]{})
	s.Set("sidebar:v1", "<nav></nav>", time.Hour)
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			s.Get("sidebar:v1")
		}
	})
}

func BenchmarkStoreSet(b *testing.B) {
	s := New(Options[orbit.HTML]{})
	keys := make([]string, 4096)
	for i := range keys {
		keys[i] = "fragment:" + strconv.Itoa(i)
	}
	b.ReportAllocs()
	i := 0
	for b.Loop() {
		s.Set(keys[i%len(keys)], "<nav></nav>", time.Hour, "posts")
		i++
	}
}

// BenchmarkStoreLoadHit is Load's hit path, the fragment cache's common
// case.
func BenchmarkStoreLoadHit(b *testing.B) {
	s := New(Options[orbit.HTML]{})
	s.Set("sidebar:v1", "<nav></nav>", time.Hour, "posts")
	load := func(context.Context) (orbit.HTML, error) { return "", nil }
	ctx := context.Background()
	b.ReportAllocs()
	for b.Loop() {
		s.Load(ctx, "sidebar:v1", time.Hour, nil, load)
	}
}
