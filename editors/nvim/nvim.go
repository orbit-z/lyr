// Package nvim embeds the Neovim/Vim plugin's runtime files (ftdetect/,
// ftplugin/, syntax/, lsp/, plugin/) and installs them for lyr editor.
package nvim

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
)

//go:embed ftdetect ftplugin lsp plugin syntax
var files embed.FS

// MarkerFile is written at the root of an install: it marks the directory
// as Lyr's (so a reinstall may replace it) and holds the CLI's version.
const MarkerFile = "lyr-version"

// Files returns the slash-separated paths of the embedded files, sorted.
func Files() []string {
	var out []string
	fs.WalkDir(files, ".", func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			out = append(out, p)
		}
		return err
	})
	sort.Strings(out)
	return out
}

// Marker returns the marker file's content for a CLI of the given version:
// the version, then a hash of the embedded files, so a development build
// ("(devel)") with changed files doesn't look current.
func Marker(version string) []byte {
	if version == "" {
		version = "unknown"
	}
	h := sha256.New()
	for _, p := range Files() {
		b, _ := files.ReadFile(p)
		fmt.Fprintf(h, "%s\x00%d\x00", p, len(b))
		h.Write(b)
	}
	return fmt.Appendf(nil, "%s\nfiles sha256:%s\n", version, hex.EncodeToString(h.Sum(nil))[:16])
}

// MarkerVersion returns the version line of a marker file's content.
func MarkerVersion(marker []byte) string {
	line, _, _ := bytes.Cut(marker, []byte("\n"))
	return string(bytes.TrimSpace(line))
}

// Editor names the editors lyr editor installs for.
const (
	Neovim = "nvim"
	Vim    = "vim"
)

// DefaultDir returns the package directory that the editor loads at
// startup with no configuration: Neovim's
// stdpath('data')/site/pack/lyr/start/lyr, or Vim's
// ~/.vim/pack/lyr/start/lyr (~/vimfiles on Windows). getenv and home
// replace os.Getenv and os.UserHomeDir; goos is runtime.GOOS.
func DefaultDir(editor, goos string, getenv func(string) string, home string) (string, error) {
	pack := filepath.Join("pack", "lyr", "start", "lyr")
	switch editor {
	case Neovim:
		app := getenv("NVIM_APPNAME")
		if app == "" {
			app = "nvim"
		}
		base := getenv("XDG_DATA_HOME")
		if goos == "windows" {
			// stdpath('data') is $XDG_DATA_HOME/nvim-data, else
			// %LOCALAPPDATA%\nvim-data.
			app += "-data"
			if base == "" {
				base = getenv("LOCALAPPDATA")
			}
		}
		if base == "" {
			if home == "" {
				return "", errors.New("can't find Neovim's data directory: $HOME is not set (use --dir)")
			}
			if goos == "windows" {
				base = filepath.Join(home, "AppData", "Local")
			} else {
				base = filepath.Join(home, ".local", "share")
			}
		}
		return filepath.Join(base, app, "site", pack), nil
	case Vim:
		if home == "" {
			return "", errors.New("can't find Vim's directory: $HOME is not set (use --dir)")
		}
		vimfiles := ".vim"
		if goos == "windows" {
			vimfiles = "vimfiles"
		}
		return filepath.Join(home, vimfiles, pack), nil
	}
	return "", fmt.Errorf("unknown editor %q (nvim or vim)", editor)
}

// Install writes the plugin into dir, replacing what a previous install
// left there, and the marker file for version. It refuses an existing,
// non-empty dir without the marker file. It returns the files written,
// slash-separated and relative to dir.
func Install(dir, version string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return nil, err
	case len(entries) > 0:
		if _, err := os.Stat(filepath.Join(dir, MarkerFile)); err != nil {
			return nil, fmt.Errorf("%s isn't empty and wasn't written by lyr editor (no %s file); remove it or choose another --dir", dir, MarkerFile)
		}
		if err := os.RemoveAll(dir); err != nil {
			return nil, err
		}
	}
	var written []string
	for _, p := range Files() {
		b, err := files.ReadFile(p)
		if err != nil {
			return nil, err
		}
		dst := filepath.Join(dir, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(dst, b, 0o644); err != nil {
			return nil, err
		}
		written = append(written, p)
	}
	if err := os.WriteFile(filepath.Join(dir, MarkerFile), Marker(version), 0o644); err != nil {
		return nil, err
	}
	return append(written, path.Clean(MarkerFile)), nil
}
