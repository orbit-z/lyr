package cache

import (
	"context"
	"errors"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// waitFlight waits until a load of key is running.
func waitFlight(t *testing.T, s *Store[string], key string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		s.mu.Lock()
		_, ok := s.flights[key]
		s.mu.Unlock()
		if ok {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("no flight started")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestLoadSingleFlight(t *testing.T) {
	s := NewStore(StoreOptions[string]{})
	gate := make(chan struct{})
	var loads atomic.Int32
	load := func(context.Context) (string, error) {
		loads.Add(1)
		<-gate
		return "v", nil
	}
	const n = 50
	var wg sync.WaitGroup
	results := make([]string, n)
	tagsSeen := make([][]string, n)
	for i := range n {
		wg.Go(func() {
			v, tags, err := s.Load(context.Background(), "k", time.Minute, []string{"t1", "t2"}, load)
			if err != nil {
				t.Error(err)
			}
			results[i], tagsSeen[i] = v, tags
		})
	}
	waitFlight(t, s, "k")
	time.Sleep(10 * time.Millisecond) // let the others join
	close(gate)
	wg.Wait()
	if got := loads.Load(); got != 1 {
		t.Errorf("load ran %d times, want 1", got)
	}
	for i := range n {
		if results[i] != "v" || !slices.Equal(tagsSeen[i], []string{"t1", "t2"}) {
			t.Fatalf("caller %d got %q %v", i, results[i], tagsSeen[i])
		}
	}
	// Stored with its tags: a hit returns them, and the tag drops it.
	v, tags, err := s.Load(context.Background(), "k", time.Minute, nil, func(context.Context) (string, error) {
		t.Fatal("load ran on a hit")
		return "", nil
	})
	if v != "v" || !slices.Equal(tags, []string{"t1", "t2"}) || err != nil {
		t.Errorf("hit: %q %v %v", v, tags, err)
	}
	s.InvalidateTags("t2")
	if _, ok := s.Get("k"); ok {
		t.Error("the loaded value didn't carry its tags")
	}
	if len(s.flights) != 0 {
		t.Errorf("%d flights left", len(s.flights))
	}
}

func TestLoadCancellationPerCaller(t *testing.T) {
	s := NewStore(StoreOptions[string]{})
	gate := make(chan struct{})
	leaderCtx, cancelLeader := context.WithCancel(context.Background())
	var loadCtxErr error
	type result struct {
		v   string
		err error
	}
	leader := make(chan result, 1)
	go func() {
		v, _, err := s.Load(leaderCtx, "k", time.Minute, nil, func(ctx context.Context) (string, error) {
			<-gate
			loadCtxErr = ctx.Err()
			return "v", nil
		})
		leader <- result{v, err}
	}()
	waitFlight(t, s, "k")

	// A joiner whose context ends gets its own error at once.
	jctx, cancelJoiner := context.WithCancel(context.Background())
	joiner := make(chan result, 1)
	go func() {
		v, _, err := s.Load(jctx, "k", time.Minute, nil, nil)
		joiner <- result{v, err}
	}()
	time.Sleep(5 * time.Millisecond)
	cancelJoiner()
	if r := <-joiner; !errors.Is(r.err, context.Canceled) || r.v != "" {
		t.Errorf("cancelled joiner got %q, %v", r.v, r.err)
	}

	// The leader's context ending doesn't cancel the load.
	cancelLeader()
	close(gate)
	r := <-leader
	if r.err != nil || r.v != "v" {
		t.Errorf("leader got %q, %v", r.v, r.err)
	}
	if loadCtxErr != nil {
		t.Errorf("the load's context was cancelled: %v", loadCtxErr)
	}
	if v, ok := s.Get("k"); !ok || v != "v" {
		t.Error("the load's result wasn't stored")
	}
}

func TestLoadErrorShared(t *testing.T) {
	s := NewStore(StoreOptions[string]{})
	gate := make(chan struct{})
	boom := errors.New("boom")
	var loads atomic.Int32
	load := func(context.Context) (string, error) {
		loads.Add(1)
		<-gate
		return "partial", boom
	}
	errs := make(chan error, 2)
	tags := make(chan []string, 2)
	for range 2 {
		go func() {
			v, tg, err := s.Load(context.Background(), "k", time.Minute, []string{"t"}, load)
			if v != "" {
				t.Errorf("value %q with an error", v)
			}
			errs <- err
			tags <- tg
		}()
	}
	waitFlight(t, s, "k")
	time.Sleep(5 * time.Millisecond)
	close(gate)
	for range 2 {
		if err := <-errs; !errors.Is(err, boom) {
			t.Errorf("err = %v, want boom", err)
		}
		if tg := <-tags; tg != nil {
			t.Errorf("tags %v with an error", tg)
		}
	}
	if loads.Load() != 1 {
		t.Errorf("load ran %d times", loads.Load())
	}
	if s.Len() != 0 {
		t.Error("an error was stored")
	}
	// The next Load tries again.
	v, _, err := s.Load(context.Background(), "k", time.Minute, nil, func(context.Context) (string, error) { return "ok", nil })
	if v != "ok" || err != nil {
		t.Errorf("retry: %q, %v", v, err)
	}
}

func TestLoadPanicShared(t *testing.T) {
	s := NewStore(StoreOptions[string]{})
	gate := make(chan struct{})
	load := func(context.Context) (string, error) {
		<-gate
		panic("kaboom")
	}
	leaderPanic := make(chan any, 1)
	go func() {
		defer func() { leaderPanic <- recover() }()
		s.Load(context.Background(), "k", time.Minute, nil, load)
	}()
	waitFlight(t, s, "k")
	joinerErr := make(chan error, 1)
	go func() {
		_, _, err := s.Load(context.Background(), "k", time.Minute, nil, load)
		joinerErr <- err
	}()
	time.Sleep(5 * time.Millisecond)
	close(gate)
	if p := <-leaderPanic; p != "kaboom" {
		t.Errorf("leader recovered %v, want the load's panic", p)
	}
	if err := <-joinerErr; !errors.Is(err, ErrLoadPanicked) {
		t.Errorf("joiner err = %v, want ErrLoadPanicked", err)
	}
	if s.Len() != 0 || len(s.flights) != 0 {
		t.Errorf("Len %d, flights %d after a panic", s.Len(), len(s.flights))
	}
}

func TestLoadNotStoredAfterInvalidation(t *testing.T) {
	for name, purge := range map[string]func(*Store[string]){
		"Delete":         func(s *Store[string]) { s.Delete("k") },
		"InvalidateTags": func(s *Store[string]) { s.InvalidateTags("t") },
		"Clear":          func(s *Store[string]) { s.Clear() },
	} {
		t.Run(name, func(t *testing.T) {
			s := NewStore(StoreOptions[string]{})
			v, _, err := s.Load(context.Background(), "k", time.Minute, []string{"t"}, func(context.Context) (string, error) {
				purge(s) // data changed while it was loaded
				return "old", nil
			})
			if v != "old" || err != nil {
				t.Fatalf("got %q, %v", v, err)
			}
			if _, ok := s.Get("k"); ok {
				t.Error("a value loaded across a purge was stored")
			}
		})
	}
}

func TestLoadZeroTTL(t *testing.T) {
	s := NewStore(StoreOptions[string]{})
	v, _, err := s.Load(context.Background(), "k", 0, nil, func(context.Context) (string, error) { return "v", nil })
	if v != "v" || err != nil || s.Len() != 0 {
		t.Errorf("got %q, %v, Len %d", v, err, s.Len())
	}
}

// TestLoadConcurrent mixes Load, Set and InvalidateTags on few keys, for
// -race.
func TestLoadConcurrent(t *testing.T) {
	s := NewStore(StoreOptions[int]{MaxEntries: 8})
	var wg sync.WaitGroup
	for w := range 8 {
		wg.Go(func() {
			for i := range 500 {
				k := string(rune('a' + (i+w)%4))
				switch i % 10 {
				case 0:
					s.InvalidateTags("t")
				case 1:
					s.Set(k, i, time.Minute, "t")
				default:
					v, _, err := s.Load(context.Background(), k, time.Minute, []string{"t"}, func(context.Context) (int, error) { return i, nil })
					if err != nil || v < 0 {
						t.Error(v, err)
					}
				}
			}
		})
	}
	wg.Wait()
}
