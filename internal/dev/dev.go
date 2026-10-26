// Package dev is orbit dev (PLAN.md §7): it builds and runs the user's
// server entry with ORBIT_DEV=1, watches the project, rebuilds as little as
// each change needs, restarts the server when its Go code changed, and
// reloads the browser over server-sent events. A proxy in front of the
// server serves /__orbit/* (events, the reload script, the error overlay)
// and forwards everything else.
//
// The pieces:
//   - watch.go: the fsnotify watcher and the 50 ms debounce;
//   - classify.go: what each changed path asks for (§7.1);
//   - pipeline.go: the resident pipeline (esbuild context, type checker,
//     incremental generate, go build -ldflags=-w);
//   - app.go: starting and stopping the user's server;
//   - server.go: the proxy, the reload injection and the event stream;
//   - overlay.go: the error overlay page.
package dev

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/orbit-z/orbit/internal/build"
)

// DefaultPort is the dev server's default port, as Astro's.
const DefaultPort = 4321

// portTries is how many ports Run tries, from Port up, when one is taken.
const portTries = 10

var startTime = time.Now()

// Options configure Run.
type Options struct {
	// Root is the module root.
	Root string
	// Port is the port to listen on (DefaultPort when 0). When it's taken,
	// the next ones are tried, as Astro does.
	Port int
	// Host listens on every interface instead of loopback only.
	Host bool
	// Stdout and Stderr receive orbit dev's messages and the server's
	// output.
	Stdout, Stderr io.Writer
	// Env is appended to the environment of the go commands and the
	// server.
	Env []string
	// Ready, when set, is called with the dev server's URL once the first
	// build is done (for tests).
	Ready func(url string)
	// trace is the pipeline's stage hook (tests).
	trace func(stage string)
}

// Run runs the dev server until ctx is done, then stops the user's server
// and returns nil. Build errors don't end it: they're shown in the terminal
// and the overlay. The error is for problems that stop orbit dev from
// starting (the port, the watcher, the go command).
func Run(ctx context.Context, o Options) error {
	if o.Port == 0 {
		o.Port = DefaultPort
	}
	if o.Stdout == nil {
		o.Stdout = io.Discard
	}
	if o.Stderr == nil {
		o.Stderr = io.Discard
	}
	root, err := filepath.Abs(o.Root)
	if err != nil {
		return err
	}
	goflags, err := build.GoFlags(ctx, root, o.Env)
	if err != nil {
		return fmt.Errorf("go env GOFLAGS: %w", err)
	}

	ls, port, err := listen(o.Host, o.Port)
	if err != nil {
		return err
	}
	s := newServer(root, o.Stderr)
	srv := &http.Server{Handler: s, BaseContext: func(net.Listener) context.Context { return ctx }}
	for _, l := range ls {
		go srv.Serve(l)
	}
	defer func() {
		sctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if srv.Shutdown(sctx) != nil {
			srv.Close()
		}
	}()
	printBanner(o.Stdout, o.Host, port, o.Port)

	w, err := newWatcher(root, func(err error) { fmt.Fprintln(o.Stderr, "orbit dev: watch:", err) })
	if err != nil {
		return fmt.Errorf("watch: %w", err)
	}
	defer w.close()

	d := &devLoop{
		ctx:    ctx,
		o:      o,
		s:      s,
		pl:     newPipeline(root, o.Env, goflags),
		env:    appEnv(os.Environ(), o.Env),
		exited: make(chan *app),
	}
	d.pl.trace = o.trace
	defer d.pl.close()
	defer d.stopApp()

	raw := make(chan event, 64)
	batches := make(chan Change)
	go w.run(ctx, raw)
	go debounce(ctx, raw, batches, Debounce, s.setBusy, func(rel string) bool {
		_, err := os.Lstat(filepath.Join(root, filepath.FromSlash(rel)))
		return err == nil
	})

	start := time.Now()
	d.rebuild(ctx, Change{}, true)
	if ctx.Err() == nil {
		if _, f := s.state(); f == nil {
			fmt.Fprintf(o.Stdout, "orbit dev: server ready in %v\n", round(time.Since(start)))
		}
	}
	if o.Ready != nil {
		o.Ready("http://" + net.JoinHostPort("localhost", strconv.Itoa(port)))
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case c := <-batches:
			d.rebuild(ctx, c, false)
		case a := <-d.exited:
			d.appExited(a)
		}
	}
}

