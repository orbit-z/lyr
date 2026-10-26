// Package project is the layer around the single-file compiler (milestone
// 5): it loads orbit.json and go.mod, discovers the .orbit files, derives
// page routes, runs the project-level checks and the two-pass compiler
// pipeline over every file, generates the extra packages (orbit_gen/app,
// orbit_gen/manifest, the default server entry) and writes orbit_gen/.
//
// The stages are separate so orbit dev can keep a resident
// compiler.Checker and re-run only what an edit affects (PLAN.md §3.3 Dev):
// Load (config, discovery, sources), Front (parse, analyze, index, checks,
// pass 1), Typecheck (go list -export, go/types, pass 2), GenFiles and
// WriteGenerated. Generator.Run chains them for orbit generate and orbit
// build.
package project

import (
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/orbit-z/orbit/internal/assets"
	"github.com/orbit-z/orbit/internal/compiler"
	"github.com/orbit-z/orbit/internal/config"
	"github.com/orbit-z/orbit/internal/markdown"
)

// Project is a loaded Orbit project.
type Project struct {
	// Root is the absolute module root (the directory with go.mod).
	Root   string
	Module string
	Config *config.Config
	// ServerEntry is the server entry package ("./cmd/server"), and
	// GeneratedServer is true when it is the generated default
	// (orbit_gen/cmd/server, PLAN.md §5b).
	ServerEntry     string
	GeneratedServer bool
	// Options are the codegen options for every file.
	Options compiler.Options
	// Files are the .orbit files, sorted by path.
	Files []*File
	// Index is the component index built from every file.
	Index compiler.ComponentIndex
	// Diagnostics are every parse, analysis, project and pass-1 codegen
	// diagnostic, sorted (set by Front).
	Diagnostics []Diagnostic
	// Assets is the bundle of every page's scripts and styles (set by
	// Bundle); nil until then, and the generated manifest is then empty.
	Assets *assets.Result
	// localsDiags are the problems with src/locals found by Load; Front
	// reports them.
	localsDiags []Diagnostic
	// markdown holds the source of every Markdown file under src/, by path,
	// and mdPaths their sorted paths (set by Load). Front compiles the
	// pages and the imported ones (PLAN.md §2.4); mdDiags are its link
	// warnings.
	markdown map[string]string
	mdPaths  []string
	mdDiags  []Diagnostic
}

// File is one .orbit or Markdown file of the project, through the
// pipeline.
type File struct {
	// Path is project-relative, "/"-separated: "src/components/Card.orbit".
	Path string
	// MD is the parsed Markdown file, for a Markdown file (set by Front);
	// AST and Analysis are then stand-ins (an empty template, the layout as the
	// only import).
	MD   *markdown.Doc
	Info compiler.FileInfo
	Pkg  compiler.GenPackage
	Src  string
	AST  *compiler.File
	// Analysis, Input and Pass1 are set by Front.
	Analysis *compiler.Analysis
	Input    *compiler.Input
	Pass1    *compiler.Output
	// Output is the final output: pass 2 when Typed, else Pass1 (set by
	// Typecheck; Front sets it to Pass1).
	Output *compiler.Output
	Typed  bool
	// Writes are the typed writes chosen for the file (set by Typecheck).
	Writes compiler.TypedWrites
	// callList caches compiler.ComponentCalls of AST (calls); Front
	// resets it.
	callList  []compiler.ComponentCall
	callsDone bool
}

// Load reads the project at root: orbit.json, the module path from go.mod,
// the server entry, the typed locals (src/locals, PLAN.md §5b), and every .orbit file's source. It returns an error for
// problems that stop everything (no go.mod, an invalid orbit.json, no src/
// directory, unreadable files); problems in .orbit files are diagnostics,
// reported by Front.
func Load(root string) (*Project, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	module, err := config.ReadModule(root)
	if err != nil {
		return nil, err
	}
	cfg, err := config.Load(root)
	if err != nil {
		return nil, err
	}
	paths, err := Discover(root)
	if err != nil {
		return nil, err
	}
	p := &Project{
		Root:    root,
		Module:  module,
		Config:  cfg,
		Options: compiler.Options{Module: module, Mode: compiler.Strict, CompressHTML: cfg.CompressHTML},
	}
	p.ServerEntry, p.GeneratedServer = cfg.ServerEntry(root)
	p.Options.Locals, p.localsDiags = detectLocals(root, module)
	p.markdown = map[string]string{}
	srcs := make([][]byte, len(paths))
	errs := make([]error, len(paths))
	parallelN(len(paths), func(i int) {
		srcs[i], errs[i] = os.ReadFile(filepath.Join(root, filepath.FromSlash(paths[i])))
	})
	for i, path := range paths {
		if errs[i] != nil {
			return nil, errs[i]
		}
		src := srcs[i]
		if compiler.IsMarkdown(path) {
			p.markdown[path] = string(src)
			p.mdPaths = append(p.mdPaths, path)
			continue
		}
		f, err := NewFile(path, string(src))
		if err != nil {
			return nil, err // Discover only returns clean paths under src/
		}
		p.Files = append(p.Files, f)
	}
	return p, nil
}

