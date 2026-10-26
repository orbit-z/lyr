package orbit

import (
	"context"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestAddr(t *testing.T) {
	tests := []struct{ addr, port, want string }{
		{"", "", ":4321"},
		{"", "8080", ":8080"},
		{"127.0.0.1:9000", "8080", "127.0.0.1:9000"},
		{"127.0.0.1:9000", "", "127.0.0.1:9000"},
	}
	for _, tt := range tests {
		t.Setenv("ORBIT_ADDR", tt.addr)
		t.Setenv("PORT", tt.port)
		if got := Addr(); got != tt.want {
			t.Errorf("ORBIT_ADDR=%q PORT=%q: Addr() = %q, want %q", tt.addr, tt.port, got, tt.want)
		}
	}
}

func TestServeGracefulShutdown(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{})
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-release
		io.WriteString(w, "done")
	})
	ctx, cancel := context.WithCancel(context.Background())
	addrc := make(chan net.Addr, 1)
	errc := make(chan error, 1)
	go func() { errc <- serve(ctx, nil, "127.0.0.1:0", h, func(a net.Addr) { addrc <- a }) }()
	addr := <-addrc

	// An in-flight request finishes during the shutdown.
	bodyc := make(chan string, 1)
	go func() {
		resp, err := http.Get("http://" + addr.String() + "/")
		if err != nil {
			bodyc <- "error: " + err.Error()
			return
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		bodyc <- string(b)
	}()
	<-started
	cancel()
	time.Sleep(50 * time.Millisecond)
	close(release)
	if body := <-bodyc; body != "done" {
		t.Errorf("in-flight request: %q", body)
	}
	if err := <-errc; err != nil {
		t.Errorf("serve returned %v, want nil", err)
	}
}

func TestServeListenError(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	err = serve(context.Background(), nil, ln.Addr().String(), http.NotFoundHandler(), nil)
	if err == nil || !strings.Contains(err.Error(), "address already in use") {
		t.Errorf("serve on a used address: %v", err)
	}
}

func TestListenURL(t *testing.T) {
	tests := []struct {
		addr net.Addr
		want string
	}{
		{&net.TCPAddr{Port: 4321}, "http://localhost:4321"},
		{&net.TCPAddr{IP: net.IPv4zero, Port: 4321}, "http://localhost:4321"},
		{&net.TCPAddr{IP: net.IPv6unspecified, Port: 8080}, "http://localhost:8080"},
		{&net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 9000}, "http://127.0.0.1:9000"},
		{&net.TCPAddr{IP: net.IPv6loopback, Port: 9000}, "http://[::1]:9000"},
	}
	for _, tt := range tests {
		if got := listenURL(tt.addr); got != tt.want {
			t.Errorf("listenURL(%v) = %q, want %q", tt.addr, got, tt.want)
		}
	}
}

// TestListenAndServeLogs checks the "listening on" line, and that orbit dev
// (ORBIT_DEV=1) gets none. The server is stopped with a SIGINT to the test
// process, which ListenAndServe catches.
func TestListenAndServeLogs(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sends SIGINT to itself")
	}
	for _, dev := range []bool{false, true} {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		addr := ln.Addr().String()
		ln.Close()
		t.Setenv("ORBIT_ADDR", addr)
		t.Setenv("ORBIT_DEV", map[bool]string{true: "1"}[dev])
		var logs syncLog
		log.SetOutput(&logs)
		flags := log.Flags()
		log.SetFlags(0)
		errc := make(chan error, 1)
		go func() { errc <- ListenAndServe(http.NotFoundHandler()) }()
		for i := 0; ; i++ {
			c, err := net.Dial("tcp", addr)
			if err == nil {
				c.Close()
				break
			}
			if i == 500 {
				t.Fatalf("not listening on %s: %v", addr, err)
			}
			time.Sleep(5 * time.Millisecond)
		}
		if p, err := os.FindProcess(os.Getpid()); err != nil || p.Signal(os.Interrupt) != nil {
			t.Fatal("can't send SIGINT")
		}
		if err := <-errc; err != nil {
			t.Errorf("ListenAndServe: %v", err)
		}
		log.SetOutput(os.Stderr)
		log.SetFlags(flags)
		want := "orbit: listening on http://" + addr + "\n"
		if dev {
			want = ""
		}
		if got := logs.String(); got != want {
			t.Errorf("ORBIT_DEV=%v: log %q, want %q", dev, got, want)
		}
	}
}

type syncLog struct {
	mu sync.Mutex
	b  strings.Builder
}

func (l *syncLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *syncLog) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

func TestServerTimeouts(t *testing.T) {
	if readHeaderTimeout != 10*time.Second || idleTimeout != 120*time.Second || shutdownGrace != 5*time.Second {
		t.Error("timeouts changed; update docs/server.md and the runtime API reference")
	}
}