// devLoop is the state of the rebuild loop (one goroutine).
type devLoop struct {
	ctx    context.Context
	o      Options
	s      *server
	pl     *pipeline
	env    []string // the server's environment, without ORBIT_ADDR
	exited chan *app
}

// rebuild runs the pipeline for c and swaps the server when it produced a
// new binary. Requests wait meanwhile.
func (d *devLoop) rebuild(ctx context.Context, c Change, initial bool) {
	s := d.s
	if !initial && c.Empty() {
		s.setIdle()
		return
	}
	s.setBusy()
	defer s.setIdle()
	start := time.Now()
	a, _ := s.state()
	out, err := d.pl.run(ctx, c, a != nil)
	if ctx.Err() != nil {
		return
	}
	if err != nil {
		out.Failure = &failure{Title: "orbit dev error", Text: err.Error()}
	}
	for _, w := range out.Warnings {
		fmt.Fprintln(d.o.Stderr, w)
	}
	what := describe(c)
	if f := out.Failure; f != nil {
		fmt.Fprintln(d.o.Stderr, f.Text)
		d.setFailure(f)
		return
	}
	switch {
	case out.Bin != "":
		d.stopApp()
		na, err := startApp(ctx, out.Bin, d.pl.root, d.env, d.o.Stdout, d.o.Stderr)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			f := &failure{Title: "Your server failed to start", Text: err.Error()}
			var se *startError
			if errors.As(err, &se) {
				f.Output = se.stderr
			}
			fmt.Fprintln(d.o.Stderr, "orbit dev:", f.Text)
			d.setFailure(f)
			return
		}
		d.watchExit(na)
		s.mu.Lock()
		s.app, s.failure = na, nil
		s.mu.Unlock()
		if !initial {
			fmt.Fprintf(d.o.Stdout, "orbit dev: %s: server restarted in %v\n", what, round(time.Since(start)))
		}
	case out.Assets:
		d.clearFailure()
		fmt.Fprintf(d.o.Stdout, "orbit dev: %s: CSS and JS rebuilt in %v\n", what, round(time.Since(start)))
	default:
		d.clearFailure()
		if !initial {
			fmt.Fprintf(d.o.Stdout, "orbit dev: %s: reloaded\n", what)
		}
	}
	s.hub.broadcast()
}

func (d *devLoop) setFailure(f *failure) {
	d.s.mu.Lock()
	d.s.failure = f
	d.s.mu.Unlock()
	d.s.hub.broadcast() // open pages reload into the overlay
}

func (d *devLoop) clearFailure() {
	d.s.mu.Lock()
	d.s.failure = nil
	d.s.mu.Unlock()
}

// stopApp stops the running server, if any.
func (d *devLoop) stopApp() {
	d.s.mu.Lock()
	a := d.s.app
	d.s.app = nil
	d.s.mu.Unlock()
	if a != nil {
		a.stop()
		d.s.transport.CloseIdleConnections()
	}
}

// watchExit reports a's exit on d.exited unless orbit dev stopped it.
func (d *devLoop) watchExit(a *app) {
	go func() {
		<-a.done
		if !a.stoppedByUs() {
			select {
			case d.exited <- a:
			case <-d.ctx.Done():
			}
		}
	}()
}

