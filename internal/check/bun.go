package check

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// MinBun is the oldest Bun whose bun check the TypeScript half uses.
var MinBun = Version{1, 4, 4}

// Commands named in the skip notice (docs/troubleshooting.md).
const (
	InstallBun = "curl -fsSL https://bun.sh/install | bash"
	UpgradeBun = "bun upgrade"
)

// Version is a major.minor.patch version.
type Version struct{ Major, Minor, Patch int }

func (v Version) String() string { return fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch) }

// Less reports whether v is older than w.
func (v Version) Less(w Version) bool {
	if v.Major != w.Major {
		return v.Major < w.Major
	}
	if v.Minor != w.Minor {
		return v.Minor < w.Minor
	}
	return v.Patch < w.Patch
}

// ParseBunVersion parses the output of bun --version: "1.4.4", with an
// optional "v" prefix and anything after the patch number ignored
// ("1.4.5-canary.3+abc", a trailing newline).
func ParseBunVersion(s string) (Version, bool) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "v")
	var v Version
	parts := strings.SplitN(s, ".", 3)
	if len(parts) != 3 {
		return v, false
	}
	nums := []*int{&v.Major, &v.Minor, &v.Patch}
	for i, p := range parts {
		if i == 2 {
			end := 0
			for end < len(p) && p[end] >= '0' && p[end] <= '9' {
				end++
			}
			p = p[:end]
		}
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return Version{}, false
		}
		*nums[i] = n
	}
	return v, true
}

// findBun returns the path of a usable bun, or "" and the skip notice.
// bun is the configured path, or "" to look it up on PATH.
func findBun(ctx context.Context, bun string) (path, notice string) {
	if bun == "" {
		p, err := exec.LookPath("bun")
		if err != nil {
			return "", fmt.Sprintf("orbit check: skipping TypeScript type checking: bun not found (%s or later needed); install it with: %s", MinBun, InstallBun)
		}
		bun = p
	}
	out, err := exec.CommandContext(ctx, bun, "--version").Output()
	v, ok := ParseBunVersion(string(bytes.TrimSpace(out)))
	if err != nil || !ok {
		return "", fmt.Sprintf("orbit check: skipping TypeScript type checking: can't read the version of %s (%s or later needed); reinstall it with: %s", bun, MinBun, InstallBun)
	}
	if v.Less(MinBun) {
		return "", fmt.Sprintf("orbit check: skipping TypeScript type checking: bun %s is older than %s; upgrade it with: %s", v, MinBun, UpgradeBun)
	}
	return bun, ""
}
