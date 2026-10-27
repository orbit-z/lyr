package dev

import (
	"path"
	"slices"
	"strings"

	"github.com/orbit-z/orbit/internal/compiler"
	"github.com/orbit-z/orbit/internal/config"
)

// Kind is what a changed path asks the dev pipeline to do (PLAN.md §7.1),
// before looking at its content.
type Kind int

const (
	// KindIgnore: not an input of the build (README.md, a _test.go file,
	// an editor's backup file).
	KindIgnore Kind = iota
	// KindPublic: a file under public/. The app serves it straight from
	// the project, so nothing is rebuilt; the browser just reloads.
	KindPublic
	// KindAsset: a file that only the esbuild bundles read, such as a .ts
	// or .css file under src/, or tsconfig.json: incremental esbuild, no Go
	// rebuild.
	KindAsset
	// KindOrbit: a .orbit or Markdown file under src/ (or a directory
	// there). The pipeline regenerates and compares: if only
	// <style>/<script> contents changed, it's an esbuild rebuild; otherwise
	// a Go rebuild.
	KindOrbit
	// KindGo: a user .go file, go.mod or go.sum: reload the type checker's
	// export data, regenerate, go build. A change made only of KindGo
	// paths starts go build at once, in parallel with regenerating.
	KindGo
	// KindConfig: orbit.json: regenerate everything and go build.
	KindConfig
	// KindWatch: a file that matches an orbit.json "watch" pattern and is
	// no other kind: restart the app (no rebuild) and reload.
	KindWatch
)

func (k Kind) String() string {
	return [...]string{"ignore", "public", "asset", "orbit", "go", "config", "watch"}[k]
}

// classifier classifies a changed path (Classify, or watchSet.classify).
type classifier func(rel string, isDir bool) Kind

// watchSet is the orbit.json "watch" patterns.
type watchSet []string

// classify is Classify, plus KindWatch for a path that Classify ignores
// and a pattern matches (for a directory: a pattern could match below it),
// except under Orbit's own output (orbit_gen/, dist/, .orbit/) and for
// editors' temporary files.
func (ws watchSet) classify(rel string, isDir bool) Kind {
	k := Classify(rel, isDir)
	if k != KindIgnore || len(ws) == 0 {
		return k
	}
	rel = path.Clean(rel)
	first, _, _ := strings.Cut(rel, "/")
	if first == "orbit_gen" || first == "dist" || first == ".orbit" || editorTemp(path.Base(rel)) {
		return KindIgnore
	}
	for _, p := range ws {
		if config.MatchWatch(p, rel, isDir) {
			return KindWatch
		}
	}
	return KindIgnore
}

// skipDir reports whether the watcher skips a directory, by its
// slash-separated path relative to the module root ("" is the root):
// orbit_gen/, dist/, node_modules/, dot directories (.orbit/, .git/) and,
// like the go command, testdata/ and directories starting with "_" (except
// under src/, where "_" directories hold page partials).
func skipDir(rel string) bool {
	if rel == "" || rel == "." {
		return false
	}
	switch rel {
	case "orbit_gen", "dist", "node_modules":
		return true
	}
	base := path.Base(rel)
	if strings.HasPrefix(base, ".") || base == "node_modules" || base == "testdata" {
		return true
	}
	if strings.HasPrefix(base, "_") && !strings.HasPrefix(rel, "src/") {
		return true
	}
	return false
}

