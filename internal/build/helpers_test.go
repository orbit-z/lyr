package build

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// The end-to-end tests run the real CLI: TestMain's directory holds an
// orbit binary built once, on first use, from ../../cmd/orbit.

var (
	tmpDir     string
	repoRoot   string
	orbitOnce  sync.Once
	orbitBin   string
	orbitErr   error
	goEnv      = []string{"GOFLAGS=-mod=mod", "GOWORK=off"}
	httpClient = &http.Client{
		Timeout:       10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
)

func TestMain(m *testing.M) {
	var err error
	if repoRoot, err = filepath.Abs("../.."); err != nil {
		panic(err)
	}
	if tmpDir, err = os.MkdirTemp("", "orbit-build-test-"); err != nil {
		panic(err)
	}
	code := m.Run()
	os.RemoveAll(tmpDir)
	os.Exit(code)
}

// needE2E skips the test under -short or without the go command.
func needE2E(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("builds real binaries")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go command not found")
	}
}

// orbitCLI returns the path of the orbit binary, building it once.
func orbitCLI(t *testing.T) string {
	t.Helper()
	orbitOnce.Do(func() {
		orbitBin = filepath.Join(tmpDir, "orbit")
		cmd := exec.Command("go", "build", "-o", orbitBin, "./cmd/orbit")
		cmd.Dir = repoRoot
		if out, err := cmd.CombinedOutput(); err != nil {
			orbitErr = fmt.Errorf("go build ./cmd/orbit: %v\n%s", err, out)
		}
	})
	if orbitErr != nil {
		t.Fatal(orbitErr)
	}
	return orbitBin
}

// fixture copies testdata/site into a new directory, with a go.mod that
// replaces the runtime with this repository, and, when userServer is set,
// testdata/server/main.go as cmd/server/main.go. It returns the directory.
func fixture(t *testing.T, userServer bool) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.CopyFS(dir, os.DirFS("testdata/site")); err != nil {
		t.Fatal(err)
	}
	gomod := "module example.com/site\n\ngo 1.27\n\ntoolchain go1.27.3\n\nrequire github.com/bertrandkhe/orbit v0.0.0\n\nreplace github.com/bertrandkhe/orbit => " + filepath.ToSlash(repoRoot) + "\n"
	writeFile(t, dir, "go.mod", gomod)
	if userServer {
		src, err := os.ReadFile("testdata/server/main.go")
		if err != nil {
			t.Fatal(err)
		}
		writeFile(t, dir, "cmd/server/main.go", string(src))
	}
	return dir
}

func writeFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	name := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func exists(name string) bool {
	_, err := os.Stat(name)
	return err == nil
}

// runOrbit runs the orbit CLI in dir and returns its exit code and combined
// output.
func runOrbit(t *testing.T, dir string, args ...string) (int, string) {
	t.Helper()
	cmd := exec.Command(orbitCLI(t), args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), goEnv...)
	out, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	if err != nil && !errors.As(err, &exit) {
		t.Fatalf("orbit %v: %v", args, err)
	}
	return cmd.ProcessState.ExitCode(), string(out)
}

// syncBuffer is a bytes.Buffer safe for a writer goroutine and a reader.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}

// server is a running server process.
type server struct {
	t      *testing.T
	cmd    *exec.Cmd
	base   string // http://127.0.0.1:port
	stderr *syncBuffer
	done   chan struct{}
}

// freePort returns a TCP port that was free a moment ago.
func freePort(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return strconv.Itoa(ln.Addr().(*net.TCPAddr).Port)
}

// start starts name with args in dir, with PORT set to a free port, and
// waits until the port accepts connections.
func start(t *testing.T, dir, name string, args ...string) *server {
	t.Helper()
	port := freePort(t)
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "PORT="+port, "ORBIT_ADDR=")
	s := &server{t: t, cmd: cmd, base: "http://127.0.0.1:" + port, stderr: &syncBuffer{}, done: make(chan struct{})}
	cmd.Stdout, cmd.Stderr = s.stderr, s.stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() { cmd.Wait(); close(s.done) }()
	t.Cleanup(func() {
		select {
		case <-s.done:
		default:
			cmd.Process.Kill()
			<-s.done
		}
	})
	deadline := time.Now().Add(20 * time.Second)
	for {
		c, err := net.DialTimeout("tcp", "127.0.0.1:"+port, time.Second)
		if err == nil {
			c.Close()
			return s
		}
		select {
		case <-s.done:
			t.Fatalf("%s exited before listening: %v\n%s", name, cmd.ProcessState, s.stderr)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s doesn't listen on %s\n%s", name, port, s.stderr)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// stop sends SIGINT and returns the exit code.
func (s *server) stop() int {
	s.t.Helper()
	if err := s.cmd.Process.Signal(os.Interrupt); err != nil {
		s.t.Fatal(err)
	}
	select {
	case <-s.done:
	case <-time.After(15 * time.Second):
		s.t.Fatalf("no exit after SIGINT\n%s", s.stderr)
	}
	return s.cmd.ProcessState.ExitCode()
}

// get requests path and returns the response with its body read.
func (s *server) get(path string) (*http.Response, string) {
	s.t.Helper()
	resp, err := httpClient.Get(s.base + path)
	if err != nil {
		s.t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		s.t.Fatalf("GET %s: %v", path, err)
	}
	return resp, string(body)
}

// htmlFiles lists the .html files under dir.
func htmlFiles(t *testing.T, dir string) []string {
	t.Helper()
	var found []string
	err := filepath.WalkDir(dir, func(name string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if strings.HasSuffix(name, ".html") {
			found = append(found, name)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return found
}
