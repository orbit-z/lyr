package project

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	"github.com/bertrandkhe/orbit/internal/compiler"
)

// Diagnostic is an error or warning about a project file.
type Diagnostic struct {
	// File is the project-relative path ("src/pages/index.orbit").
	File string
	// Pos is the position in File; the zero Position means the diagnostic
	// is about the whole file (route errors), printed without line:col.
	Pos      compiler.Position
	Severity compiler.Severity
	Message  string
	// off orders diagnostics within a file.
	off int
}

// String formats d as the CLI prints it: "src/x.orbit:L:C: message", or
// "src/x.orbit: message" for a file-level diagnostic, with "warning: "
// before the message of a warning.
func (d Diagnostic) String() string {
	msg := d.Message
	if d.Severity == compiler.SeverityWarning {
		msg = "warning: " + msg
	}
	if d.Pos == (compiler.Position{}) {
		return fmt.Sprintf("%s: %s", d.File, msg)
	}
	return fmt.Sprintf("%s:%d:%d: %s", d.File, d.Pos.Line, d.Pos.Col, msg)
}

// fileError is a file-level error.
func fileError(file, format string, args ...any) Diagnostic {
	return Diagnostic{File: file, Severity: compiler.SeverityError, Message: fmt.Sprintf(format, args...), off: -1}
}

// fromCompiler converts a compiler diagnostic of f. Analysis reports route
// param problems at Span{} (the file has no position for them, PLAN.md
// §2.2): they are file-level.
func fromCompiler(f *File, d compiler.Diagnostic) Diagnostic {
	out := Diagnostic{File: f.Path, Severity: d.Severity, Message: d.Message, off: d.Start}
	if d.Span == (compiler.Span{}) && f.Info.Role == compiler.RolePage && isRouteParamMessage(d.Message) {
		out.off = -1
		return out
	}
	out.Pos = f.AST.Lines.Position(d.Start)
	return out
}

func isRouteParamMessage(msg string) bool {
	return strings.HasPrefix(msg, "route param")
}

// sortDiagnostics orders diagnostics by file, then position (file-level
// first), keeping the reporting order for equal positions.
func sortDiagnostics(ds []Diagnostic) {
	slices.SortStableFunc(ds, func(a, b Diagnostic) int {
		return cmp.Or(cmp.Compare(a.File, b.File), cmp.Compare(a.off, b.off))
	})
}

// HasErrors reports whether any diagnostic is an error.
func HasErrors(ds []Diagnostic) bool {
	return slices.ContainsFunc(ds, func(d Diagnostic) bool { return d.Severity == compiler.SeverityError })
}
