package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/orbit-z/lyr/editors/nvim"
	"github.com/orbit-z/lyr/internal/pin"
)

// editorGetenv and editorHome locate the editors' directories; tests
// replace them.
var (
	editorGetenv = os.Getenv
	editorHome   = func() string { h, _ := os.UserHomeDir(); return h }
)

// runEditor is lyr editor <nvim|vim> [--dir path]: install the Neovim/Vim
// plugin (filetype detection, highlighting, the language server's config)
// in the editor's default package directory.
func runEditor(e *env, args []string) int {
	c := lookup("editor")
	fs := c.flagSet(e)
	dirFlag := fs.String("dir", "", "the directory to install into (default: the editor's start package, pack/lyr/start/lyr)")
	// Flags may come before or after the editor name, as in lyr new.
	var editor string
	for {
		if code := parse(fs, args); code >= 0 {
			return code
		}
		if fs.NArg() == 0 {
			break
		}
		if editor != "" {
			fmt.Fprintf(e.stderr, "lyr editor: unexpected argument %q\n", fs.Arg(0))
			fs.Usage()
			return exitUsage
		}
		editor, args = fs.Arg(0), fs.Args()[1:]
	}
	if editor != nvim.Neovim && editor != nvim.Vim {
		if editor == "" {
			fmt.Fprintln(e.stderr, "lyr editor: missing editor (nvim or vim)")
		} else {
			fmt.Fprintf(e.stderr, "lyr editor: unknown editor %q (nvim or vim)\n", editor)
		}
		fs.Usage()
		return exitUsage
	}
	dir := *dirFlag
	if dir == "" {
		d, err := nvim.DefaultDir(editor, runtime.GOOS, editorGetenv, editorHome())
		if err != nil {
			fmt.Fprintln(e.stderr, "lyr editor:", err)
			return exitFail
		}
		dir = d
	} else if !filepath.IsAbs(dir) {
		dir = filepath.Join(e.dir, dir)
	}
	version := pin.Current().Version
	written, err := nvim.Install(dir, version)
	if err != nil {
		fmt.Fprintln(e.stderr, "lyr editor:", err)
		return exitFail
	}
	name := map[string]string{nvim.Neovim: "Neovim", nvim.Vim: "Vim"}[editor]
	fmt.Fprintf(e.stdout, "Installed the Lyr plugin for %s in %s:\n\n", name, dir)
	for _, p := range written {
		fmt.Fprintf(e.stdout, "\t%s\n", p)
	}
	if editor == nvim.Neovim {
		fmt.Fprint(e.stdout, "\nRestart Neovim (0.11 or later): .lyr files get highlighting, and the plugin starts lyr lsp for them.\n")
	} else {
		fmt.Fprint(e.stdout, "\nRestart Vim: .lyr files get highlighting. For the language server, have your LSP client run lyr lsp.\n")
	}
	fmt.Fprintf(e.stdout, "Run lyr editor %s again after upgrading lyr; lyr doctor tells you when it's out of date.\n", editor)
	return exitOK
}
