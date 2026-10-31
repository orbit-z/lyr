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

	"github.com/orbit-z/lyr/internal/compiler"
)

// StaticDir is lyr_gen/static, written by lyr build --embed. Generate
// only writes its doc.go and never removes anything there: its embedded
// copies of dist/client have no marker.
const StaticDir = compiler.GenDir + "/static"

// WriteResult says what WriteGenerated changed, as module-relative paths,
// sorted.
type WriteResult struct {
	Written []string // created or changed files
	Removed []string // stale generated files deleted
}

// WriteGenerated writes files under root (each Path must be under
// lyr_gen/) and removes stale generated files. A file whose content is
// unchanged isn't rewritten, so its mtime (and the go build cache) stays
// warm; changed files are written atomically, to a temporary file renamed
// over the old one. A file under lyr_gen/ that isn't in files is removed
// only if it starts with the generated-code marker, so files the user put
// there are kept; directories left empty by a removal are removed too.
// lyr_gen/static is never touched.
func WriteGenerated(root string, files []GenFile) (*WriteResult, error) {
	res := &WriteResult{}
	keep := map[string]bool{}
	for _, f := range files {
		if !strings.HasPrefix(f.Path, compiler.GenDir+"/") || path.Clean(f.Path) != f.Path {
			return nil, errors.New("lyr: generated file outside " + compiler.GenDir + "/: " + f.Path)
		}
		keep[f.Path] = true
	}
	// Each file is read, compared and maybe written on its own, so they go
	// in parallel; the results are collected in files order.
	changed := make([]bool, len(files))
	errs := make([]error, len(files))
	parallelN(len(files), func(i int) {
		changed[i], errs[i] = writeIfChanged(filepath.Join(root, filepath.FromSlash(files[i].Path)), files[i].Content)
	})
	for i, f := range files {
		if errs[i] != nil {
			return nil, errs[i]
		}
		if changed[i] {
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
	tmp, err := os.CreateTemp(dir, ".lyr-tmp-*")
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

// DiffGenerated reports what WriteGenerated(root, files) would change,
// without writing anything: the files whose content differs or that are
// missing (Written), and the stale generated files it would remove
// (Removed). lyr doctor uses it to tell whether lyr_gen/ is fresh.
func DiffGenerated(root string, files []GenFile) (*WriteResult, error) {
	res := &WriteResult{}
	keep := map[string]bool{}
	for _, f := range files {
		keep[f.Path] = true
		old, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(f.Path)))
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
		if err != nil || !bytes.Equal(old, f.Content) {
			res.Written = append(res.Written, f.Path)
		}
	}
	stale, err := staleFiles(root, keep)
	if err != nil {
		return nil, err
	}
	for _, s := range stale {
		rel, _ := filepath.Rel(root, s)
		res.Removed = append(res.Removed, filepath.ToSlash(rel))
	}
	slices.Sort(res.Written)
	slices.Sort(res.Removed)
	return res, nil
}

// removeStale deletes the marked files under root/lyr_gen that aren't in
// keep, then the directories that became empty.
func removeStale(root string, keep map[string]bool) ([]string, error) {
	gen := filepath.Join(root, compiler.GenDir)
	stale, err := staleFiles(root, keep)
	if err != nil {
		return nil, err
	}
	var removed []string
	dirs := map[string]bool{}
	for _, p := range stale {
		if err := os.Remove(p); err != nil {
			return nil, err
		}
		rel, _ := filepath.Rel(root, p)
		removed = append(removed, filepath.ToSlash(rel))
		dirs[filepath.Dir(p)] = true
	}
	// Remove emptied directories, deepest first, up to (not including)
	// lyr_gen itself.
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

// staleFiles returns the absolute paths of the marked files under
// root/lyr_gen (outside lyr_gen/static) that aren't in keep.
func staleFiles(root string, keep map[string]bool) ([]string, error) {
	gen := filepath.Join(root, compiler.GenDir)
	var stale []string
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
		stale = append(stale, p)
		return nil
	})
	return stale, err
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
