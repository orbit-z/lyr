// Command lsp measures orbit lsp as an editor sees it (PLAN.md §9): startup
// up to the readiness barrier, completion/hover/definition/references
// latency, and the memory of orbit lsp and its gopls, on bench/devloop's
// synthetic project, with and without orbit_gen/ on disk. See README.md.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/orbit-z/orbit/bench/devloop/synth"
)

func main() {
	quick := os.Getenv("QUICK") == "1"
	files := flag.Int("files", 200, "number of .orbit files in the synthetic project")
	runs := flag.Int("runs", 20, "measured runs per request kind (after one warm-up run)")
	starts := flag.Int("starts", 3, "orbit lsp processes started per mode (startup timings)")
	modes := flag.String("modes", "overlay,disk", "modes to run: overlay (no orbit_gen/ on disk), disk (orbit generate first)")
	orbitRoot := flag.String("orbit", "../..", "path to the Orbit repository")
	dir := flag.String("dir", "", "project directory (default: a new temporary directory)")
	keep := flag.Bool("keep", false, "keep the project directory")
	jsonOut := flag.String("json", "", "also write every sample to this JSON file")
	verbose := flag.Bool("v", false, "print orbit lsp's log after each session")
	trace := flag.String("trace", "", "run gopls with -rpc.trace, logging to this file (prefix; one file per session)")
	typescript := flag.String("typescript", os.Getenv("ORBIT_BENCH_TYPESCRIPT"), "TypeScript 7 engine (the typescriptPath option; default: orbit lsp's own lookup)")
	flag.Parse()
	if quick {
		*files, *runs, *starts = 50, 3, 1
	}
	cfg := config{files: *files, runs: *runs, starts: *starts, verbose: *verbose, trace: *trace, typescript: *typescript}
	for _, m := range strings.Split(*modes, ",") {
		if m != "overlay" && m != "disk" {
			fmt.Fprintf(os.Stderr, "lsp: unknown mode %q (want overlay or disk)\n", m)
			os.Exit(2)
		}
		cfg.modes = append(cfg.modes, m)
	}
	if err := run(cfg, *orbitRoot, *dir, *keep, *jsonOut); err != nil {
		fmt.Fprintln(os.Stderr, "lsp:", err)
		os.Exit(1)
	}
}

type config struct {
	files, runs, starts int
	modes               []string
	verbose             bool
	trace               string // gopls -rpc.trace log prefix
	typescript          string // typescriptPath, "" for orbit lsp's lookup
	initOpts            map[string]any
}

// probes are the documents and positions the benchmark uses.
type probes struct {
	comp, index         string // relative paths
	compText, indexText string // index has an undefined name and an unknown prop
	next                int    // a component imported by comp
}

// startSample is one orbit lsp start: each event's time since the process
// started (0 when it didn't happen).
type startSample struct {
	Initialize      time.Duration `json:"initialize"`
	OrbitDiag       time.Duration `json:"orbit_diagnostic"`
	GoplsDiag       time.Duration `json:"gopls_diagnostic"`
	FirstHover      time.Duration `json:"first_hover"`
	FirstCompletion time.Duration `json:"first_completion"`
	BarrierProgress time.Duration `json:"barrier_progress_end"`
	References      time.Duration `json:"references"`
	LogGoplsReady   time.Duration `json:"log_gopls_ready"`
	LogBarrier      time.Duration `json:"log_barrier"`
	// FirstTSCompletion is a completion in the probe's <script>, answered
	// by the TypeScript child (started lazily on the didOpen).
	FirstTSCompletion time.Duration `json:"first_ts_completion"`
	TSItems           int           `json:"ts_completion_items"`
	RefCount          int           `json:"reference_count"`
	CompletionItems   int           `json:"completion_items"`
	HoverText         bool          `json:"hover_has_text"`
}

type memSample struct {
	Orbit mem `json:"orbit_lsp_kib"`
	Gopls mem `json:"gopls_kib"`
	TS    mem `json:"typescript_kib"`
}

