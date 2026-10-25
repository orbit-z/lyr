package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"
)

const root = "../../.."

// TestUpToDate fails when loader_gen.go doesn't match loader/*.ts.
func TestUpToDate(t *testing.T) {
	want, err := generate(root)
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(root, "loader_gen.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("loader_gen.go is out of date: run go generate at the repository root")
	}
}

// TestBunTest runs the loader's JS tests (bun test + happy-dom) when Bun
// ≥ 1.4.4 is installed and loader/node_modules exists. In CI:
//
//	cd loader && bun install --frozen-lockfile && bun test
func TestBunTest(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping bun test in -short mode")
	}
	bun, err := exec.LookPath("bun")
	if err != nil {
		t.Skip("bun not found on PATH: skipping the loader's JS tests (cd loader && bun install && bun test)")
	}
	out, err := exec.Command(bun, "--version").Output()
	if err != nil {
		t.Skipf("bun --version: %v", err)
	}
	if !atLeast(string(out), 1, 4, 4) {
		t.Skipf("bun %s is older than 1.4.4: skipping the loader's JS tests", bytes.TrimSpace(out))
	}
	dir := filepath.Join(root, "loader")
	if _, err := os.Stat(filepath.Join(dir, "node_modules", "happy-dom")); err != nil {
		t.Skip("loader/node_modules is missing: skipping the loader's JS tests (cd loader && bun install --frozen-lockfile)")
	}
	cmd := exec.Command(bun, "test")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("bun test: %v\n%s", err, out)
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
		{"1.3.9", false},
		{"0.9.9", false},
		{"garbage", false},
	} {
		if got := atLeast(tc.v, 1, 4, 4); got != tc.want {
			t.Errorf("atLeast(%q) = %v, want %v", tc.v, got, tc.want)
		}
	}
}
