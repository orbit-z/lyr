// Command devloop measures the stages lyr dev will chain after a save
// (PLAN.md §7): regenerate (pass 1, resident type checker), esbuild, go
// build -ldflags=-w, and the server swap, on a synthetic project, per edit
// kind. See README.md.
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/orbit-z/lyr/internal/dev"
)

// useDev is -dev, useTailwind -tailwind.
var useDev, useTailwind bool

// edit is one edit kind.
type edit struct {
	name string
	desc string
	// apply writes the k-th edit and returns the step options.
	apply func(k int) (stepOpts, error)
}

func main() {
	quick := os.Getenv("QUICK") == "1"
	files := flag.Int("files", 200, "number of .lyr files in the synthetic project")
	runs := flag.Int("runs", 7, "measured runs per edit kind (after one warm-up run)")
	lyrRoot := flag.String("lyr", "../..", "path to the Lyr repository (the runtime module)")
	dir := flag.String("dir", "", "project directory (default: a new temporary directory)")
	keep := flag.Bool("keep", false, "keep the project directory")
	ldflags := flag.String("ldflags", "-w", "go build -ldflags value (\"\" for none)")
	only := flag.String("kinds", "", "comma-separated edit kinds to run (default: all)")
	jsonOut := flag.String("json", "", "also write every sample to this JSON file")
	fresh := flag.Bool("fresh", true, "also time a fresh Checker (go list -export + full check) on every Go edit")
	extra := flag.String("buildflags", "", "extra go build flags, space-separated (e.g. \"-p=8\")")
	flag.BoolVar(&fullFront, "fullfront", false, "Load and Front the whole project on every edit instead of project.Reload (the front before per-file reloads)")
	flag.BoolVar(&noPreload, "nopreload", false, "on a Go edit, run go list -export in the check stage instead of from the start of the step (as before Checker.Preload)")
	flag.BoolVar(&useTailwind, "tailwind", false, "the project uses Tailwind CSS (PLAN.md §5d): a global.css root in the Base layout and 12 utilities per .lyr file; needs tailwindcss on PATH and -dev")
	devMode := flag.Bool("dev", false, "measure internal/dev's pipeline (what lyr dev runs) instead of the prototype stages; -ldflags, -buildflags and -fresh don't apply")
	flag.Parse()
	buildFlags = strings.Fields(*extra)
	if quick {
		*files, *runs = 50, 2
	}
	useDev = *devMode
	if useDev {
		*fresh = false
	}
	if err := run(*files, *runs, *lyrRoot, *dir, *keep, *ldflags, *only, *jsonOut, *fresh); err != nil {
		fmt.Fprintln(os.Stderr, "devloop:", err)
		os.Exit(1)
	}
}