type modeResult struct {
	Mode       string                     `json:"mode"`
	Starts     []startSample              `json:"starts"`
	Latency    map[string][]time.Duration `json:"latency"`
	MemBarrier memSample                  `json:"memory_after_barrier"`
	MemAfter   memSample                  `json:"memory_after_runs"`
	MemTSAll   mem                        `json:"typescript_after_open_all_kib"`
	Rename     bool                       `json:"rename_provider"`
	Warnings   []string                   `json:"warnings,omitempty"`
}

// latencyRows are the request kinds, in report order.
var latencyRows = []struct{ key, label string }{
	{"completion-selector", "`didChange` + completion, Go selector (`{props.`)"},
	{"completion-ident", "`didChange` + completion, bare identifier (`{up`)"},
	{"completion-props", "`didChange` + completion, component props (`<C… `)"},
	{"hover", "hover (frontmatter variable in the template)"},
	{"definition", "definition (frontmatter func)"},
	{"references-local", "references, local variable"},
	{"references-field", "references, `Props` field (used by importers)"},
	{"prepareRename", "prepareRename, local variable"},
	{"rename", "rename, local variable"},
	{"ts-completion", "`<script>`: `didChange` + completion (`document.`)"},
	{"ts-diagnostics", "`<script>`: `didChange` → TypeScript diagnostic published"},
	{"ts-hover", "`<script>`: hover"},
	{"ts-references-first", "`<script>`: first references (opens every script's document; 1 sample)"},
	{"ts-references", "`<script>`: references, after that"},
}