// Classify returns the kind of a changed path, slash-separated and relative
// to the module root. isDir is set for a directory that was created or
// removed.
func Classify(rel string, isDir bool) Kind {
	rel = path.Clean(rel)
	dir := path.Dir(rel)
	for d := dir; d != "." && d != "/"; d = path.Dir(d) {
		if skipDir(d) {
			return KindIgnore
		}
	}
	if isDir && skipDir(rel) {
		return KindIgnore
	}
	base := path.Base(rel)
	if base == "public" && dir == "." || strings.HasPrefix(rel, "public/") {
		return KindPublic
	}
	if isDir {
		if rel == "src" || strings.HasPrefix(rel, "src/") {
			return KindOrbit // files in it may not have their own events
		}
		return KindIgnore // a new directory's .go files have their own events
	}
	if editorTemp(base) {
		return KindIgnore
	}
	switch {
	case dir == "." && base == "orbit.json":
		return KindConfig
	case dir == "." && (base == "go.mod" || base == "go.sum" || base == "go.work"):
		return KindGo
	case strings.HasSuffix(base, "_test.go"):
		return KindIgnore
	case strings.HasSuffix(base, ".go"):
		return KindGo
	case strings.HasSuffix(base, ".orbit") || compiler.IsMarkdown(base):
		// Markdown files under src/ are compiled like .orbit files
		// (PLAN.md §2.4).
		if rel == "src" || strings.HasPrefix(rel, "src/") {
			return KindOrbit
		}
		return KindIgnore
	case strings.HasPrefix(rel, "src/") && path.Ext(base) == "":
		// No extension: most likely a directory that was removed or
		// renamed (which has no events for the files in it).
		return KindOrbit
	case strings.HasPrefix(rel, "src/"):
		return KindAsset
	case dir == "." && (base == "package.json" || isTsconfigName(base)):
		return KindAsset
	}
	return KindIgnore
}

// editorTemp reports whether base looks like an editor's temporary or
// backup file: Vim's swap files and its 4913 probe, Emacs's #x# and .#x,
// x~ backups, and hidden files.
func editorTemp(base string) bool {
	switch {
	case strings.HasPrefix(base, "."), strings.HasSuffix(base, "~"),
		strings.HasPrefix(base, "#") && strings.HasSuffix(base, "#"),
		strings.HasSuffix(base, ".swp"), strings.HasSuffix(base, ".swx"),
		base == "4913":
		return true
	}
	return false
}

// Change is a debounced batch of changed paths, by kind.
type Change struct {
	// Paths are the changed paths (relative, slash-separated), sorted and
	// without duplicates; ignored paths aren't listed.
	Paths []string
	kinds map[Kind]bool
}

// NewChange classifies paths into a Change. dirs are the paths that are
// directories.
func NewChange(paths []string, dirs map[string]bool) Change {
	return newChange(paths, dirs, Classify)
}

func newChange(paths []string, dirs map[string]bool, classify classifier) Change {
	c := Change{kinds: map[Kind]bool{}}
	seen := map[string]bool{}
	for _, p := range paths {
		k := classify(p, dirs[p])
		if k == KindIgnore || seen[p] {
			continue
		}
		seen[p] = true
		c.Paths = append(c.Paths, p)
		c.kinds[k] = true
	}
	slices.Sort(c.Paths)
	return c
}

// Has reports whether the change has a path of kind k.
func (c Change) Has(k Kind) bool { return c.kinds[k] }

// Empty reports whether nothing relevant changed.
func (c Change) Empty() bool { return len(c.Paths) == 0 }

// Only reports whether every path of the change is of one of kinds (and
// there is at least one path).
func (c Change) Only(kinds ...Kind) bool {
	if c.Empty() {
		return false
	}
	for k := range c.kinds {
		if !slices.Contains(kinds, k) {
			return false
		}
	}
	return true
}

// isTsconfigName reports whether a root file's name is tsconfig.json or
// tsconfig.<name>.json (a file tsconfig.json may extend): a change to one
// also re-resolves the components' import aliases (PLAN.md §2.2).
func isTsconfigName(base string) bool {
	return base == "tsconfig.json" || strings.HasPrefix(base, "tsconfig.") && strings.HasSuffix(base, ".json")
}

// touchesTsconfig reports whether the change has a root tsconfig file: the
// front runs then (not just esbuild), to re-resolve import aliases.
func (c Change) touchesTsconfig() bool {
	return slices.ContainsFunc(c.Paths, func(p string) bool { return path.Dir(p) == "." && isTsconfigName(p) })
}
