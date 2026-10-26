package dev

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/orbit-z/orbit/internal/assets"
	"github.com/orbit-z/orbit/internal/build"
	"github.com/orbit-z/orbit/internal/compiler"
	"github.com/orbit-z/orbit/internal/project"
)

// Dev output directories, relative to the module root.
const (
	// StaticDir is ORBIT_STATIC: the dev bundles under _orbit/ and their
	// manifest.json (PLAN.md §5 Manifest).
	StaticDir = ".orbit/dev"
	// binDir holds the dev server binaries.
	binDir = ".orbit/devserver"
)

// pipeline is the resident dev pipeline (PLAN.md §7.1): one esbuild
// context, one type checker and the state of the last build, kept between
// edits so each rebuild does as little as it can. It isn't safe for
// concurrent use: the dev server runs it from one goroutine.
type pipeline struct {
	root    string
	env     []string // appended to the go commands' environment
	ldflags string   // go build's -ldflags flag
	// buildFlags are go build's other flags (devBuildFlags).
	buildFlags []string
	bundler    assets.Bundler
	inc        *project.Incremental

	// The last good bundle input and its key; nil before the first bundle.
	lastInput *assets.Input
	assetKey  string
	// genOK is set when orbit_gen/ holds the generated code of the last
	// successful Typecheck, and goOK when the last go build of it
	// succeeded.
	genOK, goOK bool
	// failed is set when the last run failed: the next one runs every
	// stage, whatever changed.
	failed bool
	// entry is the server entry package of the last load.
	entry string
	// proj is the project of the last front, which project.Reload updates
	// with each change's paths; nil before the first front and after a
	// failed load or a run cut short before its front (the next run then
	// loads the whole project).
	proj *project.Project
	// slot alternates the binary's name, so go build never writes over
	// the running server's binary.
	slot int

	// goBuild runs go build; a field so tests can watch or replace it.
	goBuild func(ctx context.Context, out, entry string) ([]byte, error)
	// trace, when set, is called with the name of each stage as it starts
	// ("front", "esbuild", "check", "write", "gobuild", "gobuild-early"),
	// and "gobuild-early-done" when an early build finishes. For tests.
	trace func(stage string)
}

func newPipeline(root string, env []string, goflags string) *pipeline {
	flags := devBuildFlags(goflags)
	// The type checker's go list -export gets go build's flags, so both
	// compile the user's and std packages with the same flags and share
	// them in the build cache: with different ones a .go edit would compile
	// the edited package twice.
	// Like go build, go list compiles without the user's GOGC/GOMEMLIMIT:
	// cmd/compile tunes its heap only when both are empty (goBuildEnv).
	loader := &compiler.GoListLoader{Dir: root, Env: slices.Concat(env, []string{"GOGC=", "GOMEMLIMIT="}), BuildFlags: flags}
	pl := &pipeline{
		root:       root,
		env:        env,
		ldflags:    build.MergeLDFlags(goflags, devLDFlags),
		buildFlags: flags,
		bundler:    assets.New(assets.Options{Root: root, Dev: true}),
		inc:        project.NewIncremental(compiler.NewChecker(loader)),
	}
	pl.goBuild = pl.runGoBuild
	return pl
}

func (pl *pipeline) close() { pl.bundler.Close() }

func (pl *pipeline) tr(stage string) {
	if pl.trace != nil {
		pl.trace(stage)
	}
}

// outcome is what a pipeline run produced.
type outcome struct {
	// Bin is the new server binary to start; "" when the running server
	// stays (an asset-only or public/ change).
	Bin string
	// Assets is set when the bundles were rebuilt.
	Assets bool
	// Failure is set when the run failed; the running server stays.
	Failure *failure
	// Warnings are diagnostics to print that don't fail the run.
	Warnings []string
	// Timings of the stages that ran.
	Timings Timings
}

// Timings is the time spent in each stage of a run; zero for a stage that
// didn't run.
type Timings struct {
	Front, Esbuild, Check, Write, GoBuild time.Duration
	// Rechecked is the number of .orbit files the type checker re-checked,
	// Written the number of orbit_gen files written or removed.
	Rechecked, Written int
	// EarlyBuild is set when go build started before regenerating, and
	// EarlyUsed when its binary was used.
	EarlyBuild, EarlyUsed bool
}

// failure is a failed build, or a server that failed to start or exited:
// the overlay shows it (PLAN.md §7.1).
type failure struct {
	Title string
	// Text is the cleaned-up message, one "file:line:col: message" per
	// error where there is a position.
	Text string
	// Output is extra output to show as is: the server's stderr.
	Output string
}

