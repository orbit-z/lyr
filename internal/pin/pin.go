// Package pin decides how a globally installed lyr CLI defers to the
// version a project pins (PLAN.md §6 "Version pinning"): it re-execs
// go tool lyr when go.mod has the tool directive, and otherwise stops
// on a version mismatch with the github.com/orbit-z/lyr requirement.
package pin

import (
	"fmt"
	"runtime/debug"

	"github.com/orbit-z/lyr/internal/config"
)

const (
	// Module is the framework's module path.
	Module = "github.com/orbit-z/lyr"
	// Tool is the CLI's package, named by the go.mod tool directive.
	Tool = Module + "/cmd/lyr"
	// ReexecEnv is set to "1" for the go tool lyr the global CLI
	// re-execs, so that it never re-execs again.
	ReexecEnv = "LYR_REEXEC"
)

// Self describes the running CLI binary.
type Self struct {
	// Pinned is true when the binary was built inside another module's
	// build, with Lyr as a dependency: the go tool lyr of a project.
	// A global install (go install …/cmd/lyr@v1.2.3), and a go build or
	// go run in a clone of Lyr, have Lyr as their main module instead.
	Pinned bool
	// Version is the binary's version of the Lyr module: v1.2.3, a
	// pseudo-version, or "(devel)"; "" if unknown.
	Version string
}

// Current returns the running binary's Self, from its build info.
func Current() Self {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return Self{}
	}
	return fromBuildInfo(bi)
}

func fromBuildInfo(bi *debug.BuildInfo) Self {
	if bi.Main.Path == Module {
		return Self{Version: bi.Main.Version}
	}
	for _, d := range bi.Deps {
		if d.Path == Module {
			v := d.Version
			if d.Replace != nil && d.Replace.Version == "" {
				v = "(devel)"
			}
			return Self{Pinned: true, Version: v}
		}
	}
	// A binary without the Lyr module (only a test binary of another
	// module could be one): treat it as global, with no version.
	return Self{}
}

// Decision is what the global CLI does in a project.
type Decision struct {
	// Reexec is true when the command must run as go tool lyr.
	Reexec bool
	// Err is the version-mismatch error, when the CLI must stop.
	Err error
}

// Decide applies the pinning rule for a CLI self in the project with go.mod
// mod: a pinned binary runs as it is; a global one re-execs when go.mod has
// the tool directive, and otherwise must have the version go.mod requires,
// unless go.mod replaces Lyr with a local directory (or doesn't require
// Lyr at all, when the command reports what's wrong itself).
func Decide(mod *config.GoMod, self Self) Decision {
	if self.Pinned {
		return Decision{}
	}
	if mod.HasTool(Tool) {
		return Decision{Reexec: true}
	}
	if e := Mismatch(mod, self); e != nil {
		return Decision{Err: e}
	}
	return Decision{}
}

// MismatchError is the version-mismatch error: the global CLI's version
// differs from the Lyr version go.mod requires.
type MismatchError struct {
	Have string // the CLI's version ("unknown" if it has none)
	Want string // go.mod's requirement
}

func (e *MismatchError) Error() string {
	return fmt.Sprintf("version mismatch: this lyr is %s, but go.mod requires %s %s; pin the CLI with %s, then run go tool lyr (see lyr doctor)",
		e.Have, Module, e.Want, e.Fix())
}

// Fix is the command that pins the CLI at go.mod's version.
func (e *MismatchError) Fix() string { return "go get -tool " + Tool + "@" + e.Want }

// Mismatch returns the version-mismatch error for a CLI self in the project
// with go.mod mod, or nil when the versions match or the check is skipped
// (no Lyr requirement, or a local replace).
func Mismatch(mod *config.GoMod, self Self) *MismatchError {
	want, ok := mod.Require[Module]
	if !ok {
		return nil
	}
	if r := mod.Replacement(Module, want); r != nil && r.Local() {
		return nil
	}
	if self.Version == want {
		return nil
	}
	have := self.Version
	if have == "" {
		have = "unknown"
	}
	return &MismatchError{Have: have, Want: want}
}
