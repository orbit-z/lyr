package compiler

// The .d.ts emitter of the typed server→client bridge (PLAN.md §5c "Where
// the types live"): lyr_gen/types/go.d.ts declares every Go named type
// reached (module "lyr:types"), client.d.ts the untyped "lyr:client"
// runtime, and one module per .lyr file with data declares its data
// contract.

import (
	"encoding/json"
	"slices"
	"strconv"
	"strings"
	"unicode"
)

// UnitTypes is one .lyr file's data contract.
type UnitTypes struct {
	// Path is the project-relative .lyr path:
	// "src/components/LikeButton.lyr".
	Path string
	// Data are the data-* keys by dataset name, sorted by Name.
	Data []TSKey
	// JSON are the set:json blocks by static id, sorted by Name.
	JSON []TSKey
	// Decls are every named type its keys reach.
	Decls []TSDecl
}

// TSKey is one key of a unit's data contract.
type TSKey struct {
	Name string
	// Types are the union members (TS with refs), deduplicated, in order.
	Types []string
	// Sites are the doc comment text per site, e.g.
	// "data-post (LikeButton.lyr:9:14)".
	Sites []string
	// Spans are the attribute names of the sites in the .lyr file
	// (byte offsets), in Sites order.
	Spans []Span
}

// TSFile is one emitted declaration file.
type TSFile struct {
	// Path is module-relative, under lyr_gen/types/.
	Path    string
	Content []byte
	// Sources is the side table of the language server (PLAN.md §9b):
	// the Go declaration or attribute each declaration line stands for,
	// by 0-based line. Only lines with a known source are listed.
	Sources map[int]TSSource
}

// tsTypesDir is where the declaration files go, relative to the module root.
const tsTypesDir = GenDir + "/types"

const clientDTS = GenHeader + `
declare module "lyr:client" {
  export function data<T = unknown>(el: Element, key: string): T;
  export function json<T = unknown>(id: string): T;
}
`

// EmitTypes returns lyr_gen/types/go.d.ts and lyr_gen/types/client.d.ts
// (always, even with no units or types), and
// lyr_gen/types/<Path>.d.ts for each unit with at least one Data or JSON
// key. The output is deterministic and sorted by Path.
func EmitTypes(units []*UnitTypes) []TSFile {
	var decls []TSDecl
	seen := map[string]bool{}
	for _, u := range units {
		for _, d := range u.Decls {
			if !seen[d.Ref] {
				seen[d.Ref] = true
				decls = append(decls, d)
			}
		}
	}
	names := newTSNames(decls)
	goDTS, goSrc := names.goDTS()
	files := []TSFile{
		{Path: tsTypesDir + "/client.d.ts", Content: []byte(clientDTS)},
		{Path: tsTypesDir + "/go.d.ts", Content: goDTS, Sources: goSrc},
	}
	for _, u := range units {
		if len(u.Data) == 0 && len(u.JSON) == 0 {
			continue
		}
		content, src := names.unitDTS(u)
		files = append(files, TSFile{Path: tsTypesDir + "/" + u.Path + ".d.ts", Content: content, Sources: src})
	}
	slices.SortFunc(files, func(a, b TSFile) int { return strings.Compare(a.Path, b.Path) })
	return files
}

// tsNames assigns TS names to namespaces and declarations.
type tsNames struct {
	ns    map[string]string // NS.Key → namespace name
	decls map[string]*tsNamed
	// order lists the declarations by namespace name, then TS name.
	order []*tsNamed
}

type tsNamed struct {
	d    TSDecl
	ns   string // namespace name
	name string // TS name in it
}

func newTSNames(decls []TSDecl) *tsNames {
	n := &tsNames{ns: map[string]string{}, decls: map[string]*tsNamed{}}
	// Namespaces: one per Key, named after Base; same Base for different
	// Keys gets the shortest distinguishing suffix of their Paths.
	var spaces []tsNS
	for _, d := range decls {
		if _, ok := n.ns[d.NS.Key]; !ok {
			n.ns[d.NS.Key] = ""
			spaces = append(spaces, d.NS)
		}
	}
	slices.SortFunc(spaces, func(a, b tsNS) int { return strings.Compare(a.Key, b.Key) })
	groups := map[string][]tsNS{}
	for _, s := range spaces {
		b := tsSanitize(s.Base)
		groups[b] = append(groups[b], s)
	}
	type cand struct{ key, name string }
	var cands []cand
	for base, g := range groups {
		if len(g) == 1 {
			cands = append(cands, cand{g[0].Key, base})
			continue
		}
		for i, s := range g {
			cands = append(cands, cand{s.Key, tsDistinguish(g, i)})
		}
	}
	for i := range cands {
		cands[i].name = tsIdent(cands[i].name)
	}
	slices.SortFunc(cands, func(a, b cand) int {
		if c := strings.Compare(a.name, b.name); c != 0 {
			return c
		}
		return strings.Compare(a.key, b.key)
	})
	taken := map[string]bool{}
	for _, c := range cands {
		n.ns[c.key] = tsUnique(c.name, taken)
	}
	// Declarations, by Ref (package-level types sort before local ones of
	// the same name), unique within their namespace.
	sorted := slices.Clone(decls)
	slices.SortFunc(sorted, func(a, b TSDecl) int { return strings.Compare(a.Ref, b.Ref) })
	takenIn := map[string]map[string]bool{}
	for _, d := range sorted {
		ns := n.ns[d.NS.Key]
		if takenIn[ns] == nil {
			takenIn[ns] = map[string]bool{}
		}
		t := &tsNamed{d: d, ns: ns, name: tsUnique(tsIdent(d.Name), takenIn[ns])}
		n.decls[d.Ref] = t
		n.order = append(n.order, t)
	}
	slices.SortFunc(n.order, func(a, b *tsNamed) int {
		if c := strings.Compare(a.ns, b.ns); c != 0 {
			return c
		}
		return strings.Compare(a.name, b.name)
	})
	return n
}

