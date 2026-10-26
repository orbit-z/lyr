package format

import (
	"bufio"
	"bytes"
	"context"
	"flag"
	goast "go/ast"
	goconstant "go/constant"
	goparser "go/parser"
	gotoken "go/token"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/orbit-z/orbit/internal/check"
	"github.com/orbit-z/orbit/internal/compiler"
)

// corpusFile is one .orbit source of the corpus.
type corpusFile struct {
	name string // repo-relative, with :line for a docs block
	src  string
	// clean is true for sources that must be fmt-clean: the starter, the
	// example blog, the docs site and the docs' orbit blocks.
	clean bool
}

const repoRoot = "../.."

// orbitFiles returns the .orbit files under dir (repo-relative), skipping
// generated and dependency directories.
func orbitFiles(t testing.TB, dir string, clean bool) []corpusFile {
	t.Helper()
	var out []corpusFile
	err := filepath.WalkDir(filepath.Join(repoRoot, dir), func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == "orbit_gen" || d.Name() == "node_modules" || d.Name() == "dist" || strings.HasPrefix(d.Name(), ".") && d.Name() != ".") {
			return filepath.SkipDir
		}
		if !d.IsDir() && strings.HasSuffix(p, ".orbit") {
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(repoRoot, p)
			out = append(out, corpusFile{name: filepath.ToSlash(rel), src: string(b), clean: clean})
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// DocsBlock is a ```orbit fenced block of a docs page.
type docsBlock struct {
	file  string
	line  int
	info  string
	text  string
	clean bool
}

// docsBlocks returns the ```orbit blocks of docs/**/*.md. A block whose
// info string has "unformatted" shows code before formatting and is exempt
// from the fmt-clean rule.
func docsBlocks(t testing.TB) []docsBlock {
	t.Helper()
	var out []docsBlock
	err := filepath.WalkDir(filepath.Join(repoRoot, "docs"), func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && d.Name() == "site" {
			return filepath.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(p, ".md") {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(repoRoot, p)
		sc := bufio.NewScanner(bytes.NewReader(b))
		var cur *docsBlock
		var fence string
		n := 0
		for sc.Scan() {
			n++
			l := sc.Text()
			trimmed := strings.TrimLeft(l, " ")
			if cur == nil {
				if strings.HasPrefix(trimmed, "```orbit") {
					indent := len(l) - len(trimmed)
					info := strings.TrimSpace(strings.TrimPrefix(trimmed, "```orbit"))
					fence = l[:indent] + "```"
					cur = &docsBlock{file: filepath.ToSlash(rel), line: n, info: info, clean: !hasWord(info, "unformatted")}
				}
				continue
			}
			if strings.TrimRight(l, " ") == fence || strings.TrimSpace(l) == "```" {
				out = append(out, *cur)
				cur = nil
				continue
			}
			cur.text += strings.TrimPrefix(l, fence[:len(fence)-3]) + "\n"
		}
		return sc.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func hasWord(info, w string) bool {
	return slices.Contains(strings.Fields(info), w)
}

// corpus returns every corpus source.
func corpus(t testing.TB) []corpusFile {
	var out []corpusFile
	out = append(out, orbitFiles(t, "internal/scaffold/template", true)...)
	out = append(out, orbitFiles(t, "examples/blog", true)...)
	out = append(out, orbitFiles(t, "docs/site", true)...)
	out = append(out, orbitFiles(t, "internal/compiler/testdata", false)...)
	out = append(out, orbitFiles(t, "internal/format/testdata", false)...)
	for _, b := range docsBlocks(t) {
		out = append(out, corpusFile{name: b.file + ":" + strconv.Itoa(b.line), src: b.text, clean: b.clean})
	}
	return out
}

func corpusOptions(t testing.TB) Options {
	bun, notice := check.FindBun(context.Background(), "", "skipping <script> and <style> in the corpus")
	if bun == "" {
		t.Log(notice)
	}
	root, _ := filepath.Abs(repoRoot)
	return Options{Bun: bun, BunDir: root}
}

// TestCorpus formats every corpus file: the starter, examples/blog,
// docs/site and the docs' orbit blocks must already be formatted; for every
// file that parses, formatting is idempotent, warns nothing, and keeps the
// template tree and the Go code (as go/ast trees).
func TestCorpus(t *testing.T) {
	opts := corpusOptions(t)
	for _, c := range corpus(t) {
		f := compiler.Parse(c.src, compiler.Strict)
		if len(f.SyntaxErrors()) > 0 {
			continue // error fixtures, and fragments that don't parse alone
		}
		o := opts
		o.Path = strings.ReplaceAll(c.name, ":", ".")
		out, res := Source(c.src, o)
		if len(res.Errors) > 0 {
			// Error fixtures, and docs fragments that aren't whole files.
			if c.clean && !strings.Contains(c.name, ".md:") {
				t.Errorf("%s: %v", c.name, res.Errors)
			}
			continue
		}
		for _, w := range res.Warnings {
			t.Errorf("%s: %s", c.name, w.Format(c.name, f.Lines))
		}
		if c.clean && out != c.src {
			t.Errorf("%s isn't formatted (run orbit fmt):\n%s", c.name, Diff(c.name, c.src, out))
		}
		again, _ := Source(out, o)
		if again != out {
			t.Errorf("%s: not idempotent:\n%s", c.name, Diff(c.name, out, again))
		}
		g := compiler.Parse(out, compiler.Strict)
		if treeSig(f) != treeSig(g) {
			t.Errorf("%s: the template tree changed", c.name)
		}
		if err := sameAST(f, g); err != "" {
			t.Errorf("%s: %s", c.name, err)
		}
	}
}

// sameAST compares the Go code of two parses as go/ast trees, positions
// and comments ignored, imports sorted.
func sameAST(a, b *compiler.File) string {
	ca, cb := goRegions(a), goRegions(b)
	if len(ca) != len(cb) {
		return "the number of Go regions changed"
	}
	for i := range ca {
		if ca[i] != cb[i] {
			return "Go code changed: " + ca[i] + "\n=>\n" + cb[i]
		}
	}
	return ""
}

// goRegions dumps every Go region of f as an AST.
func goRegions(f *compiler.File) []string {
	var out []string
	if f.Frontmatter.Present {
		code := lf(f.Frontmatter.Code.Text)
		for _, c := range compiler.FrontmatterChunks(code) {
			text := code[c.Span.Start:c.Span.End]
			if c.Hoisted {
				out = append(out, dumpFile("package p\n\n"+text, true))
			} else {
				out = append(out, dumpFile("package p\n\nfunc _() {\n"+text+"\n}\n", false))
			}
		}
	}
	compiler.Walk(f.Template, func(n compiler.Node) bool {
		switch n := n.(type) {
		case *compiler.Expr:
			out = append(out, dumpExpr(n.Code.Text))
		case *compiler.Branch:
			if !n.Else {
				out = append(out, dumpFile("package p\n\nfunc _() {\nif "+lf(n.Cond.Text)+" {}\n}\n", false))
			}
		case *compiler.For:
			out = append(out, dumpFile("package p\n\nfunc _() {\nfor "+lf(n.Clause.Text)+" {}\n}\n", false))
		}
		if t := compiler.TagOf(n); t != nil {
			for _, a := range t.Attrs {
				if a.Kind == compiler.AttrExpr || a.Kind == compiler.AttrSpread || a.Kind == compiler.AttrShorthand {
					out = append(out, dumpExpr(a.Code.Text))
				}
			}
		}
		return true
	})
	return out
}

func dumpExpr(code string) string {
	if strings.TrimSpace(code) == "" {
		return ""
	}
	return dumpFile("package p\n\nvar _ = "+lf(code)+"\n", false)
}

func dumpFile(src string, sortImports bool) string {
	fset := gotoken.NewFileSet()
	f, err := goparser.ParseFile(fset, "", src, 0)
	if err != nil {
		return "error: " + err.Error()
	}
	if sortImports {
		goast.SortImports(fset, f)
		// SortImports keeps positions; compare the specs as a set.
		for _, d := range f.Decls {
			if g, ok := d.(*goast.GenDecl); ok && g.Tok == gotoken.IMPORT {
				slices.SortFunc(g.Specs, func(x, y goast.Spec) int {
					return strings.Compare(specKey(x), specKey(y))
				})
				g.Specs = slices.CompactFunc(g.Specs, func(x, y goast.Spec) bool { return specKey(x) == specKey(y) })
			}
		}
	}
	f.Imports = nil
	unparen := func(e goast.Expr) goast.Expr {
		for {
			p, ok := e.(*goast.ParenExpr)
			if !ok {
				return e
			}
			e = p.X
		}
	}
	goast.Inspect(f, func(n goast.Node) bool {
		// gofmt drops parentheses around if and for conditions.
		switch s := n.(type) {
		case *goast.IfStmt:
			s.Cond = unparen(s.Cond)
		case *goast.ForStmt:
			if s.Cond != nil {
				s.Cond = unparen(s.Cond)
			}
		}
		if l, ok := n.(*goast.BasicLit); ok && (l.Kind == gotoken.INT || l.Kind == gotoken.FLOAT || l.Kind == gotoken.IMAG) {
			l.Value = goconstant.MakeFromLiteral(l.Value, l.Kind, 0).ExactString()
		}
		return true
	})
	var b strings.Builder
	posType := reflect.TypeFor[gotoken.Pos]()
	goast.Fprint(&b, nil, f, func(name string, v reflect.Value) bool {
		if v.Type() == posType || name == "Obj" || name == "Scope" || name == "Unresolved" || name == "Comments" || name == "Doc" || name == "Comment" {
			return false
		}
		return goast.NotNilFilter(name, v)
	})
	return b.String()
}

func specKey(s goast.Spec) string {
	is := s.(*goast.ImportSpec)
	k := is.Path.Value
	if is.Name != nil {
		k = is.Name.Name + " " + k
	}
	return k
}

var rewriteDocs = flag.Bool("rewrite-docs", false, "format the docs' orbit blocks in place")

// TestRewriteDocs formats the docs' orbit blocks in place, with
// -rewrite-docs (blocks marked unformatted, and blocks that don't parse,
// are left alone).
func TestRewriteDocs(t *testing.T) {
	if !*rewriteDocs {
		t.Skip("run with -rewrite-docs")
	}
	opts := corpusOptions(t)
	byFile := map[string][]docsBlock{}
	for _, b := range docsBlocks(t) {
		byFile[b.file] = append(byFile[b.file], b)
	}
	for file, blocks := range byFile {
		p := filepath.Join(repoRoot, file)
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		lines := strings.SplitAfter(string(data), "\n")
		// Back to front, so line numbers stay valid.
		for i := len(blocks) - 1; i >= 0; i-- {
			b := blocks[i]
			if !b.clean {
				continue
			}
			o := opts
			o.Path = "x.orbit"
			out, res := Source(b.text, o)
			if len(res.Errors) > 0 || out == b.text {
				continue
			}
			n := strings.Count(b.text, "\n")
			indent := lines[b.line-1][:len(lines[b.line-1])-len(strings.TrimLeft(lines[b.line-1], " "))]
			var repl []string
			for _, l := range strings.SplitAfter(out, "\n") {
				if l == "" {
					continue
				}
				if l != "\n" {
					l = indent + l
				}
				repl = append(repl, l)
			}
			lines = slices.Replace(lines, b.line, b.line+n, repl...)
		}
		if err := os.WriteFile(p, []byte(strings.Join(lines, "")), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
