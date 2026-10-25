package project

import (
	"bytes"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/bertrandkhe/orbit/internal/compiler"
)

// StaticDir is orbit_gen/static, written by orbit build --embed. Generate
// leaves it alone: its embedded copies of dist/client have no marker.
const StaticDir = compiler.GenDir + "/static"

// WriteResult says what WriteGenerated changed, as module-relative paths,
// sorted.
type WriteResult struct {
	Written []string // created or changed files
	Removed []string // stale generated files deleted
}

// WriteGenerated writes files under root (each Path must be under
// orbit_gen/) and removes stale generated files. A file whose content is
// unchanged isn't rewritten, so its mtime (and the go build cache) stays
// warm; changed files are written atomically, to a temporary file renamed
// over the old one. A file under orbit_gen/ that isn't in files is removed
// only if it starts with the generated-code marker, so files the user put
// there are kept; directories left empty by a removal are removed too.
// orbit_gen/static is never touched.
func WriteGenerated(root string, files []GenFile) (*WriteResult, error) {
	res := &WriteResult{}
	keep := map[string]bool{}
	for _, f := range files {
		if !strings.HasPrefix(f.Path, compiler.GenDir+"/") || path.Clean(f.Path) != f.Path {
			return nil, errors.New("orbit: generated file outside " + compiler.GenDir + "/: " + f.Path)
		}
		keep[f.Path] = true
		changed, err := writeIfChanged(filepath.Join(root, filepath.FromSlash(f.Path)), f.Content)
		if err != nil {
			return nil, err
		}
		if changed {
			res.Written = append(res.Written, f.Path)
		}
	}
	removed, err := removeStale(root, keep)
	if err != nil {
		return nil, err
	}
	res.Removed = removed
	slices.Sort(res.Written)
	return res, nil
}

// writeIfChanged writes content to name atomically unless it already holds
// exactly that content.
func writeIfChanged(name string, content []byte) (bool, error) {
	if old, err := os.ReadFile(name); err == nil && bytes.Equal(old, content) {
		return false, nil
	}
	dir := filepath.Dir(name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return false, err
	}
	tmp, err := os.CreateTemp(dir, ".orbit-tmp-*")
	if err != nil {
		return false, err
	}
	_, werr := tmp.Write(content)
	cerr := tmp.Close()
	if err := errors.Join(werr, cerr, os.Chmod(tmp.Name(), 0o644)); err != nil {
		os.Remove(tmp.Name())
		return false, err
	}
	if err := os.Rename(tmp.Name(), name); err != nil {
		os.Remove(tmp.Name())
		return false, err
	}
	return true, nil
}

// removeStale deletes the marked files under root/orbit_gen that aren't in
// keep, then the directories that became empty.
func removeStale(root string, keep map[string]bool) ([]string, error) {
	gen := filepath.Join(root, compiler.GenDir)
	var removed []string
	dirs := map[string]bool{}
	err := filepath.WalkDir(gen, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) && p == gen {
				return filepath.SkipAll
			}
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if rel == StaticDir {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() || keep[rel] {
			return nil
		}
		marked, err := hasMarker(p)
		if err != nil || !marked {
			return err
		}
		if err := os.Remove(p); err != nil {
			return err
		}
		removed = append(removed, rel)
		dirs[filepath.Dir(p)] = true
		return nil
	})
	if err != nil {
		return nil, err
	}
	// Remove emptied directories, deepest first, up to (not including)
	// orbit_gen itself.
	list := make([]string, 0, len(dirs))
	for d := range dirs {
		list = append(list, d)
	}
	slices.SortFunc(list, func(a, b string) int { return len(b) - len(a) })
	for _, d := range list {
		for d != gen && strings.HasPrefix(d, gen) {
			if os.Remove(d) != nil { // not empty (or already gone)
				break
			}
			d = filepath.Dir(d)
		}
	}
	slices.Sort(removed)
	return removed, nil
}

// hasMarker reports whether the file starts with the generated-code marker
// line.
func hasMarker(name string) (bool, error) {
	f, err := os.Open(name)
	if err != nil {
		return false, err
	}
	defer f.Close()
	buf := make([]byte, len(compiler.GenHeader)+1)
	n, _ := io.ReadFull(f, buf)
	h := len(compiler.GenHeader)
	if n < h || string(buf[:h]) != compiler.GenHeader {
		return false, nil
	}
	return n == h || buf[h] == '\n' || buf[h] == '\r', nil
}