// tsUnique returns name, or name_2, name_3, … when taken, and takes it.
func tsUnique(name string, taken map[string]bool) string {
	u := name
	for i := 2; taken[u]; i++ {
		u = name + "_" + strconv.Itoa(i)
	}
	taken[u] = true
	return u
}

// tsDistinguish returns the shortest suffix of g[i].Path's segments (at
// least two), joined with "_", that no other member of g shares.
func tsDistinguish(g []tsNS, i int) string {
	segs := func(p string) []string {
		s := strings.Split(p, "/")
		for j := range s {
			s[j] = tsSanitize(s[j])
		}
		return s
	}
	suffix := func(s []string, k int) string { return strings.Join(s[max(0, len(s)-k):], "_") }
	mine := segs(g[i].Path)
	others := make([][]string, 0, len(g)-1)
	for j, s := range g {
		if j != i {
			others = append(others, segs(s.Path))
		}
	}
	for k := 2; k <= len(mine); k++ {
		c := suffix(mine, k)
		if !slices.ContainsFunc(others, func(o []string) bool { return suffix(o, k) == c }) {
			return c
		}
	}
	return suffix(mine, len(mine)) // tsUnique separates identical paths
}

// tsSanitize makes s an identifier: characters other than letters, digits,
// "_" and "$" become "_", and a leading digit gets a "_" prefix.
func tsSanitize(s string) string {
	var b strings.Builder
	for i, r := range s {
		switch {
		case r == '_' || r == '$' || unicode.IsLetter(r):
		case unicode.IsDigit(r):
			if i == 0 {
				b.WriteByte('_')
			}
		default:
			r = '_'
		}
		b.WriteRune(r)
	}
	if b.Len() == 0 {
		return "_"
	}
	return b.String()
}

// render replaces the refs in text with TS names: bare for a declaration
// of namespace key from (inside that namespace), qualified otherwise, with
// the namespace added to used. A ref without a declaration (and its type
// arguments) becomes unknown.
func (n *tsNames) render(text, from string, used map[string]bool) string {
	if !strings.Contains(text, "\x00") {
		return text
	}
	var b strings.Builder
	for {
		i := strings.IndexByte(text, 0)
		if i < 0 {
			b.WriteString(text)
			return b.String()
		}
		b.WriteString(text[:i])
		text = text[i+1:]
		j := strings.IndexByte(text, 0)
		if j < 0 {
			return b.String() // malformed
		}
		ref := text[:j]
		text = text[j+1:]
		t := n.decls[ref]
		switch {
		case t == nil:
			b.WriteString("unknown")
			if strings.HasPrefix(text, "<") {
				depth := 0
				k := 0
				for k = 0; k < len(text); k++ {
					if text[k] == '<' {
						depth++
					} else if text[k] == '>' {
						if depth--; depth == 0 {
							break
						}
					}
				}
				text = text[min(k+1, len(text)):]
			}
		case t.d.NS.Key == from:
			b.WriteString(t.name)
		default:
			if used != nil {
				used[t.ns] = true
			}
			b.WriteString(t.ns + "." + t.name)
		}
	}
}

// dtsBuilder writes a .d.ts file and its side table.
type dtsBuilder struct {
	strings.Builder
	line int // 0-based line of the next byte
	src  map[int]TSSource
}

func (b *dtsBuilder) WriteString(s string) {
	b.line += strings.Count(s, "\n")
	b.Builder.WriteString(s)
}

func (b *dtsBuilder) WriteByte(c byte) error {
	if c == '\n' {
		b.line++
	}
	return b.Builder.WriteByte(c)
}

// mark records that the current line stands for src.
func (b *dtsBuilder) mark(src TSSource) {
	if src.File == "" {
		return
	}
	if b.src == nil {
		b.src = map[int]TSSource{}
	}
	b.src[b.line] = src
}

