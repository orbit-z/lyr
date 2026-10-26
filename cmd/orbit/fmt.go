package main

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"

	"github.com/orbit-z/orbit/internal/check"
	"github.com/orbit-z/orbit/internal/compiler"
	"github.com/orbit-z/orbit/internal/config"
	"github.com/orbit-z/orbit/internal/format"
)

// bunSkipping starts orbit fmt's notice when Bun is missing or too old.
const bunSkipping = "orbit fmt: skipping <script> and <style> formatting"

// runFmt is orbit fmt (PLAN.md §6.1): format .orbit files in place, or list
// (-l) or diff (-d) the ones that aren't formatted.
func runFmt(e *env, args []string) int {
	c := lookup("fmt")
	fset := c.flagSet(e)
	list := fset.Bool("l", false, "list the files whose formatting differs; write nothing unless -w is given")
	diff := fset.Bool("d", false, "print the diffs; write nothing unless -w is given")
	write := fset.Bool("w", false, "write the formatted files (the default without -l and -d)")
	stdinPath := fset.String("stdin-filepath", "", "format stdin as the file `PATH` and print the result")
	if code := parse(fset, args); code >= 0 {
		return code
	}
	paths := fset.Args()
	if *stdinPath != "" {
		if *write || len(paths) > 0 {
			fmt.Fprintln(e.stderr, "orbit fmt: --stdin-filepath takes no paths and no -w")
			fset.Usage()
			return exitUsage
		}
		return fmtStdin(e, *stdinPath, *list, *diff)
	}
	// Without -l or -d, files are written.
	doWrite := *write || !*list && !*diff
	cwdRoot, rootErr := config.FindRoot(e.dir)
	files, failed := fmtFiles(e, paths, cwdRoot, rootErr)
	if files == nil && failed {
		return exitFail
	}
	bun := ""
	notice := ""
	if len(files) > 0 {
		bun, notice = check.FindBun(e.ctx, "", bunSkipping)
	}
	results := make([]fmtResult, len(files))
	roots := &rootCache{m: map[string]string{}}
	var wg sync.WaitGroup
	jobs := make(chan int)
	for range runtime.GOMAXPROCS(0) {
		wg.Go(func() {
			for i := range jobs {
				results[i] = fmtFile(e, files[i], bun, doWrite, roots)
			}
		})
	}
	for i := range files {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	sort.SliceStable(results, func(i, j int) bool { return results[i].file.display < results[j].file.display })
	skipped := false
	differs := false
	for _, r := range results {
		for _, m := range r.messages {
			fmt.Fprintln(e.stderr, m)
		}
		if r.failed {
			failed = true
		}
		if r.skipped {
			skipped = true
		}
		if !r.changed {
			continue
		}
		differs = true
		if *list {
			fmt.Fprintln(e.stdout, r.file.display)
		}
		if *diff {
			io.WriteString(e.stdout, format.Diff(r.file.display, r.src, r.out))
		}
	}
	if skipped && notice != "" {
		fmt.Fprintln(e.stderr, notice)
	}
	if failed || (*list || *diff) && !*write && differs {
		return exitFail
	}
	return exitOK
}

// fmtTarget is one file to format.
type fmtTarget struct {
	path    string // as opened
	display string // in messages and output: project-relative, / separators
}

type fmtResult struct {
	file     fmtTarget
	src, out string
	changed  bool
	failed   bool
	skipped  bool // script or style blocks left for lack of Bun
	messages []string
}

// displayPath returns path relative to root when it is inside it, otherwise
// as given; with / separators.
func displayPath(path, root string) string {
	if root != "" {
		if abs, err := filepath.Abs(path); err == nil {
			if rel, err := filepath.Rel(root, abs); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				return filepath.ToSlash(rel)
			}
		}
	}
	return filepath.ToSlash(filepath.Clean(path))
}

// fmtFiles lists the files to format. failed reports a path error (already
// printed); files is nil when nothing can be done.
func fmtFiles(e *env, paths []string, root string, rootErr error) (files []fmtTarget, failed bool) {
	if len(paths) == 0 {
		if rootErr != nil {
			fmt.Fprintln(e.stderr, "orbit fmt: no go.mod found; pass the files or directories to format")
			return nil, true
		}
		paths = []string{filepath.Join(root, "src")}
		if _, err := os.Stat(paths[0]); err != nil {
			return nil, false // no src/: nothing to format
		}
	}
	if rootErr != nil {
		root = ""
	}
	seen := map[string]bool{}
	add := func(p string) {
		abs, _ := filepath.Abs(p)
		if seen[abs] {
			return
		}
		seen[abs] = true
		files = append(files, fmtTarget{path: p, display: displayPath(p, root)})
	}
	for _, p := range paths {
		if !filepath.IsAbs(p) {
			p = filepath.Join(e.dir, p)
		}
		fi, err := os.Stat(p)
		if err != nil {
			fmt.Fprintf(e.stderr, "orbit fmt: %v\n", err)
			failed = true
			continue
		}
		if !fi.IsDir() {
			if !strings.HasSuffix(p, ".orbit") {
				msg := "orbit fmt formats .orbit files"
				if strings.HasSuffix(p, ".md") {
					msg += "; format Markdown with bun fmt"
				}
				fmt.Fprintf(e.stderr, "orbit fmt: %s: %s\n", displayPath(p, root), msg)
				failed = true
				continue
			}
			add(p)
			continue
		}
		err = filepath.WalkDir(p, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if path != p && skipDir(path, d.Name(), root) {
					return filepath.SkipDir
				}
				return nil
			}
			if d.Type().IsRegular() && strings.HasSuffix(d.Name(), ".orbit") {
				add(path)
			}
			return nil
		})
		if err != nil {
			fmt.Fprintf(e.stderr, "orbit fmt: %v\n", err)
			failed = true
		}
	}
	if files == nil {
		files = []fmtTarget{}
	}
	return files, failed
}

