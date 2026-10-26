package check

import (
	"bufio"
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/orbit-z/orbit/internal/compiler"
	"github.com/orbit-z/orbit/internal/project"
)

// Dir is where the TypeScript half writes its files, relative to the
// project root.
const Dir = ".orbit/check"

// moduleTail ends every extracted script, so each is a module, as esbuild
// bundles it: without it, two scripts declaring the same top-level const
// would clash in TypeScript's global scope.
const moduleTail = "\nexport {};\n"

// Script is one extracted inline script.
type Script struct {
	// Orbit is the .orbit file's project-relative path.
	Orbit string
	// Path is the extracted file's project-relative path:
	// ".orbit/check/src/components/Card.orbit.0.ts".
	Path string
	// Content is the script's text and its span in the .orbit file.
	Content compiler.RawText
	// Lines is the .orbit file's line index.
	Lines *compiler.LineIndex
}

// ExtractPath returns the extracted file's path for script n of orbitPath:
// the src/ tree is mirrored under Dir, so a relative import resolves as
// from the .orbit file's directory (through rootDirs).
func ExtractPath(orbitPath string, n int) string {
	return path.Join(Dir, orbitPath) + "." + strconv.Itoa(n) + ".ts"
}

// Extract lists the processed inline scripts of every parsed file of p.
// Scripts with src aren't extracted: their file is checked through the
// tsconfig's include.
func Extract(p *project.Project) []Script {
	var out []Script
	for _, f := range p.Files {
		if f.AST == nil {
			continue
		}
		for _, s := range compiler.Scripts(f.AST) {
			if s.Src != "" {
				continue
			}
			out = append(out, Script{Orbit: f.Path, Path: ExtractPath(f.Path, s.Index), Content: s.Content, Lines: f.AST.Lines})
		}
	}
	return out
}

// Text returns the extracted file's content.
func (s *Script) Text() string { return s.Content.Text + moduleTail }

// Sync writes the check directory under root: the tsconfig and every
// script, rewriting only files whose content changed (so mtimes stay put
// for unchanged scripts), and removes every other file in it. tsconfig is
// the generated tsconfig.json (Tsconfig).
func Sync(root string, scripts []Script, tsconfig string) error {
	dir := filepath.Join(root, filepath.FromSlash(Dir))
	want := map[string]string{path.Join(Dir, "tsconfig.json"): tsconfig}
	for i := range scripts {
		want[scripts[i].Path] = scripts[i].Text()
	}
	for rel, content := range want {
		if err := writeIfChanged(filepath.Join(root, filepath.FromSlash(rel)), content); err != nil {
			return err
		}
	}
	// Remove stale files, then empty directories (deepest first).
	var dirs []string
	err := filepath.WalkDir(dir, func(name string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if name != dir {
				dirs = append(dirs, name)
			}
			return nil
		}
		rel, err := filepath.Rel(root, name)
		if err != nil {
			return err
		}
		if _, ok := want[filepath.ToSlash(rel)]; !ok {
			return os.Remove(name)
		}
		return nil
	})
	if err != nil {
		return err
	}
	for i := len(dirs) - 1; i >= 0; i-- {
		os.Remove(dirs[i]) // fails, harmlessly, when not empty
	}
	return nil
}

func writeIfChanged(name, content string) error {
	if old, err := os.ReadFile(name); err == nil && string(old) == content {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		return err
	}
	return os.WriteFile(name, []byte(content), 0o644)
}

// tscLine is one tsc --pretty false diagnostic: file(line,col): error TS…: …
var tscLine = regexp.MustCompile(`^(.+)\((\d+),(\d+)\): (error|warning|message) (TS\d+: .*)$`)

// summary lines bun check prints after the diagnostics.
var (
	summaryLine = regexp.MustCompile(`^Found \d+ errors?\b`)
	tableLine   = regexp.MustCompile(`^\s+\d+\s+\S+:\d+$`)
)

// Output is bun check's output mapped to project positions.
type Output struct {
	// Text is the output to print: one "file:L:C: error TSxxxx: …" line per
	// diagnostic (continuation lines kept), without bun's summary.
	Text []byte
	// Errors counts the error diagnostics.
	Errors int
}

// MapOutput rewrites bun check --no-pretty output run from root: positions
// in extracted scripts become their .orbit file's line:col, positions in
// the user's .ts files keep their relative path, and both are printed as
// file:L:C with a byte column (tsc's columns count UTF-16 units). bun's
// summary ("Found 3 errors…", the per-file table, "✓ No type errors") is
// dropped; every other line is passed through.
func MapOutput(root string, out []byte, scripts []Script) Output {
	byPath := make(map[string]*Script, len(scripts))
	for i := range scripts {
		byPath[scripts[i].Path] = &scripts[i]
	}
	files := map[string][]string{} // user file lines, read on demand
	var res Output
	var b bytes.Buffer
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(nil, 1<<20)
	inSummary := false
	for sc.Scan() {
		line := sc.Text()
		switch {
		case summaryLine.MatchString(line):
			inSummary = true
			continue
		case strings.HasPrefix(line, "✓ "):
			continue
		case inSummary && (line == "" || tableLine.MatchString(line)):
			continue
		}
		inSummary = false
		m := tscLine.FindStringSubmatch(line)
		if m == nil {
			b.WriteString(line)
			b.WriteByte('\n')
			continue
		}
		if m[4] == "error" {
			res.Errors++
		}
		file := filepath.ToSlash(m[1])
		if filepath.IsAbs(m[1]) {
			if rel, err := filepath.Rel(root, m[1]); err == nil && !strings.HasPrefix(rel, "..") {
				file = filepath.ToSlash(rel)
			}
		}
		ln, _ := strconv.Atoi(m[2])
		col, _ := strconv.Atoi(m[3])
		var pos compiler.Position
		if s, ok := byPath[file]; ok {
			file, pos = s.Orbit, s.Position(ln, col)
		} else {
			lines, ok := files[file]
			if !ok {
				lines = readLines(root, file)
				files[file] = lines
			}
			pos = compiler.Position{Line: ln, Col: col}
			if ln >= 1 && ln <= len(lines) {
				pos.Col = byteCol(lines[ln-1], col)
			}
		}
		fmt.Fprintf(&b, "%s:%d:%d: %s %s\n", file, pos.Line, pos.Col, m[4], m[5])
	}
	res.Text = b.Bytes()
	return res
}

// Position maps a 1-based line and UTF-16 column in the extracted file to
// the .orbit file's line and byte column. Positions past the script's
// content (in moduleTail) are clamped to its end.
func (s *Script) Position(line, col16 int) compiler.Position {
	text := s.Content.Text
	start := 0 // offset of the line in text
	for l := 1; l < line; l++ {
		i := strings.IndexByte(text[start:], '\n')
		if i < 0 {
			return s.Lines.Position(s.Content.Start + len(text))
		}
		start += i + 1
	}
	rest := text[start:]
	if i := strings.IndexByte(rest, '\n'); i >= 0 {
		rest = rest[:i]
	}
	return s.Lines.Position(s.Content.Start + start + byteCol(rest, col16) - 1)
}

// byteCol converts a 1-based UTF-16 column on line to a 1-based byte
// column, clamped to the end of the line.
func byteCol(line string, col16 int) int {
	units := 0
	for i, r := range line {
		if units >= col16-1 {
			return i + 1
		}
		if r >= 0x10000 && r != utf8.RuneError {
			units += 2
		} else {
			units++
		}
	}
	return len(line) + 1
}

func readLines(root, rel string) []string {
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		return nil
	}
	return strings.Split(string(data), "\n")
}
