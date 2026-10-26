// Package scaffold writes the orbit new starter project (PLAN.md §6) from
// the template embedded in the CLI.
package scaffold

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"text/template"

	"github.com/orbit-z/orbit/internal/config"
)

// OrbitModule is the framework's module path.
const OrbitModule = "github.com/orbit-z/orbit"

// FallbackToolchain is the toolchain line written when the CLI wasn't built
// by a Go release (a devel toolchain): the framework's own go.mod
// toolchain. A test keeps it in sync with ../../go.mod.
const FallbackToolchain = "go1.27.3"

// ReplaceEnv names the environment variable that points orbit new at a
// local checkout of Orbit, for a replace directive.
const ReplaceEnv = "ORBIT_REPLACE"

// The template: files ending in .tmpl are text/template files, written
// without the suffix; the others are copied as they are.
//
//go:embed all:template
var templateFS embed.FS

// Options are the values written into the starter.
type Options struct {
	// Module is the module path of the new project.
	Module string
	// Toolchain is the go.mod toolchain line, such as go1.27.3; the
	// Dockerfile's golang image tag is the same version.
	Toolchain string
	// Version is the github.com/orbit-z/orbit version required.
	Version string
	// Replace, if set, is the local directory that replaces
	// github.com/orbit-z/orbit in go.mod.
	Replace string
}

// ErrNotEmpty is returned by Create when the directory has files.
var ErrNotEmpty = errors.New("directory exists and is not empty")

// Files returns the starter's files for o, by slash-separated path.
func Files(o Options) (map[string][]byte, error) {
	data := struct {
		Options
		GoImage string
	}{o, strings.TrimPrefix(o.Toolchain, "go")}
	if data.Replace != "" {
		data.Replace = modQuote(filepath.ToSlash(data.Replace))
	}
	files := map[string][]byte{}
	err := fs.WalkDir(templateFS, "template", func(name string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		src, err := templateFS.ReadFile(name)
		if err != nil {
			return err
		}
		rel := strings.TrimPrefix(name, "template/")
		if base, ok := strings.CutSuffix(rel, ".tmpl"); ok {
			t, err := template.New(path.Base(name)).Option("missingkey=error").Parse(string(src))
			if err != nil {
				return err
			}
			var b bytes.Buffer
			if err := t.Execute(&b, data); err != nil {
				return err
			}
			rel, src = base, b.Bytes()
		}
		files[rel] = src
		return nil
	})
	return files, err
}

// modQuote quotes a go.mod path when it needs it.
func modQuote(s string) string {
	if s == "" || strings.ContainsAny(s, " \t\"'`\\(){}") {
		return strconv.Quote(s)
	}
	return s
}

