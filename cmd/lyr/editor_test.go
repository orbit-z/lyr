package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/orbit-z/lyr/editors/nvim"
)

// fakeEditorEnv makes lyr editor see env and home instead of the real ones.
func fakeEditorEnv(t *testing.T, home string, env map[string]string) {
	t.Helper()
	oldGetenv, oldHome := editorGetenv, editorHome
	t.Cleanup(func() { editorGetenv, editorHome = oldGetenv, oldHome })
	editorGetenv = func(k string) string { return env[k] }
	editorHome = func() string { return home }
}

func TestEditor(t *testing.T) {
	home := t.TempDir()
	data := filepath.Join(t.TempDir(), "data")
	fakeEditorEnv(t, home, map[string]string{"XDG_DATA_HOME": data, "NVIM_APPNAME": "lyrtest"})

	code, stdout, stderr := runCLI(t, t.TempDir(), "editor", "nvim")
	nvimDir := filepath.Join(data, "lyrtest", "site", "pack", "lyr", "start", "lyr")
	if code != 0 || stderr != "" || !strings.Contains(stdout, "Installed the Lyr plugin for Neovim in "+nvimDir) ||
		!strings.Contains(stdout, "\tsyntax/lyr.vim\n") || !strings.Contains(stdout, "\tlyr-version\n") {
		t.Fatalf("lyr editor nvim: exit %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	for _, p := range append(nvim.Files(), nvim.MarkerFile) {
		if _, err := os.Stat(filepath.Join(nvimDir, filepath.FromSlash(p))); err != nil {
			t.Error(err)
		}
	}

	// Vim: ~/.vim/pack/lyr/start/lyr; flags may come first.
	code, stdout, _ = runCLI(t, t.TempDir(), "editor", "vim")
	vimDir := filepath.Join(home, ".vim", "pack", "lyr", "start", "lyr")
	if code != 0 || !strings.Contains(stdout, "for Vim in "+vimDir) {
		t.Errorf("lyr editor vim: exit %d\n%s", code, stdout)
	}

	// --dir, relative to the working directory; a reinstall replaces
	// stale files.
	wd := t.TempDir()
	if code, _, stderr := runCLI(t, wd, "editor", "--dir", "plug", "nvim"); code != 0 {
		t.Fatalf("--dir: exit %d\n%s", code, stderr)
	}
	stale := filepath.Join(wd, "plug", "syntax", "old.vim")
	os.WriteFile(stale, nil, 0o644)
	if code, _, stderr := runCLI(t, wd, "editor", "nvim", "--dir", "plug"); code != 0 {
		t.Fatalf("reinstall: exit %d\n%s", code, stderr)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("stale file kept: %v", err)
	}
	if m, err := os.ReadFile(filepath.Join(wd, "plug", nvim.MarkerFile)); err != nil || !strings.Contains(string(m), "files sha256:") {
		t.Errorf("marker: %q, %v", m, err)
	}

	// A directory lyr didn't write is refused.
	foreign := t.TempDir()
	os.WriteFile(filepath.Join(foreign, "init.vim"), nil, 0o644)
	if code, _, stderr := runCLI(t, wd, "editor", "nvim", "--dir", foreign); code != 1 || !strings.Contains(stderr, "wasn't written by lyr editor") {
		t.Errorf("foreign dir: exit %d\n%s", code, stderr)
	}

	for _, tc := range []struct {
		args   []string
		stderr string
	}{
		{[]string{"editor"}, "lyr editor: missing editor (nvim or vim)"},
		{[]string{"editor", "emacs"}, `lyr editor: unknown editor "emacs" (nvim or vim)`},
		{[]string{"editor", "nvim", "vim"}, `lyr editor: unexpected argument "vim"`},
		{[]string{"editor", "--bogus"}, "flag provided but not defined: -bogus"},
	} {
		if code, _, stderr := runCLI(t, wd, tc.args...); code != 2 || !strings.Contains(stderr, tc.stderr) {
			t.Errorf("%v: exit %d\n%s", tc.args, code, stderr)
		}
	}
}

// TestEditorInstalled starts Neovim and Vim (when installed) on an install
// made by lyr editor, with no config, so the embedded file set is complete:
// detection, highlighting and, in Neovim, the language server's config and
// its auto-enable; in Vim, plugin/lyr.lua must be harmless.
func TestEditorInstalled(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the editor start-up tests in -short mode")
	}
	fixture, err := filepath.Abs("../../editors/nvim/test/constructs.lyr")
	if err != nil {
		t.Fatal(err)
	}
	// The probe writes: the lyr LSP config's command and whether it's
	// enabled (Neovim), filetype and syntax, the groups at four positions,
	// and the last error message.
	probe := `let s:r = []
call add(s:r, exists('*luaeval') && has('nvim') ? luaeval("vim.lsp.config.lyr and vim.lsp.config.lyr.cmd[1] or 'none'") : '-')
call add(s:r, has('nvim') ? luaeval("vim.lsp.is_enabled == nil and 'n/a' or tostring(vim.lsp.is_enabled('lyr'))") : '-')
if has('nvim')
  " Don't start the server for the fixture.
  lua vim.lsp.enable('lyr', false)
elseif has('lua')
  execute 'luafile ' . fnameescape($LYR_PLUGIN_DIR . '/plugin/lyr.lua')
endif
execute 'edit ' . fnameescape($LYR_FIXTURE)
call add(s:r, &filetype . ' ' . get(b:, 'current_syntax', ''))
function! s:At(line, needle) abort
  call cursor(1, 1)
  let l = search('\V' . a:line, 'cW')
  return synIDattr(synID(l, stridx(getline(l), a:needle) + 1, 1), 'name')
endfunction
call add(s:r, s:At('---', '---'))
call add(s:r, s:At('{if props.Open', 'if'))
call add(s:r, s:At('<Card client:visible', 'Card'))
call add(s:r, s:At('const el', 'const'))
call add(s:r, v:errmsg)
call writefile(s:r, $LYR_PROBE_OUT)
qall!
`
	tmp := t.TempDir()
	script := filepath.Join(tmp, "probe.vim")
	if err := os.WriteFile(script, []byte(probe), 0o644); err != nil {
		t.Fatal(err)
	}
	empty := filepath.Join(tmp, "empty.vim")
	os.WriteFile(empty, nil, 0o644)

	for _, editor := range []string{nvim.Neovim, nvim.Vim} {
		t.Run(editor, func(t *testing.T) {
			bin, err := exec.LookPath(editor)
			if err != nil {
				t.Skipf("%s not found on PATH", editor)
			}
			home := t.TempDir()
			xdg := map[string]string{}
			for _, k := range []string{"XDG_DATA_HOME", "XDG_CONFIG_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME"} {
				xdg[k] = filepath.Join(home, strings.ToLower(k))
			}
			fakeEditorEnv(t, home, xdg)
			if code, _, stderr := runCLI(t, t.TempDir(), "editor", editor); code != 0 {
				t.Fatalf("lyr editor %s: exit %d\n%s", editor, code, stderr)
			}
			dir, _ := nvim.DefaultDir(editor, "linux", func(k string) string { return xdg[k] }, home)
			out := filepath.Join(tmp, editor+".out")
			var cmd *exec.Cmd
			if editor == nvim.Neovim {
				cmd = exec.Command(bin, "--headless", "-u", empty, "-i", "NONE", "-n", "-S", script)
			} else {
				// -u NORC: no vimrc, but plugins and packages load.
				cmd = exec.Command(bin, "-N", "-u", "NORC", "-i", "NONE", "-n", "-es",
					"--cmd", "filetype plugin on", "--cmd", "syntax on", "-S", script)
			}
			cmd.Env = append(os.Environ(), "HOME="+home, "NVIM_APPNAME=", "VIMINIT=", "MYVIMRC=",
				"LYR_FIXTURE="+fixture, "LYR_PROBE_OUT="+out, "LYR_PLUGIN_DIR="+dir)
			for k, v := range xdg {
				cmd.Env = append(cmd.Env, k+"="+v)
			}
			if b, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("%s: %v\n%s", editor, err, b)
			}
			b, err := os.ReadFile(out)
			if err != nil {
				t.Fatal(err)
			}
			got := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
			want := []string{"lyr", "true", "lyr lyr", "lyrFrontmatterDelim", "lyrCondHead", "lyrComponentName", "typescriptVariable", ""}
			if editor == nvim.Vim {
				want[0], want[1] = "-", "-"
			}
			if len(got) == len(want)-1 {
				got = append(got, "") // the empty error line, trimmed above
			}
			if strings.Join(got, "|") != strings.Join(want, "|") {
				t.Errorf("%s probe:\n got %q\nwant %q", editor, got, want)
			}
		})
	}
}