func compileFailure(text string) *failure {
	return &failure{Title: "Build failed", Text: strings.TrimRight(text, "\n")}
}

// earlyBuild is a go build started before regenerating (on .go-only edits).
type earlyBuild struct {
	bin  string
	done chan struct{}
	out  []byte
	err  error
}

// run rebuilds after change c. appRunning reports whether a server is
// running now: without one, the Go stages always run. A failure in the
// user's files is in the outcome; the error is for I/O problems and ctx
// cancellation.
func (pl *pipeline) run(ctx context.Context, c Change, appRunning bool) (out outcome, err error) {
	t := &out.Timings
	fronted := false
	defer func() {
		pl.failed = err != nil || out.Failure != nil
		// Every changed path must reach project.Reload once: a run that
		// returns before its front drops the project, so the next run
		// loads everything.
		if !fronted && err != nil {
			pl.proj = nil
		}
	}()
	full := pl.failed || pl.lastInput == nil
	if !full && c.Only(KindPublic) {
		return out, nil
	}
	// Only watched files: nothing to rebuild; the dev loop restarts the
	// running server. Without one, go on to build and start one.
	if !full && appRunning && c.Only(KindPublic, KindWatch) {
		return out, nil
	}

	// A .go-only edit: start go build now, in parallel with regenerating.
	// If regenerating writes nothing to orbit_gen/, that build stands.
	var early *earlyBuild
	if !full && c.Only(KindGo) && pl.genOK && pl.entry != "" {
		ectx, cancel := context.WithCancel(ctx)
		early = pl.startEarly(ectx)
		t.EarlyBuild = true
		defer func() {
			cancel() // when it wasn't needed after all; never leave it running
			<-early.done
		}()
	}
	if c.Has(KindGo) || c.Has(KindConfig) {
		pl.inc.Invalidate()
	}

	// Only files the bundles read: rebuild them from the last input.
	if !full && c.Only(KindAsset, KindPublic) {
		pl.tr("esbuild")
		mark := time.Now()
		f, err := pl.bundle(pl.lastInput, pl.assetKey)
		t.Esbuild = time.Since(mark)
		if err != nil || f != nil {
			out.Failure = f
			return out, err
		}
		out.Assets = true
		if appRunning {
			return out, nil
		}
		// No server running (it exited): go on, to build and start one.
	}

	pl.tr("front")
	mark := time.Now()
	p, hasErrors, err := project.Reload(pl.proj, pl.root, c.Paths)
	fronted = true
	if err != nil {
		// No go.mod, an invalid orbit.json, no src/: shown like a compile
		// error until it's fixed.
		pl.proj = nil
		out.Failure = compileFailure(err.Error())
		return out, nil
	}
	pl.proj = p
	pl.entry = p.ServerEntry
	t.Front = time.Since(mark)
	if f := diagFailure(p.Diagnostics, &out); f != nil || hasErrors {
		out.Failure = f
		return out, nil
	}

	in := p.AssetInput()
	key := inputKey(in)
	if !out.Assets && (full || c.Has(KindAsset) || key != pl.assetKey) {
		pl.tr("esbuild")
		mark = time.Now()
		f, err := pl.bundle(in, key)
		t.Esbuild = time.Since(mark)
		if err != nil || f != nil {
			out.Failure = f
			return out, err
		}
		out.Assets = true
	}

	if pl.goOK && appRunning && !c.Has(KindGo) && !c.Has(KindConfig) && !pl.inc.Changed(p) {
		return out, nil // only <style>/<script> contents changed
	}

	pl.tr("check")
	mark = time.Now()
	warnings, n, err := pl.inc.Typecheck(ctx, p)
	t.Check, t.Rechecked = time.Since(mark), n
	if err != nil {
		return out, err
	}
	out.Warnings = append(out.Warnings, warnings...)
	if f := diagFailure(p.Diagnostics, &out); f != nil {
		pl.genOK = false
		out.Failure = f
		return out, nil
	}

	pl.tr("write")
	mark = time.Now()
	// In dev the app reads asset URLs from manifest.json, so the compiled
	// manifest stays empty: a CSS edit (new hashes) never changes it.
	p.Assets = nil
	wr, err := project.WriteGenerated(p.Root, p.GenFiles())
	t.Write = time.Since(mark)
	if err != nil {
		pl.genOK = false
		return out, err
	}
	t.Written = len(wr.Written) + len(wr.Removed)
	pl.genOK = true

	mark = time.Now()
	var bin string
	var gout []byte
	var gerr error
	if e := early; e != nil {
		<-e.done
		// The .d.ts files under orbit_gen/types aren't Go: writing only
		// them keeps the early build.
		if !goWritten(wr) {
			t.EarlyUsed = true
			bin, gout, gerr = e.bin, e.out, e.err
		}
	}
	if !t.EarlyUsed {
		pl.tr("gobuild")
		bin = pl.nextBin()
		gout, gerr = pl.goBuild(ctx, bin, p.ServerEntry)
	}
	t.GoBuild = time.Since(mark)
	if gerr != nil {
		var exit *exec.ExitError
		if !errors.As(gerr, &exit) || ctx.Err() != nil {
			if ctx.Err() != nil {
				return out, ctx.Err()
			}
			return out, fmt.Errorf("go build: %w", gerr)
		}
		pl.goOK = false
		out.Failure = compileFailure(string(build.CleanOutput(p, gout)))
		return out, nil
	}
	if len(gout) > 0 {
		out.Warnings = append(out.Warnings, strings.TrimRight(string(build.CleanOutput(p, gout)), "\n"))
	}
	pl.goOK = true
	pl.slot ^= 1
	out.Bin = bin
	return out, nil
}

