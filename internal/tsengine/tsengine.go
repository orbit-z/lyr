// Package tsengine finds TypeScript 7's native compiler, whose language
// server (tsc --lsp --stdio) gives <script> blocks in .orbit files their
// TypeScript features in the editor (PLAN.md §9b "Finding the engine").
// orbit lsp starts it; orbit doctor reports it. Node is never run: an npm
// install's Node shim (typescript/bin/tsc) is resolved to the native
// binary of its platform package, as the package's own getExePath.js
// does.
package tsengine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Pinned is the TypeScript version Orbit names for the bun x fallback and
// in its docs: raised with Orbit releases, at least two weeks old.
const Pinned = "7.0.2"

// Min is the oldest TypeScript with a language server Orbit uses.
var Min = Version{7, 0, 2}

// Install is the fix line for a missing or too-old engine.
const Install = "bun add -g typescript@" + Pinned + " (or npm i -g typescript@" + Pinned + "), or set typescriptPath"

// Version is a TypeScript release (pre-release tags dropped).
type Version struct{ Major, Minor, Patch int }

func (v Version) String() string { return fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch) }

// Less reports whether v is older than w.
func (v Version) Less(w Version) bool {
	if v.Major != w.Major {
		return v.Major < w.Major
	}
	if v.Minor != w.Minor {
		return v.Minor < w.Minor
	}
	return v.Patch < w.Patch
}

var versionRE = regexp.MustCompile(`(\d+)\.(\d+)\.(\d+)`)

// ParseVersion reads the first x.y.z of s ("Version 7.0.2").
func ParseVersion(s string) (Version, bool) {
	m := versionRE.FindStringSubmatch(s)
	if m == nil {
		return Version{}, false
	}
	a, _ := strconv.Atoi(m[1])
	b, _ := strconv.Atoi(m[2])
	c, _ := strconv.Atoi(m[3])
	return Version{a, b, c}, true
}

// Engine is a TypeScript language server to run: Bin with Args, then
// "--lsp", "--stdio" (LSPArgs).
type Engine struct {
	Bin  string
	Args []string
	// Version is the TypeScript version (the pinned one for bun x).
	Version Version
	// Via says how it was found: "typescriptPath", "node_modules",
	// "PATH" or "bun x".
	Via string
}

// LSPArgs returns the arguments that start the language server.
func (e Engine) LSPArgs() []string {
	return append(append([]string(nil), e.Args...), "--lsp", "--stdio")
}

// Describe is the engine for humans: "tsc 7.0.2 (/path)" or
// "TypeScript 7.0.2 through bun x".
func (e Engine) Describe() string {
	if e.Via == "bun x" {
		return "TypeScript " + e.Version.String() + " through bun x"
	}
	return "tsc " + e.Version.String() + " (" + e.Bin + ")"
}

// Env is how Find reaches the machine; Default is the real one.
type Env struct {
	// LookPath finds a command on PATH.
	LookPath func(string) (string, error)
	// Version runs bin --version and returns its output.
	Version func(ctx context.Context, bin string) ([]byte, error)
	// GOOS and GOARCH select the platform package.
	GOOS, GOARCH string
}

// Default is the real environment.
func Default() Env {
	return Env{LookPath: exec.LookPath, Version: runVersion, GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}
}

func runVersion(ctx context.Context, bin string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, bin, "--version").Output()
}

// Options select the lookup.
type Options struct {
	// Setting is the typescriptPath initialization option: a native tsc,
	// an npm shim, or a typescript package directory. When set, it's the
	// only candidate.
	Setting string
	// Root is the project root, for its node_modules.
	Root string
	// BunX allows the bun x fallback (step 4).
	BunX bool
}

// TooOld is a TypeScript found but older than Min.
type TooOld struct {
	Path    string
	Version Version
}

// NotFound is Find's error: nothing usable, with the too-old engines it
// saw and the problems of the candidates it tried.
type NotFound struct {
	TooOld   []TooOld
	Problems []string
}

func (e *NotFound) Error() string {
	var b strings.Builder
	b.WriteString("TypeScript " + Min.String() + " or later not found")
	for _, o := range e.TooOld {
		fmt.Fprintf(&b, "; %s is TypeScript %s, which has no language server", o.Path, o.Version)
	}
	for _, p := range e.Problems {
		b.WriteString("; " + p)
	}
	return b.String()
}

