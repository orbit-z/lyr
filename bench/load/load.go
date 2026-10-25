package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"os"
	"slices"
	"sync"
	"sync/atomic"
	"time"
)

// Config is one load run.
type Config struct {
	URL         string        // page URL
	Island      bool          // after each page request, GET the page's server island
	Concurrency int           // closed-loop workers, one keep-alive connection each
	Warmup      time.Duration // requests sent but not recorded
	Duration    time.Duration // measured window
	PID         int           // server process to sample (RSS, CPU); 0 = none
	Timeout     time.Duration // per request
}

// Stats summarizes one latency series, in milliseconds.
type Stats struct {
	Count int     `json:"count"`
	Mean  float64 `json:"mean_ms"`
	P50   float64 `json:"p50_ms"`
	P90   float64 `json:"p90_ms"`
	P99   float64 `json:"p99_ms"`
	Max   float64 `json:"max_ms"`
}

// Result is the outcome of one run.
type Result struct {
	URL         string  `json:"url"`
	IslandURL   string  `json:"island_url,omitempty"`
	Concurrency int     `json:"concurrency"`
	WarmupS     float64 `json:"warmup_s"`
	DurationS   float64 `json:"duration_s"`

	// Iterations per second: page requests/s, or page+island pairs/s with Island.
	RPS      float64 `json:"rps"`
	Requests int     `json:"requests"` // HTTP requests in the window (pages + islands)
	Errors   int     `json:"errors"`   // transport errors and non-2xx statuses
	Bytes    int64   `json:"bytes"`    // response body bytes in the window
	// FirstError is the first error seen, to diagnose a failing run.
	FirstError string `json:"first_error,omitempty"`

	Latency       Stats  `json:"latency"`                  // page: request start to last body byte
	TTFB          Stats  `json:"ttfb"`                     // page: request start to first response byte
	IslandLatency *Stats `json:"island_latency,omitempty"` // island request, start to last byte

	Proc *ProcStats `json:"proc,omitempty"`
	// LoaderCPUCores is the load generator's own CPU use (CPU-seconds per
	// wall-second, warm-up included). Near its CPU count means the run
	// measured the load generator, not the server.
	LoaderCPUCores float64 `json:"loader_cpu_cores"`
}

type sample struct{ total, ttfb, island int64 }

