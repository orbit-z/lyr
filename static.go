package orbit

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// Static files: the /_orbit/* assets and the public/ files.
//
// In production, a file set is an fs.FS plus an index of its regular files,
// built once in NewSite, so the lookup that every GET runs before the page
// router is a map read: no system call and no allocation. Files added after
// startup aren't served until a restart. In dev (ORBIT_DEV=1), files are
// looked up per request, because orbit dev rebuilds them while the server
// runs.

// Shared, read-only Cache-Control values (cap == len, see htmlContentType).
var (
	cacheImmutable = []string{"public, max-age=31536000, immutable"}
	cachePublic    = []string{"public, max-age=0"}
	cacheNoCache   = []string{"no-cache"}
)

// fileSet is one source of static files. The zero value has no files.
type fileSet struct {
	fsys  fs.FS
	files map[string]fileMeta // the regular files of fsys, by slash path
	dir   string              // dev: a directory looked up per request
}

type fileMeta struct {
	etag string // set when the file has no modification time (embedded files)
}

// resolveStatic picks the static file source: $ORBIT_STATIC, then
// WithStaticDir, then WithStaticFS, then client/ in the directory of the
// running binary. A missing directory gives an empty set.
func resolveStatic(o siteOptions, dev bool) fileSet {
	if dir := os.Getenv("ORBIT_STATIC"); dir != "" {
		return newDirFiles(dir, dev)
	}
	if o.staticDir != "" {
		return newDirFiles(o.staticDir, dev)
	}
	if o.staticFS != nil {
		return newFSFiles(o.staticFS)
	}
	exe, err := os.Executable()
	if err != nil {
		return fileSet{}
	}
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		exe = real
	}
	return newDirFiles(filepath.Join(filepath.Dir(exe), "client"), dev)
}

// newDirFiles returns the files of dir. os.Root keeps every lookup inside
// dir, symlinks included.
func newDirFiles(dir string, dev bool) fileSet {
	if dev {
		return fileSet{dir: dir}
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return fileSet{}
	}
	return newFSFiles(root.FS())
}

// newFSFiles indexes the regular files of fsys.
func newFSFiles(fsys fs.FS) fileSet {
	files := make(map[string]fileMeta)
	fs.WalkDir(fsys, ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil // skip unreadable entries
		}
		info, err := fs.Stat(fsys, name) // follows symlinks
		if err != nil || !info.Mode().IsRegular() {
			return nil
		}
		var m fileMeta
		if info.ModTime().IsZero() {
			m.etag = contentETag(fsys, name)
		}
		files[name] = m
		return nil
	})
	return fileSet{fsys: fsys, files: files}
}

// contentETag returns a strong ETag from the file's content, for files
// without a modification time (go:embed), which get no Last-Modified.
func contentETag(fsys fs.FS, name string) string {
	f, err := fsys.Open(name)
	if err != nil {
		return ""
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return ""
	}
	return `"` + hex.EncodeToString(h.Sum(nil)[:16]) + `"`
}

// open returns the regular file name (a slash path), or ok false.
func (fset *fileSet) open(name string) (f fs.File, meta fileMeta, ok bool) {
	switch {
	case fset.dir != "":
		of, err := os.OpenInRoot(fset.dir, name)
		if err != nil {
			return nil, meta, false
		}
		f = of
	case fset.files != nil:
		m, found := fset.files[name]
		if !found {
			return nil, meta, false
		}
		ff, err := fset.fsys.Open(name)
		if err != nil {
			return nil, meta, false
		}
		f, meta = ff, m
	default:
		return nil, meta, false
	}
	if info, err := f.Stat(); err != nil || !info.Mode().IsRegular() {
		f.Close()
		return nil, meta, false
	}
	return f, meta, true
}

// serveFile serves a GET or HEAD for a static file, and reports whether it
// did. Paths under /_orbit/island/ are reserved for island requests and are
// never files. Directories are never served, and a path is never redirected.
func (s *Site) serveFile(w http.ResponseWriter, r *http.Request) bool {
	p := r.URL.Path
	if len(p) < 2 || p[0] != '/' {
		return false
	}
	name := p[1:]
	set, cache := &s.public, cachePublic
	if strings.HasPrefix(name, "_orbit/") {
		if strings.HasPrefix(name, "_orbit/island/") {
			return false
		}
		set, cache = &s.assets, cacheImmutable
	}
	if s.dev {
		cache = cacheNoCache
	}
	// fs.ValidPath rejects "..", ".", empty elements and a trailing slash,
	// so such paths never reach a file (os.Root also stops escapes).
	if (set.files == nil && set.dir == "") || !fs.ValidPath(name) {
		return false
	}
	if set.files != nil {
		if _, ok := set.files[name]; !ok {
			return false // the hot path for pages: a map miss, no allocation
		}
	}
	f, meta, ok := set.open(name)
	if !ok {
		return false
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return false
	}
	rs, ok := f.(io.ReadSeeker)
	if !ok {
		data, err := io.ReadAll(f)
		if err != nil {
			return false
		}
		rs = bytes.NewReader(data)
	}
	h := w.Header()
	h["Cache-Control"] = cache
	if meta.etag != "" {
		h["Etag"] = []string{meta.etag}
	}
	http.ServeContent(w, r, path.Base(name), info.ModTime(), rs)
	return true
}