// NewFile returns the File for the .orbit file at the project-relative path
// with source src, not yet through Front. The error is for a path that isn't
// a clean .orbit path under src/.
func NewFile(path, src string) (*File, error) {
	role, route := RouteFor(path)
	f := &File{Path: path, Info: compiler.FileInfo{Path: path, Role: role, Route: route}, Src: src}
	var err error
	f.Pkg, err = compiler.PackageFor(path)
	if err != nil {
		return nil, err
	}
	return f, nil
}

// Front runs the pipeline up to pass 1 on every file: parse (Strict),
// Analyze, the component index, the project checks (routes, package
// collisions, Props import cycles, src/locals) and pass-1 Generate. It sets
// p.Diagnostics, and reports whether there are errors.
func (p *Project) Front() bool { return p.front(nil) }

// front is Front. parsed, when set, is called once every .orbit file is
// parsed and analyzed, before pass 1: Generator starts go list -export
// there, from the files' Go imports (PLAN.md §3.3 Engine).
func (p *Project) front(parsed func()) bool {
	// The Markdown Files of an earlier Front are rebuilt: which ones are
	// compiled depends on the .orbit imports.
	p.Files = slices.DeleteFunc(slices.Clone(p.Files), (*File).IsMD)
	// Files are independent until the index, and the index is read-only
	// during pass 1, so both loops run in parallel.
	parallel(p.Files, p.parse)
	if parsed != nil {
		parsed()
	}
	if md := p.frontMarkdown(); len(md) > 0 {
		p.Files = append(p.Files, md...)
		sortFiles(p.Files)
	}
	p.Index = compiler.ComponentIndex{}
	for _, f := range p.Files {
		p.Index[f.Path] = p.info(f)
	}
	parallel(p.Files, p.pass1)
	p.mdDiags = checkLinks(p.Files)
	p.collectDiagnostics()
	return HasErrors(p.Diagnostics)
}

// parse parses and analyzes the .orbit file f.
func (p *Project) parse(f *File) {
	f.AST = compiler.Parse(f.Src, p.Options.Mode)
	f.Analysis = compiler.Analyze(f.AST, f.Info)
	f.callList, f.callsDone = nil, false
}

// info is f's component index entry.
func (p *Project) info(f *File) *compiler.ComponentInfo {
	if f.MD != nil {
		return markdown.Info(p.mdGenInput(f))
	}
	return compiler.NewComponentInfo(f.AST, f.Analysis)
}

// sortFiles sorts files by path, the order of Project.Files.
func sortFiles(files []*File) {
	slices.SortFunc(files, func(a, b *File) int { return strings.Compare(a.Path, b.Path) })
}

// collectDiagnostics sets p.Diagnostics from the project checks and every
// file's parse, analysis and pass-1 diagnostics.
func (p *Project) collectDiagnostics() {
	ds := slices.Clone(p.localsDiags)
	ds = append(ds, p.mdDiags...)
	ds = append(ds, checkFiles(p.Files)...)
	ds = append(ds, checkPropsCycles(p.Files)...)
	for _, f := range p.Files {
		for _, list := range [][]compiler.Diagnostic{f.AST.Diagnostics, f.Analysis.Diagnostics, f.Pass1.Diagnostics} {
			for _, d := range list {
				ds = append(ds, fromCompiler(f, d))
			}
		}
	}
	sortDiagnostics(ds)
	p.Diagnostics = ds
}

