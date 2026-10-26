package content

import (
	"cmp"
	"slices"
	"strings"
	"unicode/utf8"
)

// Result is one search hit: a section of a page.
type Result struct {
	Page    *Page
	Heading string // "" for the text before the page's first heading
	URL     string // the page URL plus the section's anchor
	Snippet Snippet
	score   int
}

// Snippet is a short excerpt around the first match, split so a template can
// wrap Match in <mark> while escaping every part.
type Snippet struct {
	Before, Match, After string
}

// MaxResults caps the number of results Search returns.
const MaxResults = 50

// Search finds the sections that contain every word of q, ignoring case.
// Matches in page titles rank first, then matches in headings, then the
// number of occurrences.
func (d *Docs) Search(q string) []Result {
	terms := strings.Fields(strings.ToLower(q))
	if len(terms) == 0 {
		return nil
	}
	var out []Result
	for _, p := range d.Pages {
		title := strings.ToLower(p.Title)
		for _, s := range p.Sections {
			score := 0
			ok := true
			for _, t := range terms {
				n := strings.Count(s.lower, t)
				if n == 0 {
					ok = false
					break
				}
				score += n
				if strings.Contains(strings.ToLower(s.Heading), t) {
					score += 50
				}
				if strings.Contains(title, t) {
					score += 20
				}
			}
			if !ok {
				continue
			}
			url := p.URL
			if s.ID != "" {
				url += "#" + s.ID
			}
			out = append(out, Result{
				Page:    p,
				Heading: s.Heading,
				URL:     url,
				Snippet: snippet(s.Text, terms[0]),
				score:   score,
			})
		}
	}
	slices.SortStableFunc(out, func(a, b Result) int { return cmp.Compare(b.score, a.score) })
	if len(out) > MaxResults {
		out = out[:MaxResults]
	}
	return out
}

// snippet cuts about 80 bytes of context on each side of the first match
// of term in text, at rune boundaries.
func snippet(text, term string) Snippet {
	const context = 80
	i := strings.Index(strings.ToLower(text), term)
	if i < 0 {
		// The term was only in the heading.
		if len(text) > 2*context {
			return Snippet{Before: cut(text, 2*context) + "…"}
		}
		return Snippet{Before: text}
	}
	// strings.ToLower can change byte lengths for some runes; fall back to
	// no highlighting if the offsets don't line up.
	end := i + len(term)
	if end > len(text) || !strings.EqualFold(text[i:end], term) {
		return Snippet{Before: cut(text, 2*context)}
	}
	start := max(0, i-context)
	for start > 0 && !utf8.RuneStart(text[start]) {
		start--
	}
	stop := min(len(text), end+context)
	for stop < len(text) && !utf8.RuneStart(text[stop]) {
		stop++
	}
	s := Snippet{Before: text[start:i], Match: text[i:end], After: text[end:stop]}
	if start > 0 {
		s.Before = "…" + s.Before
	}
	if stop < len(text) {
		s.After += "…"
	}
	return s
}

func cut(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}
