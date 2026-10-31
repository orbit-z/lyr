// Package doctor implements lyr doctor (PLAN.md §6): it checks the
// environment (Go, gopls, Bun, TypeScript, Tailwind) and, inside a project, go.mod, the pinned
// CLI, the generated code, .gitignore and the config, and reports each as
// ok, warning or error with the command that fixes it.
//
// Everything outside the project's files goes through Env, so tests run
// with fake commands and binaries.
package doctor

import (
	"bytes"
	"context"
	"debug/buildinfo"
	"errors"
	"fmt"
	"go/version"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"strings"

	"github.com/orbit-z/lyr/internal/check"
	"github.com/orbit-z/lyr/internal/compiler"
	"github.com/orbit-z/lyr/internal/config"
	"github.com/orbit-z/lyr/internal/pin"
	"github.com/orbit-z/lyr/internal/project"
)

// Status is a check's outcome.
type Status string

const (
	OK      Status = "ok"
	Warning Status = "warning"
	Error   Status = "error"
)

// Check is the result of one check, as lyr doctor --json prints it.
type Check struct {
	Name    string `json:"name"`
	Status  Status `json:"status"`
	Message string `json:"message"`
	Fix     string `json:"fix"` // empty for a passing check
}

// Report is every check, in order (PLAN.md §6).
type Report struct {
	Checks []Check `json:"checks"`
}

// Failed reports whether any check is an error (exit code 1).
func (r *Report) Failed() bool {
	for _, c := range r.Checks {
		if c.Status == Error {
			return true
		}
	}
	return false
}

// WriteText prints r as ✓ / ⚠ / ✗ lines, each problem followed by its fix.
func (r *Report) WriteText(w io.Writer) {
	marks := map[Status]string{OK: "✓", Warning: "⚠", Error: "✗"}
	for _, c := range r.Checks {
		fmt.Fprintf(w, "%s %s: %s\n", marks[c.Status], c.Name, c.Message)
		if c.Fix != "" {
			fmt.Fprintf(w, "    fix: %s\n", c.Fix)
		}
	}
}

// Env is how the checks reach the world outside the project's files.
type Env struct {
	// Dir is the working directory: the project is the module around it.
	Dir string
	// LookPath finds a command on PATH (exec.LookPath).
	LookPath func(file string) (string, error)
	// Run runs a command in dir and returns its standard output; the error
	// of a failed command carries the first line of its standard error.
	Run func(ctx context.Context, dir, name string, args ...string) ([]byte, error)
	// BuildInfo reads the Go build info of a binary (buildinfo.ReadFile).
	BuildInfo func(path string) (*debug.BuildInfo, error)
	// Self is the running CLI (pin.Current).
	Self pin.Self
	// Generate returns what lyr generate would change in the project at
	// root, without writing (project.Generator.Plan plus
	// project.DiffGenerated), and the diagnostics that stop it.
	Generate func(ctx context.Context, root string) (*project.WriteResult, []project.Diagnostic, error)
}

// DefaultEnv is the real environment, for the CLI.
func DefaultEnv(dir string) Env {
	return Env{
		Dir:       dir,
		LookPath:  exec.LookPath,
		Run:       run,
		BuildInfo: buildinfo.ReadFile,
		Self:      pin.Current(),
		Generate:  generate,
	}
}

func run(ctx context.Context, dir, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if line := firstLine(stderr.String()); line != "" {
			return out, errors.New(line)
		}
		return out, err
	}
	return out, nil
}

func generate(ctx context.Context, root string) (*project.WriteResult, []project.Diagnostic, error) {
	g := &project.Generator{Root: root}
	res, files, err := g.Plan(ctx)
	if err != nil {
		return nil, nil, err
	}
	if files == nil {
		return nil, res.Diagnostics, nil
	}
	diff, err := project.DiffGenerated(root, files)
	return diff, nil, err
}

// The minimum versions and the fix commands.
const (
	minGo        = "go1.27"
	installGo    = "install Go 1.27 or later from https://go.dev/dl/"
	installGopls = "go install golang.org/x/tools/gopls@latest"
	lyrGenerate  = "lyr generate"
)

// recommendedGopls is the gopls version Lyr's editor support was
// verified with (PLAN.md §9).
var recommendedGopls = check.Version{Major: 0, Minor: 24, Patch: 0}

