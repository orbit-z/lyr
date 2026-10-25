package compiler

import (
	goparser "go/parser"
	"go/scanner"
	gotoken "go/token"
	"strings"
	"testing"
)

func TestScanBraced(t *testing.T) {
	// Each src starts with prefix (so start = len(prefix)). want is the text
	// from the closing '}' to the end of src, or "" for unterminated (-1).
	tests := []struct {
		name   string
		prefix string
		src    string
		want   string
	}{
		{"empty", "", "{}", "}"},
		{"ident", "", "{props.Title} rest", "} rest"},
		{"literal open brace", "", `{"{"}`, "}"},
		{"literal close brace", "", `{"}"}`, "}"},
		{"literal brace in text", "", `{"{"} }`, "} }"},
		{"string with braces", "", `{fmt.Sprintf("{%d}", n)}`, "}"},
		{"string escaped quote", "", `{"a\"}"}x`, "}x"},
		{"string escaped backslash", "", `{"a\\"}x`, `}x`},
		{"raw string", "", "{`}{`}", "}"},
		{"raw string multiline", "", "{`a\n}\nb`} x", "} x"},
		{"raw string backslash", "", "{`\\`}", "}"},
		{"rune open", "", "{'{'}", "}"},
		{"rune close", "", "{'}'}", "}"},
		{"rune escaped quote", "", `{'\''}`, "}"},
		{"rune escaped backslash", "", `{'\\'}`, "}"},
		{"block comment", "", "{/* } */ x}", "}"},
		{"template comment", "", "{/* note { */}<p>", "}<p>"},
		{"block comment star slash", "", "{/*/ } */}", "}"},
		{"line comment hides brace", "", "{x // }\n}", "}"},
		{"line comment hides string", "", "{x // \"\n}", "}"},
		{"division", "", "{a / b}", "}"},
		{"division at end", "", "{a/}", "}"},
		{"composite literal", "", "{Point{X: 1, Y: 2}}", "}"},
		{"nested composite", "", "{[]Point{{1, 2}, {3, 4}}[0]} t", "} t"},
		{"map literal", "", `{map[string]int{"}": 1}["}"]}`, "}"},
		{"func literal", "", "{posts.Filter(func(p Post) bool { return p.Draft })}", "}"},
		{"func literal multiline", "", "{f(func() {\n\treturn\n})}", "}"},
		{"if block", "", "{if a > b}x{/if}", "}x{/if}"},
		{"for clause", "", "{for _, p := range posts}", "}"},
		{"spread", "", "{...attrs} />", "} />"},
		{"utf8", "", "{\"héllo 世界 }\" + nom}é", "}é"},
		{"utf8 ident", "", "{café}", "}"},
		{"mid string", "<p>", "{x}</p>", "}</p>"},
		{"mid string after brace", "{a} ", "{b}", "}"},
		{"unmatched paren", "", "{f(x} y)}", "} y)}"},

		{"unterminated empty", "", "{", ""},
		{"unterminated", "", "{x", ""},
		{"unterminated nested", "", "{a{b}", ""},
		{"unterminated string", "", `{"abc}`, ""},
		{"string newline", "", "{\"abc\n\"}", ""},
		{"string escaped newline", "", "{\"abc\\\n\"}", ""},
		{"string trailing backslash", "", `{"\`, ""},
		{"unterminated rune", "", "{'}", ""},
		{"rune newline", "", "{'\n'}", ""},
		{"unterminated raw", "", "{`}", ""},
		{"unterminated block comment", "", "{/* }", ""},
		{"unterminated line comment", "", "{x // }", ""},
		{"trailing slash", "", "{x /", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := tt.prefix + tt.src
			want := -1
			if tt.want != "" {
				want = len(src) - len(tt.want)
			}
			if got := scanBraced(src, len(tt.prefix)); got != want {
				t.Errorf("scanBraced(%q, %d) = %d, want %d", src, len(tt.prefix), got, want)
			}
		})
	}
}

func TestScanBracedBadStart(t *testing.T) {
	// Out of range, or src[start] isn't '{': -1, not a panic.
	for _, start := range []int{-1, 0, 2, 3, 100} {
		if got := scanBraced("x{}", start); got != -1 {
			t.Errorf("scanBraced(%q, %d) = %d, want -1", "x{}", start, got)
		}
	}
}

func TestScanBracedTolerant(t *testing.T) {
	tests := []struct {
		src   string
		start int
		want  int
	}{
		{"{x}", 0, 2},
		{"{x\n}", 0, 3},            // terminated on a later line
		{"{\"x\n}", 0, 3},          // unterminated string: end of line
		{"<p>{\"x}</p>\nb", 3, 11}, // the '\n'
		{"{`x\n", 0, 3},
		{"{x", 0, 2}, // last line: len(src)
		{"a{x", 1, 3},
		{"x", 0, -1},
	}
	for _, tt := range tests {
		if got := scanBracedTolerant(tt.src, tt.start); got != tt.want {
			t.Errorf("scanBracedTolerant(%q, %d) = %d, want %d", tt.src, tt.start, got, tt.want)
		}
	}
}

