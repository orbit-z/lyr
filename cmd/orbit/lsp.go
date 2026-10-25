package main

import (
	"fmt"
	"os"

	"github.com/bertrandkhe/orbit/internal/lsp"
	"github.com/bertrandkhe/orbit/internal/pin"
)

// runLSP is orbit lsp: the language server over stdin/stdout (PLAN.md §9).
// Editors start it; its log goes to stderr.
func runLSP(e *env, args []string) int {
	c := lookup("lsp")
	fs := c.flagSet(e)
	// Many editors' clients pass --stdio; stdio is the only transport.
	fs.Bool("stdio", false, "accepted and ignored: orbit lsp always uses stdio")
	if code := parseNoArgs(e, c, fs, args); code >= 0 {
		return code
	}
	err := lsp.Run(e.ctx, os.Stdin, os.Stdout, lsp.Options{Log: e.stderr, Version: pin.Current().Version})
	if err != nil {
		fmt.Fprintln(e.stderr, "orbit lsp:", err)
		return exitFail
	}
	return exitOK
}
