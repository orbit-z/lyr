package tailwind

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/tdewolff/parse/v2/css"
)

// Input is one Tailwind run: a stylesheet after Inline.
type Input struct {
	*Inlined
	// Dir is the working directory (Source.Dir).
	Dir string
	// Root is set when the output depends on the project's sources (Info
	// of the inlined text).
	Root bool
	// Marker is the offset in Text of the first Tailwind marker, where a
	// missing CLI is reported.
	Marker int
	// MarkerName names it in that error ("@import \"tailwindcss\"",
	// "@apply").
	MarkerName string
}

// Error is a Tailwind error at a position in a user file.
type Error struct {
	// File is absolute; Line and Col are 1-based (bytes), 0 when unknown.
	File      string
	Line, Col int
	Msg       string
}

func (e *Error) Error() string {
	if e.Line > 0 {
		return fmt.Sprintf("%s:%d:%d: %s", e.File, e.Line, e.Col, e.Msg)
	}
	return e.File + ": " + e.Msg
}

// Processor runs Tailwind over inputs, caching each output by its input
// (working directory and text) until Invalidate says the sources or the
// CSS changed. It's safe for concurrent use: esbuild loads files in
// parallel, and at most GOMAXPROCS runs go at once.
type Processor struct {
	root string
	find func(ctx context.Context) (CLI, error)

	once   sync.Once
	cli    CLI
	cliErr error

	sem chan struct{}

	mu      sync.Mutex
	cssGen  int // bumped by a .css change: every input reruns
	srcGen  int // bumped by a source change: root inputs rerun
	seq     int // Sweep generation
	entries map[string]*entry

	active atomic.Bool
	runs   atomic.Int64
}

type entry struct {
	in             Input
	cssGen, srcGen int
	used           int
	done           chan struct{}
	out            string
	err            error
	// prev is the output of the entry this one replaced, hasPrev set when
	// there was one without error.
	prev    string
	hasPrev bool
}

// NewProcessor returns a Processor for the project at root that finds the
// CLI with Find and DefaultEnv on first use.
func NewProcessor(root string) *Processor {
	return NewProcessorWith(root, func(ctx context.Context) (CLI, error) { return Find(ctx, DefaultEnv(), root) })
}

// NewProcessorWith is NewProcessor with its own lookup (tests, orbit
// doctor).
func NewProcessorWith(root string, find func(ctx context.Context) (CLI, error)) *Processor {
	return &Processor{root: root, find: find, sem: make(chan struct{}, max(runtime.GOMAXPROCS(0), 1)), entries: map[string]*entry{}}
}

// Active reports whether the processor has seen a Tailwind input.
func (p *Processor) Active() bool { return p != nil && p.active.Load() }

// Runs is the number of Tailwind processes started (tests).
func (p *Processor) Runs() int { return int(p.runs.Load()) }

// CLI returns the CLI the processor runs, looking it up once.
func (p *Processor) CLI(ctx context.Context) (CLI, error) {
	p.once.Do(func() { p.cli, p.cliErr = p.find(ctx) })
	return p.cli, p.cliErr
}

func key(in Input) string { return in.Dir + "\x00" + in.Text }

func (e *entry) fresh(p *Processor) bool {
	return e.cssGen == p.cssGen && (!e.in.Root || e.srcGen == p.srcGen)
}

// Process returns Tailwind's output for in: cached, in flight, or run now.
func (p *Processor) Process(ctx context.Context, in Input) (string, error) {
	p.active.Store(true)
	k := key(in)
	p.mu.Lock()
	e := p.entries[k]
	start := false
	if e == nil || !e.fresh(p) {
		e = p.newEntry(k, in, e)
		start = true
	}
	e.used = p.seq
	p.mu.Unlock()
	if start {
		p.run(ctx, e)
	}
	<-e.done
	return e.out, e.err
}

// newEntry replaces old (nil when none) with a new entry; p.mu is held.
func (p *Processor) newEntry(k string, in Input, old *entry) *entry {
	e := &entry{in: in, cssGen: p.cssGen, srcGen: p.srcGen, used: p.seq, done: make(chan struct{})}
	if old != nil {
		select {
		case <-old.done:
			e.prev, e.hasPrev = old.out, old.err == nil
		default: // still running: its output is unknown, so the new one counts as changed
		}
	}
	p.entries[k] = e
	return e
}

// Invalidate marks outputs stale: css for every input (a .css file,
// tsconfig.json or package.json changed), sources for the root inputs
// (a source file brought a new class candidate). Errors are dropped too,
// so a failed input reruns.
func (p *Processor) Invalidate(css, sources bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if css {
		p.cssGen++
	}
	if sources {
		p.srcGen++
	}
	p.dropErrors()
}

// DropErrors forgets the inputs whose last run failed, so they rerun.
func (p *Processor) DropErrors() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.dropErrors()
}

func (p *Processor) dropErrors() {
	for k, e := range p.entries {
		select {
		case <-e.done:
			if e.err != nil {
				delete(p.entries, k)
			}
		default:
		}
	}
}

// Pending is a set of runs started by Rerun.
type Pending struct{ entries []*entry }

// Wait waits for the runs and reports whether an output changed (or a
// run failed, or there was no earlier output to compare with).
func (pd *Pending) Wait() bool {
	if pd == nil {
		return false
	}
	changed := false
	for _, e := range pd.entries {
		<-e.done
		if e.err != nil || !e.hasPrev || e.out != e.prev {
			changed = true
		}
	}
	return changed
}