// goDTS returns go.d.ts and its side table.
func (n *tsNames) goDTS() ([]byte, map[int]TSSource) {
	var b dtsBuilder
	b.WriteString(GenHeader + "\n")
	if len(n.order) == 0 {
		b.WriteString("declare module \"lyr:types\" {}\n")
		return []byte(b.String()), nil
	}
	b.WriteString("declare module \"lyr:types\" {\n")
	for i, t := range n.order {
		if i == 0 || n.order[i-1].ns != t.ns {
			if i > 0 {
				b.WriteString("  }\n")
			}
			b.WriteString("  export namespace " + t.ns + " {\n")
		}
		d := &t.d
		from := d.NS.Key
		b.WriteString("    /** " + tsComment(d.GoPath) + " */\n")
		params := ""
		if len(d.TypeParams) > 0 {
			ps := make([]string, len(d.TypeParams))
			for i, p := range d.TypeParams {
				ps[i] = tsIdent(p)
			}
			params = "<" + strings.Join(ps, ", ") + ">"
		}
		b.mark(d.Source)
		if !d.Interface {
			b.WriteString("    export type " + t.name + params + " = " + n.render(d.Alias, from, nil) + ";\n")
			continue
		}
		if len(d.Fields) == 0 {
			b.WriteString("    export interface " + t.name + params + " {}\n")
			continue
		}
		b.WriteString("    export interface " + t.name + params + " {\n")
		for _, f := range d.Fields {
			b.mark(f.Source)
			b.WriteString("      " + tsPropName(f.Name))
			if f.Optional {
				b.WriteByte('?')
			}
			b.WriteString(": " + n.render(f.Type, from, nil) + ";\n")
		}
		b.WriteString("    }\n")
	}
	b.WriteString("  }\n}\n")
	return []byte(b.String()), b.src
}

// unitDTS returns the per-unit module of u and its side table: a key's
// line stands for its first site's attribute, and the interface and
// function lines for the first key's.
func (n *tsNames) unitDTS(u *UnitTypes) ([]byte, map[int]TSSource) {
	used := map[string]bool{}
	var body dtsBuilder
	site := func(k TSKey) TSSource {
		if len(k.Spans) == 0 {
			return TSSource{}
		}
		return TSSource{File: u.Path, Lyr: true, Start: k.Spans[0].Start, End: k.Spans[0].End}
	}
	section := func(iface, doc, fn, sig string, keys []TSKey) {
		body.WriteString("\n/** " + tsComment(doc) + " */\n")
		first := TSSource{}
		if len(keys) > 0 {
			first = site(keys[0])
		}
		body.mark(first)
		if len(keys) == 0 {
			body.WriteString("export interface " + iface + " {}\n")
		} else {
			body.WriteString("export interface " + iface + " {\n")
			for _, k := range keys {
				body.WriteString("  /** " + tsComment(strings.Join(k.Sites, ", ")) + " */\n")
				body.mark(site(k))
				body.WriteString("  " + tsPropName(k.Name) + ": " + n.keyType(k, used) + ";\n")
			}
			body.WriteString("}\n")
		}
		lits := "never"
		if len(keys) > 0 {
			ls := make([]string, len(keys))
			for i, k := range keys {
				ls[i] = tsString(k.Name)
			}
			lits = strings.Join(ls, " | ")
		}
		body.mark(first)
		body.WriteString("export declare function " + fn + "<K extends " + lits + ">(" + sig + "): " + iface + "[K];\n")
	}
	section("Data", "The lyr.JSON data-* attributes written by "+u.Path+", by dataset name.", "data", "el: Element, key: K", u.Data)
	section("JSONBlocks", "The set:json blocks written by "+u.Path+", by id.", "json", "id: K", u.JSON)
	var b strings.Builder
	b.WriteString(GenHeader + "\n")
	shift := 1 + strings.Count(GenHeader, "\n")
	if len(used) > 0 {
		ns := make([]string, 0, len(used))
		for s := range used {
			ns = append(ns, s)
		}
		slices.Sort(ns)
		b.WriteString("import type { " + strings.Join(ns, ", ") + " } from \"lyr:types\";\n")
		shift++
	}
	b.WriteString(body.String())
	var src map[int]TSSource
	if body.src != nil {
		src = make(map[int]TSSource, len(body.src))
		for l, s := range body.src {
			src[l+shift] = s
		}
	}
	return []byte(b.String()), src
}

// keyType returns the union of a key's types; unknown absorbs the rest.
func (n *tsNames) keyType(k TSKey, used map[string]bool) string {
	if len(k.Types) == 0 || slices.Contains(k.Types, "unknown") {
		return "unknown"
	}
	parts := make([]string, len(k.Types))
	for i, t := range k.Types {
		parts[i] = n.render(t, "", used)
	}
	return strings.Join(parts, " | ")
}

// tsPropName returns a property name: bare when it's an identifier,
// quoted otherwise.
func tsPropName(name string) string {
	if name == "" {
		return `""`
	}
	for i, r := range name {
		if !(r == '_' || r == '$' || r < 0x80 && (r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || i > 0 && r >= '0' && r <= '9')) {
			return tsString(name)
		}
	}
	return name
}

// tsString returns s as a TS string literal.
func tsString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// tsComment makes s safe inside a /** */ comment.
func tsComment(s string) string { return strings.ReplaceAll(s, "*/", "*\\/") }
