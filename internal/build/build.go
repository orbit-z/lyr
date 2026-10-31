// Package build is the lyr build pipeline (PLAN.md §6): load the config,
// bundle the assets and generate lyr_gen/, stage dist/client from the
// bundles and public/, build the server entry with go build, and clean the
// go command's error output (§3.3).
//
// The new dist/client and dist/server are staged in a temporary directory
// under dist/ and moved into place only when every step succeeded, so a
// failed build leaves the previous dist/ as it was and never a half-written
// dist/server.
package build

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/orbit-z/lyr/internal/assets"
	"github.com/orbit-z/lyr/internal/compiler"
	"github.com/orbit-z/lyr/internal/config"
	"github.com/orbit-z/lyr/internal/project"
)

// Output paths, relative to the project root.
const (
	DistDir   = "dist"
	ClientDir = DistDir + "/client"
	ServerBin = DistDir + "/server"
)

// Options configure a build.
type Options struct {
	// Root is the module root.
	Root string
	// Embed is --embed: the server embeds dist/client (lyr.json's
	// "embed": true turns it on too).
	Embed bool
	// Env is appended to the process environment of the go commands
	// (GOFLAGS, GOOS, GOARCH, CGO_ENABLED, … pass through from the process
	// environment anyway).
	Env []string
}

// Result is the outcome of a build. A build failed when Failed reports
// true; its problems are in Generate's diagnostics, Public and GoOutput, in
// that order, and nothing in dist/ was changed.
type Result struct {
	// Generate is the generate step's result: its diagnostics and warnings.
	Generate *project.Result
	// Public are the errors about public/ files ("public/_lyr/x.js: …").
	Public []string
	// GoOutput is go build's output, cleaned (positions in .lyr files,
	// the user's names); set when go build printed anything.
	GoOutput []byte
	// GoFailed is set when go build failed.
	GoFailed bool
}

// Failed reports whether the build failed.
func (r *Result) Failed() bool {
	return (r.Generate != nil && r.Generate.HasErrors()) || len(r.Public) > 0 || r.GoFailed
}

// Run builds the project at o.Root into dist/. Problems in the user's files
// are in the Result; the error is for problems that stop the build early
// (no go.mod, an invalid lyr.json, I/O errors, a missing go command, ctx
// cancellation).
func Run(ctx context.Context, o Options) (*Result, error) {
	root, err := filepath.Abs(o.Root)
	if err != nil {
		return nil, err
	}
	cfg, err := config.Load(root)
	if err != nil {
		return nil, err
	}
	embed := o.Embed || cfg.Embed
	goflags, err := GoFlags(ctx, root, o.Env)
	if err != nil {
		return nil, fmt.Errorf("go env GOFLAGS: %w", err)
	}
	if hasFlag(goflags, "overlay") {
		return nil, ErrOverlayFlag
	}

	res := &Result{}
	g := &project.Generator{Root: root, Env: o.Env}
	res.Generate, err = g.Run(ctx)
	if err != nil {
		return nil, err
	}
	if res.Generate.HasErrors() {
		return res, nil
	}
	p := res.Generate.Project

	dist := filepath.Join(root, DistDir)
	if err := os.MkdirAll(dist, 0o755); err != nil {
		return nil, err
	}
	stage, err := os.MkdirTemp(dist, ".build-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(stage)

	client := filepath.Join(stage, "client")
	// Step 3's output: the bundles (generate ran esbuild for the manifest).
	if p.Assets != nil {
		if err := assets.WriteFiles(filepath.Join(client, AssetsDir), p.Assets.Files); err != nil {
			return nil, err
		}
	}
	res.Public, err = CopyPublic(filepath.Join(root, "public"), client)
	if err != nil {
		return nil, err
	}
	if len(res.Public) > 0 {
		return res, nil
	}

	// PLAN.md §3.5: the build flavor, overlaid on lyr_gen/.
	buildFiles, err := p.BuildFiles()
	if err != nil {
		return nil, err
	}
	if err := project.WriteBuildFiles(root, buildFiles); err != nil {
		return nil, err
	}
	flags := []string{"-overlay=" + compiler.OverlayFile}
	if embed {
		if err := writeStatic(root, client); err != nil {
			return nil, err
		}
		flags = append(flags, embedTagsFlag(goflags))
	}
	flags = append(flags, islandLDFlags(goflags))

	server := filepath.Join(stage, "server")
	out, err := goBuild(ctx, root, server, p.ServerEntry, flags, o.Env)
	if len(out) > 0 {
		res.GoOutput = CleanOutput(p, out)
	}
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && ctx.Err() == nil {
			res.GoFailed = true
			return res, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("go build: %w", err)
	}

	if err := os.RemoveAll(filepath.Join(root, filepath.FromSlash(ClientDir))); err != nil {
		return nil, err
	}
	if err := os.Rename(client, filepath.Join(root, filepath.FromSlash(ClientDir))); err != nil {
		return nil, err
	}
	if err := os.Rename(server, filepath.Join(root, filepath.FromSlash(ServerBin))); err != nil {
		return nil, err
	}
	return res, nil
}

// goBuild runs go build -o out [flags] entry in root and returns its
// combined output.
func goBuild(ctx context.Context, root, out, entry string, flags, env []string) ([]byte, error) {
	args := append(append([]string{"build", "-o", out}, flags...), entry)
	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), env...)
	var b bytes.Buffer
	cmd.Stdout, cmd.Stderr = &b, &b
	err := cmd.Run()
	return b.Bytes(), err
}