// appExited handles a server that exited on its own: pages get the
// overlay with its stderr until the next change rebuilds and restarts it.
func (d *devLoop) appExited(a *app) {
	d.s.mu.Lock()
	if d.s.app != a {
		d.s.mu.Unlock()
		return
	}
	d.s.app = nil
	d.s.mu.Unlock()
	f := &failure{Title: "Your server exited", Text: "your server exited: " + exitText(a.err) + "; save a file to restart it", Output: a.tail.String()}
	fmt.Fprintln(d.o.Stderr, "orbit dev:", f.Text)
	d.setFailure(f)
}

// describe names a change for the terminal: its first path, and how many
// others.
func describe(c Change) string {
	switch len(c.Paths) {
	case 0:
		return "rebuild"
	case 1:
		return c.Paths[0]
	}
	return fmt.Sprintf("%s and %d more", c.Paths[0], len(c.Paths)-1)
}

func round(d time.Duration) time.Duration {
	if d < time.Second {
		return d.Round(time.Millisecond)
	}
	return d.Round(10 * time.Millisecond)
}

// appEnv is the user's server's environment (PLAN.md §5b): the process
// environment and extra, plus ORBIT_DEV=1, ORBIT_STATIC, ORBIT_PUBLIC and
// a per-session ORBIT_KEY, unless ORBIT_KEY is already set (the user's key
// wins). ORBIT_ADDR is added per start.
func appEnv(environ, extra []string) []string {
	env := append(append([]string{}, environ...), extra...)
	hasKey := false
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, "ORBIT_KEY="); ok && v != "" {
			hasKey = true
		}
	}
	env = append(env, "ORBIT_DEV=1", "ORBIT_STATIC="+StaticDir, "ORBIT_PUBLIC=public")
	if !hasKey {
		key := make([]byte, 32)
		rand.Read(key)
		env = append(env, "ORBIT_KEY="+base64.StdEncoding.EncodeToString(key))
	}
	return env
}

// listen opens the dev server's listeners: loopback only (127.0.0.1, plus
// [::1] when available), or every interface with host. When port is
// taken, the next ones are tried. It returns the listeners and the port.
func listen(host bool, port int) ([]net.Listener, int, error) {
	var lastErr error
	for p := port; p < port+portTries && p <= 65535; p++ {
		ps := strconv.Itoa(p)
		if host {
			l, err := net.Listen("tcp", ":"+ps)
			if err == nil {
				return []net.Listener{l}, l.Addr().(*net.TCPAddr).Port, nil
			}
			lastErr = err
			if !isAddrInUse(err) {
				return nil, 0, err
			}
			continue
		}
		l4, err := net.Listen("tcp4", "127.0.0.1:"+ps)
		if err != nil {
			lastErr = err
			if !isAddrInUse(err) {
				return nil, 0, err
			}
			continue
		}
		p = l4.Addr().(*net.TCPAddr).Port // when port is 0
		ls := []net.Listener{l4}
		if l6, err := net.Listen("tcp6", "[::1]:"+strconv.Itoa(p)); err == nil {
			ls = append(ls, l6)
		}
		return ls, p, nil
	}
	return nil, 0, lastErr
}

func isAddrInUse(err error) bool {
	return errors.Is(err, syscall.EADDRINUSE)
}

// printBanner prints the URLs, as Astro does.
func printBanner(w io.Writer, host bool, port, wanted int) {
	if port != wanted {
		fmt.Fprintf(w, "orbit dev: port %d is in use, using %d instead\n", wanted, port)
	}
	fmt.Fprintf(w, "\n  Local    http://localhost:%d/\n", port)
	if !host {
		fmt.Fprint(w, "  Network  use --host to expose\n\n")
		return
	}
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		ipn, ok := a.(*net.IPNet)
		if !ok || ipn.IP.IsLoopback() || ipn.IP.To4() == nil {
			continue
		}
		fmt.Fprintf(w, "  Network  http://%s/\n", net.JoinHostPort(ipn.IP.String(), strconv.Itoa(port)))
	}
	fmt.Fprintln(w)
}