// Run runs every check: the environment checks, plus the project checks
// when env.Dir is inside a Lyr project (a module root with go.mod and
// src/, PLAN.md §9).
func Run(ctx context.Context, env Env) *Report {
	d := &doctor{env: env, ctx: ctx}
	if root, err := config.FindRoot(env.Dir); err == nil {
		if fi, err := os.Stat(filepath.Join(root, "src")); err == nil && fi.IsDir() {
			d.root = root
			d.mod, d.modErr = config.ReadGoMod(root)
		}
	}
	r := &Report{}
	r.Checks = append(r.Checks, d.goCheck())
	if d.root != "" {
		r.Checks = append(r.Checks, d.cliCheck())
	}
	r.Checks = append(r.Checks, d.goplsCheck(), d.bunCheck(), d.typescriptCheck(), d.tailwindCheck())
	if d.root != "" {
		r.Checks = append(r.Checks, d.genCheck(), d.gitignoreCheck(), d.configCheck(), d.tsconfigCheck())
	}
	return r
}

type doctor struct {
	env    Env
	ctx    context.Context
	root   string // "" outside a project
	mod    *config.GoMod
	modErr error
}

func ok(name, format string, args ...any) Check {
	return Check{Name: name, Status: OK, Message: fmt.Sprintf(format, args...)}
}

func warn(name, fix, format string, args ...any) Check {
	return Check{Name: name, Status: Warning, Message: fmt.Sprintf(format, args...), Fix: fix}
}

func fail(name, fix, format string, args ...any) Check {
	return Check{Name: name, Status: Error, Message: fmt.Sprintf(format, args...), Fix: fix}
}

// dir is where commands run: the project root, else the working directory.
func (d *doctor) dir() string {
	if d.root != "" {
		return d.root
	}
	return d.env.Dir
}

// goCheck: the go command, the active toolchain, and go.mod's go and
// toolchain lines.
func (d *doctor) goCheck() Check {
	const name = "go"
	if _, err := d.env.LookPath("go"); err != nil {
		return fail(name, installGo, "go command not found")
	}
	// In a project, go env reports the toolchain go.mod selects (switching
	// to it with GOTOOLCHAIN=auto), which is the one builds use.
	out, err := d.env.Run(d.ctx, d.dir(), "go", "env", "GOVERSION", "GOTOOLCHAIN")
	if err != nil {
		return fail(name, installGo, "go env failed: %v", err)
	}
	active, gotoolchain, _ := strings.Cut(strings.TrimSpace(string(out)), "\n")
	active, gotoolchain = strings.TrimSpace(active), strings.TrimSpace(gotoolchain)
	if d.root == "" {
		if !version.IsValid(active) || version.Compare(active, minGo) >= 0 {
			return ok(name, "%s", active)
		}
		// Go 1.21 and later switch to the toolchain a project's go.mod
		// names, unless GOTOOLCHAIN=local.
		switch {
		case version.Compare(active, "go1.21") < 0:
			return warn(name, installGo, "%s is older than go 1.27, which Lyr projects need, and too old to switch toolchains", active)
		case gotoolchain == "local":
			return warn(name, "go env -w GOTOOLCHAIN=auto", "%s is older than go 1.27, which Lyr projects need, and GOTOOLCHAIN=local stops it from switching", active)
		}
		return ok(name, "%s (switches to the Go 1.27 toolchain in projects, GOTOOLCHAIN=%s)", active, gotoolchain)
	}
	if d.modErr != nil {
		return fail(name, "fix go.mod", "%v", d.modErr)
	}
	mod := d.mod
	if mod.Go == "" || !version.IsValid("go"+mod.Go) || version.Compare("go"+mod.Go, minGo) < 0 {
		have := "has no go line"
		if mod.Go != "" {
			have = "has go " + mod.Go
		}
		return fail(name, "go mod edit -go=1.27", "go.mod %s; Lyr needs go 1.27 or later", have)
	}
	if version.IsValid(active) && version.Compare(active, "go"+mod.Go) < 0 {
		return fail(name, "go env -w GOTOOLCHAIN=auto", "the active toolchain %s is older than go %s in go.mod", active, mod.Go)
	}
	if mod.Toolchain == "" {
		tc := active
		if !version.IsValid(tc) {
			tc = "go1.27.3"
		}
		return warn(name, "go get toolchain@"+tc, "%s (go.mod: go %s, no toolchain line, so go picks %s.0)", active, mod.Go, version.Lang("go"+mod.Go))
	}
	return ok(name, "%s (go.mod: go %s, toolchain %s)", active, mod.Go, mod.Toolchain)
}

