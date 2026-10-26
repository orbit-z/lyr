package project

import (
	"go/ast"
	"go/build"
	"go/parser"
	"go/token"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/orbit-z/orbit/internal/compiler"
)

// LocalsDir is the optional package that declares the typed locals
// (PLAN.md §5b).
const LocalsDir = "src/locals"

// detectLocals looks for type Locals in the Go package at src/locals, with
// go/parser and no type checking (PLAN.md §5b "Typed locals"). It returns
// the package's import path when the package declares Locals, else "" (the
// generated code then uses orbit.NoLocals), plus the problems found:
//
//   - a package name other than locals;
//   - a Locals that's a defined type but not a struct (an alias is
//     accepted: its target can't be checked without types);
//   - an import of orbit_gen/app or an implementation package, directly or
//     through the module's own packages: the implementation package of
//     every component that uses locals imports src/locals, so that would be
//     an import cycle (rejected even before any component uses locals).
//
// A src/locals without Go files, or whose files don't declare Locals, is
// ignored, as Astro ignores an undeclared App.Locals.
func detectLocals(root, module string) (string, []Diagnostic) {
	dir := filepath.Join(root, filepath.FromSlash(LocalsDir))
	fset := token.NewFileSet()
	files := parseGoDir(fset, dir, 0)
	if len(files) == 0 {
		return "", nil
	}
	var ds []Diagnostic
	found := false
	for _, pf := range files {
		rel := LocalsDir + "/" + pf.name
		if name := pf.f.Name.Name; name != "locals" {
			ds = append(ds, goError(fset, rel, pf.f.Name.Pos(), "src/locals must be package locals, not package "+name))
		}
		for _, decl := range pf.f.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.TYPE {
				continue
			}
			for _, spec := range gd.Specs {
				ts := spec.(*ast.TypeSpec)
				if ts.Name.Name != "Locals" {
					continue
				}
				found = true
				if _, isStruct := ts.Type.(*ast.StructType); !isStruct && !ts.Assign.IsValid() {
					ds = append(ds, goError(fset, rel, ts.Name.Pos(), "Locals must be a struct type: type Locals struct { … }"))
				}
			}
		}
	}
	ds = append(ds, checkLocalsImports(fset, root, module, files)...)
	if !found || len(ds) > 0 {
		return "", ds
	}
	return module + "/" + LocalsDir, nil
}

// CheckLocals returns the problems with src/locals that Load finds (and
// Front reports): orbit doctor's config check runs it on its own.
func CheckLocals(root, module string) []Diagnostic {
	_, ds := detectLocals(root, module)
	return ds
}

type parsedGo struct {
	name string // file name
	f    *ast.File
}

// parseGoDir parses the Go files of dir that go build would compile for
// the host (no tests, build constraints applied), sorted by name; a missing
// or unreadable directory has none. Unparsable files are skipped: go build
// reports them.
func parseGoDir(fset *token.FileSet, dir string, mode parser.Mode) []parsedGo {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []parsedGo
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		if ok, err := build.Default.MatchFile(dir, name); err != nil || !ok {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, mode|parser.SkipObjectResolution)
		if f == nil || (err != nil && f.Name == nil) {
			continue
		}
		out = append(out, parsedGo{name: name, f: f})
	}
	return out
}

// checkLocalsImports reports the imports of src/locals that lead to
// orbit_gen/app or an implementation package, directly or through the
// module's own packages (read with parser.ImportsOnly). Each offending
// import of src/locals is reported once, with the shortest chain.
func checkLocalsImports(fset *token.FileSet, root, module string, files []parsedGo) []Diagnostic {
	self := module + "/" + LocalsDir
	// chains memoizes, per module package, the shortest chain of import
	// paths from it to a forbidden package (nil: none).
	chains := map[string][]string{}
	visiting := map[string]bool{}
	var chainFrom func(pkg string) []string
	chainFrom = func(pkg string) []string {
		if forbiddenFromLocals(module, pkg) {
			return []string{pkg}
		}
		if c, ok := chains[pkg]; ok {
			return c
		}
		if visiting[pkg] || pkg == self {
			return nil // an ordinary import cycle: go build reports it
		}
		visiting[pkg] = true
		var best []string
		dir := filepath.Join(root, filepath.FromSlash(strings.TrimPrefix(pkg, module+"/")))
		for _, pf := range parseGoDir(token.NewFileSet(), dir, parser.ImportsOnly) {
			for _, im := range pf.f.Imports {
				p, err := strconv.Unquote(im.Path.Value)
				if err != nil || !inModule(module, p) {
					continue
				}
				if c := chainFrom(p); c != nil && (best == nil || len(c)+1 < len(best)) {
					best = append([]string{pkg}, c...)
				}
			}
		}
		delete(visiting, pkg)
		chains[pkg] = best
		return best
	}
	var ds []Diagnostic
	seen := map[string]bool{}
	for _, pf := range files {
		for _, im := range pf.f.Imports {
			p, err := strconv.Unquote(im.Path.Value)
			if err != nil || !inModule(module, p) || seen[p] {
				continue
			}
			chain := chainFrom(p)
			if chain == nil {
				continue
			}
			seen[p] = true
			msg := "src/locals can't import " + chain[len(chain)-1] + ": pages that use locals import src/locals, so this would be an import cycle"
			if len(chain) > 1 {
				msg += " (" + strings.Join(append([]string{"src/locals"}, chain...), " → ") + ")"
			}
			ds = append(ds, goError(fset, LocalsDir+"/"+pf.name, im.Path.Pos(), msg))
		}
	}
	return ds
}

// inModule reports whether import path p is a package of module.
func inModule(module, p string) bool {
	return strings.HasPrefix(p, module+"/")
}

// forbiddenFromLocals reports whether src/locals can't import p: the
// generated packages that import src/locals, orbit_gen/app and the
// implementation packages (and so orbit_gen/cmd/server, which imports app).
func forbiddenFromLocals(module, p string) bool {
	rel, ok := strings.CutPrefix(p, module+"/"+compiler.GenDir+"/")
	if !ok {
		return false
	}
	first, _, _ := strings.Cut(rel, "/")
	return slices.Contains([]string{"app", "impl", "cmd"}, first)
}

// goError is an error at pos in the Go file rel (project-relative).
func goError(fset *token.FileSet, rel string, pos token.Pos, msg string) Diagnostic {
	p := fset.Position(pos)
	return Diagnostic{
		File:     path.Clean(rel),
		Pos:      compiler.Position{Line: p.Line, Col: p.Column},
		Severity: compiler.SeverityError,
		Message:  msg,
		off:      p.Offset,
	}
}
