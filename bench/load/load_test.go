package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestRunWithIsland(t *testing.T) {
	var pages, islands atomic.Int64
	mux := http.NewServeMux()
	mux.HandleFunc("GET /page", func(w http.ResponseWriter, r *http.Request) {
		pages.Add(1)
		fmt.Fprint(w, `<html><body><orbit-island src="/_orbit/island/b1/Slow-1" props="a+b/c="><p>…</p></orbit-island></body></html>`)
	})
	mux.HandleFunc("GET /_orbit/island/b1/Slow-1", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("p") != "a+b/c=" {
			http.Error(w, "bad token "+r.URL.RawQuery, 400)
			return
		}
		islands.Add(1)
		time.Sleep(2 * time.Millisecond)
		fmt.Fprint(w, "<ul><li>x</li></ul>")
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	res, err := Run(context.Background(), Config{
		URL: srv.URL + "/page", Island: true, Concurrency: 4,
		Warmup: 50 * time.Millisecond, Duration: 300 * time.Millisecond,
		PID: os.Getpid(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Errors != 0 {
		t.Fatalf("errors: %d (%s)", res.Errors, res.FirstError)
	}
	if res.RPS <= 0 || res.Latency.Count == 0 || res.IslandLatency == nil {
		t.Fatalf("empty result: %+v", res)
	}
	if res.IslandLatency.P50 < 2 {
		t.Errorf("island p50 = %.2f ms, want ≥ 2 ms (handler sleeps 2 ms)", res.IslandLatency.P50)
	}
	if res.TTFB.P50 > res.Latency.P50 {
		t.Errorf("TTFB p50 %.3f > latency p50 %.3f", res.TTFB.P50, res.Latency.P50)
	}
	if !strings.HasSuffix(res.IslandURL, "/_orbit/island/b1/Slow-1?p=a%2Bb%2Fc%3D") {
		t.Errorf("island URL = %s", res.IslandURL)
	}
	// Measured requests are a subset of everything served (warm-up included).
	if int64(res.Requests) > pages.Load()+islands.Load() {
		t.Errorf("requests %d > served %d", res.Requests, pages.Load()+islands.Load())
	}
	if res.Proc == nil || res.Proc.PeakRSSMB <= 0 {
		t.Errorf("no RSS samples: %+v", res.Proc)
	}
}

func TestRunCountsErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", 500)
	}))
	defer srv.Close()
	res, err := Run(context.Background(), Config{URL: srv.URL, Concurrency: 2, Duration: 100 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if res.Errors == 0 || res.Latency.Count != 0 || !strings.Contains(res.FirstError, "status 500") {
		t.Fatalf("got %+v", res)
	}
}

func TestIslandURLAstro(t *testing.T) {
	body := `<link rel="preload" as="fetch" href="/_server-islands/SlowList?e=E%3D&amp;p=P%2B&amp;s=" crossorigin="anonymous">`
	got, err := IslandURL("http://h:1/fetch-deferred", []byte(body))
	if err != nil {
		t.Fatal(err)
	}
	if want := "http://h:1/_server-islands/SlowList?e=E%3D&p=P%2B&s="; got != want {
		t.Errorf("got %s, want %s", got, want)
	}
	if _, err := IslandURL("http://h/", []byte("<p>none</p>")); err == nil {
		t.Error("no error for a page without an island")
	}
}

func TestVisibleText(t *testing.T) {
	tests := []struct{ a, b string }{
		{`<p data-o-1>A &#34;q&#34; &amp; &lt;b&gt;</p>
  <span>x</span>`, `<p data-astro-cid-x>A &quot;q&quot; &amp; &lt;b&gt;</p><span>x</span>`},
		{`<style>.a{}</style><script type="module">x()</script><!-- c --><h1>T</h1>`, `<h1>T</h1>`},
	}
	for _, tt := range tests {
		if a, b := VisibleText([]byte(tt.a)), VisibleText([]byte(tt.b)); a != b {
			t.Errorf("%q != %q", a, b)
		}
	}
}

func TestSummarize(t *testing.T) {
	var s []sample
	for i := 1; i <= 100; i++ {
		s = append(s, sample{total: int64(i) * 1e6})
	}
	st := summarize(s, func(x sample) int64 { return x.total })
	if st.P50 != 50 || st.P90 != 90 || st.P99 != 99 || st.Max != 100 || st.Count != 100 {
		t.Errorf("got %+v", st)
	}
}

func TestReportMarkdown(t *testing.T) {
	rep := &Report{
		Meta: map[string]string{"cpu": "X"},
		Runs: []LabeledRun{
			{Server: "orbit-1", Scenario: "cards", Result: &Result{RPS: 1000, Proc: &ProcStats{SteadyRSSMB: 10}}},
			{Server: "astro", Scenario: "fetch-deferred", Result: &Result{RPS: 5, IslandLatency: &Stats{P50: 11}}},
		},
	}
	md := rep.Markdown()
	for _, want := range []string{"| cpu | X |", "Scenario 1", "| orbit-1 | 1000 |", "island p50", "| 11.00 |"} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown lacks %q:\n%s", want, md)
		}
	}
}