// goScannerEnd finds the end of the construct at src[start] with go/scanner:
// the offset of the RBRACE token that closes the first LBRACE. ok is false
// if go/scanner reports an error before that point, where the two lexers may
// legitimately disagree (scanBraced doesn't validate tokens).
func goScannerEnd(src string, start int) (end int, ok bool) {
	body := src[start:]
	fset := gotoken.NewFileSet()
	file := fset.AddFile("", -1, len(body))
	var s scanner.Scanner
	errs := 0
	s.Init(file, []byte(body), func(gotoken.Position, string) { errs++ }, scanner.ScanComments)
	depth := 0
	for {
		pos, tok, _ := s.Scan()
		if errs > 0 {
			return 0, false
		}
		switch tok {
		case gotoken.EOF:
			return -1, true
		case gotoken.LBRACE:
			depth++
		case gotoken.RBRACE:
			depth--
			if depth == 0 {
				return start + file.Offset(pos), true
			}
		}
	}
}

func FuzzScanBraced(f *testing.F) {
	seeds := []string{
		"", "x", `"{"`, `"}"`, "`}`", "'}'", `'\''`, "/* } */", "x // }\n",
		"Point{1, 2}", "func() { return }", `map[string]int{"}": 1}`,
		"posts.Filter(func(p Post) bool { return p.Draft })", "\"é}\"", "{",
	}
	for _, s := range seeds {
		f.Add("", s)
		f.Add("<p>", s+"}")
	}
	f.Fuzz(func(t *testing.T, prefix, expr string) {
		src := prefix + "{" + expr
		start := len(prefix)

		// Invariant: -1, or the index of a '}' after start.
		got := scanBraced(src, start)
		if got != -1 && (got <= start || got >= len(src) || src[got] != '}') {
			t.Fatalf("scanBraced(%q, %d) = %d: not -1 or a '}' after start", src, start, got)
		}
		tol := scanBracedTolerant(src, start)
		if got >= 0 && tol != got || got < 0 && (tol <= start || tol > len(src) || tol < len(src) && src[tol] != '\n') {
			t.Fatalf("scanBracedTolerant(%q, %d) = %d (scanBraced %d)", src, start, tol, got)
		}

		// Where go/scanner lexes the construct without errors, both agree.
		if want, ok := goScannerEnd(src, start); ok && got != want {
			t.Fatalf("scanBraced(%q, %d) = %d, go/scanner says %d", src, start, got, want)
		}

		// A valid Go expression, wrapped as {expr\n}, ends at the final '}'
		// (the newline ends a trailing line comment).
		if _, err := goparser.ParseExpr(expr); err == nil {
			wrapped := prefix + "{" + expr + "\n}"
			if got := scanBraced(wrapped, start); got != len(wrapped)-1 {
				t.Fatalf("valid expression %q: scanBraced = %d, want %d", expr, got, len(wrapped)-1)
			}
		}
	})
}

var benchExprs = []string{
	"{props.Title}",
	`{fmt.Sprintf("{%d} items", len(posts))}`,
	"{posts.Filter(func(p Post) bool { return !p.Draft /* } */ })}",
	"{for _, p := range posts}",
	"{[]Point{{1, 2}, {3, 4}}[0]}",
}

func BenchmarkScanBraced(b *testing.B) {
	src := "<p>" + strings.Join(benchExprs, "</p><p>") + "</p>"
	var starts []int
	for i := 0; i < len(src); i++ {
		if src[i] == '{' {
			if end := scanBraced(src, i); end > 0 {
				starts = append(starts, i)
				i = end
			}
		}
	}
	b.ReportAllocs()
	for b.Loop() {
		for _, s := range starts {
			if scanBraced(src, s) < 0 {
				b.Fatal("unterminated")
			}
		}
	}
}

// BenchmarkGoScanner measures go/scanner on the same input, for comparison.
func BenchmarkGoScanner(b *testing.B) {
	src := "<p>" + strings.Join(benchExprs, "</p><p>") + "</p>"
	var starts []int
	for i := 0; i < len(src); i++ {
		if src[i] == '{' {
			if end := scanBraced(src, i); end > 0 {
				starts = append(starts, i)
				i = end
			}
		}
	}
	b.ReportAllocs()
	for b.Loop() {
		for _, s := range starts {
			if end, _ := goScannerEnd(src, s); end < 0 {
				b.Fatal("unterminated")
			}
		}
	}
}
