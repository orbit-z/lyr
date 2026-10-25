package build

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// AssetsDir is where Orbit serves its bundles, under dist/client (PLAN.md
// §4); public/ files may not use it.
const AssetsDir = "_orbit"

// CopyPublic copies the files of the public directory src into dst, which
// must not exist, and creates dst/_orbit. A missing src copies nothing.
// Symbolic links to files are copied as the files they point to; a symbolic
// link to a directory is an error. Problems with the user's files (a file
// under public/_orbit, a symbolic link to a directory) are returned as
// messages, "public/<path>: …", sorted, and dst is then incomplete; the
// error is for I/O failures. An empty public/_orbit directory is ignored.
func CopyPublic(src, dst string) (problems []string, err error) {
	if err := os.MkdirAll(filepath.Join(dst, AssetsDir), 0o755); err != nil {
		return nil, err
	}
	if _, err := os.Stat(src); errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	err = filepath.WalkDir(src, func(name string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, name)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		slash := filepath.ToSlash(rel)
		if slash == AssetsDir || strings.HasPrefix(slash, AssetsDir+"/") {
			if !d.IsDir() {
				problems = append(problems, fmt.Sprintf("public/%s: the /%s/ path is reserved for Orbit's assets; move it out of public/%s", slash, AssetsDir, AssetsDir))
			}
			return nil
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.Mkdir(target, 0o755)
		}
		info, err := os.Stat(name) // follows symbolic links
		if err != nil {
			return err
		}
		if info.IsDir() {
			problems = append(problems, fmt.Sprintf("public/%s: a symbolic link to a directory isn't supported; copy the directory or link its files", slash))
			return nil
		}
		if !info.Mode().IsRegular() {
			problems = append(problems, fmt.Sprintf("public/%s: not a regular file", slash))
			return nil
		}
		return copyFile(name, target, info.Mode().Perm())
	})
	if err != nil {
		return nil, err
	}
	slices.Sort(problems)
	return problems, nil
}

func copyFile(src, dst string, perm fs.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
