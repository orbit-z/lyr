package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
)

// Run is one labeled load run, one line of the runner's JSONL file.
type LabeledRun struct {
	Server   string  `json:"server"`
	Scenario string  `json:"scenario"`
	Result   *Result `json:"result"`
}

// Report is the full results file.
type Report struct {
	Meta map[string]string `json:"meta"`
	Runs []LabeledRun      `json:"runs"`
}

// ReadRuns reads JSONL runs.
func ReadRuns(r io.Reader) ([]LabeledRun, error) {
	var runs []LabeledRun
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var lr LabeledRun
		if err := json.Unmarshal([]byte(line), &lr); err != nil {
			return nil, err
		}
		runs = append(runs, lr)
	}
	return runs, sc.Err()
}

// ReadMeta reads a flat JSON object of strings; a missing file is empty.
func ReadMeta(path string) (map[string]string, error) {
	m := map[string]string{}
	if path == "" {
		return m, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return m, json.Unmarshal(b, &m)
}

var scenarioTitles = map[string]string{
	"cards":               "Scenario 1: render-heavy page (`/cards`, 500 nested-component cards)",
	"fetch":               "Scenario 2: 10 ms simulated fetch in the frontmatter (`/fetch`)",
	"fetch-deferred":      "Scenario 4: the 10 ms part in a `server:defer` island (`/fetch-deferred`, then its island request)",
	"fetch-deferred-page": "Scenario 4b: the deferred page on its own (`/fetch-deferred`, island not fetched)",
	"fetch-cached":        "Scenario 5: the fetch page from the route cache (`/fetch-cached`, cache hits)",
}

// Markdown renders the report as Markdown tables, one per scenario.
func (rep *Report) Markdown() string {
	var b strings.Builder
	b.WriteString("# Lyr vs. Astro: SSR benchmark\n\n")
	if len(rep.Meta) > 0 {
		b.WriteString("| | |\n|---|---|\n")
		keys := make([]string, 0, len(rep.Meta))
		for k := range rep.Meta {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		for _, k := range keys {
			fmt.Fprintf(&b, "| %s | %s |\n", k, strings.ReplaceAll(rep.Meta[k], "|", `\|`))
		}
		b.WriteString("\n")
	}
	var scenarios []string
	for _, r := range rep.Runs {
		if !slices.Contains(scenarios, r.Scenario) {
			scenarios = append(scenarios, r.Scenario)
		}
	}
	for _, sc := range scenarios {
		title := scenarioTitles[sc]
		if title == "" {
			title = sc
		}
		fmt.Fprintf(&b, "## %s\n\n", title)
		island := sc == "fetch-deferred"
		b.WriteString("| Server | req/s | p50 ms | p90 ms | p99 ms | TTFB p50 | TTFB p99 |")
		if island {
			b.WriteString(" island p50 | island p99 |")
		}
		b.WriteString(" errors | RSS steady / peak MB | CPU cores | CPU ms / 1k req | loader CPU |\n|---|---:|---:|---:|---:|---:|---:|")
		if island {
			b.WriteString("---:|---:|")
		}
		b.WriteString("---:|---:|---:|---:|---:|\n")
		for _, r := range rep.Runs {
			if r.Scenario != sc || r.Result == nil {
				continue
			}
			x := r.Result
			fmt.Fprintf(&b, "| %s | %.0f | %.2f | %.2f | %.2f | %.2f | %.2f |", r.Server, x.RPS,
				x.Latency.P50, x.Latency.P90, x.Latency.P99, x.TTFB.P50, x.TTFB.P99)
			if island {
				if x.IslandLatency != nil {
					fmt.Fprintf(&b, " %.2f | %.2f |", x.IslandLatency.P50, x.IslandLatency.P99)
				} else {
					b.WriteString(" – | – |")
				}
			}
			if p := x.Proc; p != nil {
				fmt.Fprintf(&b, " %d | %.0f / %.0f | %.2f | %.0f |", x.Errors, p.SteadyRSSMB, p.PeakRSSMB, p.CPUCores, p.CPUmsPer1000)
			} else {
				fmt.Fprintf(&b, " %d | – | – | – |", x.Errors)
			}
			fmt.Fprintf(&b, " %.2f |\n", x.LoaderCPUCores)
		}
		if island {
			b.WriteString("\nreq/s here counts iterations: one page request followed by its island request. Latency and TTFB columns are the page's; island columns are the island request's own latency.\n")
		}
		b.WriteString("\n")
	}
	return b.String()
}
