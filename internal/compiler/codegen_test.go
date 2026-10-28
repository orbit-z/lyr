package compiler

import (
	"bytes"
	"fmt"
	goscanner "go/scanner"
	gotoken "go/token"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// A codegen case is a directory testdata/codegen/<case>/ holding a project
// tree src/…/*.orbit, an optional "options" file (lines "compress=jsx",
// "compress=false" (the cases default to "html"), "mode=tolerant", "locals=<path>"), and optionally a
// main.go.txt with the HTML it must print, want.html (a golden compile
// test). Generated files are checked against <pkg>.decl.go.golden,
// <pkg>.render.go.golden and <pkg>.map.golden (pkg = Rel with "/" → "_"),
// and diagnostics against diags.golden.

type cgCase struct {
	dir   string
	files []string // project-relative .orbit paths, sorted
	opts  Options
}

func loadCase(t *testing.T, dir string) *cgCase {
	t.Helper()
	c := &cgCase{dir: dir, opts: Options{Module: testModule, CompressHTML: CompressHTML}}
	err := filepath.WalkDir(filepath.Join(dir, "src"), func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(p, ".orbit") {
			rel, _ := filepath.Rel(dir, p)
			c.files = append(c.files, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(c.files)
	if data, err := os.ReadFile(filepath.Join(dir, "options")); err == nil {
		for _, l := range strings.Split(string(data), "\n") {
			k, v, _ := strings.Cut(strings.TrimSpace(l), "=")
			switch k {
			case "compress":
				switch v {
				case "jsx":
					c.opts.CompressHTML = CompressJSX
				case "false":
					c.opts.CompressHTML = CompressNone
				}
			case "mode":
				if v == "tolerant" {
					c.opts.Mode = Tolerant
				}
			case "locals":
				c.opts.Locals = v
			}
		}
	}
	return c
}

type cgResult struct {
	path string
	f    *File
	a    *Analysis
	in   *Input
	out  *Output
}

// generateCase analyzes and generates every file of a case.
func generateCase(t *testing.T, c *cgCase) []*cgResult {
	t.Helper()
	var res []*cgResult
	index := ComponentIndex{}
	for _, p := range c.files {
		src, err := os.ReadFile(filepath.Join(c.dir, filepath.FromSlash(p)))
		if err != nil {
			t.Fatal(err)
		}
		f, a := cgAnalyze(t, p, string(src), c.opts.Mode)
		index[p] = NewComponentInfo(f, a)
		res = append(res, &cgResult{path: p, f: f, a: a})
	}
	for _, r := range res {
		r.in = &Input{File: r.f, Analysis: r.a, Index: index, Options: c.opts}
		r.out = Generate(r.in)
		checkOutput(t, r)
	}
	return res
}

func TestCodegenGolden(t *testing.T) {
	dirs, err := filepath.Glob("testdata/codegen/*")
	if err != nil {
		t.Fatal(err)
	}
	for _, dir := range dirs {
		t.Run(filepath.Base(dir), func(t *testing.T) {
			c := loadCase(t, dir)
			res := generateCase(t, c)
			var diags strings.Builder
			for _, r := range res {
				for _, d := range r.f.Diagnostics {
					fmt.Fprintln(&diags, "parse:", d.Format(r.path, r.f.Lines))
				}
				for _, d := range r.a.Diagnostics {
					fmt.Fprintln(&diags, "analyze:", d.Format(r.path, r.f.Lines))
				}
				for _, d := range r.out.Diagnostics {
					fmt.Fprintln(&diags, d.Format(r.path, r.f.Lines))
				}
				pkg, _ := PackageFor(r.path)
				base := filepath.Join(dir, strings.ReplaceAll(pkg.Rel, "/", "_"))
				golden(t, base+".decl.go.golden", string(r.out.Decl.Content))
				golden(t, base+".render.go.golden", string(r.out.Impl.Content))
				golden(t, base+".map.golden", dumpMap(r))
			}
			golden(t, filepath.Join(dir, "diags.golden"), diags.String())
			mainSrc, err := os.ReadFile(filepath.Join(dir, "main.go.txt"))
			if err != nil {
				return
			}
			want, err := os.ReadFile(filepath.Join(dir, "want.html"))
			if err != nil && !*update {
				t.Fatal(err)
			}
			t.Parallel()
			m := newGenModule(t)
			for _, r := range res {
				m.writeOutput(r.out)
			}
			m.buildAndVet()
			got := m.run(string(mainSrc))
			if *update {
				if err := os.WriteFile(filepath.Join(dir, "want.html"), []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			if got != string(want) {
				t.Errorf("rendered HTML differs:\n--- got\n%s\n--- want\n%s", got, want)
			}
		})
	}
}

var segKindNames = [...]string{"exact", "span", "echo", "spread"}

// dumpMap prints both files' segments and call sites.
func dumpMap(r *cgResult) string {
	var b strings.Builder
	src := r.f.Src
	for _, gf := range []*GenFile{r.out.Decl, r.out.Impl} {
		fmt.Fprintf(&b, "%s (%s)\n", gf.Path, gf.Map.Orbit)
		for _, s := range gf.Map.Segments {
			orbit := "-"
			if s.Kind != SegEcho {
				orbit = fmt.Sprintf("%s %q", r.f.Lines.Position(s.OrbitStart), src[s.OrbitStart:s.OrbitEnd])
			}
			fmt.Fprintf(&b, "  %-5s %s -> %d-%d %q\n", segKindNames[s.Kind], orbit, s.GoStart, s.GoEnd, gf.Content[s.GoStart:s.GoEnd])
		}
	}
	b.WriteString("call sites\n")
	for _, cs := range r.out.CallSites {
		kind := "content"
		switch cs.Kind {
		case SiteClass:
			kind = "class"
		case SiteClassProp:
			kind = "class prop"
		case SiteSpread:
			kind = "spread"
		case SiteHTML:
			kind = "set:html"
		}
		if cs.Kind == SiteAttr {
			kind = "attr " + cs.AttrName
			if cs.URLAttr {
				kind += " (url)"
			}
		}
		fmt.Fprintf(&b, "  %s key=%d %q call=%q\n", kind, cs.Key, cs.Code.Text, r.out.Impl.Content[cs.Call.Start:cs.Call.End])
	}
	return b.String()
}

// checkOutput checks the contract invariants of a generated output: exact
// segments copy their bytes, segments are sorted and don't overlap, every
// non-echo segment's start, read back through go/scanner with directives
// honored, lands on its .orbit position, call sites hold their code, and
// generating again gives the same bytes.
func checkOutput(t *testing.T, r *cgResult) {
	t.Helper()
	src := r.f.Src
	for _, gf := range []*GenFile{r.out.Decl, r.out.Impl} {
		if !bytes.HasPrefix(gf.Content, []byte(GenHeader+"\n")) {
			t.Errorf("%s: missing header", gf.Path)
		}
		segs := gf.Map.Segments
		for i, s := range segs {
			if i > 0 && s.GoStart < segs[i-1].GoEnd {
				t.Errorf("%s: segment %d overlaps or is out of order: %+v after %+v", gf.Path, i, s, segs[i-1])
			}
			if s.Kind == SegExact && src[s.OrbitStart:s.OrbitEnd] != string(gf.Content[s.GoStart:s.GoEnd]) {
				t.Errorf("%s: exact segment %+v: %q != %q", gf.Path, s, src[s.OrbitStart:s.OrbitEnd], gf.Content[s.GoStart:s.GoEnd])
			}
		}
		checkPositions(t, r, gf)
	}
	for _, cs := range r.out.CallSites {
		if tw, ok := r.in.Typed[cs.Key]; ok && (tw.Kind == WriteSkip || tw.Kind == WriteFold || tw.If != nil || tw.Opt) {
			continue // untyped nil: nothing emitted; a lowered orbit.If or Opt: Arg is its value
		}
		if literalSite(cs) {
			continue
		}
		if got := string(r.out.Impl.Content[cs.Arg.Start:cs.Arg.End]); got != cs.Code.Text {
			t.Errorf("call site %d: Arg %q != Code %q", cs.Key, got, cs.Code.Text)
		}
		if cs.Key != cs.Code.Start {
			t.Errorf("call site key %d != code start %d", cs.Key, cs.Code.Start)
		}
	}
	again := Generate(r.in)
	if !bytes.Equal(again.Impl.Content, r.out.Impl.Content) || !bytes.Equal(again.Decl.Content, r.out.Decl.Content) {
		t.Errorf("%s: output isn't deterministic", r.path)
	}
}

// checkPositions scans gf with go/scanner and checks the adjusted position
// of the token at each segment start.
func checkPositions(t *testing.T, r *cgResult, gf *GenFile) {
	t.Helper()
	fset := gotoken.NewFileSet()
	file := fset.AddFile(gf.Path, -1, len(gf.Content))
	var s goscanner.Scanner
	s.Init(file, gf.Content, func(pos gotoken.Position, msg string) {}, goscanner.ScanComments)
	at := map[int]gotoken.Pos{}
	for {
		pos, tok, lit := s.Scan()
		if tok == gotoken.EOF {
			break
		}
		if tok == gotoken.SEMICOLON && lit == "\n" {
			continue // an automatic semicolon belongs to the line before it
		}
		at[file.Offset(pos)] = pos
	}
	for _, seg := range gf.Map.Segments {
		if seg.Kind == SegEcho || seg.GoStart == seg.GoEnd {
			continue
		}
		pos, ok := at[seg.GoStart]
		if !ok {
			t.Errorf("%s: no token at segment start %d (%q)", gf.Path, seg.GoStart, gf.Content[seg.GoStart:seg.GoEnd])
			continue
		}
		got := fset.PositionFor(pos, true)
		want := r.f.Lines.Position(seg.OrbitStart)
		if path.Clean(filepath.ToSlash(got.Filename)) != r.path || got.Line != want.Line || got.Column != want.Col {
			t.Errorf("%s: segment %q at %s:%d:%d, want %s:%s", gf.Path, gf.Content[seg.GoStart:seg.GoEnd], got.Filename, got.Line, got.Column, r.path, want)
		}
	}
	// Every glue token after a directive-framed region must point back
	// into the generated file: check the token after each segment.
	offs := make([]int, 0, len(at))
	for off := range at {
		if !followsDirective(gf.Content, off) {
			offs = append(offs, off)
		}
	}
	sort.Ints(offs)
	for _, seg := range gf.Map.Segments {
		if seg.Kind == SegEcho {
			continue
		}
		k := sort.SearchInts(offs, seg.GoEnd)
		if k == len(offs) {
			continue
		}
		next := offs[k]
		if inSegment(gf.Map.Segments, next) {
			continue // the next token is itself mapped (an import path)
		}
		got := fset.PositionFor(at[next], true)
		line, col := lineCol(gf.Content, next)
		if path.Base(got.Filename) != path.Base(gf.Path) || got.Line != line || got.Column != col {
			t.Errorf("%s: glue at %d (%q) is %s, want %s:%d:%d", gf.Path, next, snippet(gf.Content, next), got, path.Base(gf.Path), line, col)
		}
	}
}

func inSegment(segs []Segment, off int) bool {
	for _, s := range segs {
		if s.GoStart <= off && off < s.GoEnd {
			return true
		}
	}
	return false
}

// followsDirective reports whether the token at off is a directive comment
// (its position is irrelevant).
func followsDirective(content []byte, off int) bool {
	return bytes.HasPrefix(content[off:], []byte("/*line ")) || bytes.HasPrefix(content[off:], []byte("//line "))
}

func snippet(b []byte, off int) string {
	end := min(len(b), off+20)
	return string(b[off:end])
}

func lineCol(b []byte, off int) (int, int) {
	line := bytes.Count(b[:off], []byte("\n")) + 1
	return line, off - (bytes.LastIndexByte(b[:off], '\n') + 1) + 1
}

func TestSourceMapRoundTrip(t *testing.T) {
	c := loadCase(t, "testdata/codegen/card")
	for _, r := range generateCase(t, c) {
		for _, gf := range []*GenFile{r.out.Decl, r.out.Impl} {
			m := gf.Map
			for _, s := range m.Segments {
				if s.Kind == SegEcho {
					if _, ok := m.OrbitOffset(s.GoStart); ok {
						t.Errorf("%s: echo segment maps back", gf.Path)
					}
					continue
				}
				for off := s.OrbitStart; off <= s.OrbitEnd; off++ {
					g, ok := m.GoOffset(off)
					if !ok {
						t.Fatalf("%s: GoOffset(%d) not ok", gf.Path, off)
					}
					back, ok := m.OrbitOffset(g)
					if !ok {
						t.Fatalf("%s: OrbitOffset(%d) not ok", gf.Path, g)
					}
					if s.Kind == SegExact && back != off {
						t.Errorf("%s: %d -> %d -> %d", gf.Path, off, g, back)
					}
					if back < s.OrbitStart || back > s.OrbitEnd {
						t.Errorf("%s: span %d -> %d -> %d outside %d-%d", gf.Path, off, g, back, s.OrbitStart, s.OrbitEnd)
					}
				}
			}
			// Glue (the package clause) maps nowhere.
			glue := bytes.Index(gf.Content, []byte("package "))
			if _, ok := m.OrbitOffset(glue); ok {
				t.Errorf("%s: glue maps back", gf.Path)
			}
		}
	}
}

func TestSourceMapLookup(t *testing.T) {
	m := &SourceMap{Segments: []Segment{
		{SegExact, 10, 15, 100, 105},
		{SegSpan, 15, 20, 105, 107}, // is-op → Is
		{SegEcho, -1, -1, 200, 203},
		{SegExact, 30, 30, 300, 300},
	}}
	for _, tc := range []struct {
		orbit, gooff int
		ok           bool
	}{{9, 0, false}, {10, 100, true}, {14, 104, true}, {15, 105, true}, {17, 107, true}, {20, 107, true}, {21, 0, false}, {30, 300, true}} {
		g, ok := m.GoOffset(tc.orbit)
		if ok != tc.ok || ok && g != tc.gooff {
			t.Errorf("GoOffset(%d) = %d, %v; want %d, %v", tc.orbit, g, ok, tc.gooff, tc.ok)
		}
	}
	for _, tc := range []struct {
		gooff, orbit int
		ok           bool
	}{{99, 0, false}, {100, 10, true}, {105, 15, true}, {106, 16, true}, {107, 17, true}, {150, 0, false}, {201, 0, false}, {300, 30, true}} {
		o, ok := m.OrbitOffset(tc.gooff)
		if ok != tc.ok || ok && o != tc.orbit {
			t.Errorf("OrbitOffset(%d) = %d, %v; want %d, %v", tc.gooff, o, ok, tc.orbit, tc.ok)
		}
	}
	var nilMap *SourceMap
	if _, ok := nilMap.GoOffset(0); ok {
		t.Error("nil map maps")
	}
}

func TestGenerateNeverPanics(t *testing.T) {
	out := Generate(nil)
	if out.Decl == nil || out.Impl == nil || len(out.Diagnostics) != 1 {
		t.Fatalf("Generate(nil) = %+v", out)
	}
}
