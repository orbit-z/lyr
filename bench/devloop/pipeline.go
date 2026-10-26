package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/orbit-z/orbit/internal/assets"
	"github.com/orbit-z/orbit/internal/compiler"
	"github.com/orbit-z/orbit/internal/dev"
	"github.com/orbit-z/orbit/internal/project"
)

// goEnv is appended to the go command's environment, as in the project
// tests.
// verbose (DEVLOOP_VERBOSE=1) logs the packages go build compiled.
var verbose = os.Getenv("DEVLOOP_VERBOSE") == "1"

// buildFlags are extra go build flags (-buildflags).
var buildFlags []string

// goEnv keeps the caller's GOFLAGS (e.g. GOFLAGS=-gcflags=-l).
var goEnv = []string{strings.TrimSpace("GOFLAGS=" + os.Getenv("GOFLAGS") + " -mod=mod"), "GOWORK=off"}

// devKey is a fixed ORBIT_KEY for the server (any 32 bytes), so it doesn't
// log the random-key warning on every start.
const devKey = "ZGV2bG9vcC1iZW5jaC1rZXktMDEyMzQ1Njc4OWFiY2Q="

// prevFile is what the resident pipeline remembers about a file between
// edits: its pass-1 output (to find the changed units) and its final
// output (reused when the checker doesn't re-check it).
type prevFile struct {
	pass1Decl, pass1Impl []byte
	out                  *compiler.Output
	typed                bool
	writes               compiler.TypedWrites
}

// pipeline is the resident state of a would-be orbit dev process.
type pipeline struct {
	root    string
	devDir  string
	binDir  string
	ldflags string
	bundler assets.Bundler
	checker *compiler.Checker
	prev    map[string]*prevFile
	server  *exec.Cmd
	addr    string
	builds  int
	// dev, with -dev, is internal/dev's pipeline: step runs it instead of
	// the prototype stages below.
	dev *dev.Pipeline
}

func newPipeline(root, binDir, ldflags string) *pipeline {
	return &pipeline{
		root:    root,
		devDir:  filepath.Join(root, ".orbit", "dev"),
		binDir:  binDir,
		ldflags: ldflags,
		bundler: assets.New(assets.Options{Root: root, Dev: true}),
		checker: compiler.NewChecker(&compiler.GoListLoader{Dir: root, Env: goEnv}),
		prev:    map[string]*prevFile{},
	}
}

func (pl *pipeline) close() {
	pl.stopServer()
	pl.bundler.Close()
	if pl.dev != nil {
		pl.dev.Close()
	}
}

// sample is one measured edit: stage durations and counters.
type sample struct {
	Front, Esbuild, Check, CheckFresh, GenWrite, GoBuild, Link, Stop, Start, FirstReq time.Duration
	// Written is the number of orbit_gen files rewritten; Compiled the
	// packages go build compiled (not from the build cache); Rechecked the
	// units the resident checker re-checked; Linked whether go build
	// relinked.
	Written, Compiled, Rechecked int
	Linked                       bool
	// Mismatch is set when the resident checker's output differs from a
	// fresh full check (a correctness problem in the incremental path).
	Mismatch string
	// Early (-dev) is set when go build started before regenerating, and
	// EarlyUsed when that build was the one used.
	Early, EarlyUsed bool
}

// binaryReady is the time from "file written" to "new server binary ready".
func (s sample) binaryReady() time.Duration {
	return s.Front + s.Esbuild + s.Check + s.GenWrite + s.GoBuild
}

// serving is the time from "file written" to "new server accepting
// connections" (old server stopped, new one started).
func (s sample) serving() time.Duration { return s.binaryReady() + s.Stop + s.Start }

type stepOpts struct {
	assetsOnly bool // <style>/<script> content only: esbuild, no Go rebuild
	invalidate bool // a user .go file changed: reload export data
	fresh      bool // also time a fresh Checker (go list + full check)
	verify     string
	// paths are the files the edit wrote, for -dev (internal/dev's
	// classifier decides from them).
	paths []string
}

// front loads the project and runs pass 1.
func (pl *pipeline) front() (*project.Project, error) {
	p, err := project.Load(pl.root)
	if err != nil {
		return nil, err
	}
	if p.Front() {
		return nil, diagErr(p.Diagnostics)
	}
	return p, nil
}

func diagErr(ds []project.Diagnostic) error {
	var b strings.Builder
	for _, d := range ds {
		b.WriteString(d.String() + "\n")
	}
	return errors.New(b.String())
}

