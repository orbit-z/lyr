package lsp

import (
	"strconv"
	"strings"

	"github.com/orbit-z/orbit/internal/compiler"
	"github.com/orbit-z/orbit/internal/project"
)

// Control block completion (PLAN.md §9 Request routing): right after a "{"
// in template content, or after "{" and an identifier or "/" prefix, the
// control block keywords are offered natively: {if}, {for} and {switch},
// the branches the enclosing open block takes ({else if}, {else}, {case},
// {default}) and the closer of the innermost open block. Where the cursor
// is also a Go region ({i could start {items}), gopls's items follow.

// blockSite is a position where control block keywords are offered.
type blockSite struct {
	// brace is the offset of the "{"; prefix is what's typed after it, up
	// to the cursor ("", "i", "/", "/fo").
	brace  int
	prefix string
	// closer is the closer of the innermost open block ("/if", "/for",
	// "/switch"), or "" (also with an element open inside that block).
	closer string
	// inIf: the innermost enclosing construct is an {if} branch other than
	// {else}, so {else if} is offered, and {else} when hasElse is false.
	inIf, hasElse bool
	// inSwitch: the innermost enclosing construct is a {switch} (or one of
	// its cases), so {case} is offered, and {default} when hasDefault is
	// false.
	inSwitch, hasDefault bool
}

// within reports whether off is inside s, end excluded (the "{" at off is
// a character of s).
func within(s compiler.Span, off int) bool { return s.Start <= off && off < s.End }

func isWordByte(c byte) bool {
	return c == '_' || '0' <= c && c <= '9' || 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z'
}

// blockSiteAt finds the block site at byte offset off of f: the cursor
// follows "{" and at most a "/" and identifier characters, and the "{"
// starts a template expression, a control tag or a malformed brace in
// template content (not in a start tag, a script, a style, a comment or
// the frontmatter; RCDATA content counts, control blocks are valid there).
func blockSiteAt(f *project.File, off int) (blockSite, bool) {
	if f.AST == nil || f.MD != nil || off < 0 || off > len(f.Src) {
		return blockSite{}, false
	}
	src := f.Src
	i := off
	for i > 0 && isWordByte(src[i-1]) {
		i--
	}
	if i > 0 && src[i-1] == '/' {
		i--
	}
	if i == 0 || src[i-1] != '{' {
		return blockSite{}, false
	}
	b := i - 1
	if !within(f.AST.Template.Span, b) {
		return blockSite{}, false
	}
	site := blockSite{brace: b, prefix: src[i:off]}
	var (
		innermost compiler.Node // the innermost enclosing open block
		curIf     *compiler.If
		curSwitch *compiler.Switch
		direct    compiler.Node // the innermost enclosing construct
	)
	// open records an enclosing open block.
	open := func(n compiler.Node) {
		innermost, direct = n, n
		switch n := n.(type) {
		case *compiler.If:
			curIf = n
		case *compiler.Switch:
			curSwitch = n
		}
	}
	var n compiler.Node = f.AST.Template
descend:
	for {
		var next compiler.Node
		for _, ch := range compiler.ChildNodes(n) {
			if within(ch.Bounds(), b) {
				next = ch
				break
			}
		}
		if next == nil {
			break // a dropped construct ({/}, {/i}) in n's content
		}
		switch x := next.(type) {
		case *compiler.Expr:
			if x.Start != b {
				return blockSite{}, false // a brace inside Go code
			}
			break descend
		case *compiler.If, *compiler.For, *compiler.Switch:
			var header, endTag compiler.Span
			switch x := x.(type) {
			case *compiler.If:
				header, endTag = x.Branches[0].Header, x.EndTag
			case *compiler.For:
				header, endTag = x.Header, x.EndTag
			case *compiler.Switch:
				header, endTag = x.Header, x.EndTag
			}
			switch {
			case x.Bounds().Start == b:
				break descend // the block opens here: it doesn't enclose
			case within(endTag, b):
				if endTag.Start != b {
					return blockSite{}, false
				}
				open(x) // retyping its closer
				break descend
			case within(header, b):
				return blockSite{}, false // inside the header's Go code
			}
			open(x)
		case *compiler.Branch:
			if within(x.Header, b) {
				if x.Header.Start != b {
					return blockSite{}, false
				}
				break descend // retyping an {else …} header: direct is the If
			}
			direct = x
		case *compiler.Case:
			if within(x.Header, b) {
				if x.Header.Start != b {
					return blockSite{}, false
				}
				break descend
			}
			direct = x
		case *compiler.Element, *compiler.Component, *compiler.Slot, *compiler.Fragment:
			t := compiler.TagOf(x)
			if within(t.StartTag, b) || within(t.EndTag, b) {
				return blockSite{}, false // an attribute or a spread
			}
			// A closer here would close the element with an error, as
			// would a branch.
			innermost, direct = nil, x
		default:
			// Text, comments, a doctype, a script or a style.
			return blockSite{}, false
		}
		n = next
	}
	switch innermost.(type) {
	case *compiler.If:
		site.closer = "/if"
	case *compiler.For:
		site.closer = "/for"
	case *compiler.Switch:
		site.closer = "/switch"
	}
	switch d := direct.(type) {
	case *compiler.If:
		site.inIf = true
	case *compiler.Branch:
		site.inIf = !d.Else
	case *compiler.Switch, *compiler.Case:
		site.inSwitch = true
	}
	if site.inIf && curIf != nil {
		for _, br := range curIf.Branches {
			if br.Else && br.Header.Start != b {
				site.hasElse = true
			}
		}
	}
	if site.inSwitch && curSwitch != nil {
		for _, cs := range curSwitch.Cases {
			if cs.Default && cs.Header.Start != b {
				site.hasDefault = true
			}
		}
	}
	return site, true
}

