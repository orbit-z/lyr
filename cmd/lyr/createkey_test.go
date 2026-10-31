package main

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestCreateKey(t *testing.T) {
	code, out1, stderr := runCLI(t, t.TempDir(), "create-key")
	if code != 0 || stderr != "" || !strings.HasSuffix(out1, "\n") {
		t.Fatalf("exit %d, stdout %q, stderr %q", code, out1, stderr)
	}
	key, err := base64.StdEncoding.DecodeString(strings.TrimSuffix(out1, "\n"))
	if err != nil || len(key) != 32 {
		t.Errorf("printed %q: %d bytes, %v; want 32 bytes of standard base64", out1, len(key), err)
	}
	if _, out2, _ := runCLI(t, t.TempDir(), "create-key"); out2 == out1 {
		t.Error("two keys are the same")
	}
	if code, _, stderr := runCLI(t, t.TempDir(), "create-key", "extra"); code != 2 || !strings.Contains(stderr, `unexpected argument "extra"`) {
		t.Errorf("create-key extra: exit %d, stderr %q", code, stderr)
	}
}