// step runs the dev pipeline after an edit and measures each stage.
func (pl *pipeline) step(ctx context.Context, o stepOpts) (sample, error) {
	if pl.dev != nil {
		return pl.stepDev(ctx, o)
	}
	var s sample
	mark := time.Now()
	p, err := pl.front()
	if err != nil {
		return s, err
	}
	s.Front = time.Since(mark)

	mark = time.Now()
	ds, err := p.Bundle(pl.bundler)
	if err != nil {
		return s, err
	}
	if project.HasErrors(ds) {
		return s, diagErr(ds)
	}
	if err := assets.WriteDev(pl.devDir, p.Assets); err != nil {
		return s, err
	}
	s.Esbuild = time.Since(mark)
	if o.assetsOnly {
		return s, nil
	}

	mark = time.Now()
	n, err := pl.check(ctx, p, o.invalidate)
	if err != nil {
		return s, err
	}
	s.Check = time.Since(mark)
	s.Rechecked = n

	if o.fresh {
		mark = time.Now()
		res, err := compiler.NewChecker(&compiler.GoListLoader{Dir: pl.root, Env: goEnv}).Check(ctx, p.Units())
		if err != nil {
			return s, err
		}
		s.CheckFresh = time.Since(mark)
		for i, u := range res.Units {
			f := p.Files[i]
			if !bytes.Equal(u.Output.Impl.Content, f.Output.Impl.Content) || !bytes.Equal(u.Output.Decl.Content, f.Output.Decl.Content) {
				s.Mismatch = f.Path
				break
			}
		}
	}

	mark = time.Now()
	// In dev the app reads asset URLs from .orbit/dev/manifest.json, so
	// the compiled manifest stays empty: otherwise a CSS-only edit (new
	// hashes) would change orbit_gen/manifest and force a Go rebuild.
	p.Assets = nil
	wr, err := project.WriteGenerated(p.Root, p.GenFiles())
	if err != nil {
		return s, err
	}
	s.GenWrite = time.Since(mark)
	s.Written = len(wr.Written) + len(wr.Removed)

	mark = time.Now()
	bin, ag, err := pl.goBuild(ctx)
	if err != nil {
		return s, err
	}
	s.GoBuild = time.Since(mark)
	s.Compiled, s.Linked, s.Link = ag.compiled, ag.linked, ag.link

	mark = time.Now()
	pl.stopServer()
	s.Stop = time.Since(mark)

	mark = time.Now()
	if err := pl.startServer(bin); err != nil {
		return s, err
	}
	s.Start = time.Since(mark)

	mark = time.Now()
	body, err := get("http://" + pl.addr + "/")
	if err != nil {
		return s, err
	}
	s.FirstReq = time.Since(mark)
	if o.verify != "" && !strings.Contains(body, o.verify) {
		return s, fmt.Errorf("GET / doesn't show the edit %q", o.verify)
	}
	return s, nil
}

// check runs the resident checker: Check the first time, then Update with
// the units whose pass-1 output changed. Files it doesn't re-check keep
// their previous output. It returns the number of units re-checked.
func (pl *pipeline) check(ctx context.Context, p *project.Project, invalidate bool) (int, error) {
	var res *compiler.CheckResult
	var err error
	if len(pl.prev) == 0 {
		res, err = pl.checker.Check(ctx, p.Units())
	} else {
		if invalidate {
			pl.checker.Invalidate()
		}
		var changed []compiler.Unit
		seen := map[string]bool{}
		for _, f := range p.Files {
			seen[f.Path] = true
			old := pl.prev[f.Path]
			if old == nil || !bytes.Equal(old.pass1Decl, f.Pass1.Decl.Content) || !bytes.Equal(old.pass1Impl, f.Pass1.Impl.Content) {
				changed = append(changed, compiler.Unit{Input: f.Input, Pass1: f.Pass1})
			}
		}
		var removed []string
		for path := range pl.prev {
			if !seen[path] {
				removed = append(removed, path)
				delete(pl.prev, path)
			}
		}
		res, err = pl.checker.Update(ctx, changed, removed)
	}
	if err != nil {
		return 0, err
	}
	byImpl := map[string]compiler.TypedUnit{}
	for _, u := range res.Units {
		byImpl[u.Output.Impl.Path] = u
		if len(u.Diagnostics) > 0 {
			return 0, fmt.Errorf("typed pass: %s", u.Diagnostics[0].Message)
		}
	}
	for _, f := range p.Files {
		old := pl.prev[f.Path]
		if u, ok := byImpl[f.Pass1.Impl.Path]; ok {
			f.Output, f.Typed, f.Writes = u.Output, u.Checked, u.Writes
		} else if old != nil {
			f.Output, f.Typed, f.Writes = old.out, old.typed, old.writes
		} else {
			return 0, fmt.Errorf("%s: not checked", f.Path)
		}
		if !f.Typed {
			return 0, fmt.Errorf("%s: kept its Expr fallbacks (a type error in the synthetic project?)", f.Path)
		}
		pl.prev[f.Path] = &prevFile{pass1Decl: f.Pass1.Decl.Content, pass1Impl: f.Pass1.Impl.Content, out: f.Output, typed: f.Typed, writes: f.Writes}
	}
	return len(res.Units), nil
}

// actionGraph is the part of go build -debug-actiongraph we read.
type actionGraph struct {
	compiled int
	linked   bool
	link     time.Duration
}

