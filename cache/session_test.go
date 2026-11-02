package cache

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const testSID = "11111111-1111-4111-8111-111111111111"

func TestSessionStore(t *testing.T) {
	t.Setenv("LYR_DEV", "")
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	s := NewSessionStore(SessionStoreOptions{MaxEntries: 16, Now: func() time.Time { return now }})
	ctx := context.Background()
	data := []byte(`{"userId":1}`)
	if err := s.Set(ctx, testSID, data, time.Hour); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(ctx, testSID)
	if err != nil || &got[0] != &data[0] {
		t.Errorf("Get: %q %v (want the same slice, no copy)", got, err)
	}
	if n := testing.AllocsPerRun(100, func() { s.Get(ctx, testSID) }); n != 0 {
		t.Errorf("Get: %v allocs", n)
	}
	// Expiry, and no expiry for ttl 0.
	s.Set(ctx, "forever", data, 0)
	now = now.Add(2 * time.Hour)
	if got, _ := s.Get(ctx, testSID); got != nil {
		t.Errorf("expired: %q", got)
	}
	if got, _ := s.Get(ctx, "forever"); got == nil {
		t.Error("ttl 0 expired")
	}
	s.Delete(ctx, "forever")
	if got, _ := s.Get(ctx, "forever"); got != nil {
		t.Error("Delete")
	}
	// Bounds.
	for i := range 100 {
		s.Set(ctx, string(rune('a'+i%26))+string(rune('A'+i/26)), data, time.Hour)
	}
	if s.Len() > 16 {
		t.Errorf("%d entries, bound 16", s.Len())
	}
	if NewSessionStore(SessionStoreOptions{}).g.maxEntries*len(NewSessionStore(SessionStoreOptions{}).g.shards) < DefaultSessionEntries {
		t.Error("default bound")
	}
	if os.Getenv("LYR_DEV") == "" && s.dir != "" {
		t.Error("production writes to disk")
	}
}

// TestSessionStoreDev: under lyr dev, a new store (a restarted server)
// keeps the sessions.
func TestSessionStoreDev(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv("LYR_DEV", "1")
	ctx := context.Background()
	s1 := NewSessionStore(SessionStoreOptions{})
	s1.Set(ctx, testSID, []byte(`{"a":1}`), time.Hour)
	s1.Set(ctx, "../evil", []byte(`x`), time.Hour)
	if _, err := os.Stat(filepath.Join(dir, ".lyr", "sessions", testSID)); err != nil {
		t.Fatal(err)
	}
	s2 := NewSessionStore(SessionStoreOptions{})
	if got, _ := s2.Get(ctx, testSID); string(got) != `{"a":1}` {
		t.Errorf("after restart: %q", got)
	}
	s2.Delete(ctx, testSID)
	if got, _ := NewSessionStore(SessionStoreOptions{}).Get(ctx, testSID); got != nil {
		t.Errorf("after Delete: %q", got)
	}
	// An expired file isn't read.
	s3 := NewSessionStore(SessionStoreOptions{Now: func() time.Time { return time.Now().Add(-2 * time.Hour) }})
	s3.Set(ctx, testSID, []byte(`{}`), time.Hour)
	if got, _ := NewSessionStore(SessionStoreOptions{}).Get(ctx, testSID); got != nil {
		t.Errorf("expired file: %q", got)
	}
	entries, _ := os.ReadDir(filepath.Join(dir, ".lyr", "sessions"))
	for _, e := range entries {
		if e.Name() != testSID {
			t.Errorf("unexpected file %s", e.Name())
		}
	}
}

// TestSessionStoreTooBig: a session over the per-shard byte bound is an
// error, not a silent drop, and isn't written to the dev directory.
func TestSessionStoreTooBig(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv("LYR_DEV", "1")
	ctx := context.Background()
	s := NewSessionStore(SessionStoreOptions{MaxBytes: 64 << 10})
	if err := s.Set(ctx, testSID, []byte(`{"a":1}`), time.Hour); err != nil {
		t.Fatal(err)
	}
	big := make([]byte, 64<<10)
	if err := s.Set(ctx, testSID, big, time.Hour); err == nil {
		t.Error("Set of an oversized session returned nil")
	}
	if got, _ := s.Get(ctx, testSID); got != nil {
		t.Errorf("a session is still served: %d bytes", len(got))
	}
	if _, err := os.Stat(filepath.Join(dir, ".lyr", "sessions", testSID)); !os.IsNotExist(err) {
		t.Errorf("dev file after a refused Set: %v", err)
	}
}

// TestSessionStoreDevConcurrent: concurrent saves of one session never
// leave a corrupt dev file.
func TestSessionStoreDevConcurrent(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv("LYR_DEV", "1")
	ctx := context.Background()
	s := NewSessionStore(SessionStoreOptions{})
	vals := [][]byte{[]byte(`{"a":"` + strings.Repeat("x", 4000) + `"}`), []byte(`{"b":2}`)}
	var wg sync.WaitGroup
	for i := range 64 {
		wg.Go(func() { s.Set(ctx, testSID, vals[i%2], time.Hour) })
	}
	wg.Wait()
	got, _ := NewSessionStore(SessionStoreOptions{}).Get(ctx, testSID)
	if string(got) != string(vals[0]) && string(got) != string(vals[1]) {
		t.Errorf("dev file corrupt: %d bytes", len(got))
	}
	entries, _ := os.ReadDir(filepath.Join(dir, ".lyr", "sessions"))
	if len(entries) != 1 {
		t.Errorf("%d files left, want 1", len(entries))
	}
}
