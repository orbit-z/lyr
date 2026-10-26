// Command gen copies Orbit's docs (docs/*.md and docs/reference/*.md,
// except DECISIONS.md) and the repository's CHANGELOG.md into
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
		if rel == "DECISIONS.md" {
			continue // the maintainers' log isn't on the site
		}
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
	// The repository's changelog, the /changelog page, as md/CHANGELOG.md.
	b, err := os.ReadFile(filepath.Join(src, "..", "CHANGELOG.md"))
	if err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dst, "CHANGELOG.md"), b, 0o644); err != nil {
		log.Fatal(err)
	}
	log.Printf("gen: copied %d files and CHANGELOG.md into %s", len(files), dst)
}
