package compiler

import (
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/importer"
	goparser "go/parser"
	gotoken "go/token"
	"go/types"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// tyStd is one process-wide importer for std and the runtime (gc export
// data found through the go command), shared by the test loaders so each
// package is read once per test binary.
var (
	tyStdOnce sync.Once
	tyStdImp  types.Importer
	tyStdFset = gotoken.NewFileSet()
)

func tyStd() types.Importer {
	tyStdOnce.Do(func() {
		tyStdImp = &lockedImporter{imp: &tyRuntimeImporter{std: importer.ForCompiler(tyStdFset, "gc", nil)}}
	})
	return tyStdImp
}

// tyRuntimeImporter type-checks the runtime (the repository root) from
// source and imports std from export data. Not safe for concurrent use.
type tyRuntimeImporter struct {
	std types.Importer
	rt  *types.Package
	err error
}

func (im *tyRuntimeImporter) Import(path string) (*types.Package, error) {
	if path != RuntimePath {
		return im.std.Import(path)
	}
	if im.rt == nil && im.err == nil {
		var files []*ast.File
		names, _ := filepath.Glob("../../*.go")
		for _, name := range names {
			if strings.HasSuffix(name, "_test.go") {
				continue
			}
			f, err := goparser.ParseFile(tyStdFset, name, nil, goparser.SkipObjectResolution)
			if err != nil {
				im.err = err
				return nil, err
			}
			files = append(files, f)
		}
		conf := types.Config{Importer: im}
		im.rt, im.err = conf.Check(RuntimePath, tyStdFset, files, nil)
	}
	return im.rt, im.err
}

// tyTestLoader is an ExportLoader for tests: user packages are type-checked
// from in-memory source, everything else comes from tyStd.
type tyTestLoader struct {
	pkgs  map[string]string // import path → the package's one file
	fail  map[string]bool   // paths whose Import fails, like unreadable export data
	err   error             // Load fails
	loads int
	paths [][]string // the paths of each Load
}

func (l *tyTestLoader) Load(_ context.Context, fset *gotoken.FileSet, paths []string) (types.Importer, error) {
	l.loads++
	l.paths = append(l.paths, paths)
	if l.err != nil {
		return nil, l.err
	}
	return &tyTestImporter{l: l, fset: fset, done: map[string]*tyTestPkg{}}, nil
}

type tyTestPkg struct {
	once sync.Once
	pkg  *types.Package
	err  error
}

type tyTestImporter struct {
	l    *tyTestLoader
	fset *gotoken.FileSet
	mu   sync.Mutex
	done map[string]*tyTestPkg
}

func (im *tyTestImporter) Import(path string) (*types.Package, error) {
	if im.l.fail[path] {
		return nil, errors.New("open /cache/xx-d: no such file or directory")
	}
	src, ok := im.l.pkgs[path]
	if !ok {
		return tyStd().Import(path)
	}
	im.mu.Lock()
	p := im.done[path]
	if p == nil {
		p = &tyTestPkg{}
		im.done[path] = p
	}
	im.mu.Unlock()
	p.once.Do(func() {
		var f *ast.File
		f, p.err = goparser.ParseFile(im.fset, path+"/x.go", src, 0)
		if p.err != nil {
			return
		}
		conf := types.Config{Importer: im}
		p.pkg, p.err = conf.Check(path, im.fset, []*ast.File{f}, nil)
	})
	return p.pkg, p.err
}

// tyWriteString renders a write as "Method/conv", "skip", or "Expr"/"Attr"
// for a fallback.
func tyWriteString(s CallSite, w TypedWrite, ok bool) string {
	if !ok || w.Kind == WriteFallback {
		if s.Kind == SiteAttr {
			return "Attr"
		}
		return "Expr"
	}
	k := w.Kind.Method()
	if w.Kind == WriteSkip {
		k = "skip"
	}
	if w.Conv != "" {
		k += "/" + w.Conv
	}
	return k
}

// tyGot lists the write chosen for each call site of a unit, in order.
func tyGot(u Unit, tu TypedUnit) []string {
	var got []string
	for _, s := range u.Pass1.CallSites {
		w, ok := tu.Writes[s.Key]
		got = append(got, tyWriteString(s, w, ok))
	}
	return got
}

func tyCheck(t *testing.T, c *Checker, units []Unit) *CheckResult {
	t.Helper()
	res, err := c.Check(context.Background(), units)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Units) != len(units) {
		t.Fatalf("Check returned %d units, want %d", len(res.Units), len(units))
	}
	return res
}

func tyChecked(res *CheckResult) string {
	s := ""
	for _, u := range res.Units {
		s += fmt.Sprintf("%v ", u.Checked)
	}
	return s
}
