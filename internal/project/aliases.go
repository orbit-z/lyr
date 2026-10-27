package project

import (
	"os"
	"path/filepath"
	"slices"

	"github.com/orbit-z/orbit/internal/compiler"
	"github.com/orbit-z/orbit/internal/tsconfig"
)

// Import aliases (PLAN.md §2.2): Load reads tsconfig.json's paths and
// baseUrl once; every file's analysis resolves its non-relative .orbit and
// Markdown imports (and a Markdown layout) through them.

// aliasResolver is the project's compiler.ImportResolver.
type aliasResolver struct {
	root    string
	aliases *tsconfig.Aliases
}

func (r aliasResolver) ResolveImport(spec string) (string, bool) {
	return r.aliases.Resolve(spec, r.exists)
}

// exists reports whether the project-relative file exists on disk: of a
// paths entry's several targets, the first existing one wins.
func (r aliasResolver) exists(rel string) bool {
	fi, err := os.Stat(filepath.Join(r.root, filepath.FromSlash(rel)))
	return err == nil && !fi.IsDir()
}

// loadAliases reads the project's tsconfig.json aliases.
func (p *Project) loadAliases() error {
	a, err := tsconfig.Load(p.Root)
	if err != nil {
		return err
	}
	p.aliases = a
	return nil
}

// importResolver is the resolver of every FileInfo, nil without aliases.
func (p *Project) importResolver() compiler.ImportResolver {
	if p.aliases.Empty() {
		return nil
	}
	return aliasResolver{root: p.Root, aliases: p.aliases}
}

// analyze analyzes the parsed .orbit file f.
func (p *Project) analyze(f *File) {
	f.Info.Aliases = p.importResolver()
	f.Analysis = compiler.Analyze(f.AST, f.Info)
}

// AliasSpellings returns the tsconfig.json paths alias import paths that
// resolve to the project-relative file target, shortest first (the
// language server completes them).
func (p *Project) AliasSpellings(target string) []string {
	if p.aliases.Empty() {
		return nil
	}
	return p.aliases.Spellings(target, aliasResolver{root: p.Root, aliases: p.aliases}.exists)
}

// AliasPrefixes returns the literal prefixes of the tsconfig.json paths
// keys ("@/" for "@/*").
func (p *Project) AliasPrefixes() []string { return p.aliases.Prefixes() }

// isTsconfig reports whether the project-relative path is the root
// tsconfig.json or a file it extends: a change to it re-resolves every
// import.
func (p *Project) isTsconfig(rel string) bool {
	return rel == "tsconfig.json" || p.aliases != nil && slices.Contains(p.aliases.Files, rel)
}

// IsTsconfig is isTsconfig, for the language server's file events.
func (p *Project) IsTsconfig(rel string) bool { return p.isTsconfig(rel) }

// ReloadAliases re-reads tsconfig.json's aliases; call Front afterwards.
func (p *Project) ReloadAliases() error { return p.loadAliases() }

// hasAliasImport reports whether f imports a component by alias.
func hasAliasImport(f *File) bool {
	if f.Analysis == nil {
		return false
	}
	for _, im := range f.Analysis.OrbitImports {
		if !compiler.IsRelativeImport(im.Path) {
			return true
		}
	}
	return false
}
