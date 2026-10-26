//go:build unix

package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

var (
	cliOnce sync.Once
	cliBin  string
	cliErr  error
)

// globalCLI builds the orbit binary once: run from a temporary project, its
// main module is Orbit, so it acts as a global install.
func globalCLI(t *testing.T) string {
	t.Helper()
	if testing.Short() {
		t.Skip("builds the CLI")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go command not found")
	}
	cliOnce.Do(func() {
		dir, err := os.MkdirTemp("", "orbit-reexec-")
		if err != nil {
			cliErr = err
			return
		}
		cliBin = filepath.Join(dir, "orbit")
		out, err := exec.Command("go", "build", "-o", cliBin, ".").CombinedOutput()
		if err != nil {
			cliErr = errors.New("go build: " + err.Error() + "\n" + string(out))
		}
	})
	if cliErr != nil {
		t.Fatal(cliErr)
	}
	return cliBin
}

// fakeGo writes a go command into a new directory that logs each call
// ("go <args> REEXEC=<$ORBIT_REEXEC>") to log, answers go tool -n orbit
// with the path of a fake pinned CLI, and go env GOROOT with goroot; the
// fake CLI logs "orbit <args> REEXEC=… PATH=…" and exits with code 7. It
// returns the PATH to use and the log file.
func fakeGo(t *testing.T, toolN string) (path, log, goroot string) {
	t.Helper()
	dir := t.TempDir()
	log = filepath.Join(dir, "log")
	goroot = filepath.Join(dir, "goroot")
	pinnedCLI := filepath.Join(dir, "pinned-orbit")
	if toolN == "" {
		toolN = `echo "` + pinnedCLI + `"`
	}
	script := `#!/bin/sh
echo "go $* REEXEC=$ORBIT_REEXEC" >> "` + log + `"
case "$*" in
"tool -n orbit") ` + toolN + ` ;;
"env GOROOT") echo "` + goroot + `" ;;
*) exit 99 ;;
esac
`
	cli := `#!/bin/sh
echo "orbit $* REEXEC=$ORBIT_REEXEC PATH=$PATH" >> "` + log + `"
exit 7
`
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "go"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pinnedCLI, []byte(cli), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin + string(os.PathListSeparator) + os.Getenv("PATH"), log, goroot
}

