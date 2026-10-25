// Package compiler turns .orbit files into Go code.
package compiler

// scanBraced finds the end of a {…} construct in a template. src[start] must
// be the opening '{'. It returns the index of the matching '}', or -1 if the
// construct is unterminated.
//
// The real implementation is Go-aware: braces inside Go strings, raw strings,
// runes and comments don't count, so {fmt.Sprintf("{%d}", n)} and
// {posts.Filter(func(p Post) bool { return p.Draft })} end at the right '}'.
//
// TEMPORARY: this naive version only counts braces. Milestone 3's scanner
// agent replaces it (keeping this exact signature and contract).
func scanBraced(src string, start int) int {
	depth := 0
	for i := start; i < len(src); i++ {
		switch src[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}
