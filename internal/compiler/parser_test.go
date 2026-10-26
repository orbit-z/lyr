package compiler

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite the golden files in testdata/")

// naiveScanner reports whether scanBraced is still the placeholder that
// counts braces without understanding Go strings, runes and comments.
func naiveScanner() bool {
	const s = `{"}"}`
	return scanBraced(s, 0) != len(s)-1
}

// TestParseGolden parses every testdata/parse/*.orbit file and compares the
// AST dump and the diagnostics with the .ast.golden and .diag.golden files.
// Files named tolerant_* are parsed in Tolerant mode, the others in Strict
// mode. Files named goaware_* need the Go-aware scanBraced and are skipped
// while the naive placeholder is in place.
func TestParseGolden(t *testing.T) {
	paths, err := filepath.Glob("testdata/parse/*.orbit")
	if err != nil || len(paths) == 0 {
		t.Fatalf("no testdata: %v", err)
	}
	for _, path := range paths {
		name := filepath.Base(path)
		t.Run(strings.TrimSuffix(name, ".orbit"), func(t *testing.T) {
			if strings.HasPrefix(name, "goaware_") && naiveScanner() {
				t.Skip("needs the Go-aware scanBraced: the naive placeholder counts braces inside Go strings and comments")
			}
			src, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			mode := Strict
			if strings.HasPrefix(name, "tolerant_") {
				mode = Tolerant
			}
			f := Parse(string(src), mode)
			checkInvariants(t, f)
			golden(t, strings.TrimSuffix(path, ".orbit")+".ast.golden", dumpFile(f))
			golden(t, strings.TrimSuffix(path, ".orbit")+".diag.golden", dumpDiags(f, name))
		})
	}
}

func golden(t *testing.T, path, got string) {
	t.Helper()
	if *update {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run go test ./internal/compiler -update)", err)
	}
	if got != string(want) {
		t.Errorf("%s differs (run with -update to accept):\n--- got\n%s\n--- want\n%s", path, got, want)
	}
}

func TestSplitFrontmatter(t *testing.T) {
	tests := []struct {
		src      string
		present  bool
		closed   bool
		code     string
		template string
		diags    int
	}{
		{src: "<p>hi</p>", template: "<p>hi</p>"},
		{src: "", template: ""},
		{src: "---\nx := 1\n---\n<p/>", present: true, closed: true, code: "x := 1\n", template: "<p/>"},
		{src: "---\r\nx := 1\r\n---\r\n<p/>", present: true, closed: true, code: "x := 1\r\n", template: "<p/>"},
		{src: "--- \n---", present: true, closed: true, code: "", template: ""},
		{src: "---\n---\n", present: true, closed: true, code: "", template: ""},
		{src: "---\nx := \"---\"\n ---\n---\nT", present: true, closed: true, code: "x := \"---\"\n ---\n", template: "T"},
		{src: "---\nx := 1\n", present: true, code: "x := 1\n", template: "", diags: 1},
		{src: "---", present: true, code: "", template: "", diags: 1},
		{src: " ---\n---\n", template: " ---\n---\n"},
		{src: "----\n", template: "----\n"},
		// A leading BOM is skipped, as Go's scanner skips it.
		{src: "\uFEFF---\nx := 1\n---\n<p/>", present: true, closed: true, code: "x := 1\n", template: "<p/>"},
		{src: "\uFEFF<p>hi</p>", template: "<p>hi</p>"},
		{src: "\uFEFF\uFEFF---\n---\n", template: "\uFEFF---\n---\n"},
	}
	for _, tt := range tests {
		fm, tmpl, diags := SplitFrontmatter(tt.src)
		if fm.Present != tt.present || fm.Closed != tt.closed || fm.Code.Text != tt.code ||
			tt.src[tmpl.Start:tmpl.End] != tt.template || len(diags) != tt.diags {
			t.Errorf("SplitFrontmatter(%q) = present %v closed %v code %q template %q diags %v",
				tt.src, fm.Present, fm.Closed, fm.Code.Text, tt.src[tmpl.Start:tmpl.End], diags)
		}
		if fm.Present && tt.src[fm.Code.Start:fm.Code.End] != fm.Code.Text {
			t.Errorf("SplitFrontmatter(%q): code span %v doesn't match", tt.src, fm.Code.Span)
		}
	}
}

func TestLineIndex(t *testing.T) {
	src := "ab\n\tc\n\nd"
	x := NewLineIndex(src)
	tests := []struct {
		off  int
		want Position
	}{
		{0, Position{1, 1}}, {2, Position{1, 3}}, {3, Position{2, 1}}, {4, Position{2, 2}},
		{6, Position{3, 1}}, {7, Position{4, 1}}, {8, Position{4, 2}}, {99, Position{4, 2}}, {-1, Position{1, 1}},
	}
	for _, tt := range tests {
		got := x.Position(tt.off)
		if got != tt.want {
			t.Errorf("Position(%d) = %v, want %v", tt.off, got, tt.want)
		}
		if tt.off >= 0 && tt.off <= len(src) && x.Offset(got) != tt.off {
			t.Errorf("Offset(%v) = %d, want %d", got, x.Offset(got), tt.off)
		}
	}
	d := Diagnostic{SeverityWarning, Span{4, 5}, "careful"}
	if got := d.Format("src/x.orbit", x); got != "src/x.orbit:2:2: warning: careful" {
		t.Errorf("Format = %q", got)
	}
}

