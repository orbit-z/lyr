package compiler

// The go/types pass for typed writes: part (C) of milestone 4 (PLAN.md §3.3
// "Typed writes" and "Engine"). This file is the contract; the
// implementation lives in typed*.go, golist*.go and cleanup*.go files owned
// by agent C. See DESIGN.md.

import (
	"context"
	gotoken "go/token"
	"go/types"
)

// Unit is one .orbit file going through the typed pass: its codegen input
// and the pass-1 output generated from it (Input.Typed nil).
type Unit struct {
	Input *Input
	Pass1 *Output
	// Retype, when set, replaces the impl check and the typed writes: it
	// is the typed pass of a Markdown file (PLAN.md §2.4), which converts
	// the frontmatter to its layout's Props field types. Once the unit's own
	// decl package checks, it is called with an importer for the generated
	// decl packages (checked from pass-1 source) and every other package;
	// it returns the pass-2 output and its diagnostics, or ok false to keep
	// Pass1 (the fallback: a layout decl package with errors). Input needs
	// only Analysis.Info and Options.
	Retype func(imp types.Importer) (out *Output, diags []Diagnostic, ok bool)
}

// ExportLoader provides type information for the packages that aren't
// generated: user packages, the runtime and std. The real one is
// GoListLoader; tests stub it (for example with importer.ForCompiler(fset,
// "source", nil), or a map of fake packages).
type ExportLoader interface {
	// Load prepares paths, the direct imports of the generated packages
	// that aren't under <module>/orbit_gen, and returns an importer for them
	// and their dependencies, recording positions in fset. The importer
	// must be safe for concurrent use. An error means no export data at
	// all; a single unreadable package is an error from Import instead.
	Load(ctx context.Context, fset *gotoken.FileSet, paths []string) (types.Importer, error)
}

// TypedUnit is the typed-pass result for one Unit.
type TypedUnit struct {
	// Output is the re-emitted pass-2 output (Generate with Writes), or
	// Pass1 unchanged when Checked is false.
	Output *Output
	Writes TypedWrites
	// Checked is false when the unit kept every fallback: its decl or impl
	// package has type errors (the later go build reports them), or export
	// data for one of its imports couldn't be read (with a warning).
	Checked bool
	// Diagnostics are the typed pass's errors at .orbit positions: the
	// server:defer Props that aren't JSON-serializable (PLAN.md §3.4).
	// Only a checked unit has them.
	Diagnostics []Diagnostic
}

// CheckResult is the result of a full or incremental check.
type CheckResult struct {
	// Units holds one entry per unit checked, in input order for Check; for
	// Update, the changed units first, then the importers re-checked
	// because a decl package changed.
	Units []TypedUnit
	// Warnings are not tied to an .orbit position, e.g. "orbit: can't read
	// export data for example.com/blog/internal/posts: …; using Expr
	// fallbacks".
	Warnings []string
}

// Checker type-checks generated packages from their pass-1 source and picks
// typed writes. It is resident in orbit dev (hot and incremental) and used
// once by orbit build/generate (TypedPass). Methods must not be called
// concurrently.
type Checker struct {
	loader ExportLoader
	// Implementation state (agent C): the shared FileSet, the importer from
	// the last Load, the memoized decl packages, the last units by path,
	// and the import graph between generated packages.
	state *tyState
	// pre is the load started by Preload, until a Check or Update takes
	// it.
	pre *tyPreload
	// generate re-emits pass 2; nil means Generate (tests stub it).
	generate func(*Input) *Output
}

// Preload starts loading export data for paths in the background (the
// go list -export of the next Check or Update), so that it overlaps the
// work before that call (PLAN.md §3.3 Engine): orbit generate starts it
// once the .orbit files are parsed, with their Go imports, and orbit dev
// when a user .go file changes, with the paths of the last load. The next
// Check, or Update that needs a load, uses it when every path it needs is
// among paths, and otherwise loads as usual: a wrong guess costs time,
// never correctness. Import errors for paths that aren't needed are never
// reported. A later Preload drops an unused one (Preload(nil) only drops
// it), and so does a Check or Update that doesn't use it.
func (c *Checker) Preload(paths []string) {
	c.preload(paths)
}

// LoadedPaths returns the paths of the last load, sorted; nil before the
// first Check.
func (c *Checker) LoadedPaths() []string {
	return c.loadedPaths()
}

// NewChecker returns a Checker that loads non-generated packages through
// loader.
func NewChecker(loader ExportLoader) *Checker {
	return &Checker{loader: loader}
}

// Check runs the full pass over every unit of the project (PLAN.md §3.3
// Engine, steps 2–4): one Load over the external imports, decl and impl
// packages type-checked from pass-1 source in parallel behind a memoized
// importer, then each unit re-emitted with Generate(Input + Writes). It
// replaces all state from earlier calls. The error is only for ctx
// cancellation; checking problems become fallbacks and warnings.
func (c *Checker) Check(ctx context.Context, units []Unit) (*CheckResult, error) {
	return c.check(ctx, units)
}

// Update re-checks after an edit (orbit dev, PLAN.md §3.3 Dev). changed are
// new pass-1 units for edited or added files; removed are project-relative
// .orbit paths of deleted files. A unit whose decl content is unchanged
// re-checks only its impl package; a changed decl package re-checks it and
// every unit importing it. Load reruns only after Invalidate. Requires an
// earlier Check. May be implemented after milestone 4 as a full Check over
// the cached units.
func (c *Checker) Update(ctx context.Context, changed []Unit, removed []string) (*CheckResult, error) {
	return c.update(ctx, changed, removed)
}

// Invalidate drops the loaded export data, so the next Update reloads it:
// call it when a user .go file, go.mod or orbit.json changes.
func (c *Checker) Invalidate() {
	c.invalidate()
}

// TypedPass is the one-shot pass of orbit build and orbit generate:
// NewChecker(loader).Check(ctx, units).
func TypedPass(ctx context.Context, units []Unit, loader ExportLoader) (*CheckResult, error) {
	return NewChecker(loader).Check(ctx, units)
}

// GoListLoader is the real ExportLoader: one "go list -export -e -json"
// over the paths (with the build's flags), read with
// importer.ForCompiler(fset, "gc", lookup).
type GoListLoader struct {
	// Dir is the module root the go command runs in.
	Dir string
	// BuildFlags are passed to go list as given (-tags, -mod, …); Env is
	// appended to os.Environ() (GOFLAGS, GOOS, …).
	BuildFlags []string
	Env        []string
}

// Load implements ExportLoader.
func (l *GoListLoader) Load(ctx context.Context, fset *gotoken.FileSet, paths []string) (types.Importer, error) {
	return l.load(ctx, fset, paths)
}