// diagFailure collects the diagnostics: errors make the failure (nil when
// there are none), warnings go to out.Warnings.
func diagFailure(ds []project.Diagnostic, out *outcome) *failure {
	var errs []string
	for _, d := range ds {
		if d.Severity == compiler.SeverityError {
			errs = append(errs, d.String())
		} else {
			out.Warnings = append(out.Warnings, d.String())
		}
	}
	if len(errs) == 0 {
		return nil
	}
	return compileFailure(strings.Join(errs, "\n"))
}

// bundle rebuilds the dev bundles from in and writes them and
// manifest.json to .orbit/dev. It returns a failure for errors in the
// user's files.
func (pl *pipeline) bundle(in *assets.Input, key string) (*failure, error) {
	res, err := pl.bundler.Bundle(in)
	if err != nil {
		return nil, err
	}
	var errs []string
	for _, d := range res.Diagnostics {
		if !d.Warning {
			errs = append(errs, assetDiag(d))
		}
	}
	if len(errs) > 0 {
		return compileFailure(strings.Join(errs, "\n")), nil
	}
	if err := assets.WriteDev(filepath.Join(pl.root, filepath.FromSlash(StaticDir)), res); err != nil {
		return nil, err
	}
	pl.lastInput, pl.assetKey = in, key
	return nil, nil
}

// assetDiag formats a bundler diagnostic as Orbit's other errors.
func assetDiag(d assets.Diagnostic) string {
	file := d.File
	if file == "" {
		file = "esbuild"
	}
	if d.Line > 0 {
		return fmt.Sprintf("%s:%d:%d: %s", file, d.Line, d.Col, d.Message)
	}
	return file + ": " + d.Message
}

// inputKey is a digest of everything the bundles depend on in in: each
// processed block's content, kind and line:col (source maps point there),
// and every page's graph. Template, frontmatter and Props edits that don't
// move a block keep it, so esbuild doesn't run for them.
func inputKey(in *assets.Input) string {
	h := sha256.New()
	w := func(parts ...string) {
		for _, s := range parts {
			h.Write([]byte(strconv.Itoa(len(s))))
			h.Write([]byte{':'})
			h.Write([]byte(s))
		}
	}
	paths := make([]string, 0, len(in.Sources))
	for p := range in.Sources {
		paths = append(paths, p)
	}
	slices.Sort(paths)
	for _, p := range paths {
		s := in.Sources[p]
		w("source", p)
		for i, b := range s.Scripts {
			span := ""
			if i < len(s.SrcSpans) {
				span = s.Lines.Position(s.SrcSpans[i].Start).String()
			}
			w("script", b.Content.Text, s.Lines.Position(b.Content.Start).String(), b.Src, span)
		}
		for _, b := range s.Styles {
			w("style", b.Content.Text, s.Lines.Position(b.Content.Start).String(), strconv.FormatBool(b.Global))
		}
	}
	for _, pg := range in.Pages {
		w("page", pg.File)
		w(pg.Modules...)
		for _, l := range pg.Lazy {
			w("lazy", l.ID)
			w(l.Modules...)
		}
		w("highlight", strconv.FormatBool(pg.Highlight))
	}
	w("highlight-css", in.HighlightCSS)
	return hex.EncodeToString(h.Sum(nil))
}