// Rerun starts, in the background, the stale inputs that the last bundle
// used, with the same text: in orbit dev, the root inputs after a source
// change, in parallel with regenerating and go build. esbuild then finds
// their outputs in the cache.
func (p *Processor) Rerun(ctx context.Context) *Pending {
	p.mu.Lock()
	pd := &Pending{}
	for k, e := range p.entries {
		if e.used != p.seq || e.fresh(p) {
			continue
		}
		ne := p.newEntry(k, e.in, e)
		pd.entries = append(pd.entries, ne)
	}
	p.mu.Unlock()
	for _, e := range pd.entries {
		go p.run(ctx, e)
	}
	return pd
}

// Begin starts a bundle: the inputs it processes are marked as its own,
// for Sweep and Rerun.
func (p *Processor) Begin() {
	p.mu.Lock()
	p.seq++
	p.mu.Unlock()
}

// Sweep forgets the inputs the bundle since Begin didn't use: called after
// a successful bundle, it keeps the cache to the current inputs.
func (p *Processor) Sweep() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for k, e := range p.entries {
		if e.used != p.seq {
			delete(p.entries, k)
		}
	}
}

// run runs Tailwind for e and closes e.done.
func (p *Processor) run(ctx context.Context, e *entry) {
	defer close(e.done)
	cli, err := p.CLI(ctx)
	if err != nil {
		file, line, col := e.in.Position(e.in.Marker)
		e.err = &Error{File: file, Line: line, Col: col,
			Msg: fmt.Sprintf("%s needs the Tailwind CSS CLI (v4): %v; install it: %s; see orbit doctor", e.in.MarkerName, err, Install(runtime.GOOS, runtime.GOARCH))}
		return
	}
	p.sem <- struct{}{}
	defer func() { <-p.sem }()
	p.runs.Add(1)
	args := append(append([]string(nil), cli.Args...), "--optimize", "-i", "-", "-o", "-")
	cmd := exec.CommandContext(ctx, cli.Bin, args...)
	cmd.Dir = e.in.Dir
	cmd.Env = append(os.Environ(), "NO_COLOR=1", "FORCE_COLOR=0")
	cmd.Stdin = strings.NewReader(e.in.Text)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			file, line, col := e.in.Position(0)
			e.err = &Error{File: file, Line: line, Col: col, Msg: fmt.Sprintf("tailwindcss: %v", err)}
			return
		}
		e.err = e.in.cliError(stderr.String())
		return
	}
	e.out = stdout.String()
}

var (
	ansiRE   = regexp.MustCompile("\x1b\\[[0-9;]*[A-Za-z]")
	stdinRE  = regexp.MustCompile(`(?:^|\s)(?:\S*/)?stdin\.css:(\d+):(\d+):\s*`)
	unknowRE = regexp.MustCompile("Cannot apply unknown utility class `([^`]+)`")
)

// CleanMessage strips the ANSI codes, box characters and banner of
// Tailwind's stderr and returns the error message.
func CleanMessage(stderr string) string {
	s := ansiRE.ReplaceAllString(stderr, "")
	var lines []string
	for _, l := range strings.Split(s, "\n") {
		l = strings.TrimRight(l, " \r")
		t := strings.TrimLeft(l, "│┌└┆ ")
		switch {
		case t == "", strings.HasPrefix(t, "≈ tailwindcss"), t == "Error:", strings.HasPrefix(t, "Done in"):
			continue
		}
		lines = append(lines, t)
	}
	msg := strings.Join(lines, "\n")
	for _, p := range []string{"Error: ", "CssSyntaxError: "} {
		msg = strings.TrimPrefix(msg, p)
	}
	return strings.TrimSpace(msg)
}

// cliError turns Tailwind's stderr into an Error at a user position:
// Tailwind's own stdin.css:line:col when it gives one, else the @apply
// that names an unknown class, else the input's start.
func (in *Inlined) cliError(stderr string) *Error {
	msg := CleanMessage(stderr)
	if msg == "" {
		msg = "tailwindcss failed"
	}
	off := 0
	if m := stdinRE.FindStringSubmatchIndex(msg); m != nil {
		line, _ := strconv.Atoi(msg[m[2]:m[3]])
		col, _ := strconv.Atoi(msg[m[4]:m[5]])
		off = in.LineCol(line, col)
		msg = strings.TrimSpace(msg[:m[0]] + " " + msg[m[1]:])
	} else if m := unknowRE.FindStringSubmatch(msg); m != nil {
		if o, ok := in.findApply(m[1]); ok {
			off = o
		}
	}
	// Tailwind's long hint after the first sentence of an @apply error.
	if i := strings.Index(msg, " Are you using CSS modules"); i >= 0 {
		msg = msg[:i]
	}
	file, line, col := in.Position(off)
	return &Error{File: file, Line: line, Col: col, Msg: msg}
}

// findApply returns the offset of the first @apply in Text that names
// class.
func (in *Inlined) findApply(class string) (int, bool) {
	toks := lex(in.Text)
	for i, t := range toks {
		if t.tt != css.AtKeywordToken || !strings.EqualFold(in.Text[t.start:t.end], "@apply") {
			continue
		}
		end := ruleEnd(toks, i+1)
		stop := len(in.Text)
		if end < len(toks) {
			stop = toks[end].start
		}
		for _, w := range strings.Fields(in.Text[t.end:stop]) {
			if w == class || strings.TrimPrefix(w, "!") == class || strings.TrimSuffix(w, "!") == class {
				return t.start, true
			}
		}
	}
	return 0, false
}

// Rel returns e's file relative to root when it's inside it.
func (e *Error) Rel(root string) string {
	if r, err := filepath.Rel(root, e.File); err == nil && !strings.HasPrefix(r, "..") {
		return filepath.ToSlash(r)
	}
	return e.File
}