func run(nfiles, runs int, lyrRoot, dir string, keep bool, ldflags, only, jsonOut string, fresh bool) error {
	ctx := context.Background()
	lyrRoot, err := filepath.Abs(lyrRoot)
	if err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(lyrRoot, "lyr.go")); err != nil {
		return fmt.Errorf("-lyr %s: not the Lyr repository (run from bench/devloop or pass -lyr)", lyrRoot)
	}
	if dir == "" {
		dir, err = os.MkdirTemp("", "lyr-devloop-")
		if err != nil {
			return err
		}
		if !keep {
			defer os.RemoveAll(dir)
		}
	}
	binDir := filepath.Join(dir, ".bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return err
	}
	// A salt unique to this process: every edit is new content, so no
	// stage is a build-cache hit from an earlier benchmark run.
	var rb [4]byte
	rand.Read(rb[:])
	salt := "s" + hex.EncodeToString(rb[:])

	s := newShape(nfiles)
	if err := writeProject(dir, lyrRoot, projectFiles(s, salt)); err != nil {
		return err
	}
	if useTailwind {
		if !useDev {
			return fmt.Errorf("-tailwind needs -dev")
		}
		if err := tailwindSetup(dir); err != nil {
			return err
		}
		fmt.Println("# -tailwind: global.css (@import \"tailwindcss\") in the Base layout, 12 utilities per .lyr file")
	}
	goVersion, _ := exec.Command("go", "env", "GOVERSION").Output()
	fmt.Printf("# devloop: %d .lyr files (%d pages, %d layouts, %d components; Badge imported by %d), %d runs + 1 warm-up\n",
		s.Files, s.Pages, s.Layouts, s.Components, len(s.badgeImporters()), runs)
	fmt.Printf("# %s, GOMAXPROCS=%d, NumCPU=%d, ldflags=%q, buildflags=%q, project %s\n", strings.TrimSpace(string(goVersion)), runtime.GOMAXPROCS(0), runtime.NumCPU(), ldflags, buildFlags, dir)
	fmt.Printf("# load average at start: %s\n", loadavg())

	pl := newPipeline(dir, binDir, ldflags)
	defer pl.close()
	if useDev {
		pl.dev, err = dev.NewPipeline(ctx, dir, goEnv)
		if err != nil {
			return err
		}
		fmt.Println("# -dev: internal/dev's pipeline (lyr dev's go build flags, esbuild skip, early go build on .go-only edits)")
	}

	// Initial build: with a warm GOCACHE for std and the runtime, this is
	// close to the first lyr dev start.
	start := time.Now()
	if _, err := pl.step(ctx, stepOpts{}); err != nil {
		return fmt.Errorf("initial build: %w", err)
	}
	fmt.Printf("# initial build (first lyr dev start, new user code): %v\n\n", time.Since(start).Round(time.Millisecond))

	t := s.target()
	n := s.chain()
	badgeT := slices.Contains(s.badgeImporters(), t)
	base := func() error {
		if err := writeFile(dir, compPath(t), componentLyr(t, n, badgeT, "", "", "", "")); err != nil {
			return err
		}
		if err := writeFile(dir, badgePath, badgeLyr("")); err != nil {
			return err
		}
		return writeFile(dir, dataPath, dataGo(salt, ""))
	}
	id := func(k int) string { return fmt.Sprintf("%s%d", salt, k) }
	goEdit := stepOpts{fresh: fresh}
	edits := []edit{
		{"noop", "rebuild with nothing changed (the floor)", func(k int) (stepOpts, error) {
			return goEdit, nil
		}},
		{"body", fmt.Sprintf("template text in C%d (rendered at /)", t), func(k int) (stepOpts, error) {
			o := goEdit
			o.verify = "edit-" + id(k)
			o.paths = []string{compPath(t)}
			return o, writeFile(dir, compPath(t), componentLyr(t, n, badgeT, " <em>"+o.verify+"</em>", "", "", ""))
		}},
		{"jsontype", fmt.Sprintf("the type of a lyr.JSON argument in C%d (its .d.ts changes)", t), func(k int) (stepOpts, error) {
			o := goEdit
			o.verify = "F" + id(k)
			o.paths = []string{compPath(t)}
			return o, writeFile(dir, compPath(t), componentLyr(t, n, badgeT, fmt.Sprintf(" <i data-v={lyr.JSON(struct{ %s int }{%d})}></i>", o.verify, k), "", "", ""))
		}},
		{"frontmatter", fmt.Sprintf("a frontmatter statement in C%d", t), func(k int) (stepOpts, error) {
			o := goEdit
			o.verify = "-f" + id(k)
			o.paths = []string{compPath(t)}
			return o, writeFile(dir, compPath(t), componentLyr(t, n, badgeT, "", fmt.Sprintf(" + %q", o.verify), "", ""))
		}},
		{"props", fmt.Sprintf("add a Props field to Badge (imported by %d components)", len(s.badgeImporters())), func(k int) (stepOpts, error) {
			o := goEdit
			o.paths = []string{badgePath}
			return o, writeFile(dir, badgePath, badgeLyr("\tExtra"+id(k)+" string\n"))
		}},
		{"newpage", "add a page (Base layout + C3)", func(k int) (stepOpts, error) {
			name, src := newPage(k)
			o := goEdit
			o.paths = []string{name}
			return o, writeFile(dir, name, strings.Replace(src, "Added page", "Added page "+id(k), 1))
		}},
		{"gofile-body", "internal/data: a function body (export data unchanged)", func(k int) (stepOpts, error) {
			o := goEdit
			o.invalidate = true
			o.paths = []string{dataPath}
			return o, writeFile(dir, dataPath, dataGo(id(k), ""))
		}},
		{"gofile-api", "internal/data: add an exported const (every Props depends on it)", func(k int) (stepOpts, error) {
			o := goEdit
			o.invalidate = true
			o.paths = []string{dataPath}
			return o, writeFile(dir, dataPath, dataGo(salt, fmt.Sprintf("\nconst API%s = %d\n", id(k), k)))
		}},
		{"css", fmt.Sprintf("scoped <style> content in C%d (esbuild only)", t), func(k int) (stepOpts, error) {
			return stepOpts{assetsOnly: true, paths: []string{compPath(t)}}, writeFile(dir, compPath(t), componentLyr(t, n, badgeT, "", "", fmt.Sprintf("\n  .c%d p { margin: %dpx; }", t, k+1), ""))
		}},
		{"ts", fmt.Sprintf("<script> content in C%d (esbuild only)", t), func(k int) (stepOpts, error) {
			return stepOpts{assetsOnly: true, paths: []string{compPath(t)}}, writeFile(dir, compPath(t), componentLyr(t, n, badgeT, "", "", "", fmt.Sprintf("\n  console.log(%q);", id(k))))
		}},
	}
	if only != "" {
		want := strings.Split(only, ",")
		edits = slices.DeleteFunc(edits, func(e edit) bool { return !slices.Contains(want, e.name) })
	}

	results := map[string][]sample{}
	k := 0
	for _, e := range edits {
		// Settle: the edited files back to their base content, built.
		if err := base(); err != nil {
			return err
		}
		if _, err := pl.step(ctx, stepOpts{paths: []string{compPath(t), badgePath, dataPath}}); err != nil {
			return fmt.Errorf("settle before %s: %w", e.name, err)
		}
		for r := 0; r <= runs; r++ {
			k++
			o, err := e.apply(k)
			if err != nil {
				return err
			}
			smp, err := pl.step(ctx, o)
			if err != nil {
				return fmt.Errorf("%s run %d: %w", e.name, r, err)
			}
			if smp.Mismatch != "" {
				return fmt.Errorf("%s run %d: the resident checker's output for %s differs from a fresh check", e.name, r, smp.Mismatch)
			}
			if r > 0 { // run 0 is the warm-up
				results[e.name] = append(results[e.name], smp)
			}
		}
		fmt.Fprintf(os.Stderr, "devloop: %s done\n", e.name)
	}
	fmt.Printf("# load average at end: %s\n\n", loadavg())
	report(edits, results)
	if jsonOut != "" {
		data, err := json.MarshalIndent(results, "", "  ")
		if err != nil {
			return err
		}
		return os.WriteFile(jsonOut, data, 0o644)
	}
	return nil
}

