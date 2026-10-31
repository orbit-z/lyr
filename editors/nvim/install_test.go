package nvim

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestFiles(t *testing.T) {
	got := Files()
	for _, want := range []string{"ftdetect/lyr.vim", "ftplugin/lyr.vim", "lsp/lyr.lua", "plugin/lyr.lua", "syntax/lyr.vim"} {
		if !slices.Contains(got, want) {
			t.Errorf("Files() = %v, missing %s", got, want)
		}
	}
	for _, p := range got {
		if strings.HasPrefix(p, "test/") || strings.HasSuffix(p, ".go") || strings.HasSuffix(p, ".md") {
			t.Errorf("Files() has %s", p)
		}
	}
}

func TestDefaultDir(t *testing.T) {
	env := func(kv ...string) func(string) string {
		return func(k string) string {
			for i := 0; i < len(kv); i += 2 {
				if kv[i] == k {
					return kv[i+1]
				}
			}
			return ""
		}
	}
	pack := filepath.Join("pack", "lyr", "start", "lyr")
	for _, tc := range []struct {
		editor, goos string
		getenv       func(string) string
		home, want   string
	}{
		{Neovim, "linux", env(), "/h", filepath.Join("/h", ".local", "share", "nvim", "site", pack)},
		{Neovim, "darwin", env("XDG_DATA_HOME", "/x"), "/h", filepath.Join("/x", "nvim", "site", pack)},
		{Neovim, "linux", env("NVIM_APPNAME", "lazy", "XDG_DATA_HOME", "/x"), "/h", filepath.Join("/x", "lazy", "site", pack)},
		{Neovim, "windows", env("LOCALAPPDATA", `C:\L`), `C:\h`, filepath.Join(`C:\L`, "nvim-data", "site", pack)},
		{Neovim, "windows", env(), `C:\h`, filepath.Join(`C:\h`, "AppData", "Local", "nvim-data", "site", pack)},
		{Vim, "linux", env("XDG_DATA_HOME", "/x"), "/h", filepath.Join("/h", ".vim", pack)},
		{Vim, "windows", env(), `C:\h`, filepath.Join(`C:\h`, "vimfiles", pack)},
	} {
		got, err := DefaultDir(tc.editor, tc.goos, tc.getenv, tc.home)
		if err != nil || got != tc.want {
			t.Errorf("DefaultDir(%s, %s) = %q, %v; want %q", tc.editor, tc.goos, got, err, tc.want)
		}
	}
	if _, err := DefaultDir(Neovim, "linux", env(), ""); err == nil {
		t.Error("no home: want an error")
	}
	if _, err := DefaultDir("emacs", "linux", env(), "/h"); err == nil {
		t.Error("emacs: want an error")
	}
}

func TestMarker(t *testing.T) {
	m := Marker("v1.2.3")
	if MarkerVersion(m) != "v1.2.3" || !strings.Contains(string(m), "\nfiles sha256:") {
		t.Errorf("Marker = %q", m)
	}
	if MarkerVersion(Marker("")) != "unknown" {
		t.Errorf("Marker(\"\") = %q", Marker(""))
	}
	if string(Marker("v1")) != string(Marker("v1")) {
		t.Error("Marker isn't deterministic")
	}
}

func TestInstall(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "lyr")
	written, err := Install(dir, "v1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if want := append(Files(), MarkerFile); !slices.Equal(written, want) {
		t.Errorf("written = %v, want %v", written, want)
	}
	b, err := os.ReadFile(filepath.Join(dir, "syntax", "lyr.vim"))
	if err != nil || !strings.Contains(string(b), "b:current_syntax = 'lyr'") {
		t.Errorf("syntax/lyr.vim: %v", err)
	}

	// A reinstall replaces stale files.
	stale := filepath.Join(dir, "syntax", "old.vim")
	if err := os.WriteFile(stale, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(dir, "v1.1.0"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("stale file kept: %v", err)
	}
	if m, _ := os.ReadFile(filepath.Join(dir, MarkerFile)); MarkerVersion(m) != "v1.1.0" {
		t.Errorf("marker = %q", m)
	}

	// An empty directory is fine; a foreign one is refused and kept.
	empty := t.TempDir()
	if _, err := Install(empty, "v1"); err != nil {
		t.Errorf("empty dir: %v", err)
	}
	foreign := t.TempDir()
	mine := filepath.Join(foreign, "mine.vim")
	if err := os.WriteFile(mine, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(foreign, "v1"); err == nil || !strings.Contains(err.Error(), "wasn't written by lyr editor") {
		t.Errorf("foreign dir: %v", err)
	}
	if _, err := os.Stat(mine); err != nil {
		t.Errorf("foreign file removed: %v", err)
	}
}
