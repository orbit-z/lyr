package lsp

import (
	"bytes"
	"errors"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/orbit-z/orbit/internal/compiler"
	"github.com/orbit-z/orbit/internal/config"
	"github.com/orbit-z/orbit/internal/project"
)

// The workspace (PLAN.md §9 Startup and Open documents): every .orbit file
// of the project parsed in tolerant mode and generated in memory, the
// editor's open buffers, and the generated files as gopls sees them. The
// language server never writes orbit_gen/: a generated file whose disk
// content differs from the in-memory one is sent to gopls as an overlay.
// All fields are guarded by Server.mu.

// workspace is one Orbit project.
type workspace struct {
	root   string // absolute module root
	module string
	// goVersion is go.mod's go directive ("1.27"), "" if missing.
	goVersion string
	proj      *project.Project
	byPath    map[string]*project.File
	// lines indexes each .orbit file's current source (File.Src).
	lines map[string]*lineIndex
	// docs are the .orbit files open in the editor, by project path.
	docs map[string]*document
	// gen are the generated files by module-relative path.
	gen map[string]*genFile
	// genMissing is set when orbit_gen/ didn't exist at startup.
	genMissing bool
	// gopls receives overlay notifications once it is running; nil
	// before, and when Go features are disabled.
	gopls *Conn
	// goplsDiags are gopls's latest diagnostics per generated file.
	goplsDiags map[string]goplsDiags
	// published is the last diagnostics JSON published per .orbit path.
	published map[string]string
	// scratch are the scratch files open in gopls, by module-relative
	// path (complete.go).
	scratch map[string]*scratchFile
	// rev counts changes to the generated files: a rename maps gopls's
	// edits only if no file changed since it asked (rename.go).
	rev int
	// mdTyped are the latest typed check's diagnostics of Markdown files
	// (mdtyped.go), by project path.
	mdTyped map[string][]mdTypedDiag
	// mdEdit counts the changes to what the typed diagnostics of Markdown
	// files depend on (mdcheck.go): a typed check's Markdown result is
	// adopted only when none happened since its snapshot.
	mdEdit uint64
}

// document is an .orbit or Markdown file open in the editor.
type document struct {
	uri     string
	version int
	// md is set for a Markdown document, and text is its buffer (an
	// .orbit file's is its File.Src).
	md   bool
	text string
}

// genFile is one generated file: its content as gopls sees it (from the
// overlay when open, else from disk, which then holds the same bytes) and
// its source map.
type genFile struct {
	rel   string // module-relative path, orbit_gen/…/render.go
	abs   string
	uri   string
	orbit string // the .orbit file's project path
	decl  bool
	// content, lines and smap describe the in-memory generated file.
	content []byte
	lines   *lineIndex
	smap    *compiler.SourceMap
	// open is true while gopls has an overlay for it, at version.
	open    bool
	version int
	// tol is non-nil while gopls reads the file from disk although the
	// workspace generates it in tolerant mode: content, lines and smap are
	// then the typed output orbit generate wrote, which a typed check
	// found on disk (disk.go), and tol and tolMap are the tolerant output
	// it stands for. The file's first change switches it back to its
	// tolerant output, as an overlay.
	tol    []byte
	tolMap *compiler.SourceMap
}

// goplsDiags is one publishDiagnostics of a generated file.
type goplsDiags struct {
	version int // 0 when gopls sent none
	items   []map[string]jsonRaw
}

// findRoot returns the project root at or above dir: the nearest directory
// with a go.mod that also has a src/ directory.
func findRoot(dir string) (string, error) {
	for d := dir; ; {
		root, err := config.FindRoot(d)
		if err != nil {
			return "", err
		}
		if fi, err := os.Stat(filepath.Join(root, project.SrcDir)); err == nil && fi.IsDir() {
			return root, nil
		}
		parent := filepath.Dir(root)
		if parent == root {
			return "", errors.New("no Orbit project (a go.mod next to a src/ directory) at or above " + dir)
		}
		d = parent
	}
}

// loadWorkspace loads the project at root and generates every file in
// memory (tolerant mode).
func loadWorkspace(root string) (*workspace, error) {
	p, err := project.Load(root)
	if err != nil {
		return nil, err
	}
	p.Options.Mode = compiler.Tolerant
	p.Front()
	ws := &workspace{root: p.Root, module: p.Module, proj: p, lines: map[string]*lineIndex{},
		docs: map[string]*document{}, gen: map[string]*genFile{}, goplsDiags: map[string]goplsDiags{},
		published: map[string]string{}, scratch: map[string]*scratchFile{}, mdTyped: map[string][]mdTypedDiag{}}
	if mod, err := config.ReadGoMod(p.Root); err == nil {
		ws.goVersion = mod.Go
	}
	if _, err := os.Stat(filepath.Join(p.Root, compiler.GenDir)); errors.Is(err, os.ErrNotExist) {
		ws.genMissing = true
	}
	ws.reindex()
	ws.syncAll()
	return ws, nil
}