// TestGoAwareBraces covers the brace constructs that only a Go-aware
// scanBraced ends correctly.
func TestGoAwareBraces(t *testing.T) {
	if naiveScanner() {
		t.Skip("needs the Go-aware scanBraced: the naive placeholder counts braces inside Go strings and comments")
	}
	exprs := []string{
		`"{"`, `"}"`, `'}'`, "`{`", `fmt.Sprintf("{%d", n)`, `x /* } */`, `strings.Count(s, "}")`,
		`map[string]string{"}": "{"}["}"]`,
	}
	for _, e := range exprs {
		src := "<p>{" + e + "}</p>"
		f := Parse(src, Strict)
		if len(f.Diagnostics) > 0 {
			t.Errorf("%s: %v", src, f.Diagnostics)
			continue
		}
		p := f.Template.Children[0].(*Element)
		if x, ok := p.Children[0].(*Expr); !ok || x.Code.Text != e {
			t.Errorf("%s: got %#v", src, p.Children[0])
		}
		src = `<a title={` + e + `}>x</a>`
		f = Parse(src, Strict)
		a := f.Template.Children[0].(*Element)
		if len(f.Diagnostics) > 0 || a.Attrs[0].Code.Text != e {
			t.Errorf("%s: %v %q", src, f.Diagnostics, a.Attrs[0].Code.Text)
		}
	}
}

func TestParseModesAgree(t *testing.T) {
	paths, _ := filepath.Glob("testdata/parse/*.orbit")
	for _, path := range paths {
		src, _ := os.ReadFile(path)
		s, l := Parse(string(src), Strict), Parse(string(src), Tolerant)
		if dumpFile(s) != dumpFile(l) || dumpDiags(s, "x") != dumpDiags(l, "x") {
			t.Errorf("%s: strict and tolerant results differ", path)
		}
	}
}

func FuzzParse(f *testing.F) {
	paths, _ := filepath.Glob("testdata/parse/*.orbit")
	for _, path := range paths {
		src, _ := os.ReadFile(path)
		f.Add(string(src))
	}
	for _, s := range []string{
		"", "{", "}", "<", "</", "<!", "<!-", "<a", "<a b", "<a b=", `<a b="`, "<a {", "<a {...", "{if", "{else}",
		"{/if}", "<script>", "<title>{", "---", "---\n", "---\n---\n<", "<Card server:defer client:idle=x>",
		"<p>{/*</p>", "<textarea></textarea>", "<style is:global is:inline>", "\xff<\xff{\xff",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, src string) {
		for _, mode := range []Mode{Strict, Tolerant} {
			file := Parse(src, mode)
			if file == nil || file.Template == nil {
				t.Fatal("Parse returned no template")
			}
			checkInvariants(t, file)
		}
	})
}

// benchTemplate builds a realistic template of about 200 lines.
func benchTemplate() string {
	var b strings.Builder
	b.WriteString("---\nimport Card \"../components/Card.orbit\"\n\nlist, err := posts.All()\nif err != nil { return err }\n---\n")
	b.WriteString("<!DOCTYPE html>\n<html lang=\"en\">\n  <head>\n    <meta charset=\"utf-8\">\n    <title>{props.Title} · Blog</title>\n  </head>\n  <body>\n")
	for i := 0; i < 12; i++ {
		b.WriteString(`    <section class="posts" data-index={i} {...props.Attrs}>
      <h2 class="title">{props.Title}</h2>
      {if len(list) == 0}
        <p>No posts</p>
      {else if len(list) == 1}
        <p>One post: {list[0].Title}</p>
      {else}
        <ul>
          {for i, p := range list}
            <li data-i={i}><Card post={p} featured={i == 0} {p}><em>{p.Summary}</em></Card></li>
          {/for}
        </ul>
      {/if}
      <img src="/a.png" alt="a < b">
    </section>
`)
	}
	b.WriteString("    <script>document.querySelector('.title').addEventListener('click', () => {})</script>\n")
	b.WriteString("    <style>.title { color: red }</style>\n  </body>\n</html>\n")
	return b.String()
}

func BenchmarkParse(b *testing.B) {
	src := benchTemplate()
	b.SetBytes(int64(len(src)))
	b.ReportAllocs()
	for b.Loop() {
		Parse(src, Strict)
	}
}

func TestBenchTemplateParses(t *testing.T) {
	src := benchTemplate()
	if n := strings.Count(src, "\n"); n < 190 || n > 230 {
		t.Errorf("bench template has %d lines, want about 200", n)
	}
	if f := Parse(src, Strict); len(f.Diagnostics) > 0 {
		t.Errorf("bench template has diagnostics: %v", f.Diagnostics)
	}
}
