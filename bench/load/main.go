// Command load is the closed-loop HTTP load generator for the Orbit vs.
// Astro benchmark (bench/run.sh drives it).
//
//	load run    -url URL [-c 64] [-warmup 5s] [-d 20s] [-island] [-pid PID] [-server S -scenario X]
//	load check  -a URL -b URL [-island]
//	load report -in runs.jsonl [-meta meta.json] [-m key=value]... [-md out.md] [-json out.json]
//
// run prints one JSON line. With -server/-scenario it's a labeled run for
// report; otherwise it's the bare result.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"maps"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"time"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	var err error
	switch os.Args[1] {
	case "run":
		err = cmdRun(os.Args[2:])
	case "check":
		err = cmdCheck(os.Args[2:])
	case "report":
		err = cmdReport(os.Args[2:])
	default:
		usage()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "load:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: load run|check|report [flags]")
	os.Exit(2)
}

func cmdRun(args []string) error {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	var cfg Config
	fs.StringVar(&cfg.URL, "url", "", "page URL")
	fs.BoolVar(&cfg.Island, "island", false, "after each page request, also GET the page's server island")
	fs.IntVar(&cfg.Concurrency, "c", 64, "concurrent keep-alive connections")
	fs.DurationVar(&cfg.Warmup, "warmup", 5*time.Second, "warm-up (not recorded)")
	fs.DurationVar(&cfg.Duration, "d", 20*time.Second, "measured duration")
	fs.IntVar(&cfg.PID, "pid", 0, "server PID to sample for RSS and CPU")
	fs.DurationVar(&cfg.Timeout, "timeout", 10*time.Second, "per-request timeout")
	server := fs.String("server", "", "server label (labeled output)")
	scenario := fs.String("scenario", "", "scenario label (labeled output)")
	fs.Parse(args)
	if cfg.URL == "" {
		return fmt.Errorf("run: -url is required")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	res, err := Run(ctx, cfg)
	if err != nil {
		return err
	}
	var out any = res
	if *server != "" || *scenario != "" {
		out = LabeledRun{Server: *server, Scenario: *scenario, Result: res}
	}
	b, _ := json.Marshal(out)
	fmt.Println(string(b))
	fmt.Fprintf(os.Stderr, "%-10s %-15s %8.0f req/s  p50 %6.2f ms  p99 %6.2f ms  ttfb p50 %6.2f ms  errors %d\n",
		*server, *scenario, res.RPS, res.Latency.P50, res.Latency.P99, res.TTFB.P50, res.Errors)
	if res.FirstError != "" {
		fmt.Fprintln(os.Stderr, "  first error:", res.FirstError)
	}
	return nil
}

func cmdCheck(args []string) error {
	fs := flag.NewFlagSet("check", flag.ExitOnError)
	a := fs.String("a", "", "first page URL")
	b := fs.String("b", "", "second page URL")
	island := fs.Bool("island", false, "also compare the pages' server island responses")
	fs.Parse(args)
	c := NewClient(2, 10*time.Second)
	p, err := Check(c, *a, *b)
	if err != nil {
		return err
	}
	report := func(p *CheckPair) error {
		fmt.Printf("%s %d bytes (%d text) | %s %d bytes (%d text)", p.A, p.SizeA, p.TextA, p.B, p.SizeB, p.TextB)
		if !p.Equal {
			fmt.Println(" MISMATCH")
			return fmt.Errorf("visible text differs at %d: %q vs %q", p.FirstDiffAt, p.DiffA, p.DiffB)
		}
		fmt.Println(" same text")
		return nil
	}
	if err := report(p); err != nil {
		return err
	}
	if !*island {
		return nil
	}
	ia, err := islandOf(c, *a)
	if err != nil {
		return err
	}
	ib, err := islandOf(c, *b)
	if err != nil {
		return err
	}
	p, err = Check(c, ia, ib)
	if err != nil {
		return err
	}
	return report(p)
}

func islandOf(c *http.Client, page string) (string, error) {
	_, body, err := Get(c, page)
	if err != nil {
		return "", err
	}
	return IslandURL(page, body)
}

func cmdReport(args []string) error {
	fs := flag.NewFlagSet("report", flag.ExitOnError)
	in := fs.String("in", "", "JSONL file of labeled runs")
	meta := fs.String("meta", "", "JSON object of string metadata")
	md := fs.String("md", "", "write Markdown here (default stdout)")
	js := fs.String("json", "", "write the combined JSON here")
	kv := map[string]string{}
	fs.Func("m", "metadata `key=value` (repeatable; wins over -meta)", func(s string) error {
		k, v, ok := strings.Cut(s, "=")
		if !ok {
			return fmt.Errorf("want key=value, got %q", s)
		}
		kv[k] = v
		return nil
	})
	fs.Parse(args)
	f, err := os.Open(*in)
	if err != nil {
		return err
	}
	defer f.Close()
	runs, err := ReadRuns(f)
	if err != nil {
		return err
	}
	m, err := ReadMeta(*meta)
	if err != nil {
		return err
	}
	maps.Copy(m, kv)
	rep := &Report{Meta: m, Runs: runs}
	if *js != "" {
		b, _ := json.MarshalIndent(rep, "", "  ")
		if err := os.WriteFile(*js, append(b, '\n'), 0o644); err != nil {
			return err
		}
	}
	out := rep.Markdown()
	if *md == "" {
		fmt.Print(out)
		return nil
	}
	return os.WriteFile(*md, []byte(out), 0o644)
}
