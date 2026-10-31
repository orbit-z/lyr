package main

import (
	"fmt"

	"github.com/orbit-z/lyr/internal/config"
	"github.com/orbit-z/lyr/internal/dev"
)

// runDev is lyr dev: the dev server (PLAN.md §7). It runs until Ctrl-C,
// then stops the user's server and exits 0.
func runDev(e *env, args []string) int {
	c := lookup("dev")
	fs := c.flagSet(e)
	port := fs.Int("port", dev.DefaultPort, "the port to listen on")
	host := fs.Bool("host", false, "listen on all network interfaces, not only localhost")
	if code := parseNoArgs(e, c, fs, args); code >= 0 {
		return code
	}
	if *port < 1 || *port > 65535 {
		fmt.Fprintf(e.stderr, "lyr dev: invalid port %d\n", *port)
		fs.Usage()
		return exitUsage
	}
	root, err := config.FindRoot(e.dir)
	if err != nil {
		fmt.Fprintln(e.stderr, "lyr:", err)
		return exitFail
	}
	err = dev.Run(e.ctx, dev.Options{Root: root, Port: *port, Host: *host, Stdout: e.stdout, Stderr: e.stderr})
	if err != nil {
		fmt.Fprintln(e.stderr, "lyr dev:", err)
		return exitFail
	}
	return exitOK
}
