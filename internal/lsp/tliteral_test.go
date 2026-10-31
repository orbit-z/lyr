package lsp

import (
	"slices"
	"strings"
	"testing"
)

// Template literals (PLAN.md §2.10): Go features inside ${…}.

const tlCard = `---
import "strings"

type Props struct {
	Slug  string
	Index int
}

title := "Hi"
---
<a href={` + "`/fiction/${props.Slug}`" + `} aria-label={` + "`Chapter ${props.Index + 1} of ${title}`" + `}>x</a>
<p>{strings.ToUpper(` + "`a ${title} b`" + `)}</p>
{if title == ` + "`x-${props.Slug}`" + `}<i>y</i>{/if}
<p>{` + "`outer ${strings.ToLower(`in ${title}`)}`" + `}</p>
<p>{` + "`line one\n  ${title} two ${strings.Repeat(title, 2)}`" + `}</p>
`

func TestTemplateLiteralLSP(t *testing.T) {
	needGopls(t)
	const path = "src/components/Card.lyr"
	files := map[string]string{path: tlCard}
	dir := writeProject(t, maps2(files))
	c := startClient(t, dir, nil)
	text := c.open(path)

	// Every title use in the template: whole value, call argument, control
	// header, nested, a later line of a multi-line literal.
	uses := []string{"${title}`}>", "${title} b", "{if title", "${title}`)}", "${title} two"}
	offs := map[string]int{"${title}`}>": 2, "${title} b": 2, "{if title": 4, "${title}`)}": 2, "${title} two": 2}

	t.Run("hover", func(t *testing.T) {
		for _, u := range uses {
			if h, _ := c.hover(path, at(t, text, u, 0, offs[u])); !strings.Contains(h, "string") {
				t.Errorf("hover at %q: %q", u, h)
			}
		}
		if h, _ := c.hover(path, at(t, text, "Chapter ", 0, 2)); h != "" {
			t.Errorf("hover in static text: %q", h)
		}
	})

	t.Run("definition", func(t *testing.T) {
		for _, u := range uses {
			locs := c.locations(c.call("textDocument/definition", c.textPos(path, at(t, text, u, 0, offs[u]))))
			if len(locs) != 1 || rangeText(text, locs[0].Range) != "title" || locs[0].Range.Start.Line != 8 {
				t.Errorf("definition at %q: %v", u, locs)
			}
		}
	})

	t.Run("references", func(t *testing.T) {
		locs := c.locations(c.call("textDocument/references", map[string]any{
			"textDocument": map[string]any{"uri": c.uri(path)}, "position": at(t, text, "title :=", 0, 1),
			"context": map[string]any{"includeDeclaration": true}}))
		if len(locs) != 7 {
			t.Errorf("%d references, want 7: %v", len(locs), locs)
		}
	})

	t.Run("completion", func(t *testing.T) {
		defer c.change(path, text)
		for _, tc := range []struct{ from, to string }{
			{"${props.Slug}`}", "${props.}`}"},
			{"x-${props.Slug}", "x-${props.}"},
			{"`in ${title}`", "`in ${props.}`"},
			{"two ${strings.Repeat(title, 2)}", "two ${props.}"},
		} {
			if !strings.Contains(text, tc.from) {
				t.Fatalf("%q not in the file", tc.from)
			}
			src := strings.Replace(text, tc.from, tc.to, 1)
			c.change(path, src)
			if items := labels(c.complete(path, at(t, src, "props.}", 0, len("props.")))); !slices.Contains(items, "Slug") {
				t.Errorf("completion in %q: %v", tc.to, items)
			}
		}
		// An empty piece.
		src := strings.Replace(text, "${props.Slug}`}", "${}`}", 1)
		c.change(path, src)
		if items := labels(c.complete(path, at(t, src, "${}`}", 0, 2))); !slices.Contains(items, "title") {
			t.Fatalf("completion in ${}: %v", items)
		}
		// An unterminated literal.
		src = strings.Replace(text, "<p>{strings.ToUpper(`a ${title} b`)}</p>", "<p>{`a ${prop", 1)
		c.change(path, src)
		if items := labels(c.complete(path, at(t, src, "${prop", 0, len("${prop")))); !slices.Contains(items, "props") {
			t.Errorf("completion in an unterminated literal: %v", items)
		}
		c.change(path, text)
		if items := c.complete(path, at(t, text, "Chapter ", 0, 3)); len(items) != 0 {
			t.Errorf("completion in static text: %v", labels(items))
		}
	})

	t.Run("signature help", func(t *testing.T) {
		res := c.call("textDocument/signatureHelp", c.textPos(path, at(t, text, "Repeat(title, 2)", 0, len("Repeat(title, "))))
		if !strings.Contains(string(res), "Repeat(s string, count int)") {
			t.Errorf("signature help: %s", res)
		}
	})

	t.Run("rename", func(t *testing.T) {
		res := c.call("textDocument/rename", c.renameParams(path, at(t, text, "${title} two", 0, 2), "heading"))
		checkRename(t, c.workspaceEdit(res, true), files, map[string]string{path: strings.ReplaceAll(tlCard, "title", "heading")})
	})
}

// TestClassifyTemplateLiteral: a literal's static text is no region; its
// pieces, from right after "${" to right before "}", are Go.
func TestClassifyTemplateLiteral(t *testing.T) {
	const path = "src/components/Card.lyr"
	ws := memWorkspace(t, map[string]string{path: tlCard})
	f := ws.byPath[path]
	for _, tc := range []struct {
		at    string
		delta int
		want  regionKind
	}{
		{"`/fiction/", 0, regionGo}, // before the opening backtick
		{"`/fiction/", 1, regionNone},
		{"/fiction/$", 9, regionNone}, // on the "$"
		{"${props.Slug}", 2, regionGo},
		{"${props.Slug}", 12, regionGo}, // before the "}"
		{"${props.Slug}", 13, regionNone},
		{"Chapter ", 3, regionNone},
		{" of ${title}", 1, regionNone},
		{"strings.ToUpper(`", 17, regionNone},
		{"x-${", 1, regionNone},
		{"x-${props", 4, regionGo},
		{"outer ${", 2, regionNone},
		{"strings.ToLower(`in", 18, regionNone}, // nested static text
		{"${title}`)}", 3, regionGo},            // nested piece
		{"`)}`}", 1, regionGo},                  // between the nested literal and ")"
		{"line one", 2, regionNone},
		{"${title} two", 2, regionGo},
		{"} two ", 3, regionNone},
	} {
		if got := classify(f, strings.Index(tlCard, tc.at)+tc.delta).kind; got != tc.want {
			t.Errorf("classify %q+%d = %d, want %d", tc.at, tc.delta, got, tc.want)
		}
	}
}
