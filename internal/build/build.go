// Package build is the orbit build pipeline (PLAN.md §6): load the config,
// bundle the assets and generate orbit_gen/, stage dist/client from the
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
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"

	"github.com/bertrandkhe/orbit/internal/assets"
	"github.com/bertrandkhe/orbit/internal/config"
	"github.com/bertrandkhe/orbit/internal/project"
)

// Output paths, relative to the project root.
const (
	DistDir   = "dist"
	ClientDir = DistDir + "/client"
	ServerBin = DistDir + "/server"
)

// ErrEmbedNotImplemented is returned for --embed and "embed": true until
// milestone 7.
var ErrEmbedNotImplemented = errors.New("orbit build --embed (or \"embed\": true in orbit.json): not implemented yet")

// Options configure a build.
type Options struct {
	// Root is the module root.
	Root string
	// Embed is --embed.
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
	// Public are the errors about public/ files ("public/_orbit/x.js: …").
	Public []string
	// GoOutput is go build's output, cleaned (positions in .orbit files,
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
// (no go.mod, an invalid orbit.json, --embed, I/O errors, a missing go
// command, ctx cancellation).
func Run(ctx context.Context, o Options) (*Result, error) {
	root, err := filepath.Abs(o.Root)
	if err != nil {
		return nil, err
	}
	cfg, err := config.Load(root)
	if err != nil {
		return nil, err
	}
	if o.Embed || cfg.Embed {
		return nil, ErrEmbedNotImplemented
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

	server := filepath.Join(stage, "server")
	out, err := goBuild(ctx, root, server, p.ServerEntry, o.Env)
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

// goBuild runs go build -o out entry in root and returns its combined
// output.
func goBuild(ctx context.Context, root, out, entry string, env []string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "go", "build", "-o", out, entry)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), env...)
	var b bytes.Buffer
	cmd.Stdout, cmd.Stderr = &b, &b
	err := cmd.Run()
	return b.Bytes(), err
}

// dotSlash is Go's "./" in front of a position at the start of a line or
// after a space: ./internal/x/x.go:3:9.
var dotSlash = regexp.MustCompile(`(?m)(^|\s)\./([^\s:\[\]]+:\d+)`)

// CleanOutput cleans go build output run from the project root (PLAN.md
// §3.3 "Error output"): .orbit positions lose Go's "./" and their bracketed
// generated positions, generated names become the user's (both by
// project.CleanGoOutput), and any other "./path:L" position loses its "./".
// "# pkg" header lines are kept.
func CleanOutput(p *project.Project, out []byte) []byte {
	out = p.CleanGoOutput(out)
	return dotSlash.ReplaceAll(out, []byte("$1$2"))
}
