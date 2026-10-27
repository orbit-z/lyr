package project

import (
	"os"
	"path"
	"path/filepath"
	"reflect"
	"slices"
	"strings"

	"github.com/orbit-z/orbit/internal/compiler"
	"github.com/orbit-z/orbit/internal/config"
)

// Reload returns the project at root after the files at paths changed on
// disk (written, created, removed or renamed), through Front: it reports
// whether there are errors, as Front does. It's the per-file front of orbit
// dev (PLAN.md §7.1 step 1).
//
// prev is the project of an earlier Load (or Reload) and Front of root,
// whose Files may have been through Bundle and Typecheck since. Reload
// updates it in place and returns it, redoing only what the change
// affects: it rediscovers src/, reads only the changed and new .orbit and
// Markdown files, re-parses them, updates their index entries, and runs
// pass 1 on them and on every file that imports one whose entry changed (or
// appeared or went away). The project checks and the diagnostics are
// recomputed. The result is the same as Load followed by Front, which
// Reload falls back to (returning a new Project) when prev is nil or a
// path is one that every file depends on: go.mod, orbit.json, or a Go file
// that changes which src/locals type the generated code uses.
//
// paths are relative to root and "/"-separated (as in orbit dev's change
// batches), or absolute; paths outside root are ignored, and so are paths
// that Load doesn't read (public/, .ts files…). A directory stands for
// every file under it. A changed file whose content is unchanged (an editor
// saving without changes) changes nothing. The error is Load's (an I/O
// error, no src/ directory…); after one, prev may be partly updated, so
// pass nil next time.
func Reload(prev *Project, root string, paths []string) (*Project, bool, error) {
	full := func() (*Project, bool, error) {
		p, err := Load(root)
		if err != nil {
			return nil, false, err
		}
		return p, p.Front(), nil
	}
	if prev == nil || prev.Index == nil {
		return full()
	}
	p := prev
	changed := make([]string, 0, len(paths))
	locals := false // re-detect src/locals
	for _, name := range paths {
		rel, ok := relPath(p.Root, name)
		if !ok {
			continue
		}
		switch {
		case rel == "go.mod" || rel == config.FileName || rel == "." || p.isTsconfig(rel):
			// tsconfig.json (or a file it extends): its paths aliases
			// may resolve any import differently (PLAN.md §2.2).
			return full()
		case strings.HasSuffix(rel, ".go") || rel == LocalsDir || strings.HasPrefix(rel, LocalsDir+"/") ||
			strings.HasPrefix(LocalsDir, rel+"/"):
			// A Go file anywhere: src/locals, or a package it imports
			// (detectLocals follows the module's imports).
			locals = true
		}
		changed = append(changed, rel)
	}
	if locals {
		l, route, ds := detectLocals(p.Root, p.Module)
		if l != p.Options.Locals {
			return full() // every file's codegen options changed
		}
		p.localsRoute, p.localsDiags = route, ds // app.go follows the route field
	}
	// cmd/server can appear or go away.
	p.ServerEntry, p.GeneratedServer = p.Config.ServerEntry(p.Root)

	discovered, err := Discover(p.Root)
	if err != nil {
		return nil, false, err
	}
	// isChanged reports whether a discovered file is at or under a changed
	// path.
	isChanged := func(file string) bool {
		for _, c := range changed {
			if file == c || strings.HasPrefix(file, c+"/") {
				return true
			}
		}
		return false
	}
	byPath := make(map[string]*File, len(p.Files))
	for _, f := range p.Files {
		if !f.IsMD() {
			byPath[f.Path] = f
		}
	}
	found := make(map[string]bool, len(discovered))
	dirty := map[string]bool{} // .orbit and Markdown files with a new source
	mdSet := false             // a Markdown file was added or removed
	var orbit []*File
	for _, file := range discovered {
		found[file] = true
		md := compiler.IsMarkdown(file)
		var old *File
		var oldSrc string
		known := false
		if md {
			oldSrc, known = p.markdown[file]
		} else if old = byPath[file]; old != nil {
			oldSrc, known = old.Src, true
		}
		if known && !isChanged(file) {
			if old != nil {
				orbit = append(orbit, old)
			}
			continue
		}
		data, err := os.ReadFile(filepath.Join(p.Root, filepath.FromSlash(file)))
		if err != nil {
			return nil, false, err
		}
		src := string(data)
		switch {
		case md:
			if !known {
				p.mdPaths = append(p.mdPaths, file)
				mdSet = true
			}
			if !known || src != oldSrc {
				p.markdown[file] = src
				dirty[file] = true
			}
		case old != nil:
			if src != oldSrc {
				old.Src = src
				dirty[file] = true
			}
			orbit = append(orbit, old)
		default:
			f, err := NewFile(file, src)
			if err != nil {
				return nil, false, err // Discover only returns clean paths under src/
			}
			orbit = append(orbit, f)
			dirty[file] = true
		}
	}
	for file := range p.markdown {
		if !found[file] {
			delete(p.markdown, file)
			mdSet = true
		}
	}
	if mdSet {
		p.mdPaths = p.mdPaths[:0]
		for _, file := range discovered {
			if compiler.IsMarkdown(file) {
				p.mdPaths = append(p.mdPaths, file)
			}
		}
	}
	// A paths alias with several targets resolves to the first existing
	// one: when files come or go, the alias imports resolve again.
	if p.aliases.MultiTarget() && (mdSet || len(orbit) != len(byPath) || slices.ContainsFunc(orbit, func(f *File) bool { return byPath[f.Path] == nil })) {
		for _, f := range orbit {
			if hasAliasImport(f) {
				dirty[f.Path] = true
			}
		}
	}
	// orbit is in discovered (sorted) order and holds no removed file.
	return p, p.frontChanged(orbit, dirty, mdSet), nil
}

