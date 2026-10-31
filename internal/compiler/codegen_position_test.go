package compiler

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// TestLineDirectivePositions is PLAN.md §3.3's position test, through the
// real pipeline (Analyze, Generate, TypedPass with go list, pass 2): a
// project with deliberate errors in the frontmatter body, a hoisted helper,
// a continuation line of a multi-line declaration, an {expr}, a control
// header, a {switch} header and a duplicate {case} value, a wrapped
// frontmatter return (an error inside its multi-line result, and a result
// that isn't an error), a redeclared helper (a related position), a type
// error naming another component's Props, and an unknown prop with the pre-check skipped
// (an embedded Props). go build and go vet must both report each at its
// .lyr L:C, and CleanGoOutput must turn both into the same
// src/…lyr:L:C: msg lines in the user's names (testdata/positions/
// cleaned.golden).
func TestLineDirectivePositions(t *testing.T) {
	const dir = "testdata/positions"
	c := loadCase(t, dir)
	m := newGenModule(t)
	p := plPipeline(t, m, plReadCase(t, c), c.opts)
	if d := p.diagnostics(); len(d) > 0 {
		t.Fatalf("unexpected Lyr diagnostics:\n%s", strings.Join(d, "\n"))
	}
	var analyses []*Analysis
	for _, pf := range p.files {
		analyses = append(analyses, pf.a)
		if want := pf.path == "src/components/Emb.lyr"; pf.typed.Checked != want {
			t.Errorf("%s: checked = %v, want %v", pf.path, pf.typed.Checked, want)
		}
	}
	p.write(m, true)
	want := []struct {
		file, marker string // the error is at the start of marker's first occurrence
		msg          string // the cleaned message
	}{
		{"src/components/E1.lyr", `"x"`, `cannot use "x" (untyped string constant) as int value in argument to strconv.Itoa`},
		{"src/components/E2.lyr", `"n"`, `cannot use "n" (untyped string constant) as int value in argument to strconv.Itoa`},
		{"src/components/E3.lyr", "intt", "undefined: intt"},
		{"src/components/E4.lyr", "titel", "undefined: titel"},
		{"src/components/E5.lyr", "Dpth", "props.Dpth undefined (type Props has no field or method Dpth)"},
		{"src/components/E6.lyr", "dup() int { return 2", "dup redeclared in this block"},
		{"src/components/E7.lyr", `"r"`, `cannot use "r" (untyped string constant) as int value in argument to strconv.Itoa`},
		{"src/components/E8.lyr", `"oops"`, `cannot use "oops" (constant of type string) as error value in return statement: string does not implement error (missing method Error)`},
		{"src/components/E9.lyr", "stauts", "undefined: stauts"},
		{"src/components/E10.lyr", `"a"}<p>b`, `duplicate case "a" (constant of type string) in expression switch`},
		{"src/components/E12.lyr", `n}>x`, "non-boolean condition in if statement"},
		{"src/components/E13.lyr", "N>x", "cannot use N (variable of type int) as string value in argument to lyr__w.TagOpen"},
		{"src/components/E11.lyr", `n} depth`, "cannot use n (variable of type int) as bool value in class:list condition"},
		{"src/components/E14.lyr", `"x"`, `cannot use "x" (untyped string constant) as int value in optional prop value`},
		// Template literals (PLAN.md §2.10): a piece on the first and a
		// later line of a multi-line literal, in a nested literal, and a
		// literal's value.
		{"src/components/E15.lyr", "undefA", "undefined: undefA"},
		{"src/components/E17.lyr", "undefB", "undefined: undefB"},
		{"src/components/E18.lyr", "undefC", "undefined: undefC"},
		{"src/components/E19.lyr", "undefD", "undefined: undefD"},
		{"src/components/E16.lyr", "`a${n}`", "invalid operation: n == template literal (mismatched types int and string)"},
		{"src/pages/alias.lyr", "Emb.Props{}", "cannot use Emb.Props{} (value of struct type Emb.Props) as int value in variable declaration"},
		{"src/pages/index.lyr", "nmae", "unknown field Nmae in struct literal of type Emb.Props"},
	}
	build, vet := m.buildErrors()
	t.Logf("go build:\n%s\ngo vet:\n%s", build, vet)
	names := NewErrorNames(testModule, analyses)
	cleaned := map[string]string{
		"build": string(CleanGoOutput([]byte(build), names)),
		"vet":   string(CleanGoOutput([]byte(vet), names)),
	}
	for _, w := range want {
		src, err := os.ReadFile(filepath.Join(dir, w.file))
		if err != nil {
			t.Fatal(err)
		}
		off := strings.Index(string(src), w.marker)
		pos := NewLineIndex(string(src)).Position(off)
		loc := w.file + ":" + pos.String() + ": "
		for name, out := range map[string]string{"build": build, "vet": vet} {
			if findLine(out, "./"+loc) == "" {
				t.Errorf("go %s: no error at ./%s\n%s", name, loc, out)
			}
			if !hasLine(cleaned[name], loc+w.msg) {
				t.Errorf("cleaned go %s lacks the line\n%s%s\n%s", name, loc, w.msg, cleaned[name])
			}
		}
	}
	// The related position of the redeclaration (go build only: vet's
	// type check prints just the first line), cleaned.
	if !hasLine(cleaned["build"], "\tsrc/components/E6.lyr:2:6: other declaration of dup") {
		t.Errorf("cleaned go build lacks the other declaration\n%s", cleaned["build"])
	}
	// No error may point into a generated file, raw or cleaned; cleaned
	// output has no ./, [gen…] or generated package names left.
	gen := regexp.MustCompile(`(?m)^[^\s]*lyr_gen/[^\s]*\.go:\d+`)
	for name, out := range map[string]string{"build": build, "vet": vet} {
		if l := gen.FindString(out); l != "" {
			t.Errorf("go %s reports a generated position: %s\n%s", name, l, out)
		}
	}
	leftover := regexp.MustCompile(`\./src|\.go:\d|\[|emb\.|e5\.|_impl|vet: `)
	for name, out := range cleaned {
		if l := leftover.FindString(out); l != "" {
			t.Errorf("cleaned go %s still contains %q\n%s", name, l, out)
		}
	}
	golden(t, filepath.Join(dir, "cleaned.golden"), "-- go build --\n"+sortPackages(cleaned["build"])+"-- go vet --\n"+sortPackages(cleaned["vet"]))
}

// sortPackages makes go command output deterministic for goldens: the
// order of packages (and of vet's analyzer lines, which carry no "# pkg"
// header) varies with the parallel build, so it drops the headers and
// sorts the errors, each with its tab-indented continuation lines.
func sortPackages(out string) string {
	var errs []string
	for _, l := range strings.SplitAfter(out, "\n") {
		switch {
		case l == "" || strings.HasPrefix(l, "# "):
		case strings.HasPrefix(l, "\t") && len(errs) > 0:
			errs[len(errs)-1] += l
		default:
			errs = append(errs, l)
		}
	}
	slices.Sort(errs)
	return strings.Join(errs, "")
}

func findLine(out, prefix string) string {
	for _, l := range strings.Split(out, "\n") {
		if i := strings.Index(l, prefix); i >= 0 {
			return l[i:]
		}
	}
	return ""
}

func hasLine(out, line string) bool {
	return strings.Contains("\n"+out+"\n", "\n"+line+"\n")
}
