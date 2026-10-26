package project

// Generate-speed benchmarks: the full orbit generate pipeline on a synthetic
// 200-file project (40 pages, half of them with route params, and 160
// components with props, default and named slots, nested calls and Go
// package imports). Run with
//
//	go test ./internal/project -run '^$' -bench Generate -benchtime 10x
//
// Each benchmark reports the stage breakdown as custom metrics (ms/op per
// stage; see Timings) and files written per run.

import (
	"context"
	"fmt"
	"math/rand/v2"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/orbit-z/orbit/internal/assets"
	"github.com/orbit-z/orbit/internal/compiler"
)

const (
	benchPages      = 40
	benchComponents = 160
)

// benchFiles returns the synthetic project. salt goes into the user Go
// package, so a different salt makes go list -export recompile it.
func benchFiles(salt string) map[string]string {
	files := map[string]string{
		"internal/data/data.go": `package data

import "time"

// Salt: ` + salt + `

type Item struct {
	ID    int
	Name  string
	Price float64
	Tags  []string
	When  time.Time
	Draft bool
}

type Status int

func (s Status) String() string { return [...]string{"draft", "live"}[s] }

func Items(n int) []Item {
	out := make([]Item, n)
	for i := range out {
		out[i] = Item{ID: i, Name: "item", Price: 1.5, Tags: []string{"a", "b"}}
	}
	return out
}
`,
	}
	// Components form layers: component i renders i+1 and i+2 (when they
	// exist), so calls nest about 80 deep and every component is used.
	for i := range benchComponents {
		var imports, calls strings.Builder
		for _, j := range []int{i + 1, i + 2} {
			if j < benchComponents {
				fmt.Fprintf(&imports, "\tC%d \"./C%d.orbit\"\n", j, j)
				fmt.Fprintf(&calls, "  {if props.Depth > 0}<C%d title={props.Title} count={props.Count + 1} items={props.Items} depth={props.Depth - 1}><span>{props.Count}</span><p slot=\"footer\">f</p></C%d>{/if}\n", j, j)
			}
		}
		files[fmt.Sprintf("src/components/C%d.orbit", i)] = fmt.Sprintf(`---
import (
	"fmt"
	"strings"

	"example.com/blog/internal/data"
%s)

type Props struct {
	Title  string
	Count  int
	Items  []data.Item
	Active bool
	Depth  int
}

func label(it data.Item) string { return fmt.Sprintf("%%s-%%d", it.Name, it.ID) }

upper := strings.ToUpper(props.Title)
status := data.Status(props.Count %% 2)
---
<section class="c%d" data-count={props.Count} data-active={props.Active}>
  <h2 title={upper}>{upper} ({status})</h2>
  <ul>
    {for _, it := range props.Items}
      <li id={label(it)} class={strings.Join(it.Tags, " ")}>{it.Name}: {it.Price} {it.ID} {it.Draft}</li>
    {/for}
  </ul>
  <slot />
%s  <footer><slot name="footer">none</slot></footer>
</section>
`, imports.String(), i, calls.String())
	}
	files["src/layouts/Base.orbit"] = `---
type Props struct{ Title string }
---
<!DOCTYPE html>
<html><head><title>{props.Title}</title></head><body><slot /></body></html>
`
	for i := range benchPages {
		name := fmt.Sprintf("src/pages/p%d.orbit", i)
		title := `"Page"`
		switch i % 4 {
		case 0, 1:
			name = fmt.Sprintf("src/pages/s%d/[slug].orbit", i)
			title = "params.Slug"
		case 2:
			if i%8 == 2 {
				name = fmt.Sprintf("src/pages/r%d/[...rest].orbit", i)
				title = "params.Rest"
			}
		}
		c := (i * 4) % benchComponents
		files[name] = fmt.Sprintf(`---
import (
	"example.com/blog/internal/data"
	Base "../../layouts/Base.orbit"
	C%d "../../components/C%d.orbit"
)

items := data.Items(3)
---
<Base title={%s}>
  <h1>{%s}</h1>
  <C%d title="x" count={1} items={items} active depth={2}>body</C%d>
</Base>
`, c, c, title, title, c, c)
		if !strings.Contains(name, "[") {
			files[name] = strings.ReplaceAll(files[name], "../../", "../")
		}
	}
	return files
}

