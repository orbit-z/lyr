package dev

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/orbit-z/orbit/internal/config"
)

// Debounce is how long the watcher waits after the last change before it
// hands a batch to the pipeline (PLAN.md §7).
const Debounce = 50 * time.Millisecond

// event is one relevant file-system change, relative to the module root
// and slash-separated (starting with ../ for a watched path outside it).
type event struct {
	rel   string
	isDir bool
	// created is set for a Create event: a path created and removed again
	// within one batch (an editor's or sed -i's temporary file) is left
	// out of it.
	created bool
}

// watcher watches the module root (PLAN.md §7): src/, public/ and every
// .go file in the module, excluding the directories skipDir rejects
// (orbit_gen/, dist/, .orbit/, node_modules/). fsnotify isn't recursive, so
// it adds every directory, and the new ones as they're created. It also
// watches the directories of the orbit.json "watch" patterns (setWatch),
// which may be outside the module.
type watcher struct {
	root string
	fw   *fsnotify.Watcher
	errf func(error) // reports watch errors

	mu sync.Mutex
	ws watchSet
}

func newWatcher(root string, errf func(error)) (*watcher, error) {
	fw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	w := &watcher{root: root, fw: fw, errf: errf}
	if err := w.addTree(root, nil); err != nil {
		fw.Close()
		return nil, err
	}
	return w, nil
}

func (w *watcher) close() error { return w.fw.Close() }

// rel returns name relative to the root, slash-separated; it starts with
// ../ outside the root.
func (w *watcher) rel(name string) (string, bool) {
	r, err := filepath.Rel(w.root, name)
	if err != nil {
		return "", false
	}
	return filepath.ToSlash(r), true
}

// classify classifies a path with the current watch patterns.
func (w *watcher) classify(rel string, isDir bool) Kind {
	w.mu.Lock()
	ws := w.ws
	w.mu.Unlock()
	return ws.classify(rel, isDir)
}

// setWatch sets the "watch" patterns and watches their directories: a
// pattern's fixed leading directory, and the directories under it when
// the pattern can match deeper (skipping the directories skipDir rejects
// below it). A directory that doesn't exist is reported to warn and
// skipped. Watches added for earlier patterns stay; their events are
// ignored once no pattern matches.
func (w *watcher) setWatch(patterns []string, warn func(string)) {
	w.mu.Lock()
	w.ws = watchSet(patterns)
	w.mu.Unlock()
	for _, p := range patterns {
		dir, recursive := config.WatchBase(p)
		abs := filepath.Join(w.root, filepath.FromSlash(dir))
		fi, err := os.Stat(abs)
		if err != nil || !fi.IsDir() {
			switch {
			case err == nil:
				warn(fmt.Sprintf("orbit.json: watch %q: %s isn't a directory; not watched", p, dir))
			case errors.Is(err, fs.ErrNotExist):
				warn(fmt.Sprintf("orbit.json: watch %q: directory %s doesn't exist; not watched", p, dir))
			default:
				w.errf(err)
			}
			continue
		}
		if !recursive {
			if err := w.fw.Add(abs); err != nil {
				w.errf(err)
			}
			continue
		}
		if err := w.addTree(abs, nil); err != nil {
			w.errf(err)
		}
	}
}

// addTree watches dir and every directory under it that isn't skipped.
// When found is non-nil, it is called with every file and directory found
// below dir (they may have been created before the watch was added).
func (w *watcher) addTree(dir string, found func(event)) error {
	return filepath.WalkDir(dir, func(name string, d fs.DirEntry, err error) error {
		if err != nil {
			if name == dir {
				return err
			}
			return nil // vanished meanwhile
		}
		rel, ok := w.rel(name)
		if !ok {
			return nil
		}
		if d.IsDir() {
			if name != dir && skipDir(rel) {
				return filepath.SkipDir
			}
			if err := w.fw.Add(name); err != nil {
				w.errf(err)
			}
		}
		if found != nil && name != dir {
			found(event{rel: rel, isDir: d.IsDir()})
		}
		return nil
	})
}

// run forwards relevant events to out until ctx is done.
func (w *watcher) run(ctx context.Context, out chan<- event) {
	send := func(e event) {
		if w.classify(e.rel, e.isDir) == KindIgnore {
			return
		}
		select {
		case out <- e:
		case <-ctx.Done():
		}
	}
	for {
		select {
		case <-ctx.Done():
			return
		case err, ok := <-w.fw.Errors:
			if !ok {
				return
			}
			w.errf(err)
		case ev, ok := <-w.fw.Events:
			if !ok {
				return
			}
			if ev.Op == fsnotify.Chmod {
				continue
			}
			rel, ok := w.rel(ev.Name)
			if !ok || rel == "." {
				continue
			}
			isDir := false
			if ev.Has(fsnotify.Create) {
				if fi, err := os.Stat(ev.Name); err == nil && fi.IsDir() {
					isDir = true
					if skipDir(rel) {
						continue
					}
					if strings.HasPrefix(rel, "../") && w.classify(rel, true) == KindIgnore {
						continue // outside the module, and no pattern reaches into it
					}
					if err := w.addTree(ev.Name, send); err != nil {
						w.errf(err)
					}
				}
			}
			send(event{rel: rel, isDir: isDir, created: ev.Has(fsnotify.Create)})
		}
	}
}

// debounce batches events: a batch is ready Debounce after its last event
// (each event restarts the wait) and is sent on out, as the changed paths
// and the set of those that are directories. Events that arrive while out
// isn't ready to receive (the pipeline is busy) join the next batch. busy is
// called on every event, so requests can start waiting for the rebuild
// before the batch is even ready. exists reports whether a path exists
// when the batch is made: a path created during the batch that no longer
// exists is dropped. classify classifies the paths of a batch.
func debounce(ctx context.Context, in <-chan event, out chan<- Change, d time.Duration, busy func(), exists func(rel string) bool, classify classifier) {
	var (
		pending []event
		timer   *time.Timer
		timerC  <-chan time.Time
		ready   []event
		outC    chan<- Change
		batch   Change
	)
	for {
		select {
		case <-ctx.Done():
			if timer != nil {
				timer.Stop()
			}
			return
		case e := <-in:
			if busy != nil {
				busy()
			}
			pending = append(pending, e)
			if timer == nil {
				timer = time.NewTimer(d)
			} else {
				timer.Stop()
				timer.Reset(d)
			}
			timerC = timer.C
		case <-timerC:
			timerC = nil
			ready = append(ready, pending...)
			pending = nil
			var paths []string
			dirs, created := map[string]bool{}, map[string]bool{}
			for _, e := range ready {
				created[e.rel] = created[e.rel] || e.created
			}
			for _, e := range ready {
				if created[e.rel] && !exists(e.rel) {
					continue // a temporary file
				}
				paths = append(paths, e.rel)
				if e.isDir {
					dirs[e.rel] = true
				}
			}
			batch = newChange(paths, dirs, classify)
			outC = out
		case outC <- batch:
			ready, outC = nil, nil
		}
	}
}
