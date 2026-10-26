package markdown

import (
	"errors"
	"fmt"
	"io"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"go.yaml.in/yaml/v3"

	"github.com/orbit-z/orbit/internal/compiler"
)

// The frontmatter (PLAN.md §2.4): YAML between --- lines at the top of the
// file, parsed into yaml.Node so every key and value has a position, then
// into a Value tree with inferred Go types.

// Kind is the type of a frontmatter value.
type Kind uint8

const (
	KindString Kind = iota
	KindBool
	KindInt
	KindFloat
	KindTime
	KindList
	KindMap
)

// Value is one frontmatter value.
type Value struct {
	Kind Kind
	// Pos is the offset of the value in the .md file.
	Pos int
	// Raw is a scalar's value as written (unquoted); Quoted is set for a
	// quoted or block scalar, which is always a string.
	Raw    string
	Quoted bool
	Bool   bool
	Int    int64
	Float  float64
	Time   time.Time
	Items  []*Value // KindList
	Fields []*Field // KindMap, in source order
}

// Field is one key of a map.
type Field struct {
	// Key is the key as written, KeyPos its offset in the file, Name its
	// Go field name (compiler.FieldName).
	Key    string
	KeyPos int
	Name   string
	Value  *Value
}

// splitFrontmatter finds the frontmatter of src: a first line "---", then
// YAML up to a line "---". It returns the YAML's span (start == end when
// there is none), the span of the whole frontmatter block (both fences and
// their line breaks), and an error message at the opening fence if it isn't
// closed.
func splitFrontmatter(src string) (yamlSpan, block compiler.Span, unclosed bool) {
	first, rest, _ := cutLine(src)
	if strings.TrimRight(first, " \t\r") != "---" {
		return
	}
	start := len(src) - len(rest)
	off := start
	for off < len(src) {
		line, next, _ := cutLine(src[off:])
		if strings.TrimRight(line, " \t\r") == "---" {
			end := len(src) - len(next)
			return compiler.Span{Start: start, End: off}, compiler.Span{Start: 0, End: end}, false
		}
		off = len(src) - len(next)
	}
	return compiler.Span{Start: start, End: len(src)}, compiler.Span{Start: 0, End: len(src)}, true
}

// cutLine splits s after its first line break; ok is false when s has
// none (line is then all of s).
func cutLine(s string) (line, rest string, ok bool) {
	i := strings.IndexByte(s, '\n')
	if i < 0 {
		return s, "", false
	}
	return s[:i], s[i+1:], true
}

// fmParser converts the YAML of one file.
type fmParser struct {
	src   string
	lines *compiler.LineIndex
	// line0 is the file line of the YAML's first line, minus one.
	line0 int
	diags []compiler.Diagnostic
}

func (p *fmParser) errorf(off int, format string, args ...any) {
	p.diags = append(p.diags, compiler.Diagnostic{Severity: compiler.SeverityError,
		Span: compiler.Span{Start: off, End: off}, Message: fmt.Sprintf(format, args...)})
}

// offset converts a yaml.Node line and column (1-based, in characters) to
// a byte offset in the file.
func (p *fmParser) offset(line, col int) int {
	start := p.lines.Offset(compiler.Position{Line: p.line0 + line, Col: 1})
	off := start
	for i := 1; i < col && off < len(p.src) && p.src[off] != '\n'; i++ {
		_, size := utf8.DecodeRuneInString(p.src[off:])
		off += size
	}
	return off
}

func (p *fmParser) pos(n *yaml.Node) int { return p.offset(n.Line, n.Column) }

var yamlLineErr = regexp.MustCompile(`^yaml: line (\d+): (.*)$`)

// parse parses the YAML at span into top-level fields.
func (p *fmParser) parse(span compiler.Span) []*Field {
	p.line0 = p.lines.Position(span.Start).Line - 1
	dec := yaml.NewDecoder(strings.NewReader(p.src[span.Start:span.End]))
	var doc yaml.Node
	if err := dec.Decode(&doc); err != nil {
		if errors.Is(err, io.EOF) {
			return nil // empty
		}
		p.yamlError(err, span)
		return nil
	}
	var more yaml.Node
	if err := dec.Decode(&more); !errors.Is(err, io.EOF) {
		if err != nil {
			p.yamlError(err, span)
		} else {
			p.errorf(p.pos(&more), "the frontmatter has more than one YAML document; keep one")
		}
		return nil
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		return nil
	}
	root := doc.Content[0]
	if root.Kind == yaml.ScalarNode && root.ShortTag() == "!!null" {
		return nil // only comments, or ~
	}
	if root.Kind != yaml.MappingNode {
		p.errorf(p.pos(root), "the frontmatter must be YAML keys and values (key: value)")
		return nil
	}
	if !p.plain(root) {
		return nil
	}
	return p.fields(root, "")
}

