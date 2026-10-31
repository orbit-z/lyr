// Package nvim holds no Go code: this test only hooks the Neovim/Vim
// plugin's syntax tests (test/run.sh) into go test ./... .
package nvim

import (
	"os/exec"
	"testing"
)

// TestSyntax runs test/run.sh, which opens .lyr files headless in Neovim and
// Vim (each when installed) and checks the syntax items at chosen
// positions. Run it directly with editors/nvim/test/run.sh.
func TestSyntax(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the Neovim plugin's syntax tests in -short mode")
	}
	_, errNvim := exec.LookPath("nvim")
	_, errVim := exec.LookPath("vim")
	if errNvim != nil && errVim != nil {
		t.Skip("neither nvim nor vim found on PATH: skipping the Neovim plugin's syntax tests")
	}
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("sh not found on PATH: skipping the Neovim plugin's syntax tests")
	}
	out, err := exec.Command(sh, "test/run.sh").CombinedOutput()
	if err != nil {
		t.Fatalf("test/run.sh: %v\n%s", err, out)
	}
	t.Logf("%s", out)
}
