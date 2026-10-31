package tailwind

import (
	"bytes"
	"context"
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

// Pinned is the Tailwind CSS version Lyr names for the bun x fallback
// and in its install lines: raised with Lyr releases, at least two weeks
// old.
const Pinned = "4.3.3"

// Version is a Tailwind release.
type Version struct{ Major, Minor, Patch int }

func (v Version) String() string { return fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch) }

var versionRE = regexp.MustCompile(`tailwindcss v(\d+)\.(\d+)\.(\d+)`)

// ParseVersion reads the version in tailwindcss --help's first line
// ("≈ tailwindcss v4.3.3"); the CLI has no --version.
func ParseVersion(help string) (Version, bool) {
	m := versionRE.FindStringSubmatch(help)
	if m == nil {
		return Version{}, false
	}
	a, _ := strconv.Atoi(m[1])
	b, _ := strconv.Atoi(m[2])
	c, _ := strconv.Atoi(m[3])
	return Version{a, b, c}, true
}

// CLI is a Tailwind CSS v4 CLI to run: Bin with Args, then the build
// flags.
type CLI struct {
	Bin  string
	Args []string
	// Version is the CLI's version (the pinned one for bun x).
	Version Version
	// Via says how it was found: "node_modules", "PATH" or "bun x".
	Via string
	// Path is the tailwindcss file found (Bin, or the script Bin runs).
	Path string
	// NodePath is the node_modules directory that holds the CLI's own
	// packages, tailwindcss among them, added to NODE_PATH for each run:
	// Tailwind resolves @import "tailwindcss" from the stylesheet's
	// directory, where bun x's install (or a pnpm one) isn't found. ""
	// for the standalone binary, which has tailwindcss built in.
	NodePath string
}

// Describe is the CLI for humans: "tailwindcss 4.3.3 (/path)" or
// "Tailwind CSS 4.3.3 through bun x".
func (c CLI) Describe() string {
	if c.Via == "bun x" {
		return "Tailwind CSS " + c.Version.String() + " through bun x"
	}
	return "tailwindcss " + c.Version.String() + " (" + c.Path + ")"
}

// Env is how Find reaches the machine; DefaultEnv is the real one.
type Env struct {
	// LookPath finds a command on PATH.
	LookPath func(string) (string, error)
	// Help runs bin with args and --help, and returns its output.
	Help func(ctx context.Context, bin string, args []string) ([]byte, error)
	// Output runs bin with args and returns its standard output; Find uses
	// it to locate bun x's install. nil skips that (NodePath stays "").
	Output func(ctx context.Context, bin string, args []string) ([]byte, error)
	// GOOS and GOARCH select the release binary in the install lines.
	GOOS, GOARCH string
}

// DefaultEnv is the real environment.
func DefaultEnv() Env {
	return Env{LookPath: exec.LookPath, Help: runHelp, Output: runOutput, GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}
}

func runOutput(ctx context.Context, bin string, args []string) ([]byte, error) {
	// The first bun x run downloads the CLI.
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Env = append(os.Environ(), "NO_COLOR=1")
	return cmd.Output()
}

func runHelp(ctx context.Context, bin string, args []string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, append(append([]string(nil), args...), "--help")...)
	cmd.Env = append(os.Environ(), "NO_COLOR=1")
	var b bytes.Buffer
	cmd.Stdout, cmd.Stderr = &b, &b
	err := cmd.Run()
	return b.Bytes(), err
}

// NotFound is Find's error: no v4 CLI, with the other versions it saw and
// the problems of the candidates it tried.
type NotFound struct {
	Old      []string // "path is Tailwind CSS 3.4.17"
	Problems []string
}

func (e *NotFound) Error() string {
	if d := e.details(); d != "" {
		return "the Tailwind CSS CLI (v4) was not found; " + d
	}
	return "the Tailwind CSS CLI (v4) was not found"
}

// details are the versions and problems seen, "; "-separated.
func (e *NotFound) details() string {
	var s []string
	for _, o := range e.Old {
		s = append(s, o+", and Lyr needs v4")
	}
	return strings.Join(append(s, e.Problems...), "; ")
}

