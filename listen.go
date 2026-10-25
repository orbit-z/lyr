package orbit

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

const (
	readHeaderTimeout = 10 * time.Second
	idleTimeout       = 120 * time.Second
	shutdownGrace     = 5 * time.Second
)

// Addr returns the address to listen on: $ORBIT_ADDR, else ":"+$PORT, else
// ":4321". orbit dev sets ORBIT_ADDR, so a server that starts its own
// http.Server should listen on Addr() to work under orbit dev.
func Addr() string {
	if a := os.Getenv("ORBIT_ADDR"); a != "" {
		return a
	}
	if p := os.Getenv("PORT"); p != "" {
		return ":" + p
	}
	return ":4321"
}

// ListenAndServe serves h on Addr() until SIGINT or SIGTERM, then shuts down
// gracefully, giving in-flight requests up to 5 seconds to finish. It returns
// nil after a graceful shutdown, so log.Fatal(orbit.ListenAndServe(h)) exits
// cleanly on Ctrl-C, and otherwise the error that stopped the server. A
// second signal during the shutdown stops the process at once. It reads no
// command-line flags.
func ListenAndServe(h http.Handler) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return serve(ctx, stop, Addr(), h, nil)
}

// serve listens on addr and serves h until ctx is done, then shuts down
// gracefully. stop, if not nil, is called once ctx is done (it restores the
// default signal behavior). ready, if not nil, is called with the listener's
// address once it's listening.
func serve(ctx context.Context, stop func(), addr string, h http.Handler, ready func(net.Addr)) error {
	srv := &http.Server{
		Addr:              addr,
		Handler:           h,
		ReadHeaderTimeout: readHeaderTimeout,
		IdleTimeout:       idleTimeout,
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	if ready != nil {
		ready(ln.Addr())
	}
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ln) }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
	}
	if stop != nil {
		stop()
	}
	sctx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
	defer cancel()
	err = srv.Shutdown(sctx)
	if serr := <-done; !errors.Is(serr, http.ErrServerClosed) && err == nil {
		err = serr
	}
	if err != nil {
		srv.Close()
		return fmt.Errorf("orbit: shutdown: %w", err)
	}
	return nil
}
