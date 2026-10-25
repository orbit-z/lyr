//go:build unix

package orbit

import (
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"testing"
	"time"
)

// TestListenAndServeSignal sends SIGINT to the test process and checks that
// ListenAndServe returns nil.
func TestListenAndServeSignal(t *testing.T) {
	// Keep SIGINT from killing the test binary, whenever it arrives.
	sigc := make(chan os.Signal, 1)
	signal.Notify(sigc, os.Interrupt)
	defer signal.Stop(sigc)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	t.Setenv("ORBIT_ADDR", addr)

	errc := make(chan error, 1)
	go func() { errc <- ListenAndServe(http.NotFoundHandler()) }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		c, err := net.Dial("tcp", addr)
		if err == nil {
			c.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("server didn't start:", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := syscall.Kill(os.Getpid(), syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-errc:
		if err != nil {
			t.Errorf("ListenAndServe = %v, want nil", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("ListenAndServe didn't return after SIGINT")
	}
}