// Find looks for the CLI in order (PLAN.md §5d): the project's
// node_modules/.bin/tailwindcss (run with Node, else Bun), tailwindcss on
// PATH, then bun x with the pinned @tailwindcss/cli. A CLI that isn't v4
// is skipped and reported in the NotFound error if nothing else works.
func Find(ctx context.Context, env Env, root string) (CLI, error) {
	nf := &NotFound{}
	try := func(c CLI) (CLI, bool) {
		out, err := env.Help(ctx, c.Bin, c.Args)
		v, ok := ParseVersion(string(out))
		if !ok {
			msg := strings.TrimSpace(firstLine(string(out)))
			if err != nil {
				nf.Problems = append(nf.Problems, fmt.Sprintf("%s --help: %v %s", c.Path, err, msg))
			} else {
				nf.Problems = append(nf.Problems, fmt.Sprintf("%s --help printed %q", c.Path, msg))
			}
			return CLI{}, false
		}
		if v.Major != 4 {
			nf.Old = append(nf.Old, fmt.Sprintf("%s is Tailwind CSS %s", c.Path, v))
			return CLI{}, false
		}
		c.Version = v
		return c, true
	}
	if root != "" {
		name := "tailwindcss"
		if env.GOOS == "windows" {
			name += ".exe"
		}
		p := filepath.Join(root, "node_modules", ".bin", name)
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			var c CLI
			switch {
			case !isNodeScript(p):
				c = CLI{Bin: p}
			default:
				if _, err := env.LookPath("node"); err == nil {
					c = CLI{Bin: p}
				} else if bun, err := env.LookPath("bun"); err == nil {
					c = CLI{Bin: bun, Args: []string{p}}
				}
			}
			if c.Bin != "" {
				c.Via, c.Path = "node_modules", p
				c.NodePath = packageNodeModules(p)
				if c, ok := try(c); ok {
					return c, nil
				}
			}
		}
	}
	if p, err := env.LookPath("tailwindcss"); err == nil {
		if c, ok := try(CLI{Bin: p, Via: "PATH", Path: p}); ok {
			return c, nil
		}
	}
	if bun, err := env.LookPath("bun"); err == nil {
		v, _ := ParseVersion("tailwindcss v" + Pinned)
		c := CLI{Bin: bun, Args: []string{"x", "--package", "@tailwindcss/cli@" + Pinned, "tailwindcss"}, Version: v, Via: "bun x", Path: bun}
		// bun x installs the CLI and its dependencies outside the project
		// (~/.bun/install/cache/.bunx-…/node_modules) and puts that .bin
		// first on the command's PATH: ask Bun where tailwindcss is there.
		if env.Output != nil {
			out, err := env.Output(ctx, bun, []string{"x", "--package", "@tailwindcss/cli@" + Pinned, "bun", "-e", `process.stdout.write(Bun.which("tailwindcss") ?? "")`})
			if p := strings.TrimSpace(string(out)); err == nil && p != "" {
				c.NodePath = packageNodeModules(p)
			}
		}
		return c, nil
	}
	return CLI{}, nf
}

// packageNodeModules returns the node_modules directory that the
// tailwindcss bin at bin belongs to, "" when it has no tailwindcss
// package: the nearest node_modules above the file the bin links to
// (node_modules/@tailwindcss/cli/dist/index.mjs; with pnpm,
// node_modules/.pnpm/@tailwindcss+cli@…/node_modules), where the package
// manager put the CLI's dependencies.
func packageNodeModules(bin string) string {
	real, err := filepath.EvalSymlinks(bin)
	if err != nil {
		real = bin
	}
	for d := filepath.Dir(real); filepath.Dir(d) != d; d = filepath.Dir(d) {
		if filepath.Base(d) != "node_modules" {
			continue
		}
		if fi, err := os.Stat(filepath.Join(d, "tailwindcss", "package.json")); err == nil && !fi.IsDir() {
			return d
		}
		return ""
	}
	return ""
}

// isNodeScript reports whether path is a script run by Node (#!… node),
// like @tailwindcss/cli's bin.
func isNodeScript(path string) bool {
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

// ReleaseAsset is the standalone CLI's release file for a platform
// ("tailwindcss-linux-x64"), "" when there is none.
func ReleaseAsset(goos, goarch string) string {
	arch := map[string]string{"amd64": "x64", "arm64": "arm64"}[goarch]
	if arch == "" {
		return ""
	}
	switch goos {
	case "linux":
		return "tailwindcss-linux-" + arch
	case "darwin":
		return "tailwindcss-macos-" + arch
	case "windows":
		if arch == "x64" {
			return "tailwindcss-windows-x64.exe"
		}
	}
	return ""
}

// ReleaseURL is the download URL of a release asset.
func ReleaseURL(asset string) string {
	return "https://github.com/tailwindlabs/tailwindcss/releases/download/v" + Pinned + "/" + asset
}

// Install is the fix line for a missing CLI on goos/goarch.
func Install(goos, goarch string) string {
	var lines []string
	if a := ReleaseAsset(goos, goarch); a != "" && goos != "windows" {
		lines = append(lines, "curl -sLo ~/.local/bin/tailwindcss "+ReleaseURL(a)+" && chmod +x ~/.local/bin/tailwindcss")
	} else if a != "" {
		lines = append(lines, "download "+ReleaseURL(a)+" as tailwindcss.exe into a directory on PATH")
	}
	if goos == "darwin" || goos == "linux" {
		lines = append(lines, "brew install tailwindcss")
	}
	lines = append(lines, "bun add -d @tailwindcss/cli@"+Pinned)
	return strings.Join(lines, ", or ")
}

func firstLine(s string) string {
	l, _, _ := strings.Cut(s, "\n")
	return l
}
