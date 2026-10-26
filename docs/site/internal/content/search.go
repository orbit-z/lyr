package content

import (
	"cmp"
	"slices"
	"strings"
	"unicode"
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

// MaxPerPage caps the results from one page, so one long page doesn't
// crowd out the others.
const MaxPerPage = 3

// Search finds the sections that contain every word of q, ignoring case.
// A page whose title matches the query ranks first (its opening section),
// then matches in headings, then matches in page titles, then the number
// of occurrences.
func (d *Docs) Search(q string) []Result {
	terms := strings.Fields(strings.ToLower(q))
	if len(terms) == 0 {
		return nil
	}
	query := strings.Join(terms, " ")
	var out []Result
	for _, p := range d.Pages {
		title := strings.ToLower(p.Title)
		titleHit := strings.Contains(title, query)
		for i, s := range p.Sections {
			score := 0
			ok := true
			heading := strings.ToLower(s.Heading)
			for _, t := range terms {
				n := strings.Count(s.lower, t)
				if n == 0 && !(i == 0 && titleHit) {
					ok = false
					break
				}
				score += min(n, 20)
				if strings.Contains(heading, t) {
					score += 50
				}
				if strings.Contains(title, t) {
					score += 20
				}
			}
			if !ok {
				continue
			}
			if i == 0 && titleHit {
				score += 1000
				if title == query {
					score += 1000
				}
			}
			if heading == query {
				score += 100
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
	perPage := map[*Page]int{}
	out = slices.DeleteFunc(out, func(r Result) bool {
		perPage[r.Page]++
		return perPage[r.Page] > MaxPerPage
	})
	if len(out) > MaxResults {
		out = out[:MaxResults]
	}
	return out
}

// Suggest returns q with each word that appears nowhere in the docs
// replaced by the closest word that does (one typo for words of 4 to 7
// letters, two from 8), or "" when there's nothing to correct.
func (d *Docs) Suggest(q string) string {
	terms := strings.Fields(strings.ToLower(q))
	changed := false
	for i, t := range terms {
		n := utf8.RuneCountInString(t)
		if n < 4 || d.contains(t) {
			continue
		}
		maxDist := 1
		if n >= 8 {
			maxDist = 2
		}
		best, bestDist := "", maxDist+1
		for _, w := range d.words {
			if abs(len(w)-len(t)) > maxDist {
				continue
			}
			if dist := editDistance(t, w, bestDist); dist < bestDist {
				best, bestDist = w, dist
			}
		}
		if best != "" {
			terms[i], changed = best, true
		}
	}
	if !changed {
		return ""
	}
	return strings.Join(terms, " ")
}

func (d *Docs) contains(t string) bool {
	for _, p := range d.Pages {
		for _, s := range p.Sections {
			if strings.Contains(s.lower, t) {
				return true
			}
		}
	}
	return false
}

// indexWords collects the distinct words of the docs, for Suggest.
func (d *Docs) indexWords() {
	seen := map[string]bool{}
	for _, p := range d.Pages {
		for _, s := range p.Sections {
			for _, w := range strings.FieldsFunc(s.lower, func(r rune) bool {
				return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_'
			}) {
				if len(w) >= 3 && !seen[w] {
					seen[w] = true
					d.words = append(d.words, w)
				}
			}
		}
	}
	slices.Sort(d.words) // deterministic suggestions on ties
}

// editDistance is the Levenshtein distance of a and b (bytes), or any
// value ≥ limit once it's known to reach limit.
func editDistance(a, b string, limit int) int {
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		rowMin := cur[0]
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
			rowMin = min(rowMin, cur[j])
		}
		if rowMin >= limit {
			return limit
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
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
