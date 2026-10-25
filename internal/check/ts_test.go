package check

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bertrandkhe/orbit/internal/compiler"
	"github.com/bertrandkhe/orbit/internal/project"
)

const cardSrc = `---
type Props struct{ Title string }
---
<h2>{props.Title}</h2>
<script>
  // é ü: non-ASCII before the error
  const s = "日本"; const n: number = "x";
</script>
<script>const k: number = "first";</script>
<script src="./card.ts"></script>
<script is:inline>var inline = 1;</script>
`

// parseScripts parses src as orbitPath and returns its extracted scripts.
func parseScripts(t *testing.T, orbitPath, src string) []Script {
	t.Helper()
	f := compiler.Parse(src, compiler.Strict)
	if f.HasErrors() {
		t.Fatalf("parse: %v", f.Diagnostics)
	}
	p := &project.Project{Files: []*project.File{{Path: orbitPath, Src: src, AST: f}}}
	return Extract(p)
}

func TestExtract(t *testing.T) {
	scripts := parseScripts(t, "src/components/Card.orbit", cardSrc)
	if len(scripts) != 2 {
		t.Fatalf("got %d scripts, want 2 (src and is:inline skipped)", len(scripts))
	}
	want := []string{".orbit/check/src/components/Card.orbit.0.ts", ".orbit/check/src/components/Card.orbit.1.ts"}
	for i, s := range scripts {
		if s.Path != want[i] || s.Orbit != "src/components/Card.orbit" {
			t.Errorf("script %d: %s from %s, want %s", i, s.Path, s.Orbit, want[i])
		}
		if !strings.HasSuffix(s.Text(), "\nexport {};\n") {
			t.Errorf("script %d isn't a module: %q", i, s.Text())
		}
	}
	if !strings.HasPrefix(scripts[1].Text(), `const k: number = "first";`) {
		t.Errorf("script 1 text %q", scripts[1].Text())
	}
}

func TestScriptPosition(t *testing.T) {
	scripts := parseScripts(t, "src/components/Card.orbit", cardSrc)
	tests := []struct {
		s            int
		line, col16  int
		wantL, wantC int
	}{
		// Line 3 of script 0 is .orbit line 7; "n" is UTF-16 column 25
		// but byte column 29 ("日本" is 3 bytes per character).
		{0, 3, 25, 7, 29},
		{0, 3, 1, 7, 1},
		// Line 1 of script 0 is the rest of the <script> line.
		{0, 1, 1, 5, 9},
		// Script 1 starts on its tag's line: column offset 8.
		{1, 1, 7, 9, 15},
		// Past the end (moduleTail): clamped to the content's end.
		{1, 3, 1, 9, 35},
		// Past the line's end: clamped to the end of the line.
		{0, 2, 99, 6, 39},
	}
	for _, tt := range tests {
		got := scripts[tt.s].Position(tt.line, tt.col16)
		if got != (compiler.Position{Line: tt.wantL, Col: tt.wantC}) {
			t.Errorf("script %d (%d,%d) = %v, want %d:%d", tt.s, tt.line, tt.col16, got, tt.wantL, tt.wantC)
		}
	}
}

func TestByteCol(t *testing.T) {
	tests := []struct {
		line  string
		col16 int
		want  int
	}{
		{"abc", 1, 1},
		{"abc", 3, 3},
		{"abc", 4, 4},
		{"abc", 9, 4},
		{"éx", 2, 3},
		{"😀x", 3, 5}, // a surrogate pair is two UTF-16 units, four bytes
		{"", 1, 1},
	}
	for _, tt := range tests {
		if got := byteCol(tt.line, tt.col16); got != tt.want {
			t.Errorf("byteCol(%q, %d) = %d, want %d", tt.line, tt.col16, got, tt.want)
		}
	}
}

func TestMapOutput(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "src/scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "src/scripts/bad.ts"), []byte("const é: number = \"y\";\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	scripts := parseScripts(t, "src/components/Card.orbit", cardSrc)
	out := `.orbit/check/src/components/Card.orbit.0.ts(3,25): error TS2322: Type 'string' is not assignable to type 'number'.
.orbit/check/src/components/Card.orbit.1.ts(1,7): error TS2322: Type 'string' is not assignable to type 'number'.
src/scripts/bad.ts(1,10): error TS2322: Type 'string' is not assignable to type 'number'.
` + filepath.Join(root, "src/scripts/bad.ts") + `(1,7): warning TS6133: 'é' is declared but its value is never read.
src/other.ts(4,2): error TS2345: Argument of type 'X' is not assignable to parameter of type 'Y'.
  Types of property 'a' are incompatible.
error TS5083: Cannot read file '/x/tsconfig.json'.
Found 4 errors in 3 files, checked 5 files [13.09ms]

  2  .orbit/check/src/components/Card.orbit.0.ts:3
  1  src/scripts/bad.ts:1
`
	got := MapOutput(root, []byte(out), scripts)
	want := `src/components/Card.orbit:7:29: error TS2322: Type 'string' is not assignable to type 'number'.
src/components/Card.orbit:9:15: error TS2322: Type 'string' is not assignable to type 'number'.
src/scripts/bad.ts:1:11: error TS2322: Type 'string' is not assignable to type 'number'.
src/scripts/bad.ts:1:7: warning TS6133: 'é' is declared but its value is never read.
src/other.ts:4:2: error TS2345: Argument of type 'X' is not assignable to parameter of type 'Y'.
  Types of property 'a' are incompatible.
error TS5083: Cannot read file '/x/tsconfig.json'.
`
	if string(got.Text) != want {
		t.Errorf("MapOutput:\n%s\nwant:\n%s", got.Text, want)
	}
	if got.Errors != 4 {
		t.Errorf("Errors = %d, want 4", got.Errors)
	}

	clean := MapOutput(root, []byte("✓ No type errors in 2 files [13.87ms]\n"), scripts)
	if len(clean.Text) != 0 || clean.Errors != 0 {
		t.Errorf("clean output: %q, %d errors", clean.Text, clean.Errors)
	}
}

