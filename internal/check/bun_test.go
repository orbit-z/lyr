package check

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestParseBunVersion(t *testing.T) {
	tests := []struct {
		in   string
		want Version
		ok   bool
	}{
		{"1.4.4\n", Version{1, 4, 4}, true},
		{"v1.10.0", Version{1, 10, 0}, true},
		{"1.4.5-canary.3+abcdef", Version{1, 4, 5}, true},
		{"2.0.1+deadbeef", Version{2, 0, 1}, true},
		{"1.4", Version{}, false},
		{"", Version{}, false},
		{"bun 1.4.4", Version{}, false},
		{"1.x.4", Version{}, false},
	}
	for _, tt := range tests {
		got, ok := ParseBunVersion(tt.in)
		if got != tt.want || ok != tt.ok {
			t.Errorf("ParseBunVersion(%q) = %v, %v; want %v, %v", tt.in, got, ok, tt.want, tt.ok)
		}
	}
	if !(Version{1, 3, 9}).Less(MinBun) || (Version{1, 4, 4}).Less(MinBun) || (Version{2, 0, 0}).Less(MinBun) {
		t.Error("Less is wrong")
	}
}

// fakeBun writes a shell script that prints version for --version.
func fakeBun(t *testing.T, version string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell script")
	}
	name := filepath.Join(t.TempDir(), "bun")
	if err := os.WriteFile(name, []byte("#!/bin/sh\necho "+version+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return name
}

func TestFindBun(t *testing.T) {
	ctx := context.Background()
	if p, notice := findBun(ctx, fakeBun(t, "1.4.4")); p == "" {
		t.Errorf("1.4.4 refused: %s", notice)
	}
	_, notice := findBun(ctx, fakeBun(t, "1.3.2"))
	if want := "orbit check: skipping TypeScript type checking: bun 1.3.2 is older than 1.4.4; upgrade it with: bun upgrade"; notice != want {
		t.Errorf("old bun notice %q, want %q", notice, want)
	}
	t.Setenv("PATH", t.TempDir())
	_, notice = findBun(ctx, "")
	if !strings.Contains(notice, "bun not found") || !strings.Contains(notice, InstallBun) {
		t.Errorf("missing bun notice %q", notice)
	}
}