// NewClient returns a client with up to n keep-alive connections to one host.
func NewClient(n int, timeout time.Duration) *http.Client {
	tr := &http.Transport{
		Proxy:               nil,
		DialContext:         (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		MaxIdleConns:        n,
		MaxIdleConnsPerHost: n,
		MaxConnsPerHost:     n,
		IdleConnTimeout:     90 * time.Second,
		DisableCompression:  true,
		ForceAttemptHTTP2:   false,
	}
	return &http.Client{Transport: tr, Timeout: timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

// Get fetches url and returns the status and body.
func Get(c *http.Client, url string) (int, []byte, error) {
	resp, err := c.Get(url)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	return resp.StatusCode, b, err
}

// Run executes cfg and returns its result.
func Run(ctx context.Context, cfg Config) (*Result, error) {
	if cfg.Concurrency < 1 {
		return nil, errors.New("concurrency must be at least 1")
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 10 * time.Second
	}
	client := NewClient(cfg.Concurrency, cfg.Timeout)
	defer client.CloseIdleConnections()

	res := &Result{URL: cfg.URL, Concurrency: cfg.Concurrency,
		WarmupS: cfg.Warmup.Seconds(), DurationS: cfg.Duration.Seconds()}

	islandURL := ""
	if cfg.Island {
		status, body, err := Get(client, cfg.URL)
		if err != nil {
			return nil, err
		}
		if status != 200 {
			return nil, fmt.Errorf("GET %s: status %d", cfg.URL, status)
		}
		islandURL, err = IslandURL(cfg.URL, body)
		if err != nil {
			return nil, err
		}
		res.IslandURL = islandURL
	}

	var sampler *ProcSampler
	if cfg.PID > 0 {
		sampler = NewProcSampler(cfg.PID)
		sampler.Baseline()
	}

	start := time.Now()
	selfCPU := cpuTime(os.Getpid())
	measureStart := start.Add(cfg.Warmup)
	end := measureStart.Add(cfg.Duration)
	if sampler != nil {
		go sampler.Run(ctx, measureStart, end)
	}

	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		all      []sample
		requests atomic.Int64
		errs     atomic.Int64
		bytes    atomic.Int64
		firstErr atomic.Value
	)
	fail := func(err error) {
		errs.Add(1)
		firstErr.CompareAndSwap(nil, err.Error())
	}
	for range cfg.Concurrency {
		wg.Go(func() {
			local := make([]sample, 0, 4096)
			var ttfbAt time.Time
			trace := &httptrace.ClientTrace{GotFirstResponseByte: func() { ttfbAt = time.Now() }}
			tctx := httptrace.WithClientTrace(ctx, trace)
			for ctx.Err() == nil {
				t0 := time.Now()
				if t0.After(end) {
					break
				}
				measured := !t0.Before(measureStart)
				n, err := do(client, tctx, cfg.URL)
				t1 := time.Now()
				var s sample
				ok := err == nil
				if measured {
					requests.Add(1)
					bytes.Add(n)
					if err != nil {
						fail(err)
					}
				}
				s.total, s.ttfb = int64(t1.Sub(t0)), int64(ttfbAt.Sub(t0))
				if ok && islandURL != "" {
					t2 := time.Now()
					n, err = do(client, ctx, islandURL)
					s.island = int64(time.Since(t2))
					if measured {
						requests.Add(1)
						bytes.Add(n)
						if err != nil {
							fail(err)
							ok = false
						}
					}
				}
				if measured && ok {
					local = append(local, s)
				}
				if err != nil && ctx.Err() == nil {
					time.Sleep(time.Millisecond) // don't spin on a dead server
				}
			}
			mu.Lock()
			all = append(all, local...)
			mu.Unlock()
		})
	}
	wg.Wait()
	res.LoaderCPUCores = (cpuTime(os.Getpid()) - selfCPU).Seconds() / time.Since(start).Seconds()

	elapsed := cfg.Duration.Seconds()
	res.Requests = int(requests.Load())
	res.Errors = int(errs.Load())
	res.Bytes = bytes.Load()
	if v := firstErr.Load(); v != nil {
		res.FirstError = v.(string)
	}
	res.RPS = float64(len(all)) / elapsed
	res.Latency = summarize(all, func(s sample) int64 { return s.total })
	res.TTFB = summarize(all, func(s sample) int64 { return s.ttfb })
	if islandURL != "" {
		st := summarize(all, func(s sample) int64 { return s.island })
		res.IslandLatency = &st
	}
	if sampler != nil {
		res.Proc = sampler.Stats(len(all))
	}
	return res, ctx.Err()
}

// do sends one GET and drains the body. A non-2xx status is an error.
func do(c *http.Client, ctx context.Context, url string) (int64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}
	resp, err := c.Do(req)
	if err != nil {
		return 0, err
	}
	n, err := io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if err != nil {
		return n, err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return n, fmt.Errorf("GET %s: status %d", url, resp.StatusCode)
	}
	return n, nil
}

func summarize(all []sample, f func(sample) int64) Stats {
	if len(all) == 0 {
		return Stats{}
	}
	v := make([]int64, len(all))
	var sum int64
	for i, s := range all {
		v[i] = f(s)
		sum += v[i]
	}
	slices.Sort(v)
	ms := func(ns int64) float64 { return float64(ns) / 1e6 }
	q := func(p float64) float64 {
		i := int(p*float64(len(v))+0.5) - 1 // nearest rank
		return ms(v[min(max(i, 0), len(v)-1)])
	}
	return Stats{
		Count: len(v),
		Mean:  ms(sum / int64(len(v))),
		P50:   q(0.50),
		P90:   q(0.90),
		P99:   q(0.99),
		Max:   ms(v[len(v)-1]),
	}
}
