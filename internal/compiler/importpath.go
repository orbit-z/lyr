package compiler

import (
	"fmt"
	"path"
	"strconv"
	"strings"
)

// ImportResolver resolves the component import paths that aren't relative:
// tsconfig.json compilerOptions.paths aliases (PLAN.md §2.2), such as
// "@/components/Card.orbit".
type ImportResolver interface {
	// ResolveImport returns the project-relative, "/"-separated path that
	// spec resolves to ("src/components/Card.orbit"), or false when no
	// alias matches it.
	ResolveImport(spec string) (string, bool)
}

// IsRelativeImport reports whether an import path is relative to the
// importing file: "./…", "../…", "." or "..".
func IsRelativeImport(p string) bool {
	return strings.HasPrefix(p, "./") || strings.HasPrefix(p, "../") || p == "." || p == ".."
}

// ResolveImportPath returns the project-relative path that the component
// import path p, written in the file at the project-relative path from,
// refers to: a relative path is joined to from's directory, any other path
// goes through aliases (nil: none). ok is false when no alias matches
// (NoAliasError is the error). The caller checks that the result is under
// src/.
func ResolveImportPath(from, p string, aliases ImportResolver) (string, bool) {
	if IsRelativeImport(p) {
		return path.Join(path.Dir(from), p), true
	}
	if aliases == nil {
		return "", false
	}
	r, ok := aliases.ResolveImport(p)
	if !ok {
		return "", false
	}
	return path.Clean(r), true
}

// NoAliasError is the error for a non-relative component import path that
// no tsconfig.json paths alias matches.
func NoAliasError(p string) string {
	msg := "no tsconfig.json paths alias matches " + strconv.Quote(p)
	seg, _, ok := strings.Cut(p, "/")
	switch {
	case seg == "":
		return msg + "; use a relative path, starting with ./ or ../"
	case !ok:
		return msg + "; use a relative path, such as " + strconv.Quote("./"+p)
	}
	target := "./src/*"
	if c := seg[0]; c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' {
		target = "./src/" + seg + "/*" // a directory name, not an alias sigil
	}
	return fmt.Sprintf("%s; use a relative path or add %q: [%q] to compilerOptions.paths", msg, seg+"/*", target)
}
