package config

import (
	"reflect"
	"testing"
)

func TestParseGoMod(t *testing.T) {
	src := `module example.com/blog // the site

go 1.27

toolchain go1.27.3

tool github.com/bertrandkhe/orbit/cmd/orbit
tool (
	golang.org/x/tools/gopls
)

require github.com/bertrandkhe/orbit v0.1.0

require (
	github.com/evanw/esbuild v0.25.0 // indirect
	"example.com/quoted" v1.0.0
)

replace github.com/bertrandkhe/orbit => ../orbit

replace (
	example.com/a v1.0.0 => example.com/b v1.1.0
	example.com/c => /abs/c
)

exclude example.com/x v1.0.0
`
	m, err := ParseGoMod([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	want := &GoMod{
		Module: "example.com/blog", Go: "1.27", Toolchain: "go1.27.3",
		Tools: []string{"github.com/bertrandkhe/orbit/cmd/orbit", "golang.org/x/tools/gopls"},
		Require: map[string]string{
			"github.com/bertrandkhe/orbit": "v0.1.0",
			"github.com/evanw/esbuild":     "v0.25.0",
			"example.com/quoted":           "v1.0.0",
		},
		Replace: []Replace{
			{Old: "github.com/bertrandkhe/orbit", New: "../orbit"},
			{Old: "example.com/a", OldVersion: "v1.0.0", New: "example.com/b", NewVersion: "v1.1.0"},
			{Old: "example.com/c", New: "/abs/c"},
		},
	}
	if !reflect.DeepEqual(m, want) {
		t.Errorf("got  %+v\nwant %+v", m, want)
	}
	if !m.HasTool("github.com/bertrandkhe/orbit/cmd/orbit") || m.HasTool("example.com/nope") {
		t.Error("HasTool")
	}
	if r := m.Replacement("github.com/bertrandkhe/orbit", "v0.1.0"); r == nil || !r.Local() {
		t.Errorf("Replacement(orbit) = %+v", r)
	}
	if r := m.Replacement("example.com/a", "v1.0.0"); r == nil || r.Local() {
		t.Errorf("Replacement(a) = %+v", r)
	}
	if r := m.Replacement("example.com/a", "v2.0.0"); r != nil {
		t.Errorf("Replacement(a v2) = %+v", r)
	}

	for _, bad := range []string{"module m\ngo\n", "module m\nrequire x\n", "module m\nreplace a b\n", "go 1.27\n"} {
		if _, err := ParseGoMod([]byte(bad)); err == nil {
			t.Errorf("ParseGoMod(%q): no error", bad)
		}
	}
}