// skipDir reports whether a directory walk skips dir: hidden directories,
// node_modules, orbit_gen and the project's dist/.
func skipDir(path, name, root string) bool {
	if strings.HasPrefix(name, ".") || name == "node_modules" || name == "orbit_gen" {
		return true
	}
	return name == "dist" && root != "" && filepath.Dir(path) == root
}

// rootCache finds the project root of each directory once.
type rootCache struct {
	mu sync.Mutex
	m  map[string]string
}

func (c *rootCache) root(dir string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	r, ok := c.m[dir]
	if !ok {
		r, _ = config.FindRoot(dir)
		c.m[dir] = r
	}
	return r
}

// bunOptions returns the options for formatting the file at abs: Bun runs
// in its project root, else in its directory.
func bunOptions(e *env, abs, bun string, roots *rootCache) format.Options {
	dir := filepath.Dir(abs)
	root := roots.root(dir)
	o := format.Options{Bun: bun, Context: e.ctx}
	if root == "" {
		o.BunDir, o.Path = dir, filepath.Base(abs)
		return o
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil {
		rel = filepath.Base(abs)
	}
	o.BunDir, o.Path = root, filepath.ToSlash(rel)
	return o
}

func fmtFile(e *env, t fmtTarget, bun string, doWrite bool, roots *rootCache) fmtResult {
	r := fmtResult{file: t}
	data, err := os.ReadFile(t.path)
	if err != nil {
		r.failed = true
		r.messages = append(r.messages, fmt.Sprintf("orbit fmt: %v", err))
		return r
	}
	abs, _ := filepath.Abs(t.path)
	r.src = string(data)
	out, res := format.Source(r.src, bunOptions(e, abs, bun, roots))
	r.messages = diagMessages(t.display, r.src, res)
	r.skipped = res.Skipped > 0
	if len(res.Errors) > 0 {
		r.failed = true
		return r
	}
	r.out = out
	r.changed = out != r.src
	if r.changed && doWrite {
		if err := writeAtomic(t.path, []byte(out)); err != nil {
			r.failed = true
			r.messages = append(r.messages, fmt.Sprintf("orbit fmt: %v", err))
		}
	}
	return r
}

// diagMessages formats a result's errors and warnings as file:L:C lines.
func diagMessages(display, src string, res format.Result) []string {
	if len(res.Errors) == 0 && len(res.Warnings) == 0 {
		return nil
	}
	lines := compiler.NewLineIndex(src)
	var out []string
	for _, d := range res.Errors {
		out = append(out, d.Format(display, lines))
	}
	for _, d := range res.Warnings {
		out = append(out, d.Format(display, lines))
	}
	return out
}

// writeAtomic replaces path with data through a temporary file in the same
// directory, keeping the file's mode.
func writeAtomic(path string, data []byte) error {
	fi, err := os.Stat(path)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".fmt*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	_, err = tmp.Write(data)
	if err == nil {
		err = tmp.Chmod(fi.Mode().Perm())
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(name, path)
	}
	if err != nil {
		os.Remove(name)
	}
	return err
}

// fmtStdin formats stdin as the file path.
func fmtStdin(e *env, path string, list, diff bool) int {
	data, err := io.ReadAll(e.stdin)
	if err != nil {
		fmt.Fprintln(e.stderr, "orbit fmt:", err)
		return exitFail
	}
	abs := path
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(e.dir, path)
	}
	bun, notice := check.FindBun(e.ctx, "", bunSkipping)
	src := string(data)
	out, res := format.Source(src, bunOptions(e, abs, bun, &rootCache{m: map[string]string{}}))
	display := filepath.ToSlash(path)
	for _, m := range diagMessages(display, src, res) {
		fmt.Fprintln(e.stderr, m)
	}
	if res.Skipped > 0 && notice != "" {
		fmt.Fprintln(e.stderr, notice)
	}
	if len(res.Errors) > 0 {
		return exitFail
	}
	switch {
	case list || diff:
		if out == src {
			return exitOK
		}
		if list {
			fmt.Fprintln(e.stdout, display)
		}
		if diff {
			io.WriteString(e.stdout, format.Diff(display, src, out))
		}
		return exitFail
	}
	io.WriteString(e.stdout, out)
	return exitOK
}