func TestTsconfig(t *testing.T) {
	tests := []struct {
		name string
		u    *UserTsconfig
		want []string // substrings
		not  []string
	}{
		{"user, default include", &UserTsconfig{}, []string{
			`"extends": "../../tsconfig.json"`, `"rootDirs": ["../..", "."]`,
			`"include": ["../../src/**/*.ts", "src/**/*.ts"]`,
		}, []string{"exclude", "allowImportingTsExtensions"}},
		{"user include and exclude", &UserTsconfig{
			Include: []string{"./src/**/*.ts", "types/*.d.ts", "/abs/x.ts"},
			Exclude: []string{"src/**/*.test.ts"},
		}, []string{
			`"include": ["../../src/**/*.ts", "../../types/*.d.ts", "/abs/x.ts", "src/**/*.ts"]`,
			`"exclude": ["../../src/**/*.test.ts"]`,
		}, nil},
		{"no user tsconfig", nil, []string{
			`"strict": true`, `"moduleResolution": "Bundler"`, `"isolatedModules": true`, `"noEmit": true`,
			`"allowImportingTsExtensions": true`, `"rootDirs"`, `"include": ["../../src/**/*.ts", "src/**/*.ts"]`,
		}, []string{"extends", "exclude"}},
	}
	for _, tt := range tests {
		got := Tsconfig(tt.u)
		for _, s := range tt.want {
			if !strings.Contains(got, s) {
				t.Errorf("%s: lacks %s:\n%s", tt.name, s, got)
			}
		}
		for _, s := range tt.not {
			if strings.Contains(got, s) {
				t.Errorf("%s: has %s:\n%s", tt.name, s, got)
			}
		}
	}
}

func TestReadUserTsconfig(t *testing.T) {
	root := t.TempDir()
	if u, err := ReadUserTsconfig(root); u != nil || err != nil {
		t.Fatalf("no tsconfig: %v, %v", u, err)
	}
	src := `// comment
{
  /* block, with "quotes" */
  "compilerOptions": { "strict": true, },
  "include": ["src/**/*.ts", "lib/**/*.ts", // trailing
  ],
  "exclude": ["a//b"],
}
`
	os.WriteFile(filepath.Join(root, "tsconfig.json"), []byte(src), 0o644)
	u, err := ReadUserTsconfig(root)
	if err != nil || u == nil {
		t.Fatal(u, err)
	}
	if strings.Join(u.Include, ",") != "src/**/*.ts,lib/**/*.ts" || strings.Join(u.Exclude, ",") != "a//b" {
		t.Errorf("got %+v", u)
	}

	// Unparseable: still extended, default include.
	os.WriteFile(filepath.Join(root, "tsconfig.json"), []byte("{ nope"), 0o644)
	if u, err := ReadUserTsconfig(root); err != nil || u == nil || u.Include != nil {
		t.Errorf("invalid: %+v, %v", u, err)
	}
}

func TestSync(t *testing.T) {
	root := t.TempDir()
	scripts := parseScripts(t, "src/components/Card.orbit", cardSrc)
	if err := Sync(root, scripts, Tsconfig(&UserTsconfig{})); err != nil {
		t.Fatal(err)
	}
	read := func(rel string) string {
		b, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	if got := read(".orbit/check/src/components/Card.orbit.1.ts"); got != scripts[1].Text() {
		t.Errorf("extracted %q", got)
	}
	if got := read(".orbit/check/tsconfig.json"); got != Tsconfig(&UserTsconfig{}) {
		t.Errorf("tsconfig %q", got)
	}

	// Unchanged files aren't rewritten; stale files and empty dirs go.
	name := filepath.Join(root, ".orbit/check/src/components/Card.orbit.0.ts")
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(name, old, old); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(root, ".orbit/check/src/pages/Old.orbit.0.ts")
	os.MkdirAll(filepath.Dir(stale), 0o755)
	os.WriteFile(stale, []byte("x"), 0o644)
	if err := Sync(root, scripts[:1], Tsconfig(nil)); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(name); err != nil || !fi.ModTime().Equal(old) {
		t.Errorf("unchanged script rewritten: %v", err)
	}
	for _, rel := range []string{".orbit/check/src/pages", ".orbit/check/src/components/Card.orbit.1.ts"} {
		if _, err := os.Stat(filepath.Join(root, rel)); err == nil {
			t.Errorf("stale %s still there", rel)
		}
	}
	if got := read(".orbit/check/tsconfig.json"); got != Tsconfig(nil) {
		t.Errorf("tsconfig not updated: %q", got)
	}
}
