package config

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// GoMod is the part of a go.mod file the CLI reads, for the CLI version
// pinning and orbit doctor (PLAN.md §6).
type GoMod struct {
	Module    string
	Go        string // the go directive, such as "1.27"; "" if missing
	Toolchain string // the toolchain directive, such as "go1.27.3"; "" if missing
	// Tools are the tool directives' package paths.
	Tools []string
	// Require maps each required module path to its version.
	Require map[string]string
	// Replace are the replace directives, in order.
	Replace []Replace
}

// Replace is one replace directive: Old [OldVersion] => New [NewVersion].
type Replace struct {
	Old, OldVersion string
	New, NewVersion string
}

// Local reports whether the replacement is a local directory, as the go
// command decides it: a path starting with ./ or ../ (or .\ and ..\), or an
// absolute path, with no version.
func (r Replace) Local() bool {
	if r.NewVersion != "" {
		return false
	}
	for _, p := range []string{"./", "../", `.\`, `..\`, "/"} {
		if strings.HasPrefix(r.New, p) {
			return true
		}
	}
	return r.New == "." || r.New == ".." || filepath.IsAbs(r.New)
}

// HasTool reports whether go.mod has a tool directive for pkg.
func (m *GoMod) HasTool(pkg string) bool { return slices.Contains(m.Tools, pkg) }

// Replacement returns the replace directive that applies to module at
// version, or nil. A replace of that exact version wins over one without a
// version, as in the go command.
func (m *GoMod) Replacement(module, version string) *Replace {
	var all *Replace
	for i := range m.Replace {
		r := &m.Replace[i]
		switch {
		case r.Old != module:
		case r.OldVersion == version:
			return r
		case r.OldVersion == "":
			all = r
		}
	}
	return all
}

// ReadGoMod parses dir/go.mod.
func ReadGoMod(dir string) (*GoMod, error) {
	data, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		return nil, err
	}
	return ParseGoMod(data)
}

// ParseGoMod parses the module, go, toolchain, tool, require and replace
// directives of a go.mod file, in their line and block forms; other
// directives are skipped. It isn't a validating parser: the go command
// reports a malformed go.mod better.
func ParseGoMod(data []byte) (*GoMod, error) {
	m := &GoMod{Require: map[string]string{}}
	var err error
	if m.Module, err = ModulePath(data); err != nil {
		return nil, err
	}
	block := "" // the directive of the open block
	for n, line := range strings.Split(string(data), "\n") {
		toks, err := modTokens(line)
		if err != nil {
			return nil, fmt.Errorf("go.mod:%d: %v", n+1, err)
		}
		if len(toks) == 0 {
			continue
		}
		verb, args := block, toks
		switch {
		case block != "" && toks[0] == ")":
			block = ""
			continue
		case block == "":
			verb, args = toks[0], toks[1:]
			if len(args) == 1 && args[0] == "(" {
				block = verb
				continue
			}
		}
		for i, a := range args {
			if a[0] == '"' || a[0] == '`' {
				if u, err := strconv.Unquote(a); err == nil {
					args[i] = u
				}
			}
		}
		bad := fmt.Errorf("go.mod:%d: malformed %s directive", n+1, verb)
		switch verb {
		case "go", "toolchain", "tool":
			if len(args) != 1 {
				return nil, bad
			}
			switch verb {
			case "go":
				m.Go = args[0]
			case "toolchain":
				m.Toolchain = args[0]
			default:
				m.Tools = append(m.Tools, args[0])
			}
		case "require":
			if len(args) != 2 {
				return nil, bad
			}
			m.Require[args[0]] = args[1]
		case "replace":
			var r Replace
			switch {
			case len(args) == 3 && args[1] == "=>":
				r = Replace{Old: args[0], New: args[2]}
			case len(args) == 4 && args[1] == "=>":
				r = Replace{Old: args[0], New: args[2], NewVersion: args[3]}
			case len(args) == 4 && args[2] == "=>":
				r = Replace{Old: args[0], OldVersion: args[1], New: args[3]}
			case len(args) == 5 && args[2] == "=>":
				r = Replace{Old: args[0], OldVersion: args[1], New: args[3], NewVersion: args[4]}
			default:
				return nil, bad
			}
			m.Replace = append(m.Replace, r)
		}
	}
	return m, nil
}
