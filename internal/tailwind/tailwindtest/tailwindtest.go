// Package tailwindtest has the test helpers for Tailwind CSS (PLAN.md §5d):
// a fake tailwindcss CLI that always works, and the real CLI for the
// integration tests, which skip with a notice without one.
package tailwindtest

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/orbit-z/orbit/internal/tailwind"
)

// fakeScript is the fake CLI: --help prints a v4 banner (FAKE_TW_VERSION
// to change it); a build logs "cwd=… args=…" and a copy of its input to
// $FAKE_TW_LOG, then prints the input with @import "tailwindcss" replaced
// by one rule per tw-* word found under $FAKE_TW_SCAN (Tailwind's source
// detection, in miniature), @apply x replaced by --applied: x, and
// @reference rules removed. An input with FAIL_TW fails as Tailwind does,
// with a boxed, colored message on stderr; FAIL_TW_POS adds a
// stdin.css:2:3 position.
const fakeScript = `#!/bin/sh
for a in "$@"; do
  if [ "$a" = "--help" ]; then echo "≈ tailwindcss v${FAKE_TW_VERSION:-4.3.3}"; echo; echo "Usage:"; exit 0; fi
done
in=$(cat)
if [ -n "$FAKE_TW_LOG" ]; then
  printf 'cwd=%s args=%s\n' "$PWD" "$*" >> "$FAKE_TW_LOG"
fi
case "$in" in
  *FAIL_TW_POS*) printf '\033[31mError:\033[39m\n\033[2m┌\033[22m\n\033[2m│\033[22m CssSyntaxError: stdin.css:2:3: Missing closing } at .a\n\033[2m└\033[22m\n' >&2; exit 1;;
  *FAIL_TW*) printf '≈ tailwindcss v4.3.3\n\n\033[31mError:\033[39m\n\033[2m┌\033[22m\n\033[2m│\033[22m Error: Cannot apply unknown utility class ` + "`nope`" + `. Are you using CSS modules or similar and missing ` + "`@reference`" + `? https://tailwindcss.com/docs\n\033[2m└\033[22m\n' >&2; exit 1;;
esac
utils=""
if [ -n "$FAKE_TW_SCAN" ]; then
  for c in $(grep -rhoE 'tw-[a-z0-9-]+' "$FAKE_TW_SCAN" --include='*.orbit' --include='*.go' --include='*.md' 2>/dev/null | sort -u); do
    utils="$utils.$c{color:red}"
  done
fi
echo "/*! fake tailwindcss */"
printf '%s\n' "$in" | sed -e "s/@import \"tailwindcss\"[^;]*;/.tw-base{margin:0}$utils/" -e 's/@apply \([^;]*\);/--applied: \1;/' -e '/@reference/d'
`

// Fake writes the fake CLI into a new directory, puts it first on PATH,
// and returns the path of its log (one "cwd=… args=…" line per run). scan
// is the directory it searches for tw-* classes ("" for none).
func Fake(t *testing.T, scan string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake tailwindcss is a shell script")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "tailwindcss")
	if err := os.WriteFile(bin, []byte(fakeScript), 0o755); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(dir, "log")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FAKE_TW_LOG", log)
	t.Setenv("FAKE_TW_SCAN", scan)
	return log
}

// Log returns the fake CLI's log lines.
func Log(t *testing.T, log string) []string {
	t.Helper()
	data, err := os.ReadFile(log)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(string(data)), "\n")
}

// Real finds a real Tailwind CSS v4 CLI, ORBIT_TEST_TAILWIND or
// tailwindcss on PATH, and puts its directory first on PATH; without one
// it skips the test with a notice (like the TypeScript and Bun tests).
// bun x isn't used: it would download.
func Real(t *testing.T) string {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping the Tailwind CSS integration test in -short mode")
	}
	p := os.Getenv("ORBIT_TEST_TAILWIND")
	if p == "" {
		var err error
		if p, err = exec.LookPath("tailwindcss"); err != nil {
			t.Skipf("NOTICE: the Tailwind CSS CLI v%s was not found; skipping the Tailwind integration test (set ORBIT_TEST_TAILWIND to a tailwindcss binary)", tailwind.Pinned)
		}
	}
	out, _ := exec.Command(p, "--help").CombinedOutput()
	if v, ok := tailwind.ParseVersion(string(out)); !ok || v.Major != 4 {
		t.Skipf("NOTICE: %s isn't the Tailwind CSS CLI v4; skipping the Tailwind integration test", p)
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		t.Fatal(err)
	}
	// A directory with only tailwindcss in it, first on PATH.
	dir := t.TempDir()
	if err := os.Symlink(abs, filepath.Join(dir, "tailwindcss")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return abs
}
