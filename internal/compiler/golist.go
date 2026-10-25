package compiler

// GoListLoader's implementation (PLAN.md §3.3 Engine, step 2): one
// "go list -export -e -json" over the generated packages' external imports,
// read with the gc importer. Owned by agent C.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/importer"
	gotoken "go/token"
	"go/types"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
)

type goListPackage struct {
	ImportPath string
	Export     string
	Error      *struct{ Err string }
}

func (l *GoListLoader) load(ctx context.Context, fset *gotoken.FileSet, paths []string) (types.Importer, error) {
	exports := map[string]goListPackage{}
	if len(paths) > 0 {
		args := append([]string{"list", "-export", "-e", "-json=ImportPath,Export,Error"}, l.BuildFlags...)
		args = append(args, paths...)
		cmd := exec.CommandContext(ctx, "go", args...)
		cmd.Dir = l.Dir
		cmd.Env = append(os.Environ(), l.Env...)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if err != nil {
			msg := firstLine(stderr.String())
			if msg == "" {
				msg = err.Error()
			}
			return nil, fmt.Errorf("go list -export: %s", msg)
		}
		dec := json.NewDecoder(bytes.NewReader(out))
		for {
			var p goListPackage
			if err := dec.Decode(&p); err == io.EOF {
				break
			} else if err != nil {
				return nil, fmt.Errorf("go list -export: %v", err)
			}
			exports[p.ImportPath] = p
		}
	}
	lookup := func(path string) (io.ReadCloser, error) {
		p, ok := exports[path]
		switch {
		case !ok:
			return nil, fmt.Errorf("not listed by go list")
		case p.Error != nil && p.Export == "":
			return nil, errors.New(goListErr(p.Error.Err))
		case p.Export == "":
			return nil, fmt.Errorf("no export data")
		}
		return os.Open(p.Export)
	}
	return &lockedImporter{imp: importer.ForCompiler(fset, "gc", lookup)}, nil
}

// lockedImporter makes an importer safe for concurrent use.
type lockedImporter struct {
	mu  sync.Mutex
	imp types.Importer
}

func (l *lockedImporter) Import(path string) (*types.Package, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.imp.Import(path)
}

// goListErr is the first line of a go list package error that isn't a
// "# pkg" header: "internal/posts/posts.go:13:14: cannot use …".
func goListErr(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if line = strings.TrimSpace(line); line != "" && !strings.HasPrefix(line, "# ") {
			return line
		}
	}
	return firstLine(s)
}
