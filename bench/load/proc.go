package main

import (
	"bytes"
	"context"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ProcStats is what the sampler saw of the server process.
type ProcStats struct {
	PID int `json:"pid"`
	// RSS before the run (after any earlier runs against the same process).
	BaselineRSSMB float64 `json:"baseline_rss_mb"`
	// Peak and median RSS over the measured window (VmRSS, sampled every 100 ms).
	PeakRSSMB   float64 `json:"peak_rss_mb"`
	SteadyRSSMB float64 `json:"steady_rss_mb"`
	// The process's VmHWM (peak RSS since start) at the end of the window.
	HWMMB float64 `json:"hwm_mb"`
	// CPU time used by the process during the window, in CPU-seconds per
	// wall-second (1.0 = one core fully busy), and per 1000 iterations.
	CPUCores     float64 `json:"cpu_cores"`
	CPUmsPer1000 float64 `json:"cpu_ms_per_1000"`
	Threads      int     `json:"threads"`
}

// ProcSampler samples /proc/<pid> for RSS and CPU time.
type ProcSampler struct {
	pid      int
	mu       sync.Mutex
	baseline int64 // KB
	samples  []int64
	hwm      int64
	threads  int
	cpu0     time.Duration
	cpu1     time.Duration
	t0, t1   time.Time
}

func NewProcSampler(pid int) *ProcSampler { return &ProcSampler{pid: pid} }

// Baseline records the RSS before the run.
func (s *ProcSampler) Baseline() {
	st, _ := readStatus(s.pid)
	s.baseline = st["VmRSS"]
}

// Run samples from start to end.
func (s *ProcSampler) Run(ctx context.Context, start, end time.Time) {
	select {
	case <-time.After(time.Until(start)):
	case <-ctx.Done():
		return
	}
	s.mu.Lock()
	s.t0, s.cpu0 = time.Now(), cpuTime(s.pid)
	s.mu.Unlock()
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		st, err := readStatus(s.pid)
		s.mu.Lock()
		if err == nil {
			s.samples = append(s.samples, st["VmRSS"])
			s.hwm = st["VmHWM"]
			s.threads = int(st["Threads"])
		}
		s.mu.Unlock()
		select {
		case <-ctx.Done():
			return
		case now := <-tick.C:
			if now.After(end) {
				s.mu.Lock()
				s.t1, s.cpu1 = now, cpuTime(s.pid)
				s.mu.Unlock()
				return
			}
		}
	}
}

// Stats returns the summary; iterations is the number of measured iterations.
func (s *ProcSampler) Stats(iterations int) *ProcStats {
	time.Sleep(150 * time.Millisecond) // let Run take its final sample
	s.mu.Lock()
	defer s.mu.Unlock()
	mb := func(kb int64) float64 { return float64(kb) / 1024 }
	ps := &ProcStats{PID: s.pid, BaselineRSSMB: mb(s.baseline), HWMMB: mb(s.hwm), Threads: s.threads}
	if len(s.samples) > 0 {
		v := slices.Clone(s.samples)
		slices.Sort(v)
		ps.PeakRSSMB = mb(v[len(v)-1])
		ps.SteadyRSSMB = mb(v[len(v)/2])
	}
	if !s.t1.IsZero() {
		cpu := s.cpu1 - s.cpu0
		ps.CPUCores = cpu.Seconds() / s.t1.Sub(s.t0).Seconds()
		if iterations > 0 {
			ps.CPUmsPer1000 = float64(cpu.Milliseconds()) * 1000 / float64(iterations)
		}
	}
	return ps
}

// readStatus parses the "Key: value kB" lines of /proc/<pid>/status.
func readStatus(pid int) (map[string]int64, error) {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/status")
	if err != nil {
		return nil, err
	}
	m := map[string]int64{}
	for line := range bytes.Lines(b) {
		k, v, ok := strings.Cut(string(line), ":")
		if !ok {
			continue
		}
		f := strings.Fields(v)
		if len(f) == 0 {
			continue
		}
		if n, err := strconv.ParseInt(f[0], 10, 64); err == nil {
			m[k] = n
		}
	}
	return m, nil
}

// cpuTime returns utime+stime of the process (all threads) from /proc/<pid>/stat.
func cpuTime(pid int) time.Duration {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return 0
	}
	// The command name is in parentheses and may contain spaces; fields after it.
	i := bytes.LastIndexByte(b, ')')
	if i < 0 {
		return 0
	}
	f := strings.Fields(string(b[i+1:]))
	// After ")": state(3) … utime is field 14, stime 15 (1-based) → index 11, 12.
	if len(f) < 13 {
		return 0
	}
	ut, _ := strconv.ParseInt(f[11], 10, 64)
	st, _ := strconv.ParseInt(f[12], 10, 64)
	const clkTck = 100 // USER_HZ on Linux
	return time.Duration(ut+st) * time.Second / clkTck
}
