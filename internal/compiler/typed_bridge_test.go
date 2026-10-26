package compiler

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestBridgeTypes runs each testdata/types/<case> through the real
// pipeline and compares the emitted .d.ts files (EmitTypes over every
// unit's Types) with want/, and the typed pass's diagnostics with
// diags.golden. A case has src/**/*.orbit files and, under go/, user Go
// packages copied into the module (go/internal/posts/posts.go →
// internal/posts/posts.go). Refresh with -update.
func TestBridgeTypes(t *testing.T) {
	dirs, err := filepath.Glob("testdata/types/*")
	if err != nil {
		t.Fatal(err)
	}
	for _, dir := range dirs {
		t.Run(filepath.Base(dir), func(t *testing.T) {
			t.Parallel()
			m := newGenModule(t)
			srcs := map[string]string{}
			err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
				if err != nil || d.IsDir() {
					return err
				}
				rel, _ := filepath.Rel(dir, p)
				rel = filepath.ToSlash(rel)
				data, err := os.ReadFile(p)
				if err != nil {
					return err
				}
				switch {
				case strings.HasPrefix(rel, "src/") && strings.HasSuffix(rel, ".orbit"):
					srcs[rel] = string(data)
				case strings.HasPrefix(rel, "go/"):
					m.write(strings.TrimPrefix(rel, "go/"), string(data))
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			p := plPipeline(t, m, srcs, Options{Module: testModule})
			if d := p.diagnostics(); len(d) > 0 {
				t.Fatalf("diagnostics:\n%s", strings.Join(d, "\n"))
			}
			var units []*UnitTypes
			var diags []string
			for _, pf := range p.files {
				if pf.typed.Types != nil {
					units = append(units, pf.typed.Types)
				}
				for _, d := range pf.typed.Diagnostics {
					diags = append(diags, d.Format(pf.path, pf.f.Lines))
				}
				// Pass 2 writes every JSONValue attribute with JSONAttr.
				if pf.typed.Checked && pf.typed.Types != nil && len(pf.typed.Types.Data) > 0 &&
					!bytes.Contains(pf.typed.Output.Impl.Content, []byte(".JSONAttr(")) {
					t.Errorf("%s: no JSONAttr in pass 2", pf.path)
				}
			}
			got := map[string][]byte{}
			for _, f := range EmitTypes(units) {
				got[filepath.FromSlash(strings.TrimPrefix(f.Path, GenDir+"/types/"))] = f.Content
			}
			got["diags.golden"] = []byte(strings.Join(diags, "\n") + "\n")
			want := filepath.Join(dir, "want")
			if *update {
				os.RemoveAll(want)
			}
			for name, content := range got {
				file := filepath.Join(want, name)
				if *update {
					if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(file, content, 0o644); err != nil {
						t.Fatal(err)
					}
					continue
				}
				w, err := os.ReadFile(file)
				if err != nil {
					t.Errorf("%s: %v", name, err)
					continue
				}
				if !bytes.Equal(content, w) {
					t.Errorf("%s differs from %s:\n%s", name, file, content)
				}
			}
			if !*update {
				filepath.WalkDir(want, func(p string, d fs.DirEntry, err error) error {
					if err == nil && !d.IsDir() {
						rel, _ := filepath.Rel(want, p)
						if _, ok := got[rel]; !ok {
							t.Errorf("%s: no longer emitted", rel)
						}
					}
					return nil
				})
			}
		})
	}
}