func loadavg() string {
	b, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return "n/a"
	}
	f := strings.Fields(string(b))
	if len(f) < 3 {
		return "n/a"
	}
	return strings.Join(f[:3], " ")
}

// report prints a Markdown table per edit kind: median (min) per stage.
func report(edits []edit, results map[string][]sample) {
	type col struct {
		name string
		get  func(sample) time.Duration
	}
	cols := []col{
		{"front", func(s sample) time.Duration { return s.Front }},
		{"esbuild", func(s sample) time.Duration { return s.Esbuild }},
		{"check", func(s sample) time.Duration { return s.Check }},
		{"gen+write", func(s sample) time.Duration { return s.GenWrite }},
		{"go build", func(s sample) time.Duration { return s.GoBuild }},
		{"(link)", func(s sample) time.Duration { return s.Link }},
		{"stop old", func(s sample) time.Duration { return s.Stop }},
		{"start", func(s sample) time.Duration { return s.Start }},
		{"**binary ready**", sample.binaryReady},
		{"**serving**", sample.serving},
		{"(1st GET /)", func(s sample) time.Duration { return s.FirstReq }},
		{"(check, fresh)", func(s sample) time.Duration { return s.CheckFresh }},
	}
	fmt.Println("Times in ms, median (min). \"binary ready\" = front + esbuild + check + gen+write + go build; \"serving\" adds stop old + start.")
	fmt.Println()
	fmt.Print("| edit |")
	for _, c := range cols {
		fmt.Printf(" %s |", c.name)
	}
	fmt.Println(" written | rechecked | compiled | linked | early build |")
	fmt.Print("|---|")
	for range cols {
		fmt.Print("---:|")
	}
	fmt.Println("---:|---:|---:|---|---|")
	for _, e := range edits {
		ss := results[e.name]
		if len(ss) == 0 {
			continue
		}
		fmt.Printf("| %s |", e.name)
		for _, c := range cols {
			fmt.Printf(" %s |", stat(ss, c.get))
		}
		last := ss[len(ss)-1]
		early := "–"
		switch {
		case last.EarlyUsed:
			early = "used"
		case last.Early:
			early = "rebuilt after"
		}
		fmt.Printf(" %d | %d | %d | %v | %s |\n", last.Written, last.Rechecked, last.Compiled, last.Linked, early)
	}
	fmt.Println()
	for _, e := range edits {
		fmt.Printf("- **%s**: %s\n", e.name, e.desc)
	}
}

func stat(ss []sample, get func(sample) time.Duration) string {
	ds := make([]time.Duration, len(ss))
	for i, s := range ss {
		ds[i] = get(s)
	}
	slices.Sort(ds)
	med := ds[len(ds)/2]
	if len(ds)%2 == 0 {
		med = (ds[len(ds)/2-1] + ds[len(ds)/2]) / 2
	}
	if ds[len(ds)-1] == 0 {
		return "–"
	}
	return fmt.Sprintf("%s (%s)", ms(med), ms(ds[0]))
}

func ms(d time.Duration) string {
	f := float64(d) / float64(time.Millisecond)
	if f < 10 {
		return fmt.Sprintf("%.1f", f)
	}
	return fmt.Sprintf("%.0f", f)
}
