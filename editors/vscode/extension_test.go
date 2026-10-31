// Package vscode holds no Go code: this test only hooks the VS Code
// extension's own checks (bun check and the grammar snapshot tests) into
// go test ./... .
package vscode

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"
)

// TestBunTest runs `bun run test` (type check + grammar snapshots) when Bun
// ≥ 1.4.4 is installed and node_modules exists. In CI:
//
//	cd editors/vscode && bun install --frozen-lockfile && bun run test
func TestBunTest(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the VS Code extension's tests in -short mode")
	}
	bun, err := exec.LookPath("bun")
	if err != nil {
		t.Skip("bun not found on PATH: skipping the VS Code extension's tests (cd editors/vscode && bun install && bun run test)")
	}
	out, err := exec.Command(bun, "--version").Output()
	if err != nil {
		t.Skipf("bun --version: %v", err)
	}
	if !atLeast(string(out), 1, 4, 4) {
		t.Skipf("bun %s is older than 1.4.4: skipping the VS Code extension's tests", bytes.TrimSpace(out))
	}
	if _, err := os.Stat(filepath.Join("node_modules", "vscode-tmgrammar-test")); err != nil {
		t.Skip("editors/vscode/node_modules is missing: skipping the VS Code extension's tests (cd editors/vscode && bun install --frozen-lockfile)")
	}
	cmd := exec.Command(bun, "run", "test")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("bun run test: %v\n%s", err, out)
	}
}

// TestManifest checks package.json's configurationDefaults: Lyr is the
// default formatter of .lyr files, and Tailwind CSS IntelliSense treats
// .lyr files as HTML (PLAN.md §5d, Q5).
func TestManifest(t *testing.T) {
	data, err := os.ReadFile("package.json")
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		Contributes struct {
			ConfigurationDefaults map[string]map[string]string `json:"configurationDefaults"`
		} `json:"contributes"`
	}
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	d := m.Contributes.ConfigurationDefaults
	if got := d["tailwindCSS.includeLanguages"]["lyr"]; got != "html" {
		t.Errorf(`configurationDefaults["tailwindCSS.includeLanguages"].lyr = %q, want "html"`, got)
	}
	if got := d["[lyr]"]["editor.defaultFormatter"]; got != "orbit-z.lyr" {
		t.Errorf(`configurationDefaults["[lyr]"]["editor.defaultFormatter"] = %q`, got)
	}
}

var versionRE = regexp.MustCompile(`^(\d+)\.(\d+)\.(\d+)`)

// atLeast reports whether version v ("1.4.4\n") is at least maj.min.patch.
func atLeast(v string, maj, min, patch int) bool {
	m := versionRE.FindStringSubmatch(v)
	if m == nil {
		return false
	}
	have := [3]int{}
	for i := range have {
		have[i], _ = strconv.Atoi(m[i+1])
	}
	want := [3]int{maj, min, patch}
	for i := range have {
		if have[i] != want[i] {
			return have[i] > want[i]
		}
	}
	return true
}

func TestAtLeast(t *testing.T) {
	for _, tc := range []struct {
		v    string
		want bool
	}{
		{"1.4.4\n", true},
		{"1.4.3", false},
		{"1.5.0", true},
		{"2.0.0", true},
		{"garbage", false},
	} {
		if got := atLeast(tc.v, 1, 4, 4); got != tc.want {
			t.Errorf("atLeast(%q) = %v, want %v", tc.v, got, tc.want)
		}
	}
}