// blockKeyword is one control block completion: its snippet and its plain
// text (both what follows the "{").
type blockKeyword struct {
	label, snippet, plain, doc string
}

var (
	kwIf     = blockKeyword{"if", "if ${1:cond}}$0{/if}", "if ", "Renders its content when the condition is true: `{if cond}…{/if}`."}
	kwFor    = blockKeyword{"for", "for ${1:_, x := range xs}}$0{/for}", "for ", "Renders its content for each iteration of a Go for clause: `{for _, x := range xs}…{/for}`."}
	kwSwitch = blockKeyword{"switch", "switch ${1:x}}{case ${2:v}}$0{/switch}", "switch ", "Renders the first matching case: `{switch x}{case v}…{default}…{/switch}`."}
	kwElseIf = blockKeyword{"else if", "else if ${1:cond}}", "else if ", "Another branch of the `{if}`, taken when its condition is true."}
	kwElse   = blockKeyword{"else", "else}", "else}", "The last branch of the `{if}`, taken when no condition is true."}
	kwCase   = blockKeyword{"case", "case ${1:v}}", "case ", "A case of the `{switch}`: a list of values (or types, or conditions)."}
	kwDflt   = blockKeyword{"default", "default}", "default}", "The `{switch}` case taken when no other matches."}
)

// keywords are the keywords offered at site, in order: only the
// closer after "{/", else {if}, {for}, {switch}, the enclosing block's
// branches, then the closer.
func (site blockSite) keywords() []blockKeyword {
	var out []blockKeyword
	closer := func() {
		if site.closer != "" {
			out = append(out, blockKeyword{site.closer, site.closer + "}", site.closer + "}", "Closes the innermost open block."})
		}
	}
	if strings.HasPrefix(site.prefix, "/") {
		closer()
		return out
	}
	out = append(out, kwIf, kwFor, kwSwitch)
	if site.inIf {
		out = append(out, kwElseIf)
		if !site.hasElse {
			out = append(out, kwElse)
		}
	}
	if site.inSwitch {
		out = append(out, kwCase)
		if !site.hasDefault {
			out = append(out, kwDflt)
		}
	}
	closer()
	return out
}

// blockItems are the completion items for site: Keyword items whose edit
// replaces what's typed after the "{" and, when the inserted text ends
// with "}", a "}" right after the cursor (the editor's auto-closed brace).
func (c cursor) blockItems(site blockSite, snippets bool) []any {
	var items []any
	for i, kw := range site.keywords() {
		text, format := kw.plain, formatPlain
		if snippets {
			text, format = kw.snippet, formatSnippet
		}
		end := c.off
		if strings.HasSuffix(text, "}") && end < len(c.f.Src) && c.f.Src[end] == '}' {
			end++
		}
		items = append(items, map[string]any{
			"label": kw.label, "kind": kindKeyword, "detail": "{" + kw.label + "}",
			"sortText": "0" + strconv.Itoa(10+i), "filterText": kw.label, "insertTextFormat": format,
			"textEdit":      c.textEdit(compiler.Span{Start: site.brace + 1, End: end}, text),
			"documentation": map[string]any{"kind": "markdown", "value": kw.doc},
		})
	}
	return items
}

// mergeBlockItems puts Orbit's keyword items before the items of gopls's
// (mapped) completion list, whose sortTexts get a "1" prefix so the
// keywords sort first.
func mergeBlockItems(items []any, list jsonRaw) any {
	head, raw := completionItems(list)
	if head == nil {
		head = obj{}
		head.set("isIncomplete", false)
	}
	out := make([]any, 0, len(items)+len(raw))
	out = append(out, items...)
	for _, ri := range raw {
		it := decodeObj(ri)
		if it == nil {
			continue
		}
		st := it.str("sortText")
		if st == "" {
			st = it.str("label")
		}
		it.set("sortText", "1"+st)
		out = append(out, it.marshal())
	}
	head.set("items", out)
	return head.marshal()
}
