package format

import (
	"fmt"
	"strings"
)

// Diff returns a unified diff of old and new in gofmt -d's format, with
// "--- PATH.orig" and "+++ PATH" headers and 3 lines of context, or "" if
// they're equal.
func Diff(path, old, new string) string {
	if old == new {
		return ""
	}
	a, b := splitLines(old), splitLines(new)
	ops := diffLines(a, b)
	var out strings.Builder
	fmt.Fprintf(&out, "diff %s.orig %s\n--- %s.orig\n+++ %s\n", path, path, path, path)
	const ctx = 3
	// Group the ops into hunks.
	for i := 0; i < len(ops); {
		if ops[i].kind == opEqual {
			i++
			continue
		}
		start := max(0, i-ctx)
		end := i
		for end < len(ops) {
			if ops[end].kind != opEqual {
				end++
				continue
			}
			run := end
			for run < len(ops) && ops[run].kind == opEqual {
				run++
			}
			if run == len(ops) || run-end > 2*ctx {
				end = min(len(ops), end+ctx)
				break
			}
			end = run
		}
		h := ops[start:end]
		as, bs := ops[start].a, ops[start].b
		var al, bl int
		for _, o := range h {
			if o.kind != opInsert {
				al++
			}
			if o.kind != opDelete {
				bl++
			}
		}
		fmt.Fprintf(&out, "@@ -%s +%s @@\n", hunkRange(as, al), hunkRange(bs, bl))
		for _, o := range h {
			var line string
			switch o.kind {
			case opEqual:
				line = " " + a[o.a]
			case opDelete:
				line = "-" + a[o.a]
			case opInsert:
				line = "+" + b[o.b]
			}
			out.WriteString(line)
			if !strings.HasSuffix(line, "\n") {
				out.WriteString("\n\\ No newline at end of file\n")
			}
		}
		i = end
	}
	return out.String()
}

func hunkRange(start, n int) string {
	switch n {
	case 0:
		return fmt.Sprintf("%d,0", start)
	case 1:
		return fmt.Sprintf("%d", start+1)
	}
	return fmt.Sprintf("%d,%d", start+1, n)
}

// splitLines splits s into lines, each with its line break.
func splitLines(s string) []string {
	var out []string
	for s != "" {
		i := strings.IndexByte(s, '\n')
		if i < 0 {
			out = append(out, s)
			break
		}
		out = append(out, s[:i+1])
		s = s[i+1:]
	}
	return out
}

type opKind uint8

const (
	opEqual opKind = iota
	opDelete
	opInsert
)

// diffOp is one line of an edit script: a and b are the line indexes in
// the old and new text (for an insert, a is where it goes, and vice versa).
type diffOp struct {
	kind opKind
	a, b int
}

// diffLines returns a line edit script from a to b: the common prefix and
// suffix, and a longest common subsequence of the middle (a quadratic table,
// fine for formatter changes; above 16M cells the middle is replaced whole).
func diffLines(a, b []string) []diffOp {
	var ops []diffOp
	p := 0
	for p < len(a) && p < len(b) && a[p] == b[p] {
		ops = append(ops, diffOp{opEqual, p, p})
		p++
	}
	s := 0
	for s < len(a)-p && s < len(b)-p && a[len(a)-1-s] == b[len(b)-1-s] {
		s++
	}
	ma, mb := a[p:len(a)-s], b[p:len(b)-s]
	n, m := len(ma), len(mb)
	if n*m > 16<<20 {
		for i := range ma {
			ops = append(ops, diffOp{opDelete, p + i, p})
		}
		for j := range mb {
			ops = append(ops, diffOp{opInsert, p + n, p + j})
		}
	} else {
		// lcs[i][j] = LCS length of ma[i:] and mb[j:].
		lcs := make([]int32, (n+1)*(m+1))
		at := func(i, j int) *int32 { return &lcs[i*(m+1)+j] }
		for i := n - 1; i >= 0; i-- {
			for j := m - 1; j >= 0; j-- {
				if ma[i] == mb[j] {
					*at(i, j) = *at(i+1, j+1) + 1
				} else {
					*at(i, j) = max(*at(i+1, j), *at(i, j+1))
				}
			}
		}
		i, j := 0, 0
		for i < n || j < m {
			switch {
			case i < n && j < m && ma[i] == mb[j]:
				ops = append(ops, diffOp{opEqual, p + i, p + j})
				i++
				j++
			case i < n && (j == m || *at(i+1, j) >= *at(i, j+1)):
				ops = append(ops, diffOp{opDelete, p + i, p + j})
				i++
			default:
				ops = append(ops, diffOp{opInsert, p + i, p + j})
				j++
			}
		}
	}
	for k := 0; k < s; k++ {
		ops = append(ops, diffOp{opEqual, len(a) - s + k, len(b) - s + k})
	}
	return ops
}
