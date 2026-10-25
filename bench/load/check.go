package main

import (
	"fmt"
	"html"
	"net/http"
	"regexp"
	"strings"
	"unicode"
)

var (
	dropBlocks = regexp.MustCompile(`(?is)<script\b.*?</script>|<style\b.*?</style>|<!--.*?-->`)
	tags       = regexp.MustCompile(`(?s)<[^>]*>`)
)

// VisibleText returns the text a reader would see on a page: scripts,
// styles, comments and tags removed, entities decoded, all whitespace
// dropped. Two sites with equivalent markup give the same string even when
// their whitespace, attribute order or entity spelling differ.
func VisibleText(body []byte) string {
	s := dropBlocks.ReplaceAllString(string(body), "")
	s = tags.ReplaceAllString(s, "")
	s = html.UnescapeString(s)
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, s)
}

// CheckPair is the outcome of comparing one page on two servers.
type CheckPair struct {
	A, B         string
	SizeA, SizeB int
	TextA, TextB int // visible text lengths
	Equal        bool
	FirstDiffAt  int
	DiffA, DiffB string
}

// Check fetches a and b, requires 200 from both, and compares their visible text.
func Check(c *http.Client, a, b string) (*CheckPair, error) {
	sa, ba, err := Get(c, a)
	if err != nil {
		return nil, err
	}
	sb, bb, err := Get(c, b)
	if err != nil {
		return nil, err
	}
	if sa != 200 {
		return nil, fmt.Errorf("GET %s: status %d", a, sa)
	}
	if sb != 200 {
		return nil, fmt.Errorf("GET %s: status %d", b, sb)
	}
	ta, tb := VisibleText(ba), VisibleText(bb)
	p := &CheckPair{A: a, B: b, SizeA: len(ba), SizeB: len(bb), TextA: len(ta), TextB: len(tb), Equal: ta == tb, FirstDiffAt: -1}
	if !p.Equal {
		i := 0
		for i < len(ta) && i < len(tb) && ta[i] == tb[i] {
			i++
		}
		p.FirstDiffAt = i
		p.DiffA = ta[i:min(len(ta), i+60)]
		p.DiffB = tb[i:min(len(tb), i+60)]
	}
	return p, nil
}