func pinProject(t *testing.T, gomod string) string {
	t.Helper()
	dir := t.TempDir()
	writeFileT(t, dir, "go.mod", gomod)
	if err := os.MkdirAll(filepath.Join(dir, "src", "pages"), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// runBin runs bin with args in dir, with extra environment variables, and
// returns its exit code and combined output.
func runBin(t *testing.T, bin, dir string, env []string, args ...string) (int, string) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	switch {
	case err == nil:
		return 0, string(out)
	case errors.As(err, &exit):
		return exit.ExitCode(), string(out)
	}
	t.Fatal(err)
	return 0, ""
}

func readLog(t *testing.T, log string) string {
	t.Helper()
	data, err := os.ReadFile(log)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	return string(data)
}

const (
	pinHead = "module example.com/site\n\ngo 1.27\n\ntoolchain go1.27.3\n\n"
	pinTool = "tool github.com/orbit-z/orbit/cmd/orbit\n\n"
	pinReq  = "require github.com/orbit-z/orbit v9.9.9\n"
)

// TestReexec checks the global CLI's re-exec into the pinned CLI with a
// fake go command and pinned CLI.
func TestReexec(t *testing.T) {
	bin := globalCLI(t)
	path, log, goroot := fakeGo(t, "")
	dir := pinProject(t, pinHead+pinTool+pinReq)
	sub := filepath.Join(dir, "src", "pages") // the root is found upwards

	code, out := runBin(t, bin, sub, []string{"PATH=" + path, "ORBIT_REEXEC="}, "generate", "--bogus", "x y")
	if code != 7 {
		t.Errorf("exit %d, want the pinned CLI's 7\n%s", code, out)
	}
	got := readLog(t, log)
	want := "go tool -n orbit REEXEC=\ngo env GOROOT REEXEC=\norbit generate --bogus x y REEXEC=1 PATH=" + filepath.Join(goroot, "bin") + ":"
	if !strings.HasPrefix(got, want) {
		t.Errorf("calls:\n%s\nwant prefix:\n%s", got, want)
	}

	// ORBIT_REEXEC=1: the CLI runs the command itself.
	os.Remove(log)
	if code, out := runBin(t, bin, dir, []string{"PATH=" + path, "ORBIT_REEXEC=1"}, "help"); code != 0 || !strings.Contains(out, "orbit <command>") {
		t.Errorf("ORBIT_REEXEC=1 orbit help: exit %d\n%s", code, out)
	}
	// orbit new and orbit doctor are exempt.
	if code, out := runBin(t, bin, dir, []string{"PATH=" + path, "ORBIT_REEXEC="}, "new"); code != 2 || !strings.Contains(out, "orbit new: missing directory") {
		t.Errorf("orbit new: exit %d\n%s", code, out)
	}
	if code, out := runBin(t, bin, dir, []string{"PATH=" + path, "ORBIT_REEXEC="}, "doctor", "--bogus"); code != 2 {
		t.Errorf("orbit doctor --bogus: exit %d\n%s", code, out)
	}
	if got := readLog(t, log); got != "" {
		t.Errorf("the go command ran:\n%s", got)
	}

	// go tool -n orbit fails (a build error): its exit code and output pass
	// through.
	path, log, _ = fakeGo(t, `echo "cmd/orbit: build failed" >&2; exit 3`)
	code, out = runBin(t, bin, dir, []string{"PATH=" + path, "ORBIT_REEXEC="}, "build")
	if code != 3 || !strings.Contains(out, "cmd/orbit: build failed") {
		t.Errorf("failing go tool -n: exit %d\n%s", code, out)
	}
	if got := readLog(t, log); strings.Contains(got, "orbit build") {
		t.Errorf("the pinned CLI ran:\n%s", got)
	}
}

// TestVersionMismatch: without the tool directive, a global CLI of another
// version stops, unless go.mod replaces Orbit with a local directory.
func TestVersionMismatch(t *testing.T) {
	bin := globalCLI(t)
	path, log, _ := fakeGo(t, "")
	env := []string{"PATH=" + path, "ORBIT_REEXEC="}

	dir := pinProject(t, pinHead+pinReq)
	code, out := runBin(t, bin, dir, env, "generate")
	want := "orbit: version mismatch: this orbit is "
	tail := ", but go.mod requires github.com/orbit-z/orbit v9.9.9; pin the CLI with go get -tool github.com/orbit-z/orbit/cmd/orbit@v9.9.9, then run go tool orbit (see orbit doctor)\n"
	if code != 1 || !strings.HasPrefix(out, want) || !strings.HasSuffix(out, tail) {
		t.Errorf("exit %d\n%s", code, out)
	}
	// orbit doctor still runs, to report it.
	if code, out := runBin(t, bin, dir, env, "doctor", "-h"); code != 0 {
		t.Errorf("orbit doctor -h: exit %d\n%s", code, out)
	}

	dir = pinProject(t, pinHead+pinReq+"replace github.com/orbit-z/orbit => ../orbit\n")
	if code, out := runBin(t, bin, dir, env, "help"); code != 0 {
		t.Errorf("local replace: exit %d\n%s", code, out)
	}
	if got := readLog(t, log); got != "" {
		t.Errorf("the go command ran:\n%s", got)
	}
}

// TestReexecProject runs the global CLI in an orbit new project: it hands
// over to the real pinned CLI, exit codes pass through, and orbit dev
// exits 0 on a SIGINT, sent to the process or to its whole group (a
// terminal's Ctrl-C).
func TestReexecProject(t *testing.T) {
	bin := globalCLI(t)
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	base := t.TempDir()
	env := []string{"ORBIT_REPLACE=" + root, "GOWORK=off", "ORBIT_REEXEC="}
	if code, out := runBin(t, bin, base, env, "new", "site"); code != 0 {
		t.Fatalf("orbit new: exit %d\n%s", code, out)
	}
	dir := filepath.Join(base, "site")

	// A wrapper go on PATH logs the calls, to show the hand-over.
	wrap := t.TempDir()
	realGo, _ := exec.LookPath("go")
	log := filepath.Join(wrap, "log")
	writeFileT(t, wrap, "go", "#!/bin/sh\necho \"go $*\" >> \""+log+"\"\nexec \""+realGo+"\" \"$@\"\n")
	if err := os.Chmod(filepath.Join(wrap, "go"), 0o755); err != nil {
		t.Fatal(err)
	}
	env = append(env, "PATH="+wrap+string(os.PathListSeparator)+os.Getenv("PATH"))

	if code, out := runBin(t, bin, dir, env, "generate"); code != 0 {
		t.Fatalf("orbit generate: exit %d\n%s", code, out)
	}
	if got := readLog(t, log); !strings.HasPrefix(got, "go tool -n orbit\ngo env GOROOT\n") {
		t.Errorf("go calls:\n%s", got)
	}
	if code, out := runBin(t, bin, dir, env, "generate", "--bogus"); code != 2 || !strings.Contains(out, "flag provided but not defined: -bogus") {
		t.Errorf("orbit generate --bogus: exit %d\n%s", code, out)
	}

	for _, group := range []bool{false, true} {
		port := freePortT(t)
		cmd := exec.Command(bin, "dev", "--port", port)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), env...)
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		var out bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &out
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		waitDev(t, "http://127.0.0.1:"+port+"/", &out)
		pid := cmd.Process.Pid
		if group {
			pid = -pid
		}
		if err := syscall.Kill(pid, syscall.SIGINT); err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("orbit dev, SIGINT (group %v): %v\n%s", group, err, out.String())
			}
		case <-time.After(20 * time.Second):
			syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			t.Fatalf("orbit dev didn't exit on SIGINT\n%s", out.String())
		}
	}
}

func httpGet(url string) (string, error) {
	resp, err := http.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	return string(body), err
}

func waitDev(t *testing.T, url string, out *bytes.Buffer) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	for ctx.Err() == nil {
		if body, err := httpGet(url); err == nil && strings.Contains(body, "Welcome to Orbit") {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("%s didn't come up\n%s", url, out.String())
}