func reportTimings(b *testing.B, ts []Timings, written int) {
	var sum Timings
	for _, t := range ts {
		sum.Load += t.Load
		sum.Front += t.Front
		sum.Assets += t.Assets
		sum.GoList += t.GoList
		sum.Check += t.Check
		sum.Extras += t.Extras
		sum.Write += t.Write
		sum.Total += t.Total
	}
	n := float64(len(ts))
	ms := func(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) / n }
	b.ReportMetric(ms(sum.Load), "load-ms")
	b.ReportMetric(ms(sum.Front), "front-ms")
	b.ReportMetric(ms(sum.Assets), "assets-ms")
	b.ReportMetric(ms(sum.GoList), "golist-ms")
	b.ReportMetric(ms(sum.Check), "check-ms")
	b.ReportMetric(ms(sum.Extras), "extras-ms")
	b.ReportMetric(ms(sum.Write), "write-ms")
	b.ReportMetric(ms(sum.Total), "total-ms")
	b.ReportMetric(float64(written)/n, "written/op")
}

func benchRun(b *testing.B, g *Generator) *Result {
	res, err := g.Run(context.Background())
	if err != nil {
		b.Fatal(err)
	}
	if res.HasErrors() || len(res.Warnings) > 0 {
		b.Fatalf("diagnostics:\n%s\nwarnings: %v", joinLines(diagStrings(res.Diagnostics)), res.Warnings)
	}
	for _, f := range res.Project.Files {
		if !f.Typed {
			b.Fatalf("%s: not type-checked (a type error in the synthetic project?)", f.Path)
		}
	}
	return res
}

func benchGenerator(dir string) *Generator {
	return &Generator{Root: dir, Env: []string{"GOFLAGS=-mod=mod", "GOWORK=off"}}
}

// BenchmarkGenerate measures orbit generate on the synthetic project:
//   - cold: a fresh project and Generator per run, with a user package the
//     build cache hasn't seen (std and the runtime are cached), and no
//     orbit_gen/;
//   - warm: the same project again after one component body edit (its
//     render.go rewritten);
//   - nochange: the same project again with no edit (nothing rewritten);
//   - emptycache: like cold, with an empty GOCACHE, so go list also
//     compiles std and the runtime (only with ORBIT_BENCH_EMPTYCACHE=1: it
//     takes seconds per run).
func BenchmarkGenerate(b *testing.B) {
	needGo(b)
	b.Run("emptycache", func(b *testing.B) {
		if os.Getenv("ORBIT_BENCH_EMPTYCACHE") != "1" {
			b.Skip("set ORBIT_BENCH_EMPTYCACHE=1")
		}
		var ts []Timings
		written := 0
		for i := 0; i < b.N; i++ {
			b.StopTimer()
			p := newTestProject(b, benchFiles(fmt.Sprint(rand.Uint64())))
			g := benchGenerator(p.Dir)
			g.Env = append(g.Env, "GOCACHE="+b.TempDir())
			b.StartTimer()
			res := benchRun(b, g)
			ts = append(ts, res.Timings)
			written += len(res.Write.Written)
		}
		reportTimings(b, ts, written)
	})
	b.Run("cold", func(b *testing.B) {
		var ts []Timings
		written := 0
		for i := 0; i < b.N; i++ {
			b.StopTimer()
			p := newTestProject(b, benchFiles(fmt.Sprint(rand.Uint64())))
			b.StartTimer()
			res := benchRun(b, benchGenerator(p.Dir))
			ts = append(ts, res.Timings)
			written += len(res.Write.Written)
		}
		reportTimings(b, ts, written)
	})
	b.Run("warm", func(b *testing.B) {
		p := newTestProject(b, benchFiles("warm"))
		g := benchGenerator(p.Dir)
		benchRun(b, g)
		var ts []Timings
		written := 0
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			b.StopTimer()
			p.write("src/components/C7.orbit", strings.Replace(benchFiles("")["src/components/C7.orbit"],
				"upper := strings.ToUpper(props.Title)", fmt.Sprintf("upper := strings.ToUpper(props.Title) + %q", fmt.Sprint(i)), 1))
			b.StartTimer()
			res := benchRun(b, g)
			ts = append(ts, res.Timings)
			written += len(res.Write.Written)
		}
		reportTimings(b, ts, written)
	})
	b.Run("nochange", func(b *testing.B) {
		p := newTestProject(b, benchFiles("nochange"))
		g := benchGenerator(p.Dir)
		benchRun(b, g)
		var ts []Timings
		written := 0
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			res := benchRun(b, g)
			ts = append(ts, res.Timings)
			written += len(res.Write.Written) + len(res.Write.Removed)
		}
		if written != 0 {
			b.Errorf("a no-change run wrote %d files", written)
		}
		reportTimings(b, ts, written)
	})
}