// reindex rebuilds byPath after Files changed.
func (ws *workspace) reindex() {
	ws.byPath = make(map[string]*project.File, len(ws.proj.Files))
	for _, f := range ws.proj.Files {
		ws.byPath[f.Path] = f
	}
}

// rel returns the module-relative "/" path of an absolute path, or "" if
// it's outside the module.
func (ws *workspace) rel(abs string) string {
	r, err := filepath.Rel(ws.root, abs)
	if err != nil || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) {
		return ""
	}
	return filepath.ToSlash(r)
}

// orbitPath returns the project path of an .orbit file URI, or "".
func (ws *workspace) orbitPath(uri string) string {
	r := ws.rel(uriToPath(uri))
	if !strings.HasPrefix(r, project.SrcDir+"/") || !strings.HasSuffix(r, ".orbit") {
		return ""
	}
	return r
}

// docPath returns the project path of an .orbit or Markdown file URI
// under src/, or "": the documents the server follows (PLAN.md §9).
func (ws *workspace) docPath(uri string) string {
	r := ws.rel(uriToPath(uri))
	if isOrbitSource(r) || isMarkdownSource(r) {
		return r
	}
	return ""
}

// orbitURI is the URI to report an .orbit file under: the editor's own
// when the file is open, so diagnostics land on its document.
func (ws *workspace) orbitURI(p string) string {
	if d := ws.docs[p]; d != nil {
		return d.uri
	}
	return pathToURI(filepath.Join(ws.root, filepath.FromSlash(p)))
}

// file returns the project file at p, its line index, or nils.
func (ws *workspace) file(p string) (*project.File, *lineIndex) {
	f := ws.byPath[p]
	if f == nil {
		return nil, nil
	}
	return f, ws.lines[p]
}

// genOf returns the decl and impl files of the .orbit file at p.
func (ws *workspace) genOf(p string) (decl, impl *genFile) {
	f := ws.byPath[p]
	if f == nil {
		return nil, nil
	}
	return ws.gen[f.Pkg.DeclFile()], ws.gen[f.Pkg.ImplFile()]
}

// genAt returns the generated file at an absolute path, or nil.
func (ws *workspace) genAt(abs string) *genFile {
	r := ws.rel(abs)
	if r == "" {
		return nil
	}
	return ws.gen[r]
}

// setSource replaces the source of the .orbit file at p (an editor buffer
// or the disk content), adding the file to the project if it's new, and
// regenerates what depends on it.
func (ws *workspace) setSource(p, src string) {
	if f := ws.byPath[p]; f != nil {
		if f.Src == src {
			return
		}
		f.Src = src
		ws.sync(ws.proj.Refresh([]string{p}))
		return
	}
	f, err := project.NewFile(p, src)
	if err != nil {
		return // not a clean path under src/: not part of the project
	}
	ws.proj.Files = append(ws.proj.Files, f)
	slices.SortFunc(ws.proj.Files, func(a, b *project.File) int { return strings.Compare(a.Path, b.Path) })
	ws.proj.Front()
	ws.reindex()
	ws.syncAll()
}

// remove drops the .orbit file at p from the project (deleted on disk).
func (ws *workspace) remove(p string) {
	if ws.byPath[p] == nil {
		return
	}
	ws.proj.Files = slices.DeleteFunc(ws.proj.Files, func(f *project.File) bool { return f.Path == p })
	ws.proj.Front()
	ws.reindex()
	ws.syncAll()
}

// reloadFromDisk makes the disk content the source of p (after the editor
// closed it, or a watched-file event), or removes the file if it's gone.
func (ws *workspace) reloadFromDisk(p string) {
	src, err := os.ReadFile(filepath.Join(ws.root, filepath.FromSlash(p)))
	if err != nil {
		ws.remove(p)
		return
	}
	ws.setSource(p, string(src))
}

// syncAll updates every generated file after Front, dropping those of
// removed files.
func (ws *workspace) syncAll() {
	keep := map[string]bool{}
	for _, f := range ws.proj.Files {
		keep[f.Pkg.DeclFile()], keep[f.Pkg.ImplFile()] = true, true
	}
	for rel, g := range ws.gen {
		if !keep[rel] {
			if g.open && ws.gopls != nil {
				ws.gopls.Notify("textDocument/didClose", map[string]any{"textDocument": map[string]any{"uri": g.uri}})
			}
			delete(ws.gen, rel)
			ws.rev++
			delete(ws.goplsDiags, rel)
		}
	}
	for p := range ws.lines {
		if ws.byPath[p] == nil {
			delete(ws.lines, p)
		}
	}
	ws.sync(ws.proj.Files)
}

// sync updates the generated files of files (just regenerated) and tells
// gopls about the ones whose content changed.
func (ws *workspace) sync(files []*project.File) {
	for _, f := range files {
		ws.lines[f.Path] = newLineIndex(f.Src)
		ws.syncGen(f, f.Pass1.Decl, true)
		ws.syncGen(f, f.Pass1.Impl, false)
	}
}

