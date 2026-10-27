// Package tsconfig reads the import aliases of a project's tsconfig.json:
// compilerOptions.paths and baseUrl, with TypeScript's semantics, so .orbit
// and Markdown component imports can use them as in Astro (PLAN.md §2.2).
// esbuild reads the same file for scripts (§5).
package tsconfig

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// Aliases are the import aliases of a project: the merged paths and
// baseUrl of its tsconfig.json and the local files it extends.
type Aliases struct {
	// root is the absolute project root.
	root string
	// patterns are the paths entries, longest prefix first.
	patterns []pattern
	// baseURL is the absolute baseUrl, "" when no config sets one.
	baseURL string
	// Files are the project-relative, "/"-separated config files read
	// (tsconfig.json first): a change to one of them changes the aliases.
	Files []string
}

// pattern is one compilerOptions.paths entry.
type pattern struct {
	key            string
	prefix, suffix string
	star           bool
	// targets are absolute, "/"-separated, each with at most one "*".
	targets []string
}

// raw is what Load reads from one config file.
type raw struct {
	Extends         json.RawMessage `json:"extends"`
	CompilerOptions struct {
		BaseURL *string             `json:"baseUrl"`
		Paths   map[string][]string `json:"paths"`
	} `json:"compilerOptions"`
}

// Load reads root/tsconfig.json and the files it extends. A project
// without one, or with one that doesn't parse, has no aliases (bun check
// and esbuild report a broken file); only an I/O error other than a
// missing file is returned. extends follows local files (a relative or
// absolute path, ".json" added when missing) and packages under the
// root's node_modules (node_modules/<name>, as a file or a directory's
// tsconfig.json), without package.json "exports"; an extends that isn't
// found is ignored.
func Load(root string) (*Aliases, error) {
	a := &Aliases{root: filepath.ToSlash(root)}
	var basePaths string // the directory paths are relative to without baseUrl
	var paths map[string][]string
	seen := map[string]bool{}
	var read func(file string, depth int) error
	read = func(file string, depth int) error {
		if seen[file] || depth > 32 {
			return nil
		}
		seen[file] = true
		data, err := os.ReadFile(filepath.FromSlash(file))
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if rel, err := filepath.Rel(root, filepath.FromSlash(file)); err == nil {
			a.Files = append(a.Files, filepath.ToSlash(rel))
		}
		var r raw
		if json.Unmarshal(StripJSONC(data), &r) != nil {
			return nil
		}
		dir := path.Dir(file)
		// Bases first, so this file's options override theirs (an
		// extends array: later entries override earlier ones).
		for _, ext := range extendsList(r.Extends) {
			if base := a.findExtends(dir, ext); base != "" {
				if err := read(base, depth+1); err != nil {
					return err
				}
			}
		}
		if r.CompilerOptions.BaseURL != nil {
			a.baseURL = joinAbs(dir, *r.CompilerOptions.BaseURL)
		}
		if r.CompilerOptions.Paths != nil {
			paths, basePaths = r.CompilerOptions.Paths, dir
		}
		return nil
	}
	if err := read(path.Join(a.root, "tsconfig.json"), 0); err != nil {
		return nil, err
	}
	if a.baseURL != "" {
		basePaths = a.baseURL
	}
	for key, targets := range paths {
		pat := pattern{key: key}
		if strings.Count(key, "*") > 1 {
			continue // tsc rejects it
		}
		pat.prefix, pat.suffix, pat.star = strings.Cut(key, "*")
		for _, t := range targets {
			if strings.Count(t, "*") <= 1 {
				pat.targets = append(pat.targets, joinAbs(basePaths, t))
			}
		}
		if len(pat.targets) > 0 {
			a.patterns = append(a.patterns, pat)
		}
	}
	// Exact keys first, then the longest prefix, as TypeScript matches.
	sort.Slice(a.patterns, func(i, j int) bool {
		pi, pj := a.patterns[i], a.patterns[j]
		if pi.star != pj.star {
			return !pi.star
		}
		if len(pi.prefix) != len(pj.prefix) {
			return len(pi.prefix) > len(pj.prefix)
		}
		return pi.key < pj.key
	})
	return a, nil
}

// extendsList returns the extends value as a list (a string or, since
// TypeScript 5.0, an array).
func extendsList(m json.RawMessage) []string {
	if len(m) == 0 {
		return nil
	}
	var s string
	if json.Unmarshal(m, &s) == nil {
		return []string{s}
	}
	var list []string
	_ = json.Unmarshal(m, &list)
	return list
}

// findExtends returns the absolute path of an extends entry of a config
// in dir, or "".
func (a *Aliases) findExtends(dir, ext string) string {
	var cands []string
	switch {
	case ext == "":
		return ""
	case strings.HasPrefix(ext, "./") || strings.HasPrefix(ext, "../") || path.IsAbs(filepath.ToSlash(ext)) || filepath.IsAbs(ext):
		p := joinAbs(dir, ext)
		cands = []string{p, p + ".json"}
	default:
		p := path.Join(a.root, "node_modules", ext)
		cands = []string{p, p + ".json", path.Join(p, "tsconfig.json")}
	}
	for _, c := range cands {
		if fi, err := os.Stat(filepath.FromSlash(c)); err == nil && !fi.IsDir() {
			return c
		}
	}
	return ""
}

