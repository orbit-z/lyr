package check

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var goEnv = []string{"GOFLAGS=-mod=mod", "GOWORK=off"}

func needGo(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("runs go vet and bun check")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go command not found")
	}
}

func needBun(t *testing.T) {
	t.Helper()
	needGo(t)
	if p, notice := findBun(context.Background(), ""); p == "" {
		t.Skip(notice)
	}
}

// newProject writes a project with a go.mod that replaces the runtime with
// this repository, the scaffold's tsconfig.json, and files (relative path
// → content).
func newProject(t *testing.T, files map[string]string) string {
	t.Helper()
	repo, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	all := map[string]string{
		"go.mod": "module example.com/site\n\ngo 1.27\n\ntoolchain go1.27.3\n\nrequire github.com/bertrandkhe/orbit v0.0.0\n\nreplace github.com/bertrandkhe/orbit => " + filepath.ToSlash(repo) + "\n",
		"tsconfig.json": `{
  "compilerOptions": {
    "target": "ESNext",
    "module": "ESNext",
    "moduleResolution": "Bundler",
    "lib": ["ESNext", "DOM", "DOM.Iterable"],
    "strict": true,
    "noEmit": true,
    "isolatedModules": true,
    "resolveJsonModule": true,
    "allowJs": true,
    "skipLibCheck": true
  },
  "include": ["src/**/*.ts"]
}
`,
		"src/env.d.ts": "",
		"src/pages/index.orbit": `---
import Card "../components/Card.orbit"
---
<Card title="Hi" />
`,
		"src/scripts/util.ts": "export function twice(n: number): number {\n\treturn n * 2;\n}\n",
	}
	for k, v := range files {
		all[k] = v
	}
	for rel, content := range all {
		name := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(name, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func card(template, script string) string {
	return "---\ntype Props struct {\n\tTitle string\n}\n---\n" + template + "\n<script>\n" + script + "\n</script>\n"
}

// output runs a check and returns its printed output, as orbit check
// prints it.
func output(t *testing.T, dir string) (*Result, string) {
	t.Helper()
	res, err := Run(context.Background(), Options{Root: dir, Env: goEnv})
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for _, d := range res.Generate.Diagnostics {
		b.WriteString(d.String() + "\n")
	}
	b.Write(res.GoOutput)
	if res.Notice != "" {
		b.WriteString(res.Notice + "\n")
	}
	b.Write(res.TSOutput)
	return res, b.String()
}

func TestCheckClean(t *testing.T) {
	needBun(t)
	t.Parallel()
	dir := newProject(t, map[string]string{
		"src/components/Card.orbit": card("<h2>{props.Title}</h2>",
			"import { twice } from \"../scripts/util\";\nconst n: number = twice(2);\nconsole.log(n);"),
	})
	res, out := output(t, dir)
	if res.Failed() || out != "" {
		t.Fatalf("clean project failed:\n%s", out)
	}
	if !exists(filepath.Join(dir, "orbit_gen")) {
		t.Error("orbit check didn't write orbit_gen/")
	}
	if !exists(filepath.Join(dir, ".orbit/check/src/components/Card.orbit.0.ts")) {
		t.Error("script not extracted")
	}
	t.Logf("timings: %+v", res.Timings)
}

func TestCheckTSError(t *testing.T) {
	needBun(t)
	t.Parallel()
	dir := newProject(t, map[string]string{
		// <script> is line 7: the errors are on lines 9 and 10, column 7.
		"src/components/Card.orbit": card("<h2>{props.Title}</h2>",
			"import { twice } from \"../scripts/util\";\nconst n: number = \"x\";\nconst m: string = twice(1);\nconsole.log(n, m);"),
	})
	res, out := output(t, dir)
	want := "src/components/Card.orbit:9:7: error TS2322: Type 'string' is not assignable to type 'number'.\n" +
		"src/components/Card.orbit:10:7: error TS2322: Type 'number' is not assignable to type 'string'.\n"
	if !res.Failed() || out != want {
		t.Fatalf("failed=%v, output:\n%s\nwant:\n%s", res.Failed(), out, want)
	}
}

func TestCheckUserTS(t *testing.T) {
	needBun(t)
	t.Parallel()
	dir := newProject(t, map[string]string{
		"src/components/Card.orbit": card("<h2>{props.Title}</h2>", "console.log(1);"),
		"src/scripts/bad.ts":        "export const x: number = \"é\";\n",
	})
	_, out := output(t, dir)
	if want := "src/scripts/bad.ts:1:14: error TS2322: Type 'string' is not assignable to type 'number'.\n"; out != want {
		t.Fatalf("output:\n%s\nwant:\n%s", out, want)
	}
}

func TestCheckGoError(t *testing.T) {
	needBun(t)
	t.Parallel()
	dir := newProject(t, map[string]string{
		"src/components/Card.orbit": card("<h2>{props.Titel}</h2>", "console.log(1);"),
	})
	res, out := output(t, dir)
	if !res.Failed() || !strings.Contains(out, "src/components/Card.orbit:6:12: props.Titel undefined (type Props has no field or method Titel)") {
		t.Fatalf("failed=%v, output:\n%s", res.Failed(), out)
	}
	if strings.Contains(out, "orbit_gen/") && !strings.Contains(out, "# example.com/site/orbit_gen/") {
		t.Errorf("generated path in output:\n%s", out)
	}
}

func TestCheckNoTsconfig(t *testing.T) {
	needBun(t)
	t.Parallel()
	dir := newProject(t, map[string]string{
		"src/components/Card.orbit": card("<h2>{props.Title}</h2>", "const n: number = \"x\";"),
	})
	os.Remove(filepath.Join(dir, "tsconfig.json"))
	res, out := output(t, dir)
	if !res.Failed() || !strings.Contains(out, "src/components/Card.orbit:8:7: error TS2322") {
		t.Fatalf("failed=%v, output:\n%s", res.Failed(), out)
	}
}

// TestCheckUserInclude: a .ts file in a directory the user's include adds
// outside src/ is checked, and the user's exclude still applies.
func TestCheckUserInclude(t *testing.T) {
	needBun(t)
	t.Parallel()
	dir := newProject(t, map[string]string{
		"src/components/Card.orbit": card("<h2>{props.Title}</h2>", "console.log(1);"),
		"lib/bad.ts":                "export const x: number = \"y\";\n",
		"src/skip.test.ts":          "export const y: number = \"z\";\n",
	})
	tsconfig, err := os.ReadFile(filepath.Join(dir, "tsconfig.json"))
	if err != nil {
		t.Fatal(err)
	}
	tsconfig = []byte(strings.Replace(string(tsconfig), `"include": ["src/**/*.ts"]`,
		`"include": ["src/**/*.ts", "lib/**/*.ts"], // comment
  "exclude": ["src/**/*.test.ts"],`, 1))
	os.WriteFile(filepath.Join(dir, "tsconfig.json"), tsconfig, 0o644)
	res, out := output(t, dir)
	if want := "lib/bad.ts:1:14: error TS2322: Type 'string' is not assignable to type 'number'.\n"; !res.Failed() || out != want {
		t.Fatalf("output:\n%s\nwant:\n%s", out, want)
	}
}

// TestCheckTSExtension: without a user tsconfig, the defaults allow
// importing with a .ts extension, as esbuild does.
func TestCheckTSExtension(t *testing.T) {
	needBun(t)
	t.Parallel()
	dir := newProject(t, map[string]string{
		"src/components/Card.orbit": card("<h2>{props.Title}</h2>",
			"import { twice } from \"../scripts/util.ts\";\nconst n: number = twice(2);\nconsole.log(n);"),
	})
	os.Remove(filepath.Join(dir, "tsconfig.json"))
	if res, out := output(t, dir); res.Failed() || out != "" {
		t.Fatalf("output:\n%s", out)
	}
}

// TestCheckNoBun runs with a PATH that has the go command but no bun.
func TestCheckNoBun(t *testing.T) {
	needGo(t)
	goPath, err := exec.LookPath("go")
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	if err := os.Symlink(goPath, filepath.Join(bin, "go")); err != nil {
		t.Skip(err)
	}
	t.Setenv("PATH", bin)
	dir := newProject(t, map[string]string{
		"src/components/Card.orbit": card("<h2>{props.Titel}</h2>", "const n: number = \"x\";"),
	})
	res, out := output(t, dir)
	notice := "orbit check: skipping TypeScript type checking: bun not found (1.4.4 or later needed); install it with: curl -fsSL https://bun.sh/install | bash"
	if !strings.Contains(out, notice) || !strings.Contains(out, "props.Titel undefined") || strings.Contains(out, "TS2322") {
		t.Fatalf("output:\n%s", out)
	}
	if !res.Failed() {
		t.Error("Go error didn't fail the check")
	}

	// A clean Go half passes despite the notice.
	os.WriteFile(filepath.Join(dir, "src/components/Card.orbit"), []byte(card("<h2>{props.Title}</h2>", "const n: number = \"x\";")), 0o644)
	res, out = output(t, dir)
	if res.Failed() || strings.TrimSpace(out) != notice {
		t.Fatalf("failed=%v, output:\n%s", res.Failed(), out)
	}
}

func exists(name string) bool {
	_, err := os.Stat(name)
	return err == nil
}
