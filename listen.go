package lyr

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"
)

const (
	readHeaderTimeout = 10 * time.Second
	idleTimeout       = 120 * time.Second
	shutdownGrace     = 5 * time.Second
)

// Addr returns the address to listen on: $LYR_ADDR, else ":"+$PORT, else
// ":4321". lyr dev sets LYR_ADDR, so a server that starts its own
// http.Server should listen on Addr() to work under lyr dev.
func Addr() string {
	if a := os.Getenv("LYR_ADDR"); a != "" {
		return a
	}
	if p := os.Getenv("PORT"); p != "" {
		return ":" + p
	}
	return ":4321"
}

// ListenAndServe serves h on Addr() until SIGINT or SIGTERM, then shuts down
// gracefully, giving in-flight requests up to 5 seconds to finish. It returns
// nil after a graceful shutdown, and otherwise the error that stopped the
// server, so call it as
//
//	if err := lyr.ListenAndServe(h); err != nil {
//		log.Fatal(err)
//	}
//
// (log.Fatal always exits 1, even for a nil error). A
// second signal during the shutdown stops the process at once. It reads no
// command-line flags.
//
// Once listening, it logs the server's URL with the log package, such as
// "lyr: listening on http://localhost:4321", except under lyr dev
// (LYR_DEV=1), whose proxy prints its own URL.
func ListenAndServe(h http.Handler) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	var ready func(net.Addr)
	if os.Getenv("LYR_DEV") != "1" {
		ready = func(a net.Addr) { log.Printf("lyr: listening on %s", listenURL(a)) }
	}
	return serve(ctx, stop, Addr(), h, ready)
}

// listenURL is the URL of a server listening on a: an unspecified address
// (":4321", "0.0.0.0", "[::]") is shown as localhost.
func listenURL(a net.Addr) string {
	t, ok := a.(*net.TCPAddr)
	if !ok {
		return "http://" + a.String()
	}
	host := "localhost"
	if t.IP != nil && !t.IP.IsUnspecified() {
		host = t.IP.String()
	}
	return "http://" + net.JoinHostPort(host, strconv.Itoa(t.Port))
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
		return fmt.Errorf("lyr: shutdown: %w", err)
	}
	return nil
}
