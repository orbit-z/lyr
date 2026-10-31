package main

import (
	"fmt"
	"os"

	"github.com/orbit-z/lyr/internal/lsp"
	"github.com/orbit-z/lyr/internal/pin"
)

// runLSP is lyr lsp: the language server over stdin/stdout (PLAN.md §9).
// Editors start it; its log goes to stderr.
func runLSP(e *env, args []string) int {
	c := lookup("lsp")
	fs := c.flagSet(e)
	// Many editors' clients pass --stdio; stdio is the only transport.
	fs.Bool("stdio", false, "accepted and ignored: lyr lsp always uses stdio")
	if code := parseNoArgs(e, c, fs, args); code >= 0 {
		return code
	}
	err := lsp.Run(e.ctx, os.Stdin, os.Stdout, lsp.Options{Log: e.stderr, Version: pin.Current().Version})
	if err != nil {
		fmt.Fprintln(e.stderr, "lyr lsp:", err)
		return exitFail
	}
	return exitOK
}
