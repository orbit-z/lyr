// Command gen copies Orbit's docs (docs/*.md and docs/reference/*.md) into
// internal/content/md, so the server can embed them. go generate runs it in
// internal/content, three levels below docs/.
package main

import (
	"log"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	const src, dst = "../../..", "md"
	old, _ := filepath.Glob(filepath.Join(dst, "*", "*.md"))
	top, _ := filepath.Glob(filepath.Join(dst, "*.md"))
	for _, f := range append(old, top...) {
		if err := os.Remove(f); err != nil {
			log.Fatal(err)
		}
	}
	files, _ := filepath.Glob(filepath.Join(src, "*.md"))
	more, _ := filepath.Glob(filepath.Join(src, "reference", "*.md"))
	files = append(files, more...)
	if len(files) == 0 {
		log.Fatalf("gen: no .md files in %s", src)
	}
	for _, f := range files {
		rel := strings.TrimPrefix(filepath.ToSlash(f), "../../../")
		b, err := os.ReadFile(f)
		if err != nil {
			log.Fatal(err)
		}
		out := filepath.Join(dst, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
			log.Fatal(err)
		}
		if err := os.WriteFile(out, b, 0o644); err != nil {
			log.Fatal(err)
		}
	}
	log.Printf("gen: copied %d files into %s", len(files), dst)
}
