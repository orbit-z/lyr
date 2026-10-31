package doctor

import (
	"bytes"
	"os"
	"path/filepath"

	"github.com/orbit-z/lyr/editors/nvim"
)

func home() string {
	h, _ := os.UserHomeDir()
	return h
}

// editorChecks: the Neovim and Vim plugins installed by lyr editor, in
// their default directories, against this CLI. An editor without an
// install gets no line, so people who don't use it aren't nagged.
func (d *doctor) editorChecks() []Check {
	if d.env.Getenv == nil {
		return nil
	}
	want := nvim.Marker(d.env.Self.Version)
	var out []Check
	for _, editor := range []string{nvim.Neovim, nvim.Vim} {
		dir, err := nvim.DefaultDir(editor, d.env.GOOS, d.env.Getenv, d.env.Home)
		if err != nil {
			continue
		}
		have, err := os.ReadFile(filepath.Join(dir, nvim.MarkerFile))
		if err != nil {
			continue
		}
		fix := "lyr editor " + editor
		haveV, wantV := nvim.MarkerVersion(have), nvim.MarkerVersion(want)
		switch {
		case bytes.Equal(have, want):
			out = append(out, ok(editor, "plugin %s in %s", haveV, dir))
		case haveV != wantV:
			out = append(out, warn(editor, fix, "plugin %s in %s, but this lyr is %s", haveV, dir, wantV))
		default:
			out = append(out, warn(editor, fix, "plugin in %s is out of date: its files differ from this lyr %s's", dir, wantV))
		}
	}
	return out
}