// BenchmarkPass2 measures pass 2 on its own (re-emitting every file with its
// typed writes, sequentially), to split BenchmarkGenerate's check-ms into
// type checking and re-emission.
func BenchmarkPass2(b *testing.B) {
	needGo(b)
	p := newTestProject(b, benchFiles("pass2"))
	res := benchRun(b, benchGenerator(p.Dir))
	ins := make([]*compiler.Input, 0, len(res.Project.Files))
	for _, f := range res.Project.Files {
		in := *f.Input
		in.Typed = f.Writes
		ins = append(ins, &in)
	}
	for b.Loop() {
		for _, in := range ins {
			compiler.Generate(in)
		}
	}
}

// BenchmarkAssets measures esbuild on the synthetic project with a scoped
// <style> and a TypeScript <script> in every sixth component (27 of each),
// from loaded and parsed files (Front) to the bundles in memory:
//
//	go test ./internal/project -run '^$' -bench Assets
func BenchmarkAssets(b *testing.B) {
	files := benchFiles("assets")
	files["src/scripts/util.ts"] = "export const twice = (n: number): number => n * 2;\n"
	for i := 0; i < benchComponents; i += 6 {
		name := fmt.Sprintf("src/components/C%d.orbit", i)
		files[name] += fmt.Sprintf("\n<style>\n  .c%d h2 { color: red; }\n  .c%d li:hover { color: blue; }\n</style>\n"+
			"<script>\n  import { twice } from \"../scripts/util\";\n  const n: number = twice(%d);\n  document.querySelectorAll(\".c%d\").forEach((el) => el.setAttribute(\"data-n\", String(n)));\n</script>\n", i, i, i, i)
	}
	p := newTestProject(b, files)
	proj, err := Load(p.Dir)
	if err != nil {
		b.Fatal(err)
	}
	if proj.Front() {
		b.Fatal(joinLines(diagStrings(proj.Diagnostics)))
	}
	for _, tt := range []struct {
		name string
		opts assets.Options
	}{
		{"prod", assets.Options{Root: proj.Root}},
		{"dev-rebuild", assets.Options{Root: proj.Root, Dev: true}},
	} {
		b.Run(tt.name, func(b *testing.B) {
			bl := assets.New(tt.opts)
			defer bl.Close()
			for b.Loop() {
				if tt.name == "prod" {
					bl.Close() // a fresh context each time, as orbit build
				}
				ds, err := proj.Bundle(bl)
				if err != nil || HasErrors(ds) {
					b.Fatal(err, diagStrings(ds))
				}
			}
			b.ReportMetric(float64(len(proj.Assets.Files)), "files")
		})
	}
}