// dotSlash is Go's "./" in front of a position at the start of a line or
// after a space: ./internal/x/x.go:3:9.
var dotSlash = regexp.MustCompile(`(?m)(^|\s)\./([^\s:]+:\d+)`)

// CleanOutput cleans go build output run from the project root (PLAN.md
// §3.3 "Error output"): .lyr positions lose Go's "./" and their bracketed
// generated positions, generated names become the user's (both by
// project.CleanGoOutput), and any other "./path:L" position loses its "./".
// "# pkg" header lines are kept.
func CleanOutput(p *project.Project, out []byte) []byte {
	out = p.CleanGoOutput(out)
	return dotSlash.ReplaceAll(out, []byte("$1$2"))
}

// runtimePkg is the import path of the Lyr runtime, whose buildKey and
// buildID variables islandLDFlags sets.
const runtimePkg = "github.com/orbit-z/lyr"

// islandLDFlags returns the -ldflags flag of a build (PLAN.md §6): the
// -ldflags already in goflags, the effective GOFLAGS (a -ldflags on the
// command line replaces the one in GOFLAGS, so both are passed), plus a fresh random island key and
// build ID set with -X, so nothing random is written to lyr_gen/.
func islandLDFlags(goflags string) string {
	key := make([]byte, 32)
	rand.Read(key)
	id := make([]byte, 8)
	rand.Read(id)
	return ldflagsWithIslandKey(goflags, base64.StdEncoding.EncodeToString(key), hex.EncodeToString(id))
}

// ErrOverlayFlag is the error for an -overlay flag in GOFLAGS: lyr build
// passes its own (PLAN.md §3.5), which would silently replace it.
var ErrOverlayFlag = errors.New("-overlay in GOFLAGS isn't supported (lyr build passes its own)")

// hasFlag reports whether goflags (the value of GOFLAGS) sets the go flag
// name, as -name=… or --name=… (GOFLAGS flags always have a value).
func hasFlag(goflags, name string) bool {
	for _, f := range splitQuoted(goflags) {
		f = strings.TrimPrefix(strings.TrimPrefix(f, "-"), "-")
		if f == name || strings.HasPrefix(f, name+"=") {
			return true
		}
	}
	return false
}

// ldflagsWithIslandKey merges the -ldflags of goflags (the value of
// GOFLAGS) with the -X flags for key and id.
func ldflagsWithIslandKey(goflags, key, id string) string {
	return MergeLDFlags(goflags, "-X "+runtimePkg+".buildKey="+key+" -X "+runtimePkg+".buildID="+id)
}

// MergeLDFlags returns a -ldflags flag holding the -ldflags of goflags (the
// value of GOFLAGS), then add. A -ldflags on the go command line replaces
// the one in GOFLAGS, so a command that passes its own must pass both
// (lyr build adds the island key, lyr dev -w).
func MergeLDFlags(goflags, add string) string {
	ld := ""
	for _, f := range splitQuoted(goflags) {
		f = strings.TrimPrefix(f, "-")
		if v, ok := strings.CutPrefix(f, "-ldflags="); ok {
			ld = v
		} else if v, ok := strings.CutPrefix(f, "ldflags="); ok {
			ld = v
		}
	}
	if ld = strings.TrimSpace(ld); ld != "" {
		return "-ldflags=" + ld + " " + add
	}
	return "-ldflags=" + add
}

// GoFlags returns the effective GOFLAGS (go env GOFLAGS) of the go command
// run in root with env appended to the process environment.
func GoFlags(ctx context.Context, root string, env []string) (string, error) {
	cmd := exec.CommandContext(ctx, "go", "env", "GOFLAGS")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// splitQuoted splits s at spaces, keeping single- or double-quoted runs
// together without their quotes, as the go command splits GOFLAGS.
func splitQuoted(s string) []string {
	var out []string
	var cur strings.Builder
	in, quote := false, byte(0)
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			} else {
				cur.WriteByte(c)
			}
		case c == '\'' || c == '"':
			quote, in = c, true
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			if in {
				out = append(out, cur.String())
				cur.Reset()
				in = false
			}
		default:
			cur.WriteByte(c)
			in = true
		}
	}
	if in {
		out = append(out, cur.String())
	}
	return out
}
