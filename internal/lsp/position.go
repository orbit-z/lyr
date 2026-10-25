package lsp

import (
	"net/url"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"unicode/utf8"
)

// Positions (PLAN.md §9): LSP positions are a 0-based line and a column in
// some encoding's code units, UTF-16 unless negotiated otherwise. Inside
// Orbit everything is a byte offset; the conversion happens at both edges,
// with a line index of the text the position refers to.

// encoding is a position encoding: what a "character" counts.
type encoding uint8

const (
	encUTF16 encoding = iota
	encUTF8
	encUTF32
)

// parseEncoding maps an LSP PositionEncodingKind to an encoding; unknown
// kinds are UTF-16, the protocol's default.
func parseEncoding(kind string) encoding {
	switch kind {
	case "utf-8":
		return encUTF8
	case "utf-32":
		return encUTF32
	}
	return encUTF16
}

// Position is an LSP position.
type Position struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

// Range is an LSP range.
type Range struct {
	Start Position `json:"start"`
	End   Position `json:"end"`
}

// lineIndex converts between byte offsets and LSP positions in one text.
type lineIndex struct {
	src    string
	starts []int // offset of each line's first byte
}

func newLineIndex(src string) *lineIndex {
	starts := make([]int, 1, 64)
	for i := 0; i < len(src); i++ {
		if src[i] == '\n' {
			starts = append(starts, i+1)
		}
	}
	return &lineIndex{src: src, starts: starts}
}

// lineEnd returns the offset of the end of line l's content (before "\n"
// or "\r\n").
func (x *lineIndex) lineEnd(l int) int {
	end := len(x.src)
	if l+1 < len(x.starts) {
		end = x.starts[l+1] - 1
		if end > x.starts[l] && x.src[end-1] == '\r' {
			end--
		}
	}
	return end
}

// offset returns the byte offset of p, clamped to the text: a line past the
// end gives the end of the text, a character past the end of its line the
// end of that line, and a character inside a multi-unit rune (half of a
// surrogate pair) the start of that rune.
func (x *lineIndex) offset(p Position, enc encoding) int {
	if p.Line < 0 {
		return 0
	}
	if p.Line >= len(x.starts) {
		return len(x.src)
	}
	off, end := x.starts[p.Line], x.lineEnd(p.Line)
	if enc == encUTF8 {
		return min(off+max(p.Character, 0), end)
	}
	for n := 0; off < end; {
		r, size := utf8.DecodeRuneInString(x.src[off:end])
		w := 1
		if enc == encUTF16 && r >= 0x10000 {
			w = 2
		}
		if n+w > p.Character {
			break
		}
		n += w
		off += size
	}
	return off
}

// position returns the position of byte offset off, clamped to the text.
// An offset inside a multi-byte rune counts as that rune's start.
func (x *lineIndex) position(off int, enc encoding) Position {
	off = max(0, min(off, len(x.src)))
	l := sort.Search(len(x.starts), func(i int) bool { return x.starts[i] > off }) - 1
	start := x.starts[l]
	if enc == encUTF8 {
		return Position{Line: l, Character: off - start}
	}
	n := 0
	for i := start; i < off; {
		r, size := utf8.DecodeRuneInString(x.src[i:])
		if i+size > off {
			break
		}
		if enc == encUTF16 && r >= 0x10000 {
			n += 2
		} else {
			n++
		}
		i += size
	}
	return Position{Line: l, Character: n}
}

// rangeOf converts a byte range.
func (x *lineIndex) rangeOf(start, end int, enc encoding) Range {
	return Range{Start: x.position(start, enc), End: x.position(end, enc)}
}

// pathToURI returns the file URI of an absolute path.
func pathToURI(p string) string {
	p = filepath.ToSlash(p)
	if runtime.GOOS == "windows" && !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return (&url.URL{Scheme: "file", Path: p}).String()
}

// uriToPath returns the absolute path of a file URI, or "" for another
// scheme.
func uriToPath(uri string) string {
	u, err := url.Parse(uri)
	if err != nil || u.Scheme != "file" {
		return ""
	}
	p := u.Path
	if runtime.GOOS == "windows" {
		p = strings.TrimPrefix(p, "/")
	}
	return filepath.Clean(filepath.FromSlash(p))
}
