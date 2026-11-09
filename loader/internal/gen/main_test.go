package main

import (
	"bytes"
	"compress/gzip"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

const root = "../../.."

// TestUpToDate fails when internal/loader/loader_gen.go doesn't match
// loader/*.ts.
func TestUpToDate(t *testing.T) {
	want, err := generate(root)
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(root, out))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal(out + " is out of date: run go generate at the repository root")
	}
}

// TestLoader checks that the loader is one deterministic classic script
// with every part, and logs the sizes quoted in the docs
// (go test -v -run TestLoader ./loader/internal/gen).
func TestLoader(t *testing.T) {
	js, err := build(filepath.Join(root, "loader", "loader.ts"))
	if err != nil {
		t.Fatal(err)
	}
	again, err := build(filepath.Join(root, "loader", "loader.ts"))
	if err != nil || again != js {
		t.Fatalf("the build isn't deterministic (%v)", err)
	}
	for _, f := range []struct{ name, s string }{{"loader", js}, {"wrappers", wrappersCSS}} {
		var z bytes.Buffer
		zw, _ := gzip.NewWriterLevel(&z, gzip.BestCompression)
		zw.Write([]byte(f.s))
		zw.Close()
		t.Logf("%s: %d B, %d B gzipped", f.name, len(f.s), z.Len())
	}
	// A classic script: an IIFE, no top-level import or export (a module
	// would run once per document, not on every partial insertion).
	if !strings.HasPrefix(js, `"use strict";(()=>{`) || !strings.HasSuffix(js, "})();") {
		t.Errorf("the loader isn't an IIFE: %.40s…%s", js, js[max(len(js)-20, 0):])
	}
	if regexp.MustCompile(`(^|[;}])\s*(import|export)[\s{*"]`).MatchString(js) {
		t.Error("the loader has a static import or export")
	}
	// Every part: islands, strategies, lazy, the partial runner, the guard.
	for _, w := range []string{"lyr-island", "IntersectionObserver", "matchMedia", "requestIdleCallback",
		"lyr-lazy,lyr-island[client]", "no fallback element to observe", "currentScript", "data-lyr-loader",
		"lyr__ran", "/_lyr/island/", "location.origin", "import("} {
		if !strings.Contains(js, w) {
			t.Errorf("the loader lacks %q", w)
		}
	}
}

func TestName(t *testing.T) {
	// sha256("") = e3b0c442…
	if got := name("x", ".js", ""); got != "x-e3b0c442.js" {
		t.Errorf("name = %q", got)
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