// relPath returns name relative to root, "/"-separated and clean, or false
// when it's outside root.
func relPath(root, name string) (string, bool) {
	if filepath.IsAbs(name) {
		r, err := filepath.Rel(root, name)
		if err != nil {
			return "", false
		}
		name = r
	}
	rel := path.Clean(filepath.ToSlash(name))
	if rel == ".." || strings.HasPrefix(rel, "../") || path.IsAbs(rel) {
		return "", false
	}
	return rel, true
}

// frontChanged is Front after an edit (Reload): orbit are the project's
// .orbit Files now, sorted, and dirty the .orbit and Markdown files whose
// source is new; mdSet is set when Markdown files were added or removed,
// which changes how every Markdown file's links resolve.
func (p *Project) frontChanged(orbit []*File, dirty map[string]bool, mdSet bool) bool {
	var reparse []*File
	for _, f := range orbit {
		if dirty[f.Path] || f.AST == nil {
			reparse = append(reparse, f)
		}
	}
	parallel(reparse, p.parse)

	oldMD := map[string]*File{}
	for _, f := range p.Files {
		if f.MD != nil {
			oldMD[f.Path] = f
		}
	}
	p.Files = orbit
	var mdFiles, mdParse []*File
	for _, file := range p.mdActive() {
		if f := oldMD[file]; f != nil && !mdSet && !dirty[file] {
			mdFiles = append(mdFiles, f)
			continue
		}
		f, err := NewFile(file, p.markdown[file])
		if err != nil {
			continue // Discover only returns clean paths under src/
		}
		mdFiles = append(mdFiles, f)
		mdParse = append(mdParse, f)
	}
	p.parseMarkdown(mdParse)
	if len(mdFiles) > 0 {
		p.Files = append(p.Files, mdFiles...)
		sortFiles(p.Files)
	}

	// Index entries: a re-parsed file's entry may change; a file that went
	// away (or a Markdown file no longer compiled) loses its entry. Either
	// way, the files that import it regenerate.
	regen := map[*File]bool{}
	infoChanged := map[string]bool{}
	current := make(map[string]bool, len(p.Files))
	for _, f := range p.Files {
		current[f.Path] = true
	}
	for file := range p.Index {
		if !current[file] {
			delete(p.Index, file)
			infoChanged[file] = true
		}
	}
	for _, list := range [][]*File{reparse, mdParse} {
		for _, f := range list {
			info := p.info(f)
			if old := p.Index[f.Path]; old == nil || !reflect.DeepEqual(old, info) {
				infoChanged[f.Path] = true
			}
			p.Index[f.Path] = info
			regen[f] = true
		}
	}
	var gen []*File
	for _, f := range p.Files {
		if !regen[f] {
			for _, im := range f.Analysis.OrbitImports {
				if infoChanged[im.Resolved] {
					regen[f] = true
					break
				}
			}
		}
		if regen[f] {
			gen = append(gen, f)
		} else {
			// As Front leaves it: Typecheck sets these.
			f.Output, f.Typed, f.Writes = f.Pass1, false, nil
		}
	}
	parallel(gen, p.pass1)
	p.mdDiags = checkLinks(p.Files)
	p.collectDiagnostics()
	return HasErrors(p.Diagnostics)
}
