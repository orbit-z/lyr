package orbit

import (
	"context"
	"io"
	"net"
	"net/http"
	"strings"
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

func TestServerTimeouts(t *testing.T) {
	if readHeaderTimeout != 10*time.Second || idleTimeout != 120*time.Second || shutdownGrace != 5*time.Second {
		t.Error("timeouts changed; update docs/server.md and the runtime API reference")
	}
}