// joinAbs joins p to the absolute directory dir, unless p is absolute.
func joinAbs(dir, p string) string {
	p = filepath.ToSlash(p)
	if path.IsAbs(p) || filepath.IsAbs(filepath.FromSlash(p)) {
		return path.Clean(p)
	}
	return path.Join(dir, p)
}

// Empty reports whether there are no aliases (no paths and no baseUrl).
func (a *Aliases) Empty() bool { return a == nil || len(a.patterns) == 0 && a.baseURL == "" }

// MultiTarget reports whether a paths entry has several targets: which one
// an import resolves to then depends on which files exist.
func (a *Aliases) MultiTarget() bool {
	if a == nil {
		return false
	}
	for _, p := range a.patterns {
		if len(p.targets) > 1 {
			return true
		}
	}
	return false
}

// Resolve resolves a non-relative import path with the paths entries (the
// exact key, else the longest matching prefix) and then, when no entry
// matches, baseUrl. Of several targets, the first one for which exists
// reports true wins, else the first one. The result is project-relative
// and "/"-separated ("src/components/Card.orbit", or "../x" outside the
// project); ok is false when nothing matches. exists gets
// project-relative paths too; nil means every file exists.
func (a *Aliases) Resolve(spec string, exists func(string) bool) (string, bool) {
	if a == nil {
		return "", false
	}
	for _, p := range a.patterns {
		var mid string
		switch {
		case !p.star:
			if spec != p.key {
				continue
			}
		case len(spec) >= len(p.prefix)+len(p.suffix) && strings.HasPrefix(spec, p.prefix) && strings.HasSuffix(spec, p.suffix):
			mid = spec[len(p.prefix) : len(spec)-len(p.suffix)]
		default:
			continue
		}
		first := ""
		for i, t := range p.targets {
			r := a.rel(strings.Replace(t, "*", mid, 1))
			if i == 0 {
				first = r
			}
			if len(p.targets) == 1 || exists == nil || exists(r) {
				return r, true
			}
		}
		return first, true
	}
	if a.baseURL != "" {
		return a.rel(path.Join(a.baseURL, spec)), true
	}
	return "", false
}

// Spellings returns the alias import paths that resolve to the
// project-relative file target, shortest first; exists is as for Resolve.
func (a *Aliases) Spellings(target string, exists func(string) bool) []string {
	if a == nil {
		return nil
	}
	abs := path.Join(a.root, target)
	var out []string
	add := func(s string) {
		if r, ok := a.Resolve(s, exists); ok && r == target {
			out = append(out, s)
		}
	}
	for _, p := range a.patterns {
		for _, t := range p.targets {
			tp, ts, star := strings.Cut(t, "*")
			switch {
			case !star || !p.star:
				if t == abs {
					add(p.key)
				}
			case len(abs) >= len(tp)+len(ts) && strings.HasPrefix(abs, tp) && strings.HasSuffix(abs, ts):
				add(p.prefix + abs[len(tp):len(abs)-len(ts)] + p.suffix)
			}
		}
	}
	if a.baseURL != "" && strings.HasPrefix(abs, a.baseURL+"/") {
		add(abs[len(a.baseURL)+1:])
	}
	sort.SliceStable(out, func(i, j int) bool { return len(out[i]) < len(out[j]) })
	return compactStrings(out)
}

// Prefixes returns the literal prefixes of the paths keys ("@/" for
// "@/*"), longest first.
func (a *Aliases) Prefixes() []string {
	if a == nil {
		return nil
	}
	var out []string
	for _, p := range a.patterns {
		out = append(out, p.prefix)
	}
	return out
}

func compactStrings(s []string) []string {
	seen := map[string]bool{}
	out := s[:0]
	for _, x := range s {
		if !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	return out
}

// rel makes an absolute "/" path project-relative.
func (a *Aliases) rel(abs string) string {
	r, err := filepath.Rel(filepath.FromSlash(a.root), filepath.FromSlash(abs))
	if err != nil {
		return abs
	}
	return filepath.ToSlash(r)
}

// StripJSONC removes // and /* */ comments and trailing commas outside
// strings: tsconfig.json is JSON with comments, as tsc accepts it.
func StripJSONC(data []byte) []byte {
	out := make([]byte, 0, len(data))
	for i := 0; i < len(data); i++ {
		c := data[i]
		switch {
		case c == '"':
			j := i + 1
			for j < len(data) && data[j] != '"' {
				if data[j] == '\\' {
					j++
				}
				j++
			}
			j = min(j, len(data)-1)
			out = append(out, data[i:j+1]...)
			i = j
		case c == '/' && i+1 < len(data) && data[i+1] == '/':
			for i < len(data) && data[i] != '\n' {
				i++
			}
			if i < len(data) {
				out = append(out, '\n')
			}
		case c == '/' && i+1 < len(data) && data[i+1] == '*':
			end := strings.Index(string(data[i+2:]), "*/")
			if end < 0 {
				return out
			}
			i += end + 3
		case c == ']' || c == '}':
			// Drop a trailing comma before the closer.
			k := len(out) - 1
			for k >= 0 && (out[k] == ' ' || out[k] == '\t' || out[k] == '\n' || out[k] == '\r') {
				k--
			}
			if k >= 0 && out[k] == ',' {
				out = append(out[:k], out[k+1:]...)
			}
			out = append(out, c)
		default:
			out = append(out, c)
		}
	}
	return out
}