// Refresh is Front for an edit of the files at paths, whose Src the caller
// has replaced (the language server's unsaved buffers, PLAN.md §9): it
// re-parses and re-analyzes those files and updates their index entries,
// then regenerates (pass 1) them and every file that imports one whose
// entry changed in a way codegen depends on (Props fields, slots), and
// recomputes p.Diagnostics. It returns the regenerated files, in p.Files
// order. Every path must already be in p.Files; after adding or removing
// files, call Front instead.
func (p *Project) Refresh(paths []string) []*File {
	byPath := p.byPath()
	changed := map[string]bool{} // files whose index entry changed
	regen := map[string]bool{}
	for _, path := range paths {
		f := byPath[path]
		if f == nil || f.MD != nil {
			continue
		}
		f.AST = compiler.Parse(f.Src, p.Options.Mode)
		f.Analysis = compiler.Analyze(f.AST, f.Info)
		f.callList, f.callsDone = nil, false
		info := compiler.NewComponentInfo(f.AST, f.Analysis)
		if !sameInfo(p.Index[f.Path], info) {
			changed[f.Path] = true
		}
		p.Index[f.Path] = info
		regen[f.Path] = true
		// A new import of a Markdown file that isn't compiled yet: only
		// Front adds files.
		for _, im := range f.Analysis.OrbitImports {
			if _, ok := p.markdown[im.Resolved]; ok && byPath[im.Resolved] == nil {
				p.Front()
				return slices.Clone(p.Files)
			}
		}
	}
	if len(changed) > 0 {
		for _, f := range p.Files {
			for _, im := range f.Analysis.OrbitImports {
				if changed[im.Resolved] {
					regen[f.Path] = true
				}
			}
		}
	}
	var out []*File
	for _, f := range p.Files {
		if regen[f.Path] {
			out = append(out, f)
		}
	}
	parallel(out, p.pass1)
	p.collectDiagnostics()
	return out
}

// sameInfo reports whether two index entries of a file generate the same
// code in its importers: the same Props field names and shape, and the
// same slots. Positions and doc comments don't matter.
func sameInfo(a, b *compiler.ComponentInfo) bool {
	if a == nil || b == nil {
		return a == b
	}
	if a.Role != b.Role || a.Name != b.Name || a.ID != b.ID || a.Pkg != b.Pkg ||
		a.Props.Declared != b.Props.Declared || a.Props.Listable != b.Props.Listable ||
		!slices.Equal(a.Slots, b.Slots) || len(a.Props.Fields) != len(b.Props.Fields) {
		return false
	}
	for i, f := range a.Props.Fields {
		if g := b.Props.Fields[i]; f.Name != g.Name || f.Type != g.Type {
			return false
		}
	}
	return true
}

// parallel calls fn for every file, on up to GOMAXPROCS goroutines.
func parallel(files []*File, fn func(*File)) {
	parallelN(len(files), func(i int) { fn(files[i]) })
}

// parallelN calls fn(i) for every i in [0, n), on up to GOMAXPROCS
// goroutines that take the next i in turn (one goroutine, the caller's, for
// n ≤ 1 or GOMAXPROCS=1).
func parallelN(n int, fn func(int)) {
	workers := min(runtime.GOMAXPROCS(0), n)
	if workers <= 1 {
		for i := range n {
			fn(i)
		}
		return
	}
	var next atomic.Int64
	var wg sync.WaitGroup
	for range workers {
		wg.Go(func() {
			for i := int(next.Add(1)) - 1; i < n; i = int(next.Add(1)) - 1 {
				fn(i)
			}
		})
	}
	wg.Wait()
}

// ErrorNames is the table compiler.CleanGoOutput uses to rewrite generated
// names in go build / go vet output.
func (p *Project) ErrorNames() *compiler.ErrorNames {
	analyses := make([]*compiler.Analysis, 0, len(p.Files))
	for _, f := range p.Files {
		analyses = append(analyses, f.Analysis)
	}
	return compiler.NewErrorNames(p.Module, analyses)
}

// CleanGoOutput cleans go build / go vet output run from the project root:
// positions become src/…:L:C and generated names the user's (PLAN.md §3.3).
func (p *Project) CleanGoOutput(out []byte) []byte {
	return compiler.CleanGoOutput(out, p.ErrorNames())
}

// Clone returns a copy of p whose Files are new Files with the same paths
// and sources, not yet through Front, generated in mode. The language
// server uses it to compute, from its current buffers, the typed output
// orbit generate would write (PLAN.md §9 Startup step 2) without touching
// its own tolerant project.
func (p *Project) Clone(mode compiler.Mode) *Project {
	c := *p
	c.Options.Mode = mode
	c.Index, c.Diagnostics, c.Assets = nil, nil, nil
	c.localsDiags = slices.Clone(p.localsDiags)
	c.markdown, c.mdPaths, c.mdDiags = maps.Clone(p.markdown), slices.Clone(p.mdPaths), nil
	c.Files = nil
	for _, f := range p.Files {
		if !f.IsMD() { // Front recompiles the Markdown files from c.markdown
			c.Files = append(c.Files, &File{Path: f.Path, Info: f.Info, Pkg: f.Pkg, Src: f.Src})
		}
	}
	return &c
}
