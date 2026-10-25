package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

type position struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

// posAt converts a byte offset in text to an LSP position in encoding enc.
func posAt(text string, off int, enc string) position {
	line := strings.Count(text[:off], "\n")
	ls := strings.LastIndex(text[:off], "\n") + 1
	seg := text[ls:off]
	ch := len(seg)
	if enc == "utf-16" {
		ch = 0
		for _, r := range seg {
			ch += len(utf16.Encode([]rune{r}))
		}
	} else if enc == "utf-32" {
		ch = utf8.RuneCountInString(seg)
	}
	return position{line, ch}
}

// posOf finds the n-th (0-based) occurrence of needle and returns the position
// delta bytes after the start of the match.
func posOf(text, needle string, nth, delta int, enc string) position {
	off := -1
	for i, base := 0, 0; i <= nth; i++ {
		j := strings.Index(text[base:], needle)
		if j < 0 {
			panic(fmt.Sprintf("needle %q #%d not found", needle, nth))
		}
		off = base + j
		base = off + 1
	}
	return posAt(text, off+delta, enc)
}

type report struct{ b bytes.Buffer }

func (r *report) h(f string, a ...any) { fmt.Fprintf(&r.b, "\n## "+f+"\n\n", a...) }
func (r *report) p(f string, a ...any) { fmt.Fprintf(&r.b, f+"\n", a...) }
func (r *report) js(label string, raw json.RawMessage, max int) {
	var v any
	json.Unmarshal(raw, &v)
	b, _ := json.MarshalIndent(v, "", "  ")
	s := string(b)
	if max > 0 && len(s) > max {
		s = s[:max] + "\n…(trimmed)"
	}
	fmt.Fprintf(&r.b, "%s:\n```json\n%s\n```\n", label, s)
}
func (r *report) save(path string) error { return os.WriteFile(path, r.b.Bytes(), 0o644) }

// completion helpers

type complItem struct {
	Label string          `json:"label"`
	Kind  int             `json:"kind"`
	Raw   json.RawMessage `json:"-"`
}

func parseCompletion(raw json.RawMessage) (incomplete bool, items []complItem) {
	var list struct {
		IsIncomplete bool              `json:"isIncomplete"`
		Items        []json.RawMessage `json:"items"`
	}
	var arr []json.RawMessage
	if err := json.Unmarshal(raw, &arr); err != nil {
		json.Unmarshal(raw, &list)
		arr = list.Items
	}
	for _, it := range arr {
		var ci complItem
		json.Unmarshal(it, &ci)
		ci.Raw = it
		items = append(items, ci)
	}
	return list.IsIncomplete, items
}

func labels(items []complItem, n int) []string {
	var out []string
	for i, it := range items {
		if i >= n {
			out = append(out, fmt.Sprintf("…(+%d)", len(items)-n))
			break
		}
		out = append(out, it.Label)
	}
	return out
}

func find(items []complItem, label string) *complItem {
	for i := range items {
		if items[i].Label == label {
			return &items[i]
		}
	}
	return nil
}

func uriOf(path string) string { return "file://" + path }