func (pl *pipeline) goBuild(ctx context.Context) (string, actionGraph, error) {
	var ag actionGraph
	pl.builds++
	bin := filepath.Join(pl.binDir, fmt.Sprintf("server-%d", pl.builds))
	agFile := filepath.Join(pl.binDir, "actiongraph.json")
	args := []string{"build", "-o", bin, "-debug-actiongraph=" + agFile}
	if pl.ldflags != "" {
		args = append(args, "-ldflags="+pl.ldflags)
	}
	args = append(args, buildFlags...)
	cmd := exec.CommandContext(ctx, "go", append(args, "./cmd/server")...)
	cmd.Dir = pl.root
	cmd.Env = append(os.Environ(), goEnv...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", ag, fmt.Errorf("go build: %v\n%s", err, out)
	}
	data, err := os.ReadFile(agFile)
	if err != nil {
		return "", ag, err
	}
	var actions []struct {
		Mode      string
		Package   string
		Cmd       []string
		TimeStart time.Time
		TimeDone  time.Time
	}
	if err := json.Unmarshal(data, &actions); err != nil {
		return "", ag, err
	}
	for _, a := range actions {
		switch {
		case a.Mode == "build" && len(a.Cmd) > 0:
			ag.compiled++
			if verbose {
				fmt.Fprintf(os.Stderr, "devloop: compiled %s (%v)\n", a.Package, a.TimeDone.Sub(a.TimeStart).Round(time.Millisecond))
			}
		case a.Mode == "link" && len(a.Cmd) > 0:
			ag.linked = true
			ag.link = a.TimeDone.Sub(a.TimeStart)
		}
	}
	// Keep only the newest binary.
	if pl.builds > 1 {
		os.Remove(filepath.Join(pl.binDir, fmt.Sprintf("server-%d", pl.builds-1)))
	}
	return bin, ag, nil
}

// stopServer stops the running server as orbit dev will: SIGINT, then wait.
func (pl *pipeline) stopServer() {
	if pl.server == nil {
		return
	}
	pl.server.Process.Signal(syscall.SIGINT)
	done := make(chan struct{})
	go func() { pl.server.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		pl.server.Process.Kill()
		<-done
	}
	pl.server = nil
}

// startServer starts bin on a free localhost port and waits until it
// accepts a TCP connection.
func (pl *pipeline) startServer(bin string) error {
	addr, err := freeAddr()
	if err != nil {
		return err
	}
	cmd := exec.Command(bin)
	cmd.Dir = pl.root
	cmd.Env = append(os.Environ(), "ORBIT_ADDR="+addr, "ORBIT_DEV=1", "ORBIT_STATIC="+pl.devDir, "ORBIT_KEY="+devKey)
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	pl.server, pl.addr = cmd, addr
	deadline := time.Now().Add(10 * time.Second)
	for {
		c, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if err == nil {
			c.Close()
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("server didn't accept connections on %s within 10 s", addr)
		}
		time.Sleep(200 * time.Microsecond)
	}
}

func freeAddr() (string, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	defer l.Close()
	return l.Addr().String(), nil
}

// client doesn't keep connections alive, so stopping the server doesn't
// wait for idle connections.
var client = &http.Client{Transport: &http.Transport{DisableKeepAlives: true}, Timeout: 10 * time.Second}

func get(url string) (string, error) {
	resp, err := client.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GET %s: %s\n%s", url, resp.Status, b)
	}
	return string(b), nil
}

// stepDev is step with -dev: internal/dev's pipeline (classifier, resident
// checker, esbuild skip, early go build on .go-only edits), then the same
// server swap and first request as the prototype.
func (pl *pipeline) stepDev(ctx context.Context, o stepOpts) (sample, error) {
	var s sample
	mark := time.Now()
	r, err := pl.dev.Run(ctx, o.paths, pl.server != nil)
	if err != nil {
		return s, err
	}
	if r.Failure != "" {
		return s, errors.New(r.Failure)
	}
	t := r.Timings
	s.Front, s.Esbuild, s.Check, s.GenWrite = t.Front, t.Esbuild, t.Check, t.Write
	// go build's share: everything after writing, including the wait for
	// an early build (which overlapped the stages before).
	s.GoBuild = max(0, time.Since(mark)-t.Front-t.Esbuild-t.Check-t.Write)
	s.Rechecked, s.Written, s.Early, s.EarlyUsed = t.Rechecked, t.Written, t.EarlyBuild, t.EarlyUsed
	if r.Bin == "" {
		if !o.assetsOnly && len(o.paths) > 0 {
			return s, fmt.Errorf("internal/dev built no server for %v", o.paths)
		}
		s.GoBuild = 0
		return s, nil
	}
	if o.assetsOnly {
		return s, fmt.Errorf("internal/dev rebuilt the server for an asset-only edit of %v", o.paths)
	}
	s.Linked = true

	mark = time.Now()
	pl.stopServer()
	s.Stop = time.Since(mark)
	mark = time.Now()
	if err := pl.startServer(r.Bin); err != nil {
		return s, err
	}
	s.Start = time.Since(mark)
	mark = time.Now()
	body, err := get("http://" + pl.addr + "/")
	if err != nil {
		return s, err
	}
	s.FirstReq = time.Since(mark)
	if o.verify != "" && !strings.Contains(body, o.verify) {
		return s, fmt.Errorf("GET / doesn't show the edit %q", o.verify)
	}
	return s, nil
}
