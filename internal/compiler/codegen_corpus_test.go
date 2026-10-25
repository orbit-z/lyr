package compiler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestGenerateParseCorpus generates every parser test file in both modes
// and every compressHTML mode: codegen must never hit an internal error,
// and its maps and directives must hold.
func TestGenerateParseCorpus(t *testing.T) {
	files, err := filepath.Glob("testdata/parse/*.orbit")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range files {
		src, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		for _, mode := range []Mode{Strict, Tolerant} {
			for _, comp := range []Compression{CompressHTML, CompressJSX, CompressNone} {
				path := "src/components/" + filepath.Base(p)
				f, a := cgAnalyze(t, path, string(src), mode)
				in := &Input{File: f, Analysis: a, Index: ComponentIndex{}, Options: Options{Module: testModule, Mode: mode, CompressHTML: comp}}
				out := Generate(in)
				for _, d := range out.Diagnostics {
					if strings.Contains(d.Message, "internal error") {
						t.Errorf("%s: %s", p, d.Message)
					}
				}
				checkOutput(t, &cgResult{path: path, f: f, a: a, in: in, out: out})
			}
		}
	}
}
