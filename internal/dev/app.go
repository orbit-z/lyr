package dev

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os/exec"
	"sync"
	"time"
)

// Stop and start limits (PLAN.md §7.1 step 3).
var (
	// stopGrace is how long a stopped app may take to shut down after
	// SIGINT before it's killed.
	stopGrace = 5 * time.Second
	// acceptTimeout is how long a started app may take to accept
	// connections on ORBIT_ADDR.
	acceptTimeout = 10 * time.Second
)

// app is one running instance of the user's server.
type app struct {
	cmd  *exec.Cmd
	addr string // 127.0.0.1:<port>, its ORBIT_ADDR
	done chan struct{}
	err  error // cmd.Wait's result, set before done is closed
	tail *tailBuffer

	mu       sync.Mutex
	stopping bool
}

// startApp runs bin in dir with env (plus ORBIT_ADDR on a fresh localhost
// port) and waits until it accepts connections. Its output goes to stdout
// and stderr; the end of its stderr is kept for the overlay. When it exits
// or doesn't accept connections in time, the error holds its stderr.
func startApp(ctx context.Context, bin, dir string, env []string, stdout, stderr io.Writer) (*app, error) {
	addr, err := freeAddr()
	if err != nil {
		return nil, err
	}
	a := &app{addr: addr, done: make(chan struct{}), tail: &tailBuffer{max: 32 << 10}}
	cmd := exec.Command(bin)
	cmd.Dir = dir
	cmd.Env = append(env, "ORBIT_ADDR="+addr)
	cmd.Stdout = stdout
	cmd.Stderr = io.MultiWriter(stderr, a.tail)
	cmd.SysProcAttr = appProcAttr()
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	a.cmd = cmd
	go func() {
		a.err = cmd.Wait()
		close(a.done)
	}()

	deadline := time.Now().Add(acceptTimeout)
	for {
		c, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if err == nil {
			c.Close()
			return a, nil
		}
		select {
		case <-a.done:
			return nil, &startError{msg: "your server exited before it accepted connections: " + exitText(a.err), stderr: a.tail.String()}
		case <-ctx.Done():
			a.stop()
			return nil, ctx.Err()
		case <-time.After(2 * time.Millisecond):
		}
		if time.Now().After(deadline) {
			a.stop()
			return nil, &startError{
				msg:    fmt.Sprintf("your server didn't accept connections on ORBIT_ADDR=%s within %v; does it listen on orbit.Addr()?", addr, acceptTimeout),
				stderr: a.tail.String(),
			}
		}
	}
}

// exitText describes how a process ended.
func exitText(err error) string {
	var exit *exec.ExitError
	switch {
	case err == nil:
		return "exit status 0"
	case errors.As(err, &exit):
		return exit.String()
	}
	return err.Error()
}

// stop stops the app: SIGINT, then a kill after stopGrace. It returns once
// the process has exited.
func (a *app) stop() {
	a.mu.Lock()
	a.stopping = true
	a.mu.Unlock()
	select {
	case <-a.done:
		return
	default:
	}
	if err := interrupt(a.cmd.Process); err != nil {
		a.cmd.Process.Kill()
	}
	select {
	case <-a.done:
	case <-time.After(stopGrace):
		a.cmd.Process.Kill()
		<-a.done
	}
}

// stoppedByUs reports whether stop was called.
func (a *app) stoppedByUs() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.stopping
}

// startError is a server that failed to start: the overlay shows msg and
// the server's stderr.
type startError struct {
	msg    string
	stderr string
}

func (e *startError) Error() string { return e.msg }

// freeAddr returns a free 127.0.0.1 port.
func freeAddr() (string, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	defer l.Close()
	return l.Addr().String(), nil
}

// tailBuffer keeps the last max bytes written to it.
type tailBuffer struct {
	mu  sync.Mutex
	max int
	b   []byte
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.b = append(t.b, p...)
	if over := len(t.b) - t.max; over > 0 {
		t.b = append(t.b[:0], t.b[over:]...)
	}
	return len(p), nil
}

func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.b)
}
