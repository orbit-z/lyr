package main

import (
	"context"
	"encoding/json"
	"runtime/debug"
	"strings"
	"testing"

	"github.com/bertrandkhe/orbit/internal/doctor"
)

// fakeDoctor replaces orbit doctor's environment: everything is found, and
// gopls was built with goplsGo.
func fakeDoctor(t *testing.T, goplsGo string) {
	t.Helper()
	old := doctorEnv
	t.Cleanup(func() { doctorEnv = old })
	doctorEnv = func(dir string) doctor.Env {
		return doctor.Env{
			Dir:      dir,
			LookPath: func(file string) (string, error) { return "/fake/" + file, nil },
			Run: func(_ context.Context, _, name string, args ...string) ([]byte, error) {
				if name == "go" {
					return []byte("go1.27.3\nauto\n"), nil
				}
				return []byte("1.4.4\n"), nil // bun --version
			},
			BuildInfo: func(string) (*debug.BuildInfo, error) {
				return &debug.BuildInfo{GoVersion: goplsGo, Main: debug.Module{Version: "v0.24.0"}}, nil
			},
		}
	}
}

func TestDoctor(t *testing.T) {
	fakeDoctor(t, "go1.27.3")
	code, stdout, stderr := runCLI(t, t.TempDir(), "doctor")
	want := "✓ go: go1.27.3\n✓ gopls: v0.24.0, built with go1.27.3\n✓ bun: 1.4.4\n"
	if code != 0 || stdout != want || stderr != "" {
		t.Errorf("exit %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}

	fakeDoctor(t, "go1.23.4")
	code, stdout, _ = runCLI(t, t.TempDir(), "doctor", "--json")
	if code != 1 {
		t.Errorf("exit %d, want 1", code)
	}
	var r struct {
		Checks []map[string]string `json:"checks"`
	}
	if err := json.Unmarshal([]byte(stdout), &r); err != nil {
		t.Fatalf("%v\n%s", err, stdout)
	}
	var names []string
	for _, c := range r.Checks {
		names = append(names, c["name"])
		if len(c) != 4 {
			t.Errorf("fields: %v", c)
		}
	}
	if strings.Join(names, " ") != "go gopls bun" {
		t.Errorf("checks: %v", names)
	}
	if g := r.Checks[1]; g["status"] != "error" || g["fix"] != "go install golang.org/x/tools/gopls@latest" {
		t.Errorf("gopls: %v", g)
	}
	if ok := r.Checks[0]; ok["status"] != "ok" || ok["fix"] != "" {
		t.Errorf("go: %v", ok)
	}

	if code, _, stderr := runCLI(t, t.TempDir(), "doctor", "extra"); code != 2 || !strings.Contains(stderr, `orbit doctor: unexpected argument "extra"`) {
		t.Errorf("orbit doctor extra: exit %d\n%s", code, stderr)
	}
}
