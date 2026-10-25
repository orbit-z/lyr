package main

import (
	"bytes"
	"compress/gzip"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"

	"github.com/bertrandkhe/orbit/internal/build"
	"github.com/bertrandkhe/orbit/internal/compiler"
	"github.com/bertrandkhe/orbit/internal/config"
	"github.com/bertrandkhe/orbit/internal/project"
)

// parseNoArgs parses a command's flags and rejects positional arguments. It
// returns -1 to continue, or the exit code.
func parseNoArgs(e *env, c *command, fs *flag.FlagSet, args []string) int {
	if code := parse(fs, args); code >= 0 {
		return code
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(e.stderr, "orbit %s: unexpected argument %q\n", c.name, fs.Arg(0))
		fs.Usage()
		return exitUsage
	}
	return -1
}

// runBuild is orbit build: build the server and its assets into dist/.
func runBuild(e *env, args []string) int {
	c := lookup("build")
	fs := c.flagSet(e)
	embed := fs.Bool("embed", false, "embed dist/client into dist/server, for a single-binary deploy")
	verbose := fs.Bool("verbose", false, "list each page's CSS and JS bundles with their sizes")
	if code := parseNoArgs(e, c, fs, args); code >= 0 {
		return code
	}
	root, err := config.FindRoot(e.dir)
	if err != nil {
		fmt.Fprintln(e.stderr, "orbit:", err)
		return exitFail
	}
	res, err := build.Run(e.ctx, build.Options{Root: root, Embed: *embed})
	if err != nil {
		fmt.Fprintln(e.stderr, "orbit:", err)
		return exitFail
	}
	report(e, res.Generate)
	for _, p := range res.Public {
		fmt.Fprintln(e.stderr, p)
	}
	e.stderr.Write(res.GoOutput)
	if res.Failed() {
		return exitFail
	}
	if *verbose {
		printBundles(e.stdout, res.Generate.Project)
	}
	return exitOK
}

// printBundles is orbit build --verbose: each page's bundles with their raw
// and gzipped sizes, then the totals over distinct files (pages share no
// bundle today, but a later code splitting would).
func printBundles(w io.Writer, p *project.Project) {
	if p == nil || p.Assets == nil {
		return
	}
	var pages []string
	for _, f := range p.Files {
		if f.Info.Role == compiler.RolePage {
			pages = append(pages, f.Path)
		}
	}
	type size struct{ raw, gz int }
	sizes := map[string]size{}
	sizeOf := func(url string) size {
		if s, ok := sizes[url]; ok {
			return s
		}
		var s size
		if f := p.Assets.File(url); f != nil {
			s = size{len(f.Contents), gzipLen(f.Contents)}
		}
		sizes[url] = s
		return s
	}
	width := len("total CSS")
	for _, page := range pages {
		for _, a := range p.Assets.Pages[page] {
			width = max(width, len(a.URL)+2)
		}
	}
	row := func(label string, s size) {
		fmt.Fprintf(w, "%-*s  %10s  gzip %10s\n", width, label, kB(s.raw), kB(s.gz))
	}
	totals := map[string]*size{"css": {}, "js": {}}
	counted := map[string]bool{}
	for _, page := range pages {
		as := p.Assets.Pages[page]
		if len(as) == 0 {
			fmt.Fprintf(w, "%s: no CSS or JS\n", page)
			continue
		}
		fmt.Fprintln(w, page)
		for _, a := range as {
			s := sizeOf(a.URL)
			row("  "+a.URL, s)
			if t := totals[a.Kind]; t != nil && !counted[a.URL] {
				counted[a.URL] = true
				t.raw += s.raw
				t.gz += s.gz
			}
		}
	}
	row("total CSS", *totals["css"])
	row("total JS", *totals["js"])
}

// gzipLen returns the gzipped size of data at the default level.
func gzipLen(data []byte) int {
	var b bytes.Buffer
	zw := gzip.NewWriter(&b)
	zw.Write(data)
	zw.Close()
	return b.Len()
}

// kB formats a size as Vite (and so Astro) does: kilobytes of 1000 bytes,
// two decimals.
func kB(n int) string { return fmt.Sprintf("%.2f kB", float64(n)/1000) }

// runPreview is orbit preview: run dist/server from the project root, with
// the CLI's environment, and exit with its exit code. SIGINT and SIGTERM
// (and SIGHUP on Unix) are forwarded to it. On Unix, the server runs in its
// own process group, so a Ctrl-C in the terminal reaches it once, through
// the CLI, rather than twice (a second SIGINT stops orbit.ListenAndServe at
// once, skipping the graceful shutdown).
func runPreview(e *env, args []string) int {
	c := lookup("preview")
	fs := c.flagSet(e)
	if code := parseNoArgs(e, c, fs, args); code >= 0 {
		return code
	}
	root, err := config.FindRoot(e.dir)
	if err != nil {
		fmt.Fprintln(e.stderr, "orbit:", err)
		return exitFail
	}
	bin := filepath.Join(root, filepath.FromSlash(build.ServerBin))
	if fi, err := os.Stat(bin); err != nil || fi.IsDir() {
		fmt.Fprintf(e.stderr, "orbit preview: no %s in %s; run orbit build first\n", build.ServerBin, root)
		return exitFail
	}
	cmd := exec.Command(bin)
	cmd.Dir = root
	cmd.Stdout, cmd.Stderr = e.stdout, e.stderr
	cmd.SysProcAttr = previewProcAttr()

	sigs := make(chan os.Signal, 1)
	if len(forwardSignals) > 0 {
		signal.Notify(sigs, forwardSignals...)
		defer signal.Stop(sigs)
	}
	if err := cmd.Start(); err != nil {
		fmt.Fprintln(e.stderr, "orbit preview:", err)
		return exitFail
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	for {
		select {
		case s := <-sigs:
			_ = cmd.Process.Signal(s)
		case err := <-done:
			var exit *exec.ExitError
			switch {
			case err == nil:
				return exitOK
			case errors.As(err, &exit) && exit.ExitCode() >= 0:
				return exit.ExitCode()
			case errors.As(err, &exit):
				// Killed by a signal: no exit code to pass on.
				fmt.Fprintf(e.stderr, "orbit preview: %s: %v\n", build.ServerBin, err)
				return exitFail
			default:
				fmt.Fprintln(e.stderr, "orbit preview:", err)
				return exitFail
			}
		}
	}
}