func run(cfg config, orbitRoot, dir string, keep bool, jsonOut string) error {
	orbitRoot, err := filepath.Abs(orbitRoot)
	if err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(orbitRoot, "orbit.go")); err != nil {
		return fmt.Errorf("-orbit %s: not the Orbit repository (run from bench/lsp or pass -orbit)", orbitRoot)
	}
	if dir == "" {
		if dir, err = os.MkdirTemp("", "orbit-bench-lsp-"); err != nil {
			return err
		}
		if !keep {
			defer os.RemoveAll(dir)
		}
	}
	bin := filepath.Join(dir, ".bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		return err
	}
	orbitBin := filepath.Join(bin, "orbit")
	if err := runCmd(orbitRoot, "go", "build", "-o", orbitBin, "./cmd/orbit"); err != nil {
		return err
	}
	goplsPath, err := exec.LookPath("gopls")
	if err != nil {
		return fmt.Errorf("gopls not on PATH (go install golang.org/x/tools/gopls@latest)")
	}
	goplsVersion, _ := exec.Command(goplsPath, "version").Output()
	if cfg.trace != "" {
		// orbit lsp starts the goplsPath init option with no arguments.
		if cfg.trace, err = filepath.Abs(cfg.trace); err != nil {
			return err
		}
		script := filepath.Join(bin, "gopls-trace")
		sh := fmt.Sprintf("#!/bin/sh\nexec %q -rpc.trace -logfile \"$ORBIT_BENCH_TRACE\" \"$@\"\n", goplsPath)
		if err := os.WriteFile(script, []byte(sh), 0o755); err != nil {
			return err
		}
		cfg.initOpts = map[string]any{"goplsPath": script}
	}
	if cfg.typescript != "" {
		if cfg.initOpts == nil {
			cfg.initOpts = map[string]any{}
		}
		cfg.initOpts["typescriptPath"] = cfg.typescript
	}

	// A fixed salt: every run sees the same content, so gopls's file cache
	// is "as it is" rather than always cold.
	s := synth.NewShape(cfg.files)
	files := synth.ProjectFiles(s, "lsp")
	if err := synth.WriteProject(dir, orbitRoot, files); err != nil {
		return err
	}
	// orbit generate and a go build warm the Go build cache for the
	// project's dependencies (and fill in go.mod's indirect requires).
	if err := runCmd(dir, orbitBin, "generate"); err != nil {
		return err
	}
	if err := runCmd(dir, "go", "build", "-o", filepath.Join(bin, "server"), "./cmd/server"); err != nil {
		return err
	}
	gen, saved := filepath.Join(dir, "orbit_gen"), filepath.Join(dir, ".orbit_gen.saved")

	t := s.Target()
	p := probes{comp: synth.CompPath(t), index: "src/pages/index.orbit", next: t + 1}
	p.compText = files[p.comp]
	idx := files[p.index]
	i := strings.Index(idx, "</h1>")
	if i < 0 {
		return fmt.Errorf("index.orbit has no </h1>")
	}
	i += len("</h1>")
	idx = idx[:i] + "\n  <p>{nope}</p>" + idx[i:]
	// …and an unknown prop on its component, which Orbit reports itself.
	i = strings.Index(idx, `title="x"`)
	if i < 0 {
		return fmt.Errorf("index.orbit has no title=\"x\"")
	}
	p.indexText = idx[:i] + `bogus="x" ` + idx[i:]

	goVersion, _ := exec.Command("go", "env", "GOVERSION").Output()
	fmt.Printf("# lsp: %d .orbit files (%d pages, %d layouts, %d components), %d starts per mode, %d runs + 1 warm-up per request\n",
		s.Files, s.Pages, s.Layouts, s.Components, cfg.starts, cfg.runs)
	fmt.Printf("# %s, %s, GOMAXPROCS=%d, NumCPU=%d, project %s\n", strings.TrimSpace(string(goVersion)),
		firstLine(string(goplsVersion)), runtime.GOMAXPROCS(0), runtime.NumCPU(), dir)
	fmt.Printf("# opened: %s (probes) and %s (with an undefined name and an unknown prop)\n", p.comp, p.index)
	fmt.Printf("# load average at start: %s\n", loadavg())

	var results []*modeResult
	for _, mode := range cfg.modes {
		if mode == "overlay" {
			if _, err := os.Stat(gen); err == nil {
				os.RemoveAll(saved)
				if err := os.Rename(gen, saved); err != nil {
					return err
				}
			}
		} else if _, err := os.Stat(gen); err != nil {
			if err := os.Rename(saved, gen); err != nil {
				return err
			}
		}
		r := &modeResult{Mode: mode, Latency: map[string][]time.Duration{}}
		for k := range cfg.starts {
			if err := runStart(cfg, orbitBin, dir, p, r, k == cfg.starts-1); err != nil {
				return fmt.Errorf("%s mode, start %d: %w", mode, k+1, err)
			}
		}
		results = append(results, r)
		fmt.Fprintf(os.Stderr, "lsp: %s mode done\n", mode)
	}
	fmt.Printf("# load average at end: %s\n\n", loadavg())
	report(cfg, results)
	if jsonOut != "" {
		meta := map[string]any{"files": cfg.files, "runs": cfg.runs, "starts": cfg.starts, "go": strings.TrimSpace(string(goVersion)),
			"gopls": firstLine(string(goplsVersion)), "numcpu": runtime.NumCPU(), "loadavg_end": loadavg(), "durations": "nanoseconds"}
		b, err := json.MarshalIndent(map[string]any{"meta": meta, "modes": results}, "", "  ")
		if err != nil {
			return err
		}
		if err := os.WriteFile(jsonOut, b, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// runStart starts orbit lsp once, records its startup events and, when
// measure is set, the request latencies and memory.
func runStart(cfg config, orbitBin, dir string, p probes, r *modeResult, measure bool) error {
	var env []string
	if cfg.trace != "" {
		env = append(env, fmt.Sprintf("ORBIT_BENCH_TRACE=%s.%s.%d.log", cfg.trace, r.Mode, len(r.Starts)+1))
	}
	sess, dInit, err := startSession(orbitBin, dir, cfg.initOpts, env...)
	if err != nil {
		return err
	}
	defer func() {
		sess.shutdown()
		if cfg.verbose {
			fmt.Fprintf(os.Stderr, "--- orbit lsp log (%s) ---\n%s\n", r.Mode, sess.logText())
		}
	}()
	smp := startSample{Initialize: dInit}
	sess.expectDiag("gopls", p.index, func(ds []diag) bool {
		return slices.ContainsFunc(ds, func(d diag) bool { return d.Source != "orbit" && strings.Contains(d.Message, "nope") })
	})
	sess.expectDiag("orbit", p.index, func(ds []diag) bool {
		return slices.ContainsFunc(ds, func(d diag) bool { return d.Source == "orbit" && strings.Contains(d.Message, "bogus") })
	})
	sess.open(p.comp, p.compText)
	sess.open(p.index, p.indexText)

	// What an editor sends right after opening a file: hover under the
	// mouse, a completion, and (here) references, held for the barrier.
	var wg sync.WaitGroup
	var errs [4]error
	wg.Go(func() {
		res, _, err := sess.call("textDocument/hover", sess.textPos(p.comp, find(p.compText, "{upper} (", 1)))
		smp.FirstHover, errs[0] = time.Since(sess.start), err
		smp.HoverText = strings.Contains(string(res), "upper")
	})
	wg.Go(func() {
		res, _, err := sess.call("textDocument/completion", sess.textPos(p.comp, find(p.compText, "{props.Count}", len("{props."))))
		smp.FirstCompletion, errs[1] = time.Since(sess.start), err
		smp.CompletionItems = len(completionLabels(res))
	})
	wg.Go(func() {
		params := sess.textPos(p.comp, find(p.compText, "Title  string", 0))
		params["context"] = map[string]any{"includeDeclaration": true}
		res, _, err := sess.call("textDocument/references", params)
		smp.References, errs[2] = time.Since(sess.start), err
		smp.RefCount = countLocations(res)
	})
	if strings.Contains(p.compText, "document.querySelectorAll") {
		wg.Go(func() {
			res, _, err := sess.call("textDocument/completion", sess.textPos(p.comp, find(p.compText, "document.querySelectorAll", len("document."))))
			smp.FirstTSCompletion, errs[3] = time.Since(sess.start), err
			smp.TSItems = len(completionLabels(res))
		})
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			return fmt.Errorf("%w\n%s", err, sess.logText())
		}
	}
	indexURI := sess.uri(p.index)
	if !sess.waitFor(60*time.Second, func() bool { return len(sess.diagHit) == 2 }) {
		r.Warnings = append(r.Warnings, fmt.Sprintf("start: a diagnostic for %s never arrived (seen: %v; last: %v)", p.index, sess.diagHit, sess.lastDiags[indexURI]))
	}
	sess.waitFor(5*time.Second, func() bool { _, ok := sess.progEnd["Orbit"]; return ok })
	sess.mu.Lock()
	smp.OrbitDiag = sess.diagHit["orbit"]
	smp.GoplsDiag = sess.diagHit["gopls"]
	smp.BarrierProgress = sess.progEnd["Orbit"]
	sess.mu.Unlock()
	smp.LogGoplsReady, _ = sess.logDuration("gopls ready in ")
	smp.LogBarrier, _ = sess.logDuration("readiness barrier: ")
	r.Starts = append(r.Starts, smp)
	if !measure {
		return nil
	}
	o, g, ts := sess.memory()
	r.MemBarrier = memSample{Orbit: o, Gopls: g, TS: ts}
	r.Rename = sess.hasRename()
	if err := measureLatency(cfg, sess, p, r); err != nil {
		return fmt.Errorf("%w\n%s", err, sess.logText())
	}
	o, g, ts = sess.memory()
	r.MemAfter = memSample{Orbit: o, Gopls: g, TS: ts}
	if err := measureTS(cfg, sess, p, r); err != nil {
		return fmt.Errorf("%w\n%s", err, sess.logText())
	}
	return nil
}

func measureLatency(cfg config, sess *session, p probes, r *modeResult) error {
	base := p.compText
	slot := strings.Index(base, "  <slot />\n")
	if slot < 0 {
		return fmt.Errorf("%s has no <slot />", p.comp)
	}
	// Completion: each run edits the document (a new salt each time) and
	// completes at the inserted probe, timing didChange → response.
	tag := fmt.Sprintf("<C%d ", p.next)
	for _, c := range []struct {
		key, probe, want string
	}{
		{"completion-selector", "<p>{props.}</p>", "Title"},
		{"completion-ident", "<p>{up}</p>", "upper"},
		{"completion-props", tag + "/>", "title"},
	} {
		at := strings.Index(c.probe, ".}")
		switch {
		case at >= 0:
			at++
		case strings.HasSuffix(c.probe, "/>"):
			at = len(tag)
		default:
			at = strings.Index(c.probe, "}")
		}
		for k := range cfg.runs + 1 {
			ins := fmt.Sprintf("  <i>%s %d</i>%s\n", c.key, k, c.probe)
			text := base[:slot] + ins + base[slot:]
			pos := posAt(text, slot+strings.Index(ins, c.probe)+at)
			t := time.Now()
			sess.change(p.comp, text)
			res, _, err := sess.call("textDocument/completion", sess.textPos(p.comp, pos))
			d := time.Since(t)
			if err != nil {
				return fmt.Errorf("%s: %w", c.key, err)
			}
			if k == 0 {
				if labels := completionLabels(res); !slices.Contains(labels, c.want) {
					r.Warnings = append(r.Warnings, fmt.Sprintf("%s: no %q among %d items (%.200s)", c.key, c.want, len(labels), strings.Join(labels, ",")))
				}
				continue
			}
			r.Latency[c.key] = append(r.Latency[c.key], d)
		}
	}
	sess.change(p.comp, base)
	time.Sleep(200 * time.Millisecond)

	refs := func(needle string, delta int) map[string]any {
		m := sess.textPos(p.comp, find(base, needle, delta))
		m["context"] = map[string]any{"includeDeclaration": true}
		return m
	}
	type req struct {
		key, method string
		params      map[string]any
		check       func(json.RawMessage) string
	}
	nonNull := func(what string) func(json.RawMessage) string {
		return func(res json.RawMessage) string {
			if string(res) == "null" || string(res) == "[]" {
				return what + ": empty result"
			}
			return ""
		}
	}
	reqs := []req{
		{"hover", "textDocument/hover", sess.textPos(p.comp, find(base, "{upper} (", 1)), nonNull("hover")},
		{"definition", "textDocument/definition", sess.textPos(p.comp, find(base, "{label(it)}", 1)), nonNull("definition")},
		{"references-local", "textDocument/references", refs("upper :=", 1), nonNull("references")},
		{"references-field", "textDocument/references", refs("Title  string", 0), func(res json.RawMessage) string {
			if n := countLocations(res); n < 3 {
				return fmt.Sprintf("references of the Title field: %d locations", n)
			}
			return ""
		}},
	}
	if r.Rename {
		rn := sess.textPos(p.comp, find(base, "upper :=", 1))
		rn["newName"] = "upperName"
		reqs = append(reqs,
			req{"prepareRename", "textDocument/prepareRename", sess.textPos(p.comp, find(base, "upper :=", 1)), nonNull("prepareRename")},
			req{"rename", "textDocument/rename", rn, nonNull("rename")})
	}
	for _, q := range reqs {
		for k := range cfg.runs + 1 {
			res, d, err := sess.call(q.method, q.params)
			if err != nil {
				return fmt.Errorf("%s: %w", q.key, err)
			}
			if k == 0 {
				if w := q.check(res); w != "" {
					r.Warnings = append(r.Warnings, w)
				}
				continue
			}
			r.Latency[q.key] = append(r.Latency[q.key], d)
		}
	}
	return nil
}

// measureTS measures the <script> requests (PLAN.md §9b): completion and
// diagnostics after an edit, hover, and references, the first of which
// opens every script's document.
func measureTS(cfg config, sess *session, p probes, r *modeResult) error {
	base := p.compText
	anchor := strings.Index(base, "document.querySelectorAll")
	if anchor < 0 {
		r.Warnings = append(r.Warnings, "the probe component has no <script>: no TypeScript rows")
		return nil
	}
	ls := strings.LastIndexByte(base[:anchor], '\n') + 1
	for k := range cfg.runs + 1 {
		ins := fmt.Sprintf("  const v%d = 1;\n  document.\n", k)
		text := base[:ls] + ins + base[ls:]
		pos := posAt(text, ls+strings.Index(ins, "document.")+len("document."))
		t := time.Now()
		sess.change(p.comp, text)
		res, _, err := sess.call("textDocument/completion", map[string]any{"textDocument": map[string]any{"uri": sess.uri(p.comp)},
			"position": pos, "context": map[string]any{"triggerKind": 2, "triggerCharacter": "."}})
		d := time.Since(t)
		if err != nil {
			return fmt.Errorf("ts-completion: %w", err)
		}
		if k == 0 {
			if labels := completionLabels(res); !slices.Contains(labels, "querySelector") {
				r.Warnings = append(r.Warnings, fmt.Sprintf("ts-completion: no querySelector among %d items", len(labels)))
			}
			continue
		}
		r.Latency["ts-completion"] = append(r.Latency["ts-completion"], d)
	}
	for k := range cfg.runs + 1 {
		name := fmt.Sprintf("missing%d", k)
		sess.expectDiag(name, p.comp, func(ds []diag) bool {
			return slices.ContainsFunc(ds, func(d diag) bool { return d.Source == "ts" && strings.Contains(d.Message, name) })
		})
		text := base[:ls] + "  " + name + "();\n" + base[ls:]
		t := time.Since(sess.start)
		sess.change(p.comp, text)
		if !sess.waitFor(10*time.Second, func() bool { _, ok := sess.diagHit[name]; return ok }) {
			r.Warnings = append(r.Warnings, "ts-diagnostics: no diagnostic for "+name)
			break
		}
		if k == 0 {
			continue
		}
		sess.mu.Lock()
		hit := sess.diagHit[name]
		sess.mu.Unlock()
		r.Latency["ts-diagnostics"] = append(r.Latency["ts-diagnostics"], hit-t)
	}
	sess.change(p.comp, base)
	time.Sleep(200 * time.Millisecond)
	at := find(base, "twice(", 1)
	refs := sess.textPos(p.comp, at)
	refs["context"] = map[string]any{"includeDeclaration": true}
	for k := range cfg.runs + 1 {
		res, d, err := sess.call("textDocument/hover", sess.textPos(p.comp, at))
		if err != nil {
			return err
		}
		if k == 0 {
			if !strings.Contains(string(res), "twice") {
				r.Warnings = append(r.Warnings, "ts-hover: "+string(res))
			}
			continue
		}
		r.Latency["ts-hover"] = append(r.Latency["ts-hover"], d)
	}
	for k := range cfg.runs + 1 {
		res, d, err := sess.call("textDocument/references", refs)
		if err != nil {
			return err
		}
		if k == 0 {
			r.Latency["ts-references-first"] = []time.Duration{d}
			if n := countLocations(res); n < 3 {
				r.Warnings = append(r.Warnings, fmt.Sprintf("ts-references: %d locations of twice", n))
			}
			_, _, ts := sess.memory()
			r.MemTSAll = ts
			continue
		}
		r.Latency["ts-references"] = append(r.Latency["ts-references"], d)
	}
	return nil
}

func completionLabels(res json.RawMessage) []string {
	var list struct {
		Items []struct {
			Label string `json:"label"`
		} `json:"items"`
	}
	if json.Unmarshal(res, &list) != nil {
		json.Unmarshal(res, &list.Items)
	}
	out := make([]string, len(list.Items))
	for i, it := range list.Items {
		out[i] = it.Label
	}
	return out
}

func countLocations(res json.RawMessage) int {
	var list []json.RawMessage
	json.Unmarshal(res, &list)
	return len(list)
}

func runCmd(dir, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), goEnv...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%s %s: %v\n%s", name, strings.Join(args, " "), err, out)
	}
	return nil
}

func firstLine(s string) string {
	l, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(l)
}

func loadavg() string {
	b, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return "?"
	}
	f := strings.Fields(string(b))
	if len(f) < 3 {
		return "?"
	}
	return strings.Join(f[:3], " ")
}
