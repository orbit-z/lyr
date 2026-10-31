package lsp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/scanner"
	"go/token"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/orbit-z/lyr/internal/compiler"
)

// Completion and signature help (PLAN.md §9 Request routing): Go regions
// go to gopls, with every range of the result mapped back; tag names,
// attributes, directives and slot names are answered natively, with a
// component's props asked of gopls through a scratch Props{} literal.

// LSP CompletionItemKind values.
const (
	kindField    = 5
	kindClass    = 7
	kindProperty = 10
	kindValue    = 12
	kindKeyword  = 14
)

// LSP InsertTextFormat values.
const (
	formatPlain   = 1
	formatSnippet = 2
)

// completion answers textDocument/completion by the cursor's region.
func (s *Server) completion(ctx context.Context, params jsonRaw) (any, *rpcError) {
	o, uri, pos, ok := textPosition(params)
	if !ok {
		return nil, nil
	}
	trigger := triggerCharacter(o)
	s.mu.Lock()
	c, ok := s.resolve(uri, pos)
	if !ok {
		s.mu.Unlock()
		return nil, nil
	}
	snippets := s.caps.snippets
	if items, ok := c.ws.importPathItems(c, trigger); ok {
		s.mu.Unlock()
		return completionList(false, items), nil
	}
	// Control block keywords (blocks.go): alone after "{/" or outside a Go
	// region, else before gopls's items.
	var blockKw []any
	if site, ok := blockSiteAt(c.f, c.off); ok && (trigger == "" || trigger == "{" || trigger == "/") {
		blockKw = c.blockItems(site, snippets)
		if len(blockKw) > 0 && (strings.HasPrefix(site.prefix, "/") || c.reg.kind != regionGo) {
			s.mu.Unlock()
			return completionList(false, blockKw), nil
		}
	}
	switch c.reg.kind {
	case regionGo:
		if !goTrigger(c.reg, trigger) {
			break
		}
		g, fwd, ok := s.toGopls(c, o)
		if !ok {
			break
		}
		content := g.content
		s.mu.Unlock()
		raw, err := s.callGopls(ctx, "textDocument/completion", fwd, false)
		if err != nil || isNull(raw) {
			if err == nil && blockKw != nil {
				return completionList(false, blockKw), nil
			}
			return nil, err
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		// The positions are only valid for the content gopls completed in
		// (overlays are re-sent with the same content, new versions).
		if s.ws.genAt(g.abs) != g || !bytes.Equal(g.content, content) {
			return completionList(true, nil), nil // the file changed meanwhile
		}
		mapped := s.mapper().mapCompletion(g, raw)
		if blockKw != nil {
			return mergeBlockItems(blockKw, mapped), nil
		}
		return mapped, nil
	case regionTag, regionTagName:
		if trigger != "" && trigger != "<" {
			break
		}
		items := c.ws.tagItems(c)
		s.mu.Unlock()
		return completionList(false, items), nil
	case regionAttr, regionProp:
		if trigger != "" && trigger != " " && trigger != ":" {
			break
		}
		if c.reg.comp == nil {
			items := c.ws.attrItems(c, snippets)
			s.mu.Unlock()
			return completionList(false, items), nil
		}
		info := c.ws.target(c.f, c.reg.comp)
		req := s.propsRequest(c.ws, info)
		s.mu.Unlock()
		var fields []obj
		fromGopls := false
		if req != nil {
			fields, fromGopls = s.goplsFields(ctx, req)
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		return completionList(false, c.ws.componentItems(c, info, fields, fromGopls, snippets)), nil
	case regionSlot:
		if trigger != "" && trigger != `"` && trigger != "'" {
			break
		}
		items := c.ws.slotItems(c)
		s.mu.Unlock()
		return completionList(false, items), nil
	}
	s.mu.Unlock()
	if blockKw != nil {
		return completionList(false, blockKw), nil
	}
	return nil, nil
}

// triggerCharacter returns the character that triggered a completion
// request, "" when it was invoked or re-triggered.
func triggerCharacter(o obj) string {
	ctx := decodeObj(o["context"])
	if ctx == nil {
		return ""
	}
	var kind int
	json.Unmarshal(ctx["triggerKind"], &kind)
	if kind != 2 {
		return ""
	}
	return ctx.str("triggerCharacter")
}

// goTrigger reports whether a completion triggered by char goes to gopls:
// only "." (gopls's own trigger) and "{" opening a template expression;
// the other trigger characters are for the template.
func goTrigger(reg region, char string) bool {
	return char == "" || char == "." || char == "{" && !reg.frontmatter
}

func completionList(incomplete bool, items []any) map[string]any {
	if items == nil {
		items = []any{}
	}
	return map[string]any{"isIncomplete": incomplete, "items": items}
}

// signatureHelp: gopls in Go regions. The result holds no document
// positions, so it passes through.
func (s *Server) signatureHelp(ctx context.Context, params jsonRaw) (any, *rpcError) {
	o, uri, pos, ok := textPosition(params)
	if !ok {
		return nil, nil
	}
	s.mu.Lock()
	c, ok := s.resolve(uri, pos)
	if !ok || c.reg.kind != regionGo {
		s.mu.Unlock()
		return nil, nil
	}
	_, fwd, ok := s.toGopls(c, o)
	s.mu.Unlock()
	if !ok {
		return nil, nil
	}
	raw, err := s.callGopls(ctx, "textDocument/signatureHelp", fwd, false)
	if err != nil || isNull(raw) {
		return nil, err
	}
	return raw, nil
}

// completionItems decodes a CompletionList or CompletionItem[] result:
// the list object (nil for an array) and its items.
func completionItems(raw jsonRaw) (obj, []jsonRaw) {
	var items []jsonRaw
	if bytes.HasPrefix(bytes.TrimSpace(raw), []byte("[")) {
		json.Unmarshal(raw, &items)
		return nil, items
	}
	list := decodeObj(raw)
	if list != nil {
		json.Unmarshal(list["items"], &items)
	}
	return list, items
}

// mapCompletion maps gopls's completion result for generated file g into
// its .lyr file: every range (textEdit's range, or an InsertReplaceEdit's
// insert and replace; additionalTextEdits; itemDefaults.editRange) is
// rewritten, other fields pass through. An item whose edit doesn't map is
// dropped, as are the generated code's own names (lyr__*) and the
// runtime's generated-code-only names (genonly.go). An unmapped
// additional edit is dropped, except an import gopls adds, which becomes an
// insertion into the frontmatter's imports.
func (m mapper) mapCompletion(g *genFile, raw jsonRaw) jsonRaw {
	list, rawItems := completionItems(raw)
	if list == nil {
		list = obj{}
		list.set("isIncomplete", false)
	}
	if d := decodeObj(list["itemDefaults"]); d != nil {
		if er := decodeObj(d["editRange"]); er != nil {
			ok := false
			if _, isRange := er["start"]; isRange {
				var r Range
				if json.Unmarshal(d["editRange"], &r) == nil {
					var nr Range
					if nr, ok = m.lyrRange(g, r); ok {
						d.set("editRange", nr)
					}
				}
			} else if ok = m.mapEditRanges(g, er); ok {
				d["editRange"] = er.marshal()
			}
			if !ok {
				delete(d, "editRange")
			}
			list["itemDefaults"] = d.marshal()
		}
	}
	out := make([]jsonRaw, 0, len(rawItems))
	for _, ri := range rawItems {
		it := decodeObj(ri)
		if it == nil || isGlueName(it.str("label")) || isGenOnlyItem(it) {
			continue
		}
		if te := decodeObj(it["textEdit"]); te != nil {
			if !m.mapEditRanges(g, te) {
				continue
			}
			it["textEdit"] = te.marshal()
		}
		if raw, has := it["additionalTextEdits"]; has {
			var edits []jsonRaw
			json.Unmarshal(raw, &edits)
			mapped, ok := m.mapAdditionalEdits(g, edits)
			if !ok {
				continue
			}
			if len(mapped) == 0 {
				delete(it, "additionalTextEdits")
			} else {
				it.set("additionalTextEdits", mapped)
			}
		}
		out = append(out, it.marshal())
	}
	list.set("items", out)
	return list.marshal()
}

// isGlueName reports whether a completion label is a name of the
// generated code (lyr__w, lyr__rt, Lyr__Render), which user code
// can't use (PLAN.md §2.2).
func isGlueName(label string) bool {
	return len(label) >= len("lyr__") && strings.EqualFold(label[:len("lyr__")], "lyr__")
}

// mapAdditionalEdits maps a completion item's additionalTextEdits. Edits
// that start in the generated file's imports (some of which map: the user's
// own import specs are segments) become one insertion of the added import
// specs into the frontmatter; other edits are mapped, or dropped when they
// don't map. ok is false when the item imports a generated package (the
// user imports the .lyr file instead) and should be dropped.
func (m mapper) mapAdditionalEdits(g *genFile, edits []jsonRaw) (out []jsonRaw, ok bool) {
	importsEnd := importSectionEnd(g.content)
	var imports []obj
	for _, e := range edits {
		o := decodeObj(e)
		if o == nil {
			continue
		}
		if r, has := o.rng("range"); has && g.lines.offset(r.Start, m.enc) <= importsEnd {
			imports = append(imports, o)
		} else if m.mapEditRanges(g, o) {
			out = append(out, o.marshal())
		}
	}
	if len(imports) == 0 {
		return out, true
	}
	specs := addedImports(g.content, g.lines, m.enc, imports)
	for _, sp := range specs {
		if strings.Contains(sp, "/"+compiler.GenDir+"/") || strings.Contains(sp, "lyr__") {
			return nil, false
		}
	}
	if len(specs) > 0 {
		if e := m.ws.importEdit(g.lyr, specs); e != nil {
			out = append(out, e)
		}
	}
	return out, true
}

// addedImports applies edits (in enc positions of content) and returns the
// import specs (`"path"` or `name "path"`) the result has that content
// doesn't.
func addedImports(content []byte, lines *lineIndex, enc encoding, edits []obj) []string {
	type edit struct {
		start, end int
		text       string
	}
	var eds []edit
	for _, o := range edits {
		r, ok := o.rng("range")
		if !ok {
			continue
		}
		eds = append(eds, edit{lines.offset(r.Start, enc), lines.offset(r.End, enc), o.str("newText")})
	}
	slices.SortStableFunc(eds, func(a, b edit) int { return b.start - a.start })
	after := string(content)
	for _, e := range eds {
		if e.start > e.end || e.end > len(after) {
			continue
		}
		after = after[:e.start] + e.text + after[e.end:]
	}
	count := map[string]int{}
	for _, sp := range importSpecs(string(content)) {
		count[sp]++
	}
	var added []string
	for _, sp := range importSpecs(after) {
		if count[sp] > 0 {
			count[sp]--
			continue
		}
		added = append(added, sp)
	}
	return added
}

// importSectionEnd returns the offset where a Go file's import declarations
// end: after the last one, or after the package clause.
func importSectionEnd(src []byte) int {
	fset := token.NewFileSet()
	f, _ := parser.ParseFile(fset, "", src, parser.ImportsOnly)
	if f == nil {
		return -1
	}
	end := f.Name.End()
	for _, d := range f.Decls {
		if gd, ok := d.(*ast.GenDecl); ok && gd.Tok == token.IMPORT {
			end = gd.End()
		}
	}
	return fset.Position(end).Offset
}

// importSpecs returns the import specs of a Go file.
func importSpecs(src string) []string {
	f, _ := parser.ParseFile(token.NewFileSet(), "", src, parser.ImportsOnly)
	if f == nil {
		return nil
	}
	var out []string
	for _, im := range f.Imports {
		sp := im.Path.Value
		if im.Name != nil {
			sp = im.Name.Name + " " + sp
		}
		out = append(out, sp)
	}
	return out
}

// importEdit returns a TextEdit adding import specs to the frontmatter of
// the .lyr file at p, or nil.
func (ws *workspace) importEdit(p string, specs []string) jsonRaw {
	f, lines := ws.file(p)
	if f == nil || lines == nil || f.AST == nil {
		return nil
	}
	off, text := importInsertion(f.Src, f.AST.Frontmatter, specs)
	b, _ := json.Marshal(map[string]any{"range": lines.rangeOf(off, off, encUTF16), "newText": text})
	return b
}

// importInsertion returns where and what to insert to add import specs to
// a file's frontmatter: into its last import group (before the ")"),
// after its last single import, or as import lines at the top of the
// frontmatter, which is created when the file has none.
func importInsertion(src string, fm compiler.Frontmatter, specs []string) (int, string) {
	var b strings.Builder
	if !fm.Present {
		b.WriteString("---\n")
		for _, sp := range specs {
			b.WriteString("import " + sp + "\n")
		}
		b.WriteString("---\n")
		return 0, b.String()
	}
	top := min(fm.Open.End, len(src))
	if strings.HasPrefix(src[top:], "\r\n") {
		top += 2
	} else if strings.HasPrefix(src[top:], "\n") {
		top++
	}
	base := fm.Code.Start
	code := src[base:min(fm.Code.End, len(src))]
	fset := token.NewFileSet()
	file := fset.AddFile("", -1, len(code))
	var sc scanner.Scanner
	sc.Init(file, []byte(code), nil, 0)
	groupClose, singleEnd, last := -1, -1, ""
scan:
	for {
		_, tok, _ := sc.Scan()
		switch tok {
		case token.SEMICOLON:
			continue
		case token.IMPORT:
		default:
			break scan
		}
		pos, tok, lit := sc.Scan()
		if tok == token.LPAREN {
			for tok != token.RPAREN && tok != token.EOF {
				pos, tok, _ = sc.Scan()
			}
			if tok == token.EOF {
				break
			}
			groupClose, last = file.Offset(pos), "group"
			continue
		}
		for tok != token.STRING && tok != token.EOF && tok != token.SEMICOLON {
			pos, tok, lit = sc.Scan()
		}
		if tok != token.STRING {
			break
		}
		singleEnd, last = file.Offset(pos)+len(lit), "single"
	}
	switch last {
	case "group":
		closeOff := base + groupClose
		ls := strings.LastIndexByte(src[:closeOff], '\n') + 1
		if strings.TrimSpace(src[ls:closeOff]) == "" {
			for _, sp := range specs {
				b.WriteString("\t" + sp + "\n")
			}
			return ls, b.String()
		}
		for _, sp := range specs {
			b.WriteString("\n\t" + sp)
		}
		b.WriteString("\n")
		return closeOff, b.String()
	case "single":
		for _, sp := range specs {
			b.WriteString("\nimport " + sp)
		}
		return base + singleEnd, b.String()
	}
	for _, sp := range specs {
		b.WriteString("import " + sp + "\n")
	}
	return top, b.String()
}

// textEdit is a TextEdit over a byte span of the cursor's document.
func (c cursor) textEdit(span compiler.Span, newText string) map[string]any {
	return map[string]any{"range": c.lines.rangeOf(span.Start, span.End, encUTF16), "newText": newText}
}

// nameSpan is what an attribute completion replaces: the name typed so
// far, or nothing at the cursor.
func (c cursor) nameSpan() compiler.Span {
	if c.reg.attr != nil {
		return c.reg.attr.NameSpan
	}
	return compiler.Span{Start: c.off, End: c.off}
}

// tagItems are the tag-name completions: the imported components first,
// then the project's other components (accepting one adds its .lyr
// import), then <Fragment> and the HTML elements.
func (ws *workspace) tagItems(c cursor) []any {
	span := c.reg.span
	item := func(label string, kind int, group, detail, doc string) map[string]any {
		it := map[string]any{"label": label, "kind": kind, "sortText": group + label, "filterText": label,
			"textEdit": c.textEdit(span, label)}
		if detail != "" {
			it["detail"] = detail
		}
		if doc != "" {
			it["documentation"] = map[string]any{"kind": "markdown", "value": doc}
		}
		return it
	}
	var items []any
	taken := map[string]bool{}
	imported := map[string]bool{}
	if c.f.Analysis != nil {
		for _, im := range c.f.Analysis.LyrImports {
			taken[im.Alias] = true
			imported[im.Resolved] = true
			items = append(items, item(im.Alias, kindClass, "0", im.Path, componentDoc(ws.proj.Index[im.Resolved])))
		}
	}
	for _, p := range sortedKeys(ws.proj.Index) {
		info := ws.proj.Index[p]
		if info.Role != compiler.RoleComponent || p == c.f.Path || imported[p] {
			continue
		}
		alias := uniqueAlias(compiler.FieldName(info.Name), taken)
		if alias == "" {
			continue
		}
		taken[alias] = true
		// The shortest tsconfig.json paths alias spelling when one covers
		// the file, as TypeScript's auto-import does; else a relative path.
		imp := relImport(c.f.Path, p)
		if sp := ws.proj.AliasSpellings(p); len(sp) > 0 {
			imp = sp[0]
		}
		spec := alias + " " + strconv.Quote(imp)
		it := item(alias, kindClass, "1", "import "+spec, componentDoc(info))
		if e := ws.importEdit(c.f.Path, []string{spec}); e != nil {
			it["additionalTextEdits"] = []jsonRaw{e}
		}
		items = append(items, it)
	}
	items = append(items, item("Fragment", kindKeyword, "2", "", "Groups children without a wrapper element."))
	for _, t := range html().Tags {
		items = append(items, item(t.Name, kindProperty, "2", "", t.Description))
	}
	return items
}

// componentDoc describes a component for its tag completion.
func componentDoc(info *compiler.ComponentInfo) string {
	if info == nil {
		return ""
	}
	return "Component `" + info.Path + "`" + propsAndSlots(info)
}

// propsAndSlots lists a component's props and slots in markdown.
func propsAndSlots(info *compiler.ComponentInfo) string {
	var b strings.Builder
	if len(info.Props.Fields) > 0 {
		b.WriteString("\n\nProps:\n")
		for _, pf := range info.Props.Fields {
			fmt.Fprintf(&b, "- `%s %s`", pf.Name, pf.Type)
			if doc := strings.TrimSpace(pf.Doc); doc != "" {
				fmt.Fprintf(&b, ": %s", strings.ReplaceAll(doc, "\n", " "))
			}
			b.WriteString("\n")
		}
	}
	if len(info.Slots) > 0 {
		fmt.Fprintf(&b, "\n\nSlots: %s", "`"+strings.Join(info.Slots, "`, `")+"`")
	}
	return b.String()
}

// uniqueAlias returns name, or name2, name3, … if taken; "" if name isn't
// a capitalized identifier (a .lyr alias must be, PLAN.md §2.2).
func uniqueAlias(name string, taken map[string]bool) string {
	r, _ := utf8.DecodeRuneInString(name)
	if !token.IsIdentifier(name) || !unicode.IsUpper(r) {
		return ""
	}
	for i := 1; ; i++ {
		cand := name
		if i > 1 {
			cand += strconv.Itoa(i)
		}
		if !taken[cand] {
			return cand
		}
	}
}

// relImport is the import path of the .lyr file to as written in from:
// relative, starting with "./" or "../".
func relImport(from, to string) string {
	rel, err := filepath.Rel(filepath.FromSlash(path.Dir(from)), filepath.FromSlash(to))
	if err != nil {
		return to
	}
	rel = filepath.ToSlash(rel)
	if !strings.HasPrefix(rel, "../") {
		rel = "./" + rel
	}
	return rel
}

// directiveDocs describe the directives offered as attributes.
var directiveDocs = map[string]string{
	"set:html":          "Sets the element's content to raw HTML (a `lyr.HTML` value).",
	"set:text":          "Sets the element's content to escaped text.",
	"set:json":          "Writes a value as JSON into this `<script type=\"application/json\">`.",
	"slot":              "Passes this content to the named slot of the component it's in.",
	"is:inline":         "Leaves this tag as written: not processed or bundled.",
	"is:global":         "Applies this style globally instead of scoping it to the component.",
	"server:defer":      "Renders this component in a separate request (a server island), showing its `fallback` slot until then.",
	"server:concurrent": "Renders this component in its own goroutine, so slow components overlap; the HTML stays in source order.",
	"client:load":       "Loads this component's scripts as soon as the page loads.",
	"client:idle":       "Loads this component's scripts once the browser is idle.",
	"client:visible":    "Loads this component's scripts when the component scrolls into view.",
	"client:media":      "Loads this component's scripts when a media query matches.",
	"class:list":        "Builds the `class` from a list: `{\"card\", props.Class, \"active\": active}` (strings, `[]string` values, and `\"class\": condition` pairs).",
}

// attrItem is an attribute completion. value is the attribute's value
// form: "" for a bare attribute, `"` for a quoted value, "{" for an
// expression; the cursor goes inside it when the client takes snippets.
func (c cursor) attrItem(name string, kind int, group, value, detail, doc string, snippets bool) map[string]any {
	text, format := name, formatPlain
	if value != "" {
		text = name + "="
		if snippets {
			closing := `"`
			if value == "{" {
				closing = "}"
			}
			text, format = name+"="+value+"$1"+closing, formatSnippet
		}
	}
	it := map[string]any{"label": name, "kind": kind, "sortText": group + name, "filterText": name,
		"insertTextFormat": format, "textEdit": c.textEdit(c.nameSpan(), text)}
	if detail != "" {
		it["detail"] = detail
	}
	if doc != "" {
		it["documentation"] = map[string]any{"kind": "markdown", "value": doc}
	}
	return it
}

// directiveItem is a directive's attribute completion.
func (c cursor) directiveItem(name string, snippets bool) map[string]any {
	value := ""
	switch name {
	case "set:html", "set:text", "set:json", "class:list":
		value = "{"
	case "slot", "client:media":
		value = `"`
	}
	return c.attrItem(name, kindKeyword, "1", value, "", directiveDocs[name], snippets)
}

// setAttrs are the lowercased names of a tag's attributes, except the one
// under the cursor.
func setAttrs(t *compiler.Tag, current *compiler.Attr) map[string]bool {
	set := map[string]bool{}
	for _, a := range t.Attrs {
		if a != current && a.Kind != compiler.AttrSpread {
			set[strings.ToLower(a.Name)] = true
		}
	}
	return set
}

// attrItems are the attribute completions on a non-component tag: the
// element's and the global HTML attributes, and the directives that are
// valid on it (never server:defer or client:*, errors there, §2.3).
func (ws *workspace) attrItems(c cursor, snippets bool) []any {
	t := compiler.TagOf(c.reg.node)
	if t == nil {
		return nil
	}
	set := setAttrs(t, c.reg.attr)
	data := html()
	var items []any
	attr := func(name string) {
		if set[name] {
			return
		}
		value := `"`
		if data.boolean[name] {
			value = ""
		}
		items = append(items, c.attrItem(name, kindProperty, "0", value, "", "", snippets))
	}
	attrs := func(tag string) {
		if d := data.byName[tag]; d != nil {
			for _, a := range d.Attributes {
				attr(a)
			}
		}
		for _, a := range data.GlobalAttributes {
			attr(a)
		}
	}
	directive := func(names ...string) {
		for _, d := range names {
			if !set[d] {
				items = append(items, c.directiveItem(d, snippets))
			}
		}
	}
	switch n := c.reg.node.(type) {
	case *compiler.Slot:
		attr("name")
	case *compiler.Fragment:
		directive("slot", "set:html", "set:text")
	case *compiler.Element:
		if n.Dynamic {
			attrs("") // the name is a variable: the global attributes (PLAN.md §2.8)
		} else {
			attrs(strings.ToLower(n.Name))
		}
		directive("slot", "class:list")
		if !n.Void {
			directive("set:html", "set:text")
		}
	case *compiler.Script:
		attrs("script")
		directive("is:inline")
		if a := t.Attr("type"); a != nil && strings.EqualFold(strings.TrimSpace(a.Value), "application/json") {
			directive("set:json")
		}
	case *compiler.Style:
		attrs("style")
		directive("is:inline", "is:global")
	}
	return items
}

// componentItems are the attribute completions on a component tag: its
// props not set yet (gopls's fields of Props when fromGopls, else the
// index's), then slot, server:defer and the client:* directives.
func (ws *workspace) componentItems(c cursor, info *compiler.ComponentInfo, fields []obj, fromGopls, snippets bool) []any {
	comp := c.reg.comp
	setFields := map[string]bool{}
	for _, a := range comp.Attrs {
		if a == c.reg.attr || a.Kind == compiler.AttrSpread || a.Directive != compiler.NotDirective || info == nil {
			continue
		}
		if name, err := compiler.MatchProp(&info.Props, a.Name); err == nil {
			setFields[name] = true
		}
	}
	var items []any
	if info != nil {
		docs := map[string]compiler.PropField{}
		for _, pf := range info.Props.Fields {
			docs[pf.Name] = pf
		}
		// No field from gopls while the index lists some: gopls couldn't
		// load the scratch package; use the index.
		if fromGopls && (len(fields) > 0 || len(info.Props.Fields) == 0) {
			for _, f := range fields {
				name := f.str("label")
				if setFields[name] {
					continue
				}
				items = append(items, c.propItem(f, docs[name], snippets))
			}
		} else {
			for _, pf := range info.Props.Fields {
				if !setFields[pf.Name] {
					items = append(items, c.attrItem(compiler.LowerCamel(pf.Name), kindField, "0", "{", pf.Type, strings.TrimSpace(pf.Doc), snippets))
				}
			}
		}
	}
	set := setAttrs(&comp.Tag, c.reg.attr)
	if !set["slot"] {
		items = append(items, c.directiveItem("slot", snippets))
	}
	if info != nil && !set["class:list"] {
		// A class prop, or pass-through attributes, which take the class.
		if _, err := compiler.MatchProp(&info.Props, "class"); err == nil || info.Props.Rest != "" {
			items = append(items, c.directiveItem("class:list", snippets))
		}
	}
	if comp.Defer == nil || comp.Defer == c.reg.attr {
		items = append(items, c.directiveItem("server:defer", snippets))
	}
	if comp.Concurrent == nil || comp.Concurrent == c.reg.attr {
		items = append(items, c.directiveItem("server:concurrent", snippets))
	}
	if comp.Client == nil || comp.Client == c.reg.attr {
		for _, d := range []string{"client:load", "client:idle", "client:visible", "client:media"} {
			items = append(items, c.directiveItem(d, snippets))
		}
	}
	return items
}

// propItem turns gopls's completion of a Props field into a prop
// attribute: the attribute name (lower camel, as in the unknown-prop
// message), attribute insert text instead of gopls's "Field: ${1:}", and
// gopls's inline documentation (scrubbed), or the index's doc comment when
// gopls has none. Other fields (detail, labelDetails, tags) pass through.
func (c cursor) propItem(f obj, pf compiler.PropField, snippets bool) map[string]any {
	name := f.str("label")
	detail := f.str("detail")
	if detail == "" {
		detail = pf.Type
	}
	it := c.attrItem(compiler.LowerCamel(name), kindField, "0", "{", detail, "", snippets)
	if st := f.str("sortText"); st != "" {
		it["sortText"] = "0" + st
	}
	for _, k := range []string{"labelDetails", "tags", "deprecated"} {
		if v, ok := f[k]; ok {
			it[k] = v
		}
	}
	doc := ""
	var mc struct {
		Value string `json:"value"`
	}
	if json.Unmarshal(f["documentation"], &mc) == nil {
		doc = mc.Value
	} else {
		doc = f.str("documentation")
	}
	doc = strings.TrimSpace(scrubHover(doc))
	if doc == "" {
		doc = strings.TrimSpace(pf.Doc)
	}
	if doc != "" {
		it["documentation"] = map[string]any{"kind": "markdown", "value": doc}
	}
	return it
}

// slotItems are the slot names for a slot="…" value: the slots of the
// component the content is passed to, only fallback in a server:defer one.
func (ws *workspace) slotItems(c cursor) []any {
	parent := c.reg.parent
	if parent == nil {
		return nil
	}
	var names []string
	if parent.Defer != nil {
		names = []string{"fallback"}
	} else if info := ws.target(c.f, parent); info != nil {
		names = info.Slots
	}
	var items []any
	for _, n := range names {
		items = append(items, map[string]any{"label": n, "kind": kindValue, "textEdit": c.textEdit(c.reg.span, n)})
	}
	return items
}

// Scratch files (PLAN.md §9 Attribute position): to complete a component's
// props, gopls is asked for completion inside a Props{} literal in a small
// overlay-only package of the LSP's own, one per component, which imports
// the component's declaration package. It's opened once and never changes:
// gopls sees the component's current Props through the overlays.

// scratchDir holds the scratch packages; nothing is ever written there.
const scratchDir = compiler.GenDir + "/lyr__lsp"

const scratchSource = `// Code generated by lyr lsp. DO NOT EDIT.

package lyr__lsp

import lyr__c %s

func _() {
	_ = lyr__c.Props{}
}
`

// scratchFile is one scratch overlay: its URI and the position inside the
// Props{} literal.
type scratchFile struct {
	uri string
	pos Position
}

// isScratchPath reports whether a module-relative path is a scratch file.
func isScratchPath(rel string) bool { return strings.HasPrefix(rel, scratchDir+"/") }

// propsRequest returns gopls's completion params for the Props{} literal
// of info's scratch file, opening it first, or nil when Go features are
// off (with s.mu held).
func (s *Server) propsRequest(ws *workspace, info *compiler.ComponentInfo) jsonRaw {
	if info == nil || !s.goEnabled || ws.gopls == nil {
		return nil
	}
	rel := scratchDir + "/" + info.Pkg.Rel + "/props.go"
	sc := ws.scratch[rel]
	if sc == nil {
		src := fmt.Sprintf(scratchSource, strconv.Quote(info.Pkg.DeclImportPath(ws.module)))
		off := strings.Index(src, "Props{") + len("Props{")
		sc = &scratchFile{uri: pathToURI(filepath.Join(ws.root, filepath.FromSlash(rel))),
			pos: newLineIndex(src).position(off, s.goplsEnc)}
		ws.scratch[rel] = sc
		ws.gopls.Notify("textDocument/didOpen", map[string]any{
			"textDocument": map[string]any{"uri": sc.uri, "languageId": "go", "version": 1, "text": src},
		})
	}
	b, _ := json.Marshal(map[string]any{"textDocument": map[string]any{"uri": sc.uri}, "position": sc.pos})
	return b
}

// goplsFields asks gopls for the fields of a Props{} literal: its kind 5
// (Field) items with no "." in the label (the rest are deep completions
// and in-scope names). ok is false when gopls didn't answer.
func (s *Server) goplsFields(ctx context.Context, params jsonRaw) (fields []obj, ok bool) {
	raw, err := s.callGopls(ctx, "textDocument/completion", params, false)
	if err != nil || isNull(raw) {
		return nil, false
	}
	_, items := completionItems(raw)
	for _, ri := range items {
		it := decodeObj(ri)
		if it == nil {
			continue
		}
		var kind int
		json.Unmarshal(it["kind"], &kind)
		if kind == kindField && !strings.Contains(it.str("label"), ".") {
			fields = append(fields, it)
		}
	}
	return fields, true
}