// Find looks for the engine in order (PLAN.md §9b): the typescriptPath
// setting, the project's node_modules/typescript, tsc on PATH, then bun x
// with the pinned version. A candidate older than Min is skipped (and
// reported in the NotFound error if nothing else works); a bad setting is
// an error of its own.
func Find(ctx context.Context, env Env, opts Options) (Engine, error) {
	nf := &NotFound{}
	try := func(path, via string) (Engine, bool) {
		e, err := env.resolve(ctx, path)
		if err != nil {
			var old *TooOld
			if errors.As(err, &old) {
				nf.TooOld = append(nf.TooOld, *old)
			} else {
				nf.Problems = append(nf.Problems, err.Error())
			}
			return Engine{}, false
		}
		e.Via = via
		return e, true
	}
	if opts.Setting != "" {
		if e, ok := try(opts.Setting, "typescriptPath"); ok {
			return e, nil
		}
		return Engine{}, fmt.Errorf("typescriptPath %s: %w", opts.Setting, nf)
	}
	if opts.Root != "" {
		pkg := filepath.Join(opts.Root, "node_modules", "typescript")
		if _, err := os.Stat(filepath.Join(pkg, "package.json")); err == nil {
			if e, ok := try(pkg, "node_modules"); ok {
				return e, nil
			}
		}
	}
	if p, err := env.LookPath("tsc"); err == nil {
		if e, ok := try(p, "PATH"); ok {
			return e, nil
		}
	}
	if opts.BunX {
		if bun, err := env.LookPath("bun"); err == nil {
			return Engine{Bin: bun, Args: []string{"x", "--package", "typescript@" + Pinned, "tsc"}, Version: Min, Via: "bun x"}, nil
		}
	}
	return Engine{}, nf
}

func (o *TooOld) Error() string {
	return fmt.Sprintf("%s is TypeScript %s, older than %s", o.Path, o.Version, Min)
}

// resolve turns a candidate path into the native binary and its version.
func (env Env) resolve(ctx context.Context, path string) (Engine, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return Engine{}, err
	}
	if real, err := filepath.EvalSymlinks(path); err == nil {
		path = real
	}
	fi, err := os.Stat(path)
	if err != nil {
		return Engine{}, fmt.Errorf("%s: %v", path, err)
	}
	if fi.IsDir() {
		return env.fromPackage(path)
	}
	if isShim(path) {
		// typescript/bin/tsc: the package is two levels up.
		return env.fromPackage(filepath.Dir(filepath.Dir(path)))
	}
	out, err := env.Version(ctx, path)
	if err != nil {
		return Engine{}, fmt.Errorf("%s --version: %v", path, err)
	}
	v, ok := ParseVersion(string(out))
	if !ok {
		return Engine{}, fmt.Errorf("%s --version printed %q", path, strings.TrimSpace(string(out)))
	}
	if v.Less(Min) {
		return Engine{}, &TooOld{Path: path, Version: v}
	}
	return Engine{Bin: path, Version: v}, nil
}

// fromPackage resolves a typescript package directory to its platform
// package's native binary.
func (env Env) fromPackage(dir string) (Engine, error) {
	b, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		return Engine{}, fmt.Errorf("%s: not a typescript package (%v)", dir, err)
	}
	var pkg struct {
		Name, Version string
	}
	if err := json.Unmarshal(b, &pkg); err != nil || pkg.Name != "typescript" {
		return Engine{}, fmt.Errorf("%s: not a typescript package", dir)
	}
	v, ok := ParseVersion(pkg.Version)
	if !ok {
		return Engine{}, fmt.Errorf("%s: version %q", dir, pkg.Version)
	}
	if v.Less(Min) {
		return Engine{}, &TooOld{Path: dir, Version: v}
	}
	plat := "@typescript/typescript-" + nodePlatform(env.GOOS) + "-" + nodeArch(env.GOARCH)
	exe := "tsc"
	if env.GOOS == "windows" {
		exe += ".exe"
	}
	for _, cand := range []string{
		filepath.Join(dir, "node_modules", filepath.FromSlash(plat), "lib", exe),
		filepath.Join(filepath.Dir(dir), filepath.FromSlash(plat), "lib", exe), // a sibling in node_modules
	} {
		if fi, err := os.Stat(cand); err == nil && !fi.IsDir() {
			return Engine{Bin: cand, Version: v}, nil
		}
	}
	return Engine{}, fmt.Errorf("%s: TypeScript %s without its native binary (%s isn't installed)", dir, v, plat)
}

// isShim reports whether path is a script run by Node (#!… node).
func isShim(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	head := make([]byte, 128)
	n, _ := f.Read(head)
	head = head[:n]
	if !bytes.HasPrefix(head, []byte("#!")) {
		return false
	}
	line, _, _ := bytes.Cut(head, []byte("\n"))
	return bytes.Contains(line, []byte("node"))
}

// nodePlatform and nodeArch map Go's names to Node's process.platform and
// process.arch.
func nodePlatform(goos string) string {
	if goos == "windows" {
		return "win32"
	}
	return goos
}

func nodeArch(goarch string) string {
	switch goarch {
	case "amd64":
		return "x64"
	case "386":
		return "ia32"
	}
	return goarch
}
