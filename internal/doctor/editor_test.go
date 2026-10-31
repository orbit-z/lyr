package doctor

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/orbit-z/lyr/editors/nvim"
)

func TestEditorChecks(t *testing.T) {
	home := t.TempDir()
	data := filepath.Join(home, "data")
	f := healthy()
	env := f.env(t.TempDir())
	env.Getenv = func(k string) string {
		if k == "XDG_DATA_HOME" {
			return data
		}
		return ""
	}
	env.Home, env.GOOS = home, "linux"
	nvimDir := filepath.Join(data, "nvim", "site", "pack", "lyr", "start", "lyr")
	vimDir := filepath.Join(home, ".vim", "pack", "lyr", "start", "lyr")

	editorChecks := func() []Check {
		var out []Check
		for _, c := range Run(context.Background(), env).Checks {
			if c.Name == "nvim" || c.Name == "vim" {
				out = append(out, c)
			}
		}
		return out
	}

	// Absent: no line at all.
	if got := editorChecks(); len(got) != 0 {
		t.Errorf("no install: %v", got)
	}

	// Matching.
	if _, err := nvim.Install(nvimDir, "v0.1.0"); err != nil {
		t.Fatal(err)
	}
	got := editorChecks()
	if len(got) != 1 || got[0].Name != "nvim" || got[0].Status != OK || got[0].Fix != "" {
		t.Errorf("matching: %v", got)
	}

	// Stale version (Vim) next to the matching Neovim install.
	if _, err := nvim.Install(vimDir, "v0.0.9"); err != nil {
		t.Fatal(err)
	}
	got = editorChecks()
	if len(got) != 2 || got[1].Name != "vim" || got[1].Status != Warning || got[1].Fix != "lyr editor vim" ||
		got[1].Message != "plugin v0.0.9 in "+vimDir+", but this lyr is v0.1.0" {
		t.Errorf("stale version: %v", got)
	}

	// Same version, different files (a development build).
	if err := os.WriteFile(filepath.Join(nvimDir, nvim.MarkerFile), []byte("v0.1.0\nfiles sha256:0000\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got = editorChecks()
	if len(got) != 2 || got[0].Status != Warning || got[0].Fix != "lyr editor nvim" {
		t.Errorf("stale files: %v", got)
	}
	for _, c := range got {
		if c.Status == Error {
			t.Errorf("an editor check must never fail doctor: %v", c)
		}
	}
}
