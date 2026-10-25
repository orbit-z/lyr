package compiler

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func tyGoListLoader(m *genModule, flags ...string) *GoListLoader {
	return &GoListLoader{Dir: m.Dir, BuildFlags: flags, Env: []string{"GOFLAGS=-mod=mod", "GOWORK=off"}}
}

// TestGoListLoader checks the multi-package fixture against real export
// data, then builds and vets the re-emitted typed files.
func TestGoListLoader(t *testing.T) {
	m := newGenModule(t)
	m.write("internal/posts/posts.go", tyPostsSrc)
	m.write("internal/posts/extra.go", "//go:build orbitextra\n\npackage posts\n\nconst Extra = 2.5\n")
	p := newTyProject()
	units := p.units(t)
	c := NewChecker(tyGoListLoader(m))
	c.generate = newTyGen(units...).generate
	res := tyCheck(t, c, units)
	if len(res.Warnings) > 0 || tyChecked(res) != "true true true true " {
		t.Fatalf("checked = %s, warnings = %v", tyChecked(res), res.Warnings)
	}
	if got := tyGot(units[0], res.Units[0]); !reflect.DeepEqual(got, []string{"String", "AttrURL/string", "Int/int64"}) {
		t.Errorf("card writes %v", got)
	}
	for _, u := range res.Units {
		m.writeOutput(u.Output)
	}
	m.buildAndVet()

	// The build's flags reach go list: posts.Extra exists only with the tag.
	p.other.declImports = "\t\"" + tyPostsPath + "\"\n"
	p.other.declBody = "\ntype Props struct{ P posts.Post }\n"
	p.other.implImports = "\t\"" + tyPostsPath + "\"\n"
	p.other.body = "\torbit__w.Expr(\nposts.Extra,\n\t)\n"
	other := []Unit{tyFixture(t, p.other)}
	for _, tc := range []struct {
		flags   []string
		checked bool
	}{{nil, false}, {[]string{"-tags=orbitextra"}, true}} {
		c := NewChecker(tyGoListLoader(m, tc.flags...))
		c.generate = newTyGen(other...).generate
		res := tyCheck(t, c, other)
		if res.Units[0].Checked != tc.checked {
			t.Errorf("flags %v: checked = %v, want %v", tc.flags, res.Units[0].Checked, tc.checked)
		}
		if tc.checked && fmt.Sprint(tyGot(other[0], res.Units[0])) != "[Float]" {
			t.Errorf("flags %v: writes %v", tc.flags, tyGot(other[0], res.Units[0]))
		}
	}
}

// TestGoListLoaderErrors: a user package that doesn't compile has no export
// data (a warning, its importers fall back); a failing go command is a Load
// error.
func TestGoListLoaderErrors(t *testing.T) {
	m := newGenModule(t)
	m.write("internal/posts/posts.go", tyPostsSrc+"\nvar _ int = \"broken\"\n")
	units := newTyProject().units(t)
	c := NewChecker(tyGoListLoader(m))
	c.generate = newTyGen(units...).generate
	res := tyCheck(t, c, units)
	if got := tyChecked(res); got != "false false false true " {
		t.Errorf("checked = %s", got)
	}
	if len(res.Warnings) != 1 || !strings.HasPrefix(res.Warnings[0], "orbit: can't read export data for "+tyPostsPath+": ") ||
		!strings.HasSuffix(res.Warnings[0], "; using Expr fallbacks") || strings.Contains(res.Warnings[0], "\n") {
		t.Errorf("warnings = %q", res.Warnings)
	}
	t.Logf("warning: %s", res.Warnings[0])

	l := &GoListLoader{Dir: m.Dir, BuildFlags: []string{"-badflag"}}
	if _, err := l.Load(context.Background(), nil, []string{"fmt"}); err == nil || strings.Contains(err.Error(), "\n") {
		t.Errorf("Load with a bad flag: err = %v", err)
	}
	// No paths: no go command at all.
	if imp, err := (&GoListLoader{Dir: "/nonexistent"}).Load(context.Background(), nil, nil); err != nil || imp == nil {
		t.Errorf("Load of nothing: %v, %v", imp, err)
	}
}
