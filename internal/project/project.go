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
	"os"
	"path/filepath"
	"runtime"
	"sync"

	"github.com/bertrandkhe/orbit/internal/compiler"
	"github.com/bertrandkhe/orbit/internal/config"
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
}

// File is one .orbit file of the project, through the pipeline.
type File struct {
	// Path is project-relative, "/"-separated: "src/components/Card.orbit".
	Path string
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
}

// Load reads the project at root: orbit.json, the module path from go.mod,
// the server entry, and every .orbit file's source. It returns an error for
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
	for _, path := range paths {
		src, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
		if err != nil {
			return nil, err
		}
		role, route := RouteFor(path)
		f := &File{Path: path, Info: compiler.FileInfo{Path: path, Role: role, Route: route}, Src: string(src)}
		f.Pkg, err = compiler.PackageFor(path)
		if err != nil {
			return nil, err // Discover only returns clean paths under src/
		}
		p.Files = append(p.Files, f)
	}
	return p, nil
}

// Front runs the pipeline up to pass 1 on every file: parse (Strict),
// Analyze, the component index, the project checks (routes, package
// collisions, Props import cycles) and pass-1 Generate. It sets
// p.Diagnostics, and reports whether there are errors.
func (p *Project) Front() bool {
	// Files are independent until the index, and the index is read-only
	// during pass 1, so both loops run in parallel.
	parallel(p.Files, func(f *File) {
		f.AST = compiler.Parse(f.Src, p.Options.Mode)
		f.Analysis = compiler.Analyze(f.AST, f.Info)
	})
	p.Index = compiler.ComponentIndex{}
	for _, f := range p.Files {
		p.Index[f.Path] = compiler.NewComponentInfo(f.AST, f.Analysis)
	}
	parallel(p.Files, func(f *File) {
		f.Input = &compiler.Input{File: f.AST, Analysis: f.Analysis, Index: p.Index, Options: p.Options}
		f.Pass1 = compiler.Generate(f.Input)
		f.Output, f.Typed, f.Writes = f.Pass1, false, nil
	})
	var ds []Diagnostic
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
	return HasErrors(ds)
}

// parallel calls fn for every file, on up to GOMAXPROCS goroutines.
func parallel(files []*File, fn func(*File)) {
	sem := make(chan struct{}, runtime.GOMAXPROCS(0))
	var wg sync.WaitGroup
	for _, f := range files {
		sem <- struct{}{}
		wg.Go(func() {
			defer func() { <-sem }()
			fn(f)
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