// Create writes the starter into dir, which must not exist or be empty. On
// an error after the first file, dir is left as it is, for inspection.
func Create(dir string, o Options) error {
	if err := CheckModulePath(o.Module); err != nil {
		return err
	}
	if err := CheckDir(dir); err != nil {
		return err
	}
	files, err := Files(o)
	if err != nil {
		return err
	}
	for rel, data := range files {
		name := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(name, data, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// CheckDir reports whether dir can receive the starter: it doesn't exist,
// or it's an empty directory.
func CheckDir(dir string) error {
	entries, err := os.ReadDir(dir)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil
	case err != nil:
		if fi, serr := os.Stat(dir); serr == nil && !fi.IsDir() {
			return fmt.Errorf("%s exists and is not a directory", dir)
		}
		return err
	case len(entries) > 0:
		return fmt.Errorf("%s: %w", dir, ErrNotEmpty)
	}
	return nil
}

// CheckModulePath checks the characters of a module path, as go mod init
// does (a subset of golang.org/x/mod/module.CheckPath that needs no domain:
// the main module's path may be a single element such as "blog").
func CheckModulePath(p string) error {
	bad := func(why string) error { return fmt.Errorf("malformed module path %q: %s", p, why) }
	if p == "" {
		return bad("empty")
	}
	for _, elem := range strings.Split(p, "/") {
		switch {
		case elem == "":
			return bad("empty path element")
		case elem == "." || elem == "..":
			return bad("invalid path element " + strconv.Quote(elem))
		case elem[0] == '.' || elem[len(elem)-1] == '.':
			return bad("leading or trailing dot in path element")
		case elem[0] == '-':
			return bad("leading dash in path element")
		}
		for _, r := range elem {
			ok := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("-._~+", r)
			if !ok {
				return bad(fmt.Sprintf("invalid char %q", r))
			}
		}
	}
	return nil
}

var releaseRE = regexp.MustCompile(`^go1\.\d+(\.\d+|(rc|beta)\d+)$`)

// Toolchain returns the toolchain line for the running CLI: the Go release
// it was built with, or FallbackToolchain for a devel toolchain.
func Toolchain() string { return toolchain(runtime.Version()) }

func toolchain(v string) string {
	if releaseRE.MatchString(v) {
		return v
	}
	return FallbackToolchain
}

// Source is where a new project gets github.com/orbit-z/orbit from.
type Source struct {
	Version string // the require version
	Replace string // a local checkout for a replace directive, or ""
}

// ResolveSource picks the Orbit version for the running CLI. replaceEnv
// is the value of $ORBIT_REPLACE:
//   - ORBIT_REPLACE set: a replace with that checkout (made absolute);
//   - a release build (v1.2.3): that version;
//   - otherwise (go run, go build in a checkout: (devel) or a
//     pseudo-version), a replace with the checkout the CLI was built from
//     when its sources are still there;
//   - otherwise a clean pseudo-version (go install …@commit) as it is.
func ResolveSource(replaceEnv string) (Source, error) {
	v, srcDir := buildVersion()
	return resolve(v, srcDir, replaceEnv)
}

// buildVersion returns the CLI's version of the Orbit module and the
// directory its sources were compiled from ("" if unknown, e.g. -trimpath).
func buildVersion() (version, srcDir string) {
	if bi, ok := debug.ReadBuildInfo(); ok {
		if bi.Main.Path == OrbitModule {
			version = bi.Main.Version
		}
		for _, d := range bi.Deps {
			if d.Path == OrbitModule {
				version = d.Version
				if d.Replace != nil && d.Replace.Version == "" {
					version = "(devel)"
				}
			}
		}
	}
	if _, file, _, ok := runtime.Caller(0); ok && filepath.IsAbs(file) {
		srcDir = filepath.Dir(filepath.Dir(filepath.Dir(file)))
	}
	return version, srcDir
}

var (
	semverRE = regexp.MustCompile(`^v\d+\.\d+\.\d+(-[0-9A-Za-z.-]+)?$`)
	pseudoRE = regexp.MustCompile(`\d{14}-[0-9a-f]{12}$`)
)

func resolve(version, srcDir, replaceEnv string) (Source, error) {
	if replaceEnv != "" {
		dir, err := filepath.Abs(replaceEnv)
		if err != nil {
			return Source{}, err
		}
		if !isOrbitCheckout(dir, false) {
			return Source{}, fmt.Errorf("%s=%s: not a checkout of %s (no go.mod with module %s)", ReplaceEnv, replaceEnv, OrbitModule, OrbitModule)
		}
		return Source{Version: "v0.0.0", Replace: dir}, nil
	}
	clean := semverRE.MatchString(version)
	if clean && !pseudoRE.MatchString(version) {
		return Source{Version: version}, nil
	}
	if srcDir != "" && isOrbitCheckout(srcDir, true) {
		return Source{Version: "v0.0.0", Replace: srcDir}, nil
	}
	if clean {
		return Source{Version: version}, nil
	}
	if version == "" {
		version = "unknown"
	}
	return Source{}, fmt.Errorf("this orbit CLI is a development build (version %s) and its sources weren't found; set %s=/path/to/orbit to use a local checkout of %s", version, ReplaceEnv, OrbitModule)
}

// isOrbitCheckout reports whether dir is the root of the Orbit module, and,
// with needGit, a version-control checkout (which a module cache copy
// never is).
func isOrbitCheckout(dir string, needGit bool) bool {
	if m, err := config.ReadModule(dir); err != nil || m != OrbitModule {
		return false
	}
	if needGit {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
			return false
		}
	}
	return true
}
