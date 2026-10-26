package project

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/orbit-z/orbit/internal/compiler"
)

func TestDiffGenerated(t *testing.T) {
	root := t.TempDir()
	marked := []byte(compiler.GenHeader + "\n\npackage x\n")
	files := []GenFile{
		{Path: "orbit_gen/a/a.go", Content: marked},
		{Path: "orbit_gen/b/b.go", Content: marked},
	}
	if _, err := WriteGenerated(root, files); err != nil {
		t.Fatal(err)
	}
	diff, err := DiffGenerated(root, files)
	if err != nil {
		t.Fatal(err)
	}
	if len(diff.Written)+len(diff.Removed) != 0 {
		t.Errorf("fresh: %+v", diff)
	}

	// A user file without the marker and orbit_gen/static are left alone.
	for rel, content := range map[string]string{"orbit_gen/user.go": "package x\n", "orbit_gen/static/client/x.js": compiler.GenHeader + "\n"} {
		name := filepath.Join(root, filepath.FromSlash(rel))
		os.MkdirAll(filepath.Dir(name), 0o755)
		if err := os.WriteFile(name, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	changed := []GenFile{
		{Path: "orbit_gen/b/b.go", Content: append(marked, "// changed\n"...)},
		{Path: "orbit_gen/c/c.go", Content: marked},
	}
	diff, err = DiffGenerated(root, changed)
	if err != nil {
		t.Fatal(err)
	}
	want := &WriteResult{Written: []string{"orbit_gen/b/b.go", "orbit_gen/c/c.go"}, Removed: []string{"orbit_gen/a/a.go"}}
	if !reflect.DeepEqual(diff, want) {
		t.Errorf("got %+v, want %+v", diff, want)
	}
	// Nothing was written.
	if _, err := os.Stat(filepath.Join(root, "orbit_gen/c/c.go")); err == nil {
		t.Error("DiffGenerated wrote a file")
	}
	// WriteGenerated does what the diff said.
	res, err := WriteGenerated(root, changed)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(res, want) {
		t.Errorf("WriteGenerated: %+v", res)
	}
}
