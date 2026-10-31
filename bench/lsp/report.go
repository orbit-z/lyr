package main

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

var modeTitle = map[string]string{"overlay": "overlay-only", "disk": "`lyr_gen/` on disk"}

func report(cfg config, results []*modeResult) {
	// Startup.
	fmt.Printf("## Startup (process start → event; median of %d starts, min–max)\n\n", cfg.starts)
	header(results, func(r *modeResult) []string { return []string{modeTitle[r.Mode]} })
	startRows := []struct {
		label string
		get   func(startSample) time.Duration
	}{
		{"`initialize` answered", func(s startSample) time.Duration { return s.Initialize }},
		{"Lyr's diagnostic on an opened page (unknown prop)", func(s startSample) time.Duration { return s.LyrDiag }},
		{"first hover answered", func(s startSample) time.Duration { return s.FirstHover }},
		{"first completion answered (`{props.`)", func(s startSample) time.Duration { return s.FirstCompletion }},
		{"first `<script>` completion answered (TypeScript child)", func(s startSample) time.Duration { return s.FirstTSCompletion }},
		{"gopls's diagnostic on that page (undefined name)", func(s startSample) time.Duration { return s.GoplsDiag }},
		{"barrier progress ended", func(s startSample) time.Duration { return s.BarrierProgress }},
		{"**references answered** (held for the barrier)", func(s startSample) time.Duration { return s.References }},
		{"server log: gopls ready (spawn → overlays sent)", func(s startSample) time.Duration { return s.LogGoplsReady }},
		{"server log: the barrier itself", func(s startSample) time.Duration { return s.LogBarrier }},
	}
	for _, row := range startRows {
		cells := []string{row.label}
		for _, r := range results {
			var ds []time.Duration
			for _, s := range r.Starts {
				if d := row.get(s); d > 0 {
					ds = append(ds, d)
				}
			}
			cells = append(cells, startCell(ds, len(r.Starts)))
		}
		fmt.Println("| " + strings.Join(cells, " | ") + " |")
	}
	fmt.Println()
	for _, r := range results {
		var refs, items, tsItems []string
		for _, s := range r.Starts {
			refs = append(refs, fmt.Sprint(s.RefCount))
			items = append(items, fmt.Sprint(s.CompletionItems))
			tsItems = append(tsItems, fmt.Sprint(s.TSItems))
		}
		fmt.Printf("%s: references found %s locations, the first completion had %s items, the first `<script>` completion %s.\n",
			modeTitle[r.Mode], strings.Join(refs, "/"), strings.Join(items, "/"), strings.Join(tsItems, "/"))
	}
	fmt.Println()

	// Latency.
	fmt.Printf("## Latency after the barrier (%d runs: median / p90)\n\n", cfg.runs)
	header(results, func(r *modeResult) []string { return []string{modeTitle[r.Mode] + " median", "p90"} })
	for _, row := range latencyRows {
		cells := []string{row.label}
		any := false
		for _, r := range results {
			ds := r.Latency[row.key]
			if len(ds) == 0 {
				cells = append(cells, "–", "–")
				continue
			}
			any = true
			cells = append(cells, fmtDur(pct(ds, 50)), fmtDur(pct(ds, 90)))
		}
		if !any {
			fmt.Printf("| %s | not advertised (no `renameProvider`) |%s\n", row.label, strings.Repeat(" |", 2*len(results)-1))
			continue
		}
		fmt.Println("| " + strings.Join(cells, " | ") + " |")
	}
	fmt.Println()

	// Memory.
	fmt.Println("## Memory (VmRSS; peak is VmHWM)")
	fmt.Println()
	header(results, func(r *modeResult) []string { return []string{modeTitle[r.Mode]} })
	memRows := []struct {
		label string
		get   func(*modeResult) int64
	}{
		{"lyr lsp, after the barrier", func(r *modeResult) int64 { return r.MemBarrier.Lyr.RSS }},
		{"gopls, after the barrier", func(r *modeResult) int64 { return r.MemBarrier.Gopls.RSS }},
		{"lyr lsp, after the request runs", func(r *modeResult) int64 { return r.MemAfter.Lyr.RSS }},
		{"gopls, after the request runs", func(r *modeResult) int64 { return r.MemAfter.Gopls.RSS }},
		{"lyr lsp, peak", func(r *modeResult) int64 { return r.MemAfter.Lyr.HWM }},
		{"gopls, peak", func(r *modeResult) int64 { return r.MemAfter.Gopls.HWM }},
		{"TypeScript child, after the request runs", func(r *modeResult) int64 { return r.MemAfter.TS.RSS }},
		{"TypeScript child, after opening every script", func(r *modeResult) int64 { return r.MemTSAll.RSS }},
	}
	for _, row := range memRows {
		cells := []string{row.label}
		for _, r := range results {
			cells = append(cells, fmtKiB(row.get(r)))
		}
		fmt.Println("| " + strings.Join(cells, " | ") + " |")
	}
	fmt.Println()
	for _, r := range results {
		for _, w := range r.Warnings {
			fmt.Printf("warning (%s): %s\n", r.Mode, w)
		}
	}
}

func header(results []*modeResult, cols func(*modeResult) []string) {
	h, sep := []string{""}, []string{"---"}
	for _, r := range results {
		for _, c := range cols(r) {
			h = append(h, c)
			sep = append(sep, "---:")
		}
	}
	fmt.Println("| " + strings.Join(h, " | ") + " |")
	fmt.Println("|" + strings.Join(sep, "|") + "|")
}

func startCell(ds []time.Duration, n int) string {
	if len(ds) == 0 {
		return "not seen"
	}
	s := fmtDur(pct(ds, 50))
	if len(ds) > 1 {
		s += fmt.Sprintf(" (%s–%s)", fmtDur(slices.Min(ds)), fmtDur(slices.Max(ds)))
	}
	if len(ds) < n {
		s += fmt.Sprintf(", seen %d/%d", len(ds), n)
	}
	return s
}

// pct is the nearest-rank percentile.
func pct(ds []time.Duration, p int) time.Duration {
	s := slices.Clone(ds)
	slices.Sort(s)
	i := (p*len(s)+99)/100 - 1
	return s[max(i, 0)]
}

func fmtDur(d time.Duration) string {
	ms := float64(d) / float64(time.Millisecond)
	switch {
	case ms >= 1000:
		return fmt.Sprintf("%.2f s", ms/1000)
	case ms >= 100:
		return fmt.Sprintf("%.0f ms", ms)
	case ms >= 10:
		return fmt.Sprintf("%.1f ms", ms)
	default:
		return fmt.Sprintf("%.2f ms", ms)
	}
}

func fmtKiB(k int64) string {
	if k == 0 {
		return "?"
	}
	return fmt.Sprintf("%d MB", (k*1024+500_000)/1_000_000)
}
