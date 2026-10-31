package tailwind

import (
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
)

// Gate is lyr dev's class-candidate gate (PLAN.md §5d, Q4): the set of
// whitespace-separated tokens of the files Tailwind scans. Tailwind's
// candidates never contain whitespace, so an edit can only bring a new
// class with a token that isn't in the set; an edit that only removes,
// moves or repeats tokens doesn't rerun Tailwind.
type Gate struct {
	root   string
	seeded chan struct{}
	mu     sync.Mutex
	tokens map[string]struct{}
}

// maxScanFile is the largest file the gate reads: bigger ones always
// count as changed.
const maxScanFile = 4 << 20

// NewGate returns a gate for the project at root, seeded in the
// background with the tokens of the project's files (skip reports the
// directories to skip, by slash-separated path relative to root).
func NewGate(root string, skip func(rel string) bool) *Gate {
	g := &Gate{root: root, seeded: make(chan struct{}), tokens: map[string]struct{}{}}
	go func() {
		defer close(g.seeded)
		filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			rel, _ := filepath.Rel(root, p)
			rel = filepath.ToSlash(rel)
			if d.IsDir() {
				if rel != "." && skip != nil && skip(rel) {
					return filepath.SkipDir
				}
				return nil
			}
			if !Scanned(rel) {
				return nil
			}
			if data, err := os.ReadFile(p); err == nil && len(data) <= maxScanFile {
				g.add(string(data))
			}
			return nil
		})
	}()
	return g
}

// Scanned reports whether a file can hold class candidates for the gate:
// not a stylesheet, lock file or binary file (Tailwind skips those too).
func Scanned(rel string) bool {
	switch path.Ext(rel) {
	case ".css", ".lock", ".png", ".jpg", ".jpeg", ".gif", ".webp", ".avif", ".ico", ".woff", ".woff2", ".ttf", ".otf", ".eot", ".pdf", ".zip", ".gz", ".mp4", ".webm", ".mp3", ".wasm", ".exe":
		return false
	}
	switch path.Base(rel) {
	case "package-lock.json", "go.sum", "bun.lockb", "pnpm-lock.yaml":
		return false
	}
	return true
}

func (g *Gate) add(text string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	added := false
	for _, t := range strings.Fields(text) {
		if _, ok := g.tokens[t]; !ok {
			g.tokens[t] = struct{}{}
			added = true
		}
	}
	return added
}

// Changed reads the changed files (slash-separated, relative to the
// root) and reports whether one brings a token that isn't in the set,
// adding them. A file that can't be read (removed) brings nothing; a
// directory or a file too big to read counts as new.
func (g *Gate) Changed(paths []string) bool {
	<-g.seeded
	changed := false
	for _, rel := range paths {
		if !Scanned(rel) {
			continue
		}
		name := filepath.Join(g.root, filepath.FromSlash(rel))
		fi, err := os.Stat(name)
		if err != nil {
			continue
		}
		if fi.IsDir() || fi.Size() > maxScanFile {
			changed = true
			continue
		}
		data, err := os.ReadFile(name)
		if err != nil {
			continue
		}
		if g.add(string(data)) {
			changed = true
		}
	}
	return changed
}
