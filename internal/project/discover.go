package project

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/orbit-z/lyr/internal/compiler"
)

// SrcDir and PagesDir are the project-relative source directories.
const (
	SrcDir   = "src"
	PagesDir = "src/pages"
)

// Discover returns the project-relative, "/"-separated paths of every .lyr
// and Markdown file under root/src, sorted. Directories and files whose names start with
// "." are skipped. Symlinked directories are followed, except when they lead
// back to a directory already being walked (a loop).
func Discover(root string) ([]string, error) {
	src := filepath.Join(root, SrcDir)
	fi, err := os.Stat(src)
	if err != nil || !fi.IsDir() {
		return nil, fmt.Errorf("%s has no %s/ directory; run lyr in a Lyr project", root, SrcDir)
	}
	var out []string
	var walk func(dir, rel string, active []os.FileInfo) error
	walk = func(dir, rel string, active []os.FileInfo) error {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return err
		}
		for _, e := range entries {
			name := e.Name()
			if strings.HasPrefix(name, ".") {
				continue
			}
			full := filepath.Join(dir, name)
			relName := path.Join(rel, name)
			typ := e.Type()
			if typ&os.ModeSymlink != 0 {
				fi, err := os.Stat(full)
				if err != nil {
					continue // a dangling link
				}
				typ = fi.Mode().Type()
			}
			switch {
			case typ.IsDir():
				fi, err := os.Stat(full)
				if err != nil {
					return err
				}
				if slices.ContainsFunc(active, func(a os.FileInfo) bool { return os.SameFile(a, fi) }) {
					continue // a symlink loop
				}
				if err := walk(full, relName, append(active, fi)); err != nil {
					return err
				}
			case typ.IsRegular() && (strings.HasSuffix(name, ".lyr") || compiler.IsMarkdown(name)):
				out = append(out, relName)
			}
		}
		return nil
	}
	if err := walk(src, SrcDir, []os.FileInfo{fi}); err != nil {
		return nil, err
	}
	slices.Sort(out)
	return out, nil
}