// yamlError reports a YAML syntax error at its line.
func (p *fmParser) yamlError(err error, span compiler.Span) {
	msg := err.Error()
	off := span.Start
	if m := yamlLineErr.FindStringSubmatch(msg); m != nil {
		line, _ := strconv.Atoi(m[1])
		off = p.offset(line, 1)
		msg = m[2]
	} else {
		msg = strings.TrimPrefix(msg, "yaml: ")
	}
	p.errorf(off, "invalid YAML in the frontmatter: %s", msg)
}

// plain reports an anchor, alias or tag on n (PLAN.md §2.4: errors, the
// simplest rule) and returns false if it has one.
func (p *fmParser) plain(n *yaml.Node) bool {
	switch {
	case n.Kind == yaml.AliasNode:
		p.errorf(p.pos(n), "YAML aliases (*%s) aren't supported in frontmatter; repeat the value", n.Value)
		return false
	case n.Anchor != "":
		p.errorf(p.pos(n), "YAML anchors (&%s) aren't supported in frontmatter", n.Anchor)
		return false
	case n.Style&yaml.TaggedStyle != 0:
		p.errorf(p.pos(n), "YAML tags (%s) aren't supported in frontmatter", n.Tag)
		return false
	}
	return true
}

var keyRE = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]*$`)

// fields converts a mapping node. prefix is the dotted key path of the
// map, for messages ("" at the top level).
func (p *fmParser) fields(n *yaml.Node, prefix string) []*Field {
	var out []*Field
	byName := map[string]*Field{}
	for i := 0; i+1 < len(n.Content); i += 2 {
		k, v := n.Content[i], n.Content[i+1]
		if !p.plain(k) {
			continue
		}
		if k.Kind != yaml.ScalarNode {
			p.errorf(p.pos(k), "a frontmatter key must be a name, such as title")
			continue
		}
		key := k.Value
		kpos := p.pos(k)
		if !keyRE.MatchString(key) {
			p.errorf(kpos, "frontmatter key %q can't become a Go field name; use letters, digits, - and _, starting with a letter", key)
			continue
		}
		name := compiler.FieldName(key)
		if prev := byName[name]; prev != nil {
			if prev.Key == key {
				p.errorf(kpos, "frontmatter key %q is repeated; keep one", prefix+key)
			} else {
				p.errorf(kpos, "frontmatter keys %q and %q both become %s; rename one", prefix+prev.Key, prefix+key, name)
			}
			continue
		}
		val := p.value(v, prefix+key)
		if val == nil {
			continue
		}
		f := &Field{Key: key, KeyPos: kpos, Name: name, Value: val}
		byName[name] = f
		out = append(out, f)
	}
	return out
}

// value converts a value node; nil after an error.
func (p *fmParser) value(n *yaml.Node, key string) *Value {
	if !p.plain(n) {
		return nil
	}
	pos := p.pos(n)
	v := &Value{Pos: pos}
	switch n.Kind {
	case yaml.MappingNode:
		v.Kind = KindMap
		v.Fields = p.fields(n, key+".")
		return v
	case yaml.SequenceNode:
		v.Kind = KindList
		ok := true
		for i, item := range n.Content {
			iv := p.value(item, key+"["+strconv.Itoa(i)+"]")
			if iv == nil {
				ok = false
				continue
			}
			v.Items = append(v.Items, iv)
		}
		if !ok {
			return nil
		}
		if len(v.Items) > 0 {
			t0 := goType(v.Items[0])
			for _, it := range v.Items[1:] {
				if t := goType(it); t != t0 {
					p.errorf(pos, "frontmatter %s: a list's items must all have the same type (%s and %s)", key, typeWord(t0), typeWord(t))
					return nil
				}
			}
		}
		return v
	case yaml.ScalarNode:
	default:
		p.errorf(pos, "frontmatter %s: unsupported YAML value", key)
		return nil
	}
	v.Raw = n.Value
	v.Quoted = n.Style&(yaml.DoubleQuotedStyle|yaml.SingleQuotedStyle|yaml.LiteralStyle|yaml.FoldedStyle) != 0
	switch n.ShortTag() {
	case "!!null":
		p.errorf(pos, "frontmatter key %q has no value; delete the line or give it a value", key)
		return nil
	case "!!bool":
		v.Kind = KindBool
		if err := n.Decode(&v.Bool); err != nil {
			p.errorf(pos, "frontmatter %s: %s", key, cleanYAMLErr(err))
			return nil
		}
	case "!!int":
		v.Kind = KindInt
		if err := n.Decode(&v.Int); err != nil {
			var u uint64
			if n.Decode(&u) == nil {
				p.errorf(pos, "frontmatter %s: %s overflows int", key, n.Value)
			} else {
				p.errorf(pos, "frontmatter %s: %s", key, cleanYAMLErr(err))
			}
			return nil
		}
	case "!!float":
		v.Kind = KindFloat
		if err := n.Decode(&v.Float); err != nil {
			p.errorf(pos, "frontmatter %s: %s", key, cleanYAMLErr(err))
			return nil
		}
		if math.IsInf(v.Float, 0) || math.IsNaN(v.Float) {
			p.errorf(pos, "frontmatter %s: %s isn't a finite number", key, n.Value)
			return nil
		}
	case "!!timestamp":
		v.Kind = KindTime
		if err := n.Decode(&v.Time); err != nil {
			p.errorf(pos, "frontmatter %s: %s", key, cleanYAMLErr(err))
			return nil
		}
	default: // !!str, !!binary can't happen without a tag
		v.Kind = KindString
	}
	return v
}

func cleanYAMLErr(err error) string {
	return strings.TrimPrefix(strings.TrimPrefix(err.Error(), "yaml: "), "unmarshal errors:\n  ")
}

// goType is the Go type of a value in the generated Frontmatter (PLAN.md
// §2.4): an empty list is a []string.
func goType(v *Value) string {
	switch v.Kind {
	case KindBool:
		return "bool"
	case KindInt:
		return "int"
	case KindFloat:
		return "float64"
	case KindTime:
		return "time.Time"
	case KindList:
		if len(v.Items) == 0 {
			return "[]string"
		}
		return "[]" + goType(v.Items[0])
	case KindMap:
		var b strings.Builder
		b.WriteString("struct{")
		for i, f := range v.Fields {
			if i > 0 {
				b.WriteString("; ")
			}
			b.WriteString(f.Name + " " + goType(f.Value))
		}
		b.WriteString("}")
		return b.String()
	}
	return "string"
}

// typeWord names a Go type in a message about a list: "string", "a map".
func typeWord(t string) string {
	if strings.HasPrefix(t, "struct{") {
		return "map"
	}
	return t
}

// goLit is the Go expression of a value of type goType(v). In a list, the
// items of a map-typed list elide their type.
func goLit(v *Value, elide bool) string {
	switch v.Kind {
	case KindBool:
		return strconv.FormatBool(v.Bool)
	case KindInt:
		return strconv.FormatInt(v.Int, 10)
	case KindFloat:
		return floatLit(v.Float)
	case KindTime:
		return timeLit(v.Time, "time")
	case KindList:
		var b strings.Builder
		b.WriteString(goType(v) + "{")
		for i, it := range v.Items {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(goLit(it, true))
		}
		b.WriteString("}")
		return b.String()
	case KindMap:
		var b strings.Builder
		if !elide {
			b.WriteString(goType(v))
		}
		b.WriteString("{")
		for i, f := range v.Fields {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(f.Name + ": " + goLit(f.Value, false))
		}
		b.WriteString("}")
		return b.String()
	}
	return strconv.Quote(v.Raw)
}

// floatLit formats a float so that Go reads it back as the same float
// constant.
func floatLit(f float64) string {
	s := strconv.FormatFloat(f, 'g', -1, 64)
	if !strings.ContainsAny(s, ".eEn") {
		s += ".0"
	}
	return s
}

// timeLit is a time.Date call for t, with the time package named pkg.
func timeLit(t time.Time, pkg string) string {
	loc := pkg + ".UTC"
	if _, off := t.Zone(); off != 0 {
		loc = pkg + ".FixedZone(\"\", " + strconv.Itoa(off) + ")"
	}
	return fmt.Sprintf("%s.Date(%d, %d, %d, %d, %d, %d, %d, %s)", pkg,
		t.Year(), int(t.Month()), t.Day(), t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), loc)
}

// usesTime reports whether a value's type or literal mentions time.
func usesTime(v *Value) bool {
	switch v.Kind {
	case KindTime:
		return true
	case KindList:
		if len(v.Items) > 0 {
			return usesTime(v.Items[0])
		}
	case KindMap:
		for _, f := range v.Fields {
			if usesTime(f.Value) {
				return true
			}
		}
	}
	return false
}

// describe is a value as an error message shows it: a quoted string, a
// scalar as written, "a list" or "a map".
func describe(v *Value) string {
	switch v.Kind {
	case KindList:
		return "a list"
	case KindMap:
		return "a map"
	case KindString:
		return strconv.Quote(v.Raw)
	}
	return v.Raw
}