// cliCheck: the running CLI against the version the project pins.
func (d *doctor) cliCheck() Check {
	const name = "cli"
	self := d.env.Self
	if d.modErr != nil {
		return fail(name, "fix go.mod", "%v", d.modErr)
	}
	mod := d.mod
	have := self.Version
	if have == "" {
		have = "unknown"
	}
	if mod.HasTool(pin.Tool) {
		// The global CLI re-execs go tool lyr: check that the go command
		// can load the pinned CLI, without building it.
		out, err := d.env.Run(d.ctx, d.root, "go", "list", "-f", "{{with .Module}}{{.Version}}{{with .Replace}} => {{.Path}}{{end}}{{end}}", pin.Tool)
		if err != nil {
			return fail(name, "go mod tidy", "go tool lyr can't load the pinned CLI: %v", err)
		}
		pinned := strings.TrimSpace(string(out))
		if self.Pinned {
			return ok(name, "running the project's pinned CLI (go tool lyr, %s)", pinned)
		}
		return ok(name, "lyr %s hands commands to the pinned go tool lyr (%s)", have, pinned)
	}
	if self.Pinned {
		return ok(name, "running %s %s from the module graph", pin.Tool, have)
	}
	want, required := mod.Require[pin.Module]
	if !required {
		return fail(name, "go get -tool "+pin.Tool+"@latest", "go.mod doesn't require %s", pin.Module)
	}
	if e := pin.Mismatch(mod, self); e != nil {
		return fail(name, e.Fix(), "this lyr is %s, but go.mod requires %s %s (no tool directive)", e.Have, pin.Module, e.Want)
	}
	if r := mod.Replacement(pin.Module, want); r != nil && r.Local() {
		return ok(name, "lyr %s; go.mod replaces %s with %s, so versions aren't compared", have, pin.Module, r.New)
	}
	return ok(name, "lyr %s matches go.mod", have)
}

// goplsCheck: the gopls on PATH, with the LSP's refuse rule (PLAN.md §9
// step 5): built with a Go older than the project's go directive is an
// error; older than the recommended version a warning.
func (d *doctor) goplsCheck() Check {
	const name = "gopls"
	path, err := d.env.LookPath("gopls")
	if err != nil {
		return fail(name, installGopls, "not found on PATH; lyr lsp needs it for Go features in .lyr files")
	}
	bi, err := d.env.BuildInfo(path)
	if err != nil {
		return fail(name, installGopls, "can't read the build info of %s: %v", path, err)
	}
	goVersion, _, _ := strings.Cut(bi.GoVersion, " ") // "go1.27.3 X:…"
	v := bi.Main.Version
	if v == "" {
		v = "(unknown version)"
	}
	want, where := minGo, "go 1.27, the oldest Go Lyr supports"
	if d.mod != nil && d.mod.Go != "" && version.IsValid("go"+d.mod.Go) {
		want, where = "go"+d.mod.Go, "go "+d.mod.Go+" in go.mod"
	}
	if version.IsValid(goVersion) && version.Compare(goVersion, want) < 0 {
		return fail(name, installGopls, "gopls %s was built with %s, older than %s", v, goVersion, where)
	}
	if pv, parsed := check.ParseBunVersion(v); parsed && pv.Less(recommendedGopls) {
		return warn(name, installGopls, "gopls %s is older than the recommended v%s", v, recommendedGopls)
	}
	return ok(name, "%s, built with %s", v, goVersion)
}

// bunCheck: Bun ≥ check.MinBun, for lyr check's TypeScript half; a
// warning only.
func (d *doctor) bunCheck() Check {
	const name = "bun"
	const skips = "lyr check will skip TypeScript type checking"
	path, err := d.env.LookPath("bun")
	if err != nil {
		return warn(name, check.InstallBun, "not found (%s or later needed); %s", check.MinBun, skips)
	}
	out, err := d.env.Run(d.ctx, d.dir(), path, "--version")
	v, parsed := check.ParseBunVersion(string(out))
	if err != nil || !parsed {
		return warn(name, check.InstallBun, "can't read the version of %s (%s or later needed); %s", path, check.MinBun, skips)
	}
	if v.Less(check.MinBun) {
		return warn(name, check.UpgradeBun, "%s is older than %s; %s", v, check.MinBun, skips)
	}
	return ok(name, "%s", v)
}

