package main

import (
	"fmt"

	"github.com/bertrandkhe/orbit/internal/check"
	"github.com/bertrandkhe/orbit/internal/config"
)

// runCheck is orbit check: type-check the Go and TypeScript of the project
// around the working directory without building it.
func runCheck(e *env, args []string) int {
	c := lookup("check")
	fs := c.flagSet(e)
	if code := parseNoArgs(e, c, fs, args); code >= 0 {
		return code
	}
	root, err := config.FindRoot(e.dir)
	if err != nil {
		fmt.Fprintln(e.stderr, "orbit:", err)
		return exitFail
	}
	res, err := check.Run(e.ctx, check.Options{Root: root})
	if err != nil {
		fmt.Fprintln(e.stderr, "orbit:", err)
		return exitFail
	}
	report(e, res.Generate)
	e.stderr.Write(res.GoOutput)
	if res.Notice != "" {
		fmt.Fprintln(e.stderr, res.Notice)
	}
	e.stderr.Write(res.TSOutput)
	if res.Failed() {
		return exitFail
	}
	return exitOK
}
