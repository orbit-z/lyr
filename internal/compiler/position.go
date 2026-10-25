package compiler

import (
	"fmt"
	"sort"
)

// Span is a half-open byte range [Start, End) in the original .orbit file.
// Every position in the AST and in diagnostics is a Span or an offset into
// the whole file, frontmatter included.
type Span struct {
	Start, End int
}

// Bounds returns the span itself. Every AST node embeds a Span, so Bounds is
// how code that holds a Node gets its range.
func (s Span) Bounds() Span { return s }

// Len returns the number of bytes in the span.
func (s Span) Len() int { return s.End - s.Start }

// Contains reports whether off is inside the span. The end is inclusive, so a
// cursor right after the last byte (props.|) still counts, as the LSP needs.
func (s Span) Contains(off int) bool { return s.Start <= off && off <= s.End }

// Position is a 1-based line and a 1-based byte column, as in go/token and
// line directives (a tab counts as one column).
type Position struct {
	Line, Col int
}

func (p Position) String() string { return fmt.Sprintf("%d:%d", p.Line, p.Col) }

// LineIndex maps byte offsets to line:column positions.
type LineIndex struct {
	starts []int // offset of the first byte of each line
	size   int
}

// NewLineIndex builds the line index of src.
func NewLineIndex(src string) *LineIndex {
	starts := make([]int, 1, 64)
	for i := 0; i < len(src); i++ {
		if src[i] == '\n' {
			starts = append(starts, i+1)
		}
	}
	return &LineIndex{starts: starts, size: len(src)}
}

// Position returns the line and column of offset off. Offsets outside the
// file are clamped to it.
func (x *LineIndex) Position(off int) Position {
	off = max(0, min(off, x.size))
	line := sort.Search(len(x.starts), func(i int) bool { return x.starts[i] > off }) - 1
	return Position{Line: line + 1, Col: off - x.starts[line] + 1}
}

// Offset returns the byte offset of a 1-based line and column, clamped to the
// file. It is the inverse of Position.
func (x *LineIndex) Offset(p Position) int {
	if p.Line < 1 {
		return 0
	}
	if p.Line > len(x.starts) {
		return x.size
	}
	return min(x.starts[p.Line-1]+max(p.Col, 1)-1, x.size)
}

// LineStart returns the offset of the start of the line that contains off.
func (x *LineIndex) LineStart(off int) int {
	return x.Offset(Position{Line: x.Position(off).Line, Col: 1})
}

// Severity says whether a diagnostic fails the build.
type Severity uint8

const (
	// SeverityError fails the build.
	SeverityError Severity = iota
	// SeverityWarning is printed but doesn't fail the build.
	SeverityWarning
)

func (s Severity) String() string {
	if s == SeverityWarning {
		return "warning"
	}
	return "error"
}

// Diagnostic is an error or a warning at a span of the .orbit file.
type Diagnostic struct {
	Severity Severity
	Span
	Message string
}

// Format renders the diagnostic in Orbit's CLI form, file:L:C: message, with
// a "warning: " prefix for warnings.
func (d Diagnostic) Format(file string, lines *LineIndex) string {
	p := lines.Position(d.Start)
	if d.Severity == SeverityWarning {
		return fmt.Sprintf("%s:%d:%d: warning: %s", file, p.Line, p.Col, d.Message)
	}
	return fmt.Sprintf("%s:%d:%d: %s", file, p.Line, p.Col, d.Message)
}

// sortDiagnostics orders diagnostics by position, keeping the order in which
// they were reported for equal positions.
func sortDiagnostics(ds []Diagnostic) {
	sort.SliceStable(ds, func(i, j int) bool { return ds[i].Start < ds[j].Start })
}