// nextBin is the path of the binary the next go build writes: never the
// one the running server was started from.
func (pl *pipeline) nextBin() string {
	name := "server-" + strconv.Itoa(pl.slot^1)
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return filepath.Join(pl.root, filepath.FromSlash(binDir), name)
}

// startEarly starts go build in the background, before regenerating.
func (pl *pipeline) startEarly(ctx context.Context) *earlyBuild {
	pl.tr("gobuild-early")
	e := &earlyBuild{bin: pl.nextBin(), done: make(chan struct{})}
	entry := pl.entry
	go func() {
		defer close(e.done)
		e.out, e.err = pl.goBuild(ctx, e.bin, entry)
		pl.tr("gobuild-early-done")
	}()
	return e
}

// devLDFlags is what dev builds add to go build's -ldflags: no symbol
// table and no DWARF, which makes linking faster. Stack traces need
// neither (they use the pclntab). orbit build doesn't use it.
const devLDFlags = "-s -w"

// devGCFlags are dev builds' compiler flags. No DWARF in any package (std,
// the user's, the generated ones), since the linker drops it anyway: it
// makes compiling and linking faster. The generated packages are also
// compiled without inlining: measured 10–14% faster on the slowest edit (a
// Go package every Props imports, which recompiles every generated package)
// and no change on template edits (milestone 10, user decision). When
// several -gcflags patterns match a package the last one wins, so the
// orbit_gen one repeats -dwarf=false. The relative pattern matches only
// packages under the module's orbit_gen/.
var devGCFlags = []string{
	"-gcflags=all=-dwarf=false",
	"-gcflags=./orbit_gen/...=-l -dwarf=false",
}

// devVCSFlag skips stamping VCS information into dev binaries: go build
// otherwise runs git on every build (10–20 ms). Nothing in Orbit reads it.
const devVCSFlag = "-buildvcs=false"

// devBuildFlags returns go build's flags other than -ldflags for a dev
// build: devGCFlags unless goflags (GOFLAGS) has its own -gcflags, and
// devVCSFlag unless it has its own -buildvcs, since a flag on the command
// line would replace GOFLAGS'.
func devBuildFlags(goflags string) []string {
	var gc, vcs bool
	for _, f := range strings.Fields(goflags) {
		name, _, _ := strings.Cut(strings.TrimLeft(f, "-"), "=")
		switch name {
		case "gcflags":
			gc = true
		case "buildvcs":
			vcs = true
		}
	}
	var flags []string
	if !gc {
		flags = append(flags, devGCFlags...)
	}
	if !vcs {
		flags = append(flags, devVCSFlag)
	}
	return flags
}

// goBuildEnv returns the environment of the go build child: environ, then
// extra, without GOGC and GOMEMLIMIT. cmd/compile tunes its starting heap
// only when both are unset, and a user GOGC=100 measured ~30% slower
// on the slowest rebuilds. The user's server keeps them.
func goBuildEnv(environ, extra []string) []string {
	env := make([]string, 0, len(environ)+len(extra))
	for _, kv := range slices.Concat(environ, extra) {
		if strings.HasPrefix(kv, "GOGC=") || strings.HasPrefix(kv, "GOMEMLIMIT=") {
			continue
		}
		env = append(env, kv)
	}
	return env
}

// runGoBuild is go build -o out -ldflags=… [flags] entry, run in the module
// root.
func (pl *pipeline) runGoBuild(ctx context.Context, out, entry string) ([]byte, error) {
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return nil, err
	}
	args := append([]string{"build", "-o", out, pl.ldflags}, pl.buildFlags...)
	cmd := exec.CommandContext(ctx, "go", append(args, entry)...)
	cmd.Dir = pl.root
	cmd.Env = goBuildEnv(os.Environ(), pl.env)
	var b bytes.Buffer
	cmd.Stdout, cmd.Stderr = &b, &b
	err := cmd.Run()
	return b.Bytes(), err
}

// goWritten reports whether wr wrote or removed a file that go build reads:
// anything but the .d.ts files of orbit_gen/types (PLAN.md §5c).
func goWritten(wr *project.WriteResult) bool {
	for _, list := range [][]string{wr.Written, wr.Removed} {
		for _, p := range list {
			if !strings.HasSuffix(p, ".d.ts") {
				return true
			}
		}
	}
	return false
}
