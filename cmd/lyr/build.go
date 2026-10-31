package main

import (
	"bytes"
	"compress/gzip"
	"errors"
	"flag"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"os/signal"
	"path"
	"path/filepath"
	"slices"

	"github.com/orbit-z/lyr/internal/assets"
	"github.com/orbit-z/lyr/internal/build"
	"github.com/orbit-z/lyr/internal/compiler"
	"github.com/orbit-z/lyr/internal/config"
	"github.com/orbit-z/lyr/internal/project"
)

// parseNoArgs parses a command's flags and rejects positional arguments. It
// returns -1 to continue, or the exit code.
func parseNoArgs(e *env, c *command, fs *flag.FlagSet, args []string) int {
	if code := parse(fs, args); code >= 0 {
		return code
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(e.stderr, "lyr %s: unexpected argument %q\n", c.name, fs.Arg(0))
		fs.Usage()
		return exitUsage
	}
	return -1
}

// runBuild is lyr build: build the server and its assets into dist/.
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
		fmt.Fprintln(e.stderr, "lyr:", err)
		return exitFail
	}
	res, err := build.Run(e.ctx, build.Options{Root: root, Embed: *embed})
	if err != nil {
		fmt.Fprintln(e.stderr, "lyr:", err)
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

// printBundles is lyr build --verbose (PLAN.md §5e): each page's bundles
// with their raw and gzipped sizes: its stylesheets, its module, the
// chunks the module imports indented below it with "(preload)", and its
// lazy entries with "(lazy)". Then the chunks that no page preloads (the
// targets of dynamic import(), and chunks only lazy entries import), and
// the totals, which count every CSS and JS file once.
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
	type line struct {
		label, note string
		url         string
	}
	// Each page's rows, in printing order, and the JS files they list.
	listed := map[string]bool{}
	rows := map[string][]line{}
	for _, page := range pages {
		var css, js, pre, lazy []line
		seen := map[string]bool{}
		for _, a := range p.Assets.Pages[page] {
			listed[a.URL] = true
			switch a.Kind {
			case "css":
				css = append(css, line{"  " + a.URL, "", a.URL})
			case "js":
				js = append(js, line{"  " + a.URL, "", a.URL})
			case "modulepreload":
				pre = append(pre, line{"    " + a.URL, "  (preload)", a.URL})
			case "lazy":
				if !seen[a.URL] { // an entry listed under several markers
					seen[a.URL] = true
					lazy = append(lazy, line{"  " + a.URL, "  (lazy)", a.URL})
				}
			}
		}
		rows[page] = slices.Concat(css, js, pre, lazy)
	}
	var dynamic []line
	var totalCSS, totalJS size
	for _, f := range p.Assets.Files {
		url := assets.URLPrefix + f.Name
		switch path.Ext(f.Name) {
		case ".css":
			s := sizeOf(url)
			totalCSS.raw, totalCSS.gz = totalCSS.raw+s.raw, totalCSS.gz+s.gz
		case ".js":
			s := sizeOf(url)
			totalJS.raw, totalJS.gz = totalJS.raw+s.raw, totalJS.gz+s.gz
			if !listed[url] {
				dynamic = append(dynamic, line{"  " + url, "", url})
			}
		}
	}

	width := len("total CSS")
	for _, rs := range append(slices.Collect(maps.Values(rows)), dynamic) {
		for _, r := range rs {
			width = max(width, len(r.label))
		}
	}
	row := func(label string, s size, note string) {
		fmt.Fprintf(w, "%-*s  %10s  gzip %10s%s\n", width, label, kB(s.raw), kB(s.gz), note)
	}
	for _, page := range pages {
		if len(rows[page]) == 0 {
			fmt.Fprintf(w, "%s: no CSS or JS\n", page)
			continue
		}
		fmt.Fprintln(w, page)
		for _, r := range rows[page] {
			row(r.label, sizeOf(r.url), r.note)
		}
	}
	if len(dynamic) > 0 {
		fmt.Fprintln(w, "loaded with import()")
		for _, r := range dynamic {
			row(r.label, sizeOf(r.url), "")
		}
	}
	row("total CSS", totalCSS, "")
	row("total JS", totalJS, "")
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

// runPreview is lyr preview: run dist/server from the project root, with
// the CLI's environment, and exit with its exit code. SIGINT and SIGTERM
// (and SIGHUP on Unix) are forwarded to it. On Unix, the server runs in its
// own process group, so a Ctrl-C in the terminal reaches it once, through
// the CLI, rather than twice (a second SIGINT stops lyr.ListenAndServe at
// once, skipping the graceful shutdown).
func runPreview(e *env, args []string) int {
	c := lookup("preview")
	fs := c.flagSet(e)
	if code := parseNoArgs(e, c, fs, args); code >= 0 {
		return code
	}
	root, err := config.FindRoot(e.dir)
	if err != nil {
		fmt.Fprintln(e.stderr, "lyr:", err)
		return exitFail
	}
	bin := filepath.Join(root, filepath.FromSlash(build.ServerBin))
	if fi, err := os.Stat(bin); err != nil || fi.IsDir() {
		fmt.Fprintf(e.stderr, "lyr preview: no %s in %s; run lyr build first\n", build.ServerBin, root)
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
		fmt.Fprintln(e.stderr, "lyr preview:", err)
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
				fmt.Fprintf(e.stderr, "lyr preview: %s: %v\n", build.ServerBin, err)
				return exitFail
			default:
				fmt.Fprintln(e.stderr, "lyr preview:", err)
				return exitFail
			}
		}
	}
}