func (ws *workspace) syncGen(f *project.File, out *compiler.GenFile, decl bool) {
	g := ws.gen[out.Path]
	if g == nil {
		abs := filepath.Join(ws.root, filepath.FromSlash(out.Path))
		g = &genFile{rel: out.Path, abs: abs, uri: pathToURI(abs), orbit: f.Path, decl: decl}
		ws.gen[out.Path] = g
	} else if g.tol != nil {
		if bytes.Equal(g.tol, out.Content) {
			g.tolMap = out.Map // the same code: gopls keeps reading the disk
			return
		}
		g.tol, g.tolMap = nil, nil
		delete(ws.goplsDiags, g.rel) // they were about the typed content
	} else if bytes.Equal(g.content, out.Content) {
		g.smap = out.Map
		return
	}
	g.content, g.lines, g.smap = out.Content, newLineIndex(string(out.Content)), out.Map
	ws.rev++
	if ws.gopls == nil {
		return
	}
	ws.pushOverlay(g)
}

// pushOverlay sends g's content to gopls: a didChange of the open overlay,
// or a didOpen when the disk content differs.
func (ws *workspace) pushOverlay(g *genFile) {
	switch {
	case g.open:
		g.version++
		ws.gopls.Notify("textDocument/didChange", map[string]any{
			"textDocument":   map[string]any{"uri": g.uri, "version": g.version},
			"contentChanges": []any{map[string]any{"text": string(g.content)}},
		})
	case !g.diskMatches():
		g.open = true
		g.version++
		ws.gopls.Notify("textDocument/didOpen", map[string]any{
			"textDocument": map[string]any{"uri": g.uri, "languageId": "go", "version": g.version, "text": string(g.content)},
		})
	}
}

// diskMatches reports whether the file on disk holds g's content.
func (g *genFile) diskMatches() bool {
	b, err := os.ReadFile(g.abs)
	return err == nil && bytes.Equal(b, g.content)
}

// overlayOnlyCount returns the number of generated files with no file on
// disk: overlays whatever the typed check finds.
func (ws *workspace) overlayOnlyCount() int {
	n := 0
	for _, g := range ws.gen {
		if _, err := os.Stat(g.abs); err != nil {
			n++
		}
	}
	return n
}

// openOverlays is called once gopls runs: it sends every generated file
// that differs from disk as an overlay.
func (ws *workspace) openOverlays(c *Conn) {
	ws.gopls = c
	clear(ws.scratch)
	for _, rel := range slices.Sorted(maps.Keys(ws.gen)) {
		ws.pushOverlay(ws.gen[rel])
	}
}

// recheckDisk re-compares g with the disk after a watched-file event or a
// didClose: an overlay of a file the editor doesn't have open is closed
// once the disk matches it, and a file that now differs from disk gets an
// overlay.
func (ws *workspace) recheckDisk(g *genFile) {
	if ws.gopls == nil {
		return
	}
	match := g.diskMatches()
	switch {
	case g.tol != nil && !match:
		// The disk changed under the typed content gopls was reading:
		// back to the tolerant output, as an overlay.
		ws.useTolerant(g)
		ws.pushOverlay(g)
	case g.open && match && ws.docs[g.orbit] == nil:
		g.open = false
		ws.gopls.Notify("textDocument/didClose", map[string]any{"textDocument": map[string]any{"uri": g.uri}})
	case !g.open && !match:
		ws.pushOverlay(g)
	}
}

// isGenPath reports whether a module-relative path is in orbit_gen/.
func isGenPath(rel string) bool {
	return strings.HasPrefix(rel, compiler.GenDir+"/")
}

// isMarkdownSource reports whether a module-relative path is a Markdown
// file under src/.
func isMarkdownSource(rel string) bool {
	return strings.HasPrefix(rel, project.SrcDir+"/") && compiler.IsMarkdown(rel)
}

// reloadMarkdown makes the disk content of the Markdown file at p its
// source, or drops it if it's gone.
func (ws *workspace) reloadMarkdown(p string) bool {
	src, err := os.ReadFile(filepath.Join(ws.root, filepath.FromSlash(p)))
	return ws.setMarkdown(p, string(src), err != nil)
}

// setMarkdown replaces the source of the Markdown file at p (an editor
// buffer or the disk content), adding it when new, or removes it, and
// regenerates what depends on it: the Markdown front of that file, in
// memory (project.UpdateMarkdown; no gopls involved). It reports whether
// anything changed.
func (ws *workspace) setMarkdown(p, src string, removed bool) bool {
	files, ok := ws.proj.UpdateMarkdown(p, src, removed)
	if !ok {
		return false
	}
	same := len(ws.proj.Files) == len(ws.byPath)
	for _, f := range ws.proj.Files {
		same = same && ws.byPath[f.Path] != nil
	}
	ws.reindex()
	if same {
		ws.sync(files)
	} else {
		ws.syncAll()
	}
	return true
}

// isOrbitSource reports whether a module-relative path is an .orbit file
// under src/.
func isOrbitSource(rel string) bool {
	return strings.HasPrefix(rel, project.SrcDir+"/") && path.Ext(rel) == ".orbit"
}