// genCheck: lyr_gen/ exists and is what lyr generate would write now.
// The generated files carry no source hash, so the project is regenerated
// in memory and compared with the disk. lyr_gen/manifest/manifest.go is
// left out: it lists asset URLs, lyr dev writes it empty, and lyr build
// rewrites it anyway.
func (d *doctor) genCheck() Check {
	const name = "lyr_gen"
	if fi, err := os.Stat(filepath.Join(d.root, compiler.GenDir)); err != nil || !fi.IsDir() {
		return warn(name, lyrGenerate, "lyr_gen/ is missing")
	}
	diff, diags, err := d.env.Generate(d.ctx, d.root)
	if err != nil {
		return warn(name, lyrGenerate, "can't regenerate to compare: %v", err)
	}
	if project.HasErrors(diags) {
		var first string
		n := 0
		for _, dg := range diags {
			if dg.Severity == compiler.SeverityError {
				if n == 0 {
					first = dg.String()
				}
				n++
			}
		}
		more := ""
		if n > 1 {
			more = fmt.Sprintf(" (and %d more)", n-1)
		}
		return warn(name, lyrGenerate, "can't check: lyr generate fails with %s%s", first, more)
	}
	var stale []string
	for _, p := range append(diff.Written, diff.Removed...) {
		if p != project.ManifestFile {
			stale = append(stale, p)
		}
	}
	if len(stale) > 0 {
		files := "1 file differs"
		if len(stale) > 1 {
			files = fmt.Sprintf("%d files differ", len(stale))
		}
		return warn(name, lyrGenerate, "out of date: %s from what lyr generate writes (%s)", files, stale[0])
	}
	return ok(name, "up to date")
}

// gitignoreCheck: .gitignore covers lyr_gen/, .lyr/ and dist/. A
// committed lyr_gen/ (PLAN.md §6 "Fresh clones and CI") ignores only
// lyr_gen/static/, and that's accepted too.
func (d *doctor) gitignoreCheck() Check {
	const name = "gitignore"
	data, err := os.ReadFile(filepath.Join(d.root, ".gitignore"))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return warn(name, "", "can't read .gitignore: %v", err)
	}
	ignored := ignoredNames(string(data))
	gen := "lyr_gen/"
	if !ignored["lyr_gen"] && ignored["lyr_gen/static"] {
		gen = "lyr_gen/static/"
		ignored["lyr_gen"] = true
	}
	var missing []string
	for _, n := range []string{"lyr_gen", ".lyr", "dist"} {
		if !ignored[n] {
			missing = append(missing, n+"/")
		}
	}
	if len(missing) > 0 {
		fix := "printf '" + strings.Join(missing, `\n`) + `\n' >> .gitignore`
		if errors.Is(err, fs.ErrNotExist) {
			return warn(name, fix, "no .gitignore, so %s aren't ignored", strings.Join(missing, ", "))
		}
		return warn(name, fix, ".gitignore doesn't cover %s", strings.Join(missing, ", "))
	}
	return ok(name, "covers %s, .lyr/ and dist/", gen)
}

// ignoredNames returns the directory names a .gitignore ignores with a
// plain pattern (name, name/, /name, /name/, name/**), minus those a later
// !pattern re-includes. Wildcard patterns aren't interpreted.
func ignoredNames(gitignore string) map[string]bool {
	ignored := map[string]bool{}
	for _, line := range strings.Split(gitignore, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line[0] == '#' {
			continue
		}
		neg := line[0] == '!'
		line = strings.TrimPrefix(line, "!")
		line = strings.TrimSuffix(line, "/**")
		line = strings.Trim(line, "/")
		ignored[line] = !neg
	}
	return ignored
}

// configCheck: lyr.json is valid, the server entry exists, and
// src/locals doesn't lead to lyr_gen/app.
func (d *doctor) configCheck() Check {
	const name = "config"
	cfg, err := config.Load(d.root)
	if err != nil {
		return fail(name, "fix lyr.json", "%v", err)
	}
	entry, generated := cfg.ServerEntry(d.root)
	if !generated {
		if fi, err := os.Stat(filepath.Join(d.root, filepath.FromSlash(entry))); err != nil || !fi.IsDir() {
			return fail(name, "create "+entry+", or remove server from lyr.json to use "+config.DefaultServer,
				"lyr.json: server %q doesn't exist", entry)
		}
	}
	if d.mod != nil {
		for _, dg := range project.CheckLocals(d.root, d.mod.Module) {
			if dg.Severity == compiler.SeverityError {
				return fail(name, "move the code that needs lyr_gen/app from src/locals into cmd/server", "%s", dg.String())
			}
		}
	}
	server := "server " + entry
	if generated {
		server = "server: the generated default (no " + config.DefaultServer + ")"
	}
	if _, err := os.Stat(filepath.Join(d.root, "lyr.json")); err != nil {
		return ok(name, "no lyr.json (defaults); %s", server)
	}
	return ok(name, "lyr.json valid; %s", server)
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	line, _, _ := strings.Cut(s, "\n")
	return strings.TrimSpace(line)
}
