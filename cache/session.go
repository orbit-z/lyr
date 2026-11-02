package cache

import (
	"bytes"
	"context"
	"fmt"
	"hash/maphash"
	"log"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/orbit-z/lyr"
)

// DefaultSessionEntries is the session bound used when
// SessionStoreOptions.MaxEntries is 0. Every eviction signs a visitor out:
// size it for your visitors.
const DefaultSessionEntries = 10_000

// SessionStoreOptions configure NewSessionStore.
type SessionStoreOptions struct {
	// MaxEntries bounds the number of sessions held. 0 means
	// DefaultSessionEntries.
	MaxEntries int
	// MaxBytes, when positive, bounds the bytes held (data plus IDs). A
	// session bigger than MaxBytes divided by the number of shards is not
	// stored.
	MaxBytes int64
	// Now is the clock for expiry; nil means time.Now. Tests set it.
	Now func() time.Time
}

// SessionStore is an in-process lyr.SessionStore, for lyr.WithSessions:
// sessions live in this process's memory, bounded by entry count and
// optionally bytes, and expire after the TTL Lyr passes. Several instances
// of an app need a shared store (such as Redis) instead. Make one with
// NewSessionStore. It's safe for concurrent use.
//
// Under lyr dev (LYR_DEV=1), it also writes each session to
// .lyr/sessions/<id> in the working directory and reads it back on a
// miss, so sessions survive the dev server's restarts. Production never
// touches the disk.
type SessionStore struct {
	g    *engine[[]byte]
	seed maphash.Seed
	dir  string // the dev write-through directory, or ""
}

// NewSessionStore returns an empty SessionStore. It panics if a bound is
// negative.
func NewSessionStore(o SessionStoreOptions) *SessionStore {
	n := o.MaxEntries
	if n == 0 {
		n = DefaultSessionEntries
	}
	s := &SessionStore{g: newEngine[[]byte](n, o.MaxBytes, o.Now, 0), seed: maphash.MakeSeed()}
	if os.Getenv("LYR_DEV") == "1" {
		if wd, err := os.Getwd(); err == nil {
			s.dir = filepath.Join(wd, ".lyr", "sessions")
		}
	}
	return s
}

// Get returns the session stored under id, or nil. The slice is the one
// given to Set, not a copy. It doesn't allocate on a memory hit.
func (s *SessionStore) Get(_ context.Context, id string) ([]byte, error) {
	h := maphash.String(s.seed, id)
	if v, _, ok := s.g.get(h, id); ok {
		return v, nil
	}
	if s.dir != "" {
		if v, exp, ok := s.readFile(id); ok {
			s.g.set(h, id, cloneKey, v, int64(len(id)+len(v)), exp, nil)
			return v, nil
		}
	}
	return nil, nil
}

// Set stores data under id for ttl (0: no expiry), keeping data as given.
// A session bigger than the per-shard byte bound (MaxBytes divided by the
// number of shards) isn't stored: Set drops the session stored under id
// before and returns an error. Lyr treats it as a failed save: a page or
// island request answers with a 500 (or your error handler's answer);
// under site.Sessions, the error is logged.
func (s *SessionStore) Set(_ context.Context, id string, data []byte, ttl time.Duration) error {
	exp := int64(math.MaxInt64)
	if ttl > 0 {
		exp = s.g.now().Add(ttl).UnixNano()
	}
	size := int64(len(id) + len(data))
	if !s.g.set(maphash.String(s.seed, id), id, cloneKey, data, size, exp, nil) {
		if s.dir != "" && safeID(id) {
			os.Remove(filepath.Join(s.dir, id))
		}
		return fmt.Errorf("cache: a session of %d bytes is bigger than the store's per-shard bound of %d bytes (SessionStoreOptions.MaxBytes divided by the shards); it isn't stored", size, s.g.maxBytes)
	}
	if s.dir != "" {
		s.writeFile(id, data, exp)
	}
	return nil
}

// Delete drops the session stored under id.
func (s *SessionStore) Delete(_ context.Context, id string) error {
	s.g.delete(maphash.String(s.seed, id), id)
	if s.dir != "" && safeID(id) {
		os.Remove(filepath.Join(s.dir, id))
	}
	return nil
}

// Len returns the number of sessions held, expired ones not yet dropped
// included.
func (s *SessionStore) Len() int { return s.g.len() }

func cloneKey(k string) string { return strings.Clone(k) }

var _ lyr.SessionStore = (*SessionStore)(nil)

// safeID reports whether id can name a file: Lyr's IDs are UUIDs.
func safeID(id string) bool {
	if id == "" || len(id) > 64 {
		return false
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		if !('0' <= c && c <= '9' || 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

// writeFile writes the dev copy: the expiry (Unix nanoseconds) on the
// first line, then the data.
func (s *SessionStore) writeFile(id string, data []byte, exp int64) {
	if !safeID(id) {
		return
	}
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		log.Printf("lyr: dev sessions: %v", err)
		return
	}
	b := strconv.AppendInt(nil, exp, 10)
	b = append(b, '\n')
	b = append(b, data...)
	// A temporary file of its own, so concurrent saves of one session
	// never write the same file; the last rename wins.
	f, err := os.CreateTemp(s.dir, "."+id+".*.tmp")
	if err != nil {
		log.Printf("lyr: dev sessions: %v", err)
		return
	}
	tmp := f.Name()
	_, err = f.Write(b)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp, filepath.Join(s.dir, id))
	}
	if err != nil {
		os.Remove(tmp)
		log.Printf("lyr: dev sessions: %v", err)
	}
}

// readFile reads a dev copy that hasn't expired.
func (s *SessionStore) readFile(id string) ([]byte, int64, bool) {
	if !safeID(id) {
		return nil, 0, false
	}
	path := filepath.Join(s.dir, id)
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, 0, false
	}
	line, data, ok := bytes.Cut(b, []byte{'\n'})
	exp, err := strconv.ParseInt(string(line), 10, 64)
	if !ok || err != nil {
		return nil, 0, false
	}
	if s.g.now().UnixNano() >= exp {
		os.Remove(path)
		return nil, 0, false
	}
	return data, exp, true
}
