package compiler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"go/ast"
	goparser "go/parser"
	gotoken "go/token"
	"go/types"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// tsMinBun is the oldest Bun whose bun check the conformance test uses
// (internal/check.MinBun; that package imports this one).
const tsMinBun = "1.4.4"

// tsNeedBun returns a usable bun or skips the test with a notice.
func tsNeedBun(t *testing.T) string {
	t.Helper()
	if testing.Short() {
		t.Skip("runs bun check")
	}
	bun, err := exec.LookPath("bun")
	if err != nil {
		t.Skipf("bun not found (%s or later needed): skipping the TypeScript conformance test", tsMinBun)
	}
	out, err := exec.Command(bun, "--version").Output()
	if err != nil {
		t.Skipf("can't read the version of %s: skipping the TypeScript conformance test", bun)
	}
	if v := strings.TrimPrefix(strings.TrimSpace(string(out)), "v"); tsVersionLess(v, tsMinBun) {
		t.Skipf("bun %s is older than %s: skipping the TypeScript conformance test", v, tsMinBun)
	}
	return bun
}

// tsVersionLess compares major.minor.patch versions (anything after the
// patch number ignored).
func tsVersionLess(a, b string) bool {
	parse := func(s string) [3]int {
		var v [3]int
		for i, p := range strings.SplitN(s, ".", 3) {
			end := 0
			for end < len(p) && p[end] >= '0' && p[end] <= '9' {
				end++
			}
			v[i], _ = strconv.Atoi(p[:end])
		}
		return v
	}
	va, vb := parse(a), parse(b)
	return slices.Compare(va[:], vb[:]) < 0
}

// TestTSConformance is the guard against drifting from encoding/json
// (PLAN.md §5c Tests): every sample of tstypes_conform_types_test.go is
// encoded with json.Marshal and assigned to a const of its mapped TS type,
// and every negative sample must fail to type-check (@ts-expect-error),
// all in one bun check.
func TestTSConformance(t *testing.T) {
	bun := tsNeedBun(t)

	fset := gotoken.NewFileSet()
	f, err := goparser.ParseFile(fset, "tstypes_conform_types_test.go", nil, goparser.SkipObjectResolution)
	if err != nil {
		t.Fatal(err)
	}
	conf := types.Config{Importer: tyStd()}
	pkg, err := conf.Check("example.com/conform", fset, []*ast.File{f}, nil)
	if err != nil {
		t.Fatal(err)
	}
	m := newTSMapper(tsTestNS)
	names := make([]string, 0, len(tcSamples))
	for name := range tcSamples {
		names = append(names, name)
	}
	slices.Sort(names)
	tsOf := map[string]string{}
	for _, name := range names {
		obj := pkg.Scope().Lookup(name)
		if obj == nil {
			t.Fatalf("tcSamples names %s, which isn't declared", name)
		}
		elem := obj.Type().(*types.Slice).Elem()
		ts, prob := m.Map(elem)
		if prob != nil {
			t.Fatalf("%s: unexpected problem %s", name, prob.path)
		}
		tsOf[name] = ts
	}
	for name := range tcNegative {
		if _, ok := tcSamples[name]; !ok {
			t.Fatalf("tcNegative names %s, which isn't in tcSamples", name)
		}
	}
	n := newTSNames(m.Decls())

	var b strings.Builder
	used := map[string]bool{}
	for _, name := range names {
		ts := n.render(tsOf[name], "", used)
		v := reflect.ValueOf(tcSamples[name])
		fmt.Fprintf(&b, "\n// %s: %s\n", name, ts)
		for i := range v.Len() {
			j, err := json.Marshal(v.Index(i).Interface())
			if err != nil {
				t.Fatalf("%s[%d]: %v", name, i, err)
			}
			fmt.Fprintf(&b, "const %s_%d: %s = %s;\n", name, i, ts, j)
		}
		for i, j := range tcNegative[name] {
			fmt.Fprintf(&b, "// @ts-expect-error\nconst %s_not%d: %s = %s;\n", name, i, ts, j)
		}
	}
	b.WriteString("\nexport {};\n")
	imports := slices.Sorted(maps.Keys(used))
	src := "import type { " + strings.Join(imports, ", ") + " } from \"orbit:types\";\n" + b.String()

	dir := t.TempDir()
	files := map[string]string{
		"conform.ts": src,
		"tsconfig.json": `{
  "compilerOptions": {
    "target": "ESNext",
    "module": "ESNext",
    "moduleResolution": "Bundler",
    "lib": ["ESNext", "DOM"],
    "strict": true,
    "noEmit": true
  },
  "include": ["*.ts"]
}
`,
	}
	for _, out := range EmitTypes([]*UnitTypes{{Path: "x.orbit", Decls: m.Decls()}}) {
		files[filepath.Base(out.Path)] = string(out.Content)
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60e9)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "check", "--no-pretty", "--no-script", "-p", ".")
	cmd.Dir = dir
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil || bytes.Contains(out.Bytes(), []byte("error TS")) {
		t.Errorf("bun check: %v\n%s\n--- conform.ts\n%s\n--- go.d.ts\n%s", err, out.Bytes(), files["conform.ts"], files["go.d.ts"])
	}
}
