package orbit

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"math"
	"reflect"
	"slices"
	"strconv"
	"strings"
)

// Pass-through attributes (PLAN.md §2.5): a component's Props field of type
// RestAttrs collects the attributes of a call site that match no other
// field, and {...props.Attrs} writes them on an element. Generated code
// builds a RestAttrs with Rest: the static attributes are pre-escaped into
// strings at build time, and each dynamic one keeps a typed value, so a call
// site with only static attributes allocates nothing.

// RestAttrs is a component's pass-through attributes: the attributes of
// the call site that match no Props field, in source order, with the
// caller's scope attribute. Their class is kept apart, so an element that
// spreads them merges it into its own class.
//
// Generated code builds it with Rest; Go code (tests, RenderComponent, a
// hand-built Props) with RestOf. The zero RestAttrs is empty.
type RestAttrs struct {
	// class is the joined class string; tail the pre-escaped static
	// attributes after the last dynamic one; extra more pre-escaped static
	// attributes (a forwarding caller's scope attribute, RestMerge).
	class, tail, extra string
	dyn                []RestAttr
	// m is an Attrs from RestOf or merged by RestMerge, and next a merged
	// RestAttrs. Both come after the attributes above, which win for the
	// same name.
	m    Attrs
	next *RestAttrs
}

// RestAttr is one dynamic attribute of a RestAttrs, with the pre-escaped
// static attributes written before it. Generated code builds it with
// RestString, RestInt, … (typed) or RestAny.
type RestAttr struct {
	pre, name string
	kind      restKind
	s         string // string kinds; the .orbit position for restJSON
	n         uint64 // int, uint and float bits
	v         any    // restAny, restJSON
}

type restKind uint8

const (
	restAny restKind = iota
	restString
	restStringURL // a string in a URL attribute: filtered
	restURL       // a URL: trusted
	restBool
	restInt
	restUint
	restFloat
	restFloat32
	restJSON
)

// Rest builds the pass-through attributes of a call site: class is the
// joined class string, tail the pre-escaped static attributes after the
// last dynamic one (` required data-o-5c7d1e2f`), dyn the dynamic
// attributes in source order. Generated code calls it.
func Rest(class, tail string, dyn ...RestAttr) RestAttrs {
	return RestAttrs{class: class, tail: tail, dyn: dyn}
}

// RestAny is a dynamic attribute whose type codegen didn't know: written
// by Attr's rules when spread.
func RestAny(pre, name string, v any) RestAttr {
	return RestAttr{pre: pre, name: name, kind: restAny, v: v}
}

// RestString is a dynamic string attribute, attribute-escaped when spread.
func RestString(pre, name, s string) RestAttr {
	return RestAttr{pre: pre, name: name, kind: restString, s: s}
}

// RestStringURL is a dynamic string in a URL attribute (href, src, …):
// attribute-escaped, with a javascript: or vbscript: URL replaced with
// about:invalid#orbit.
func RestStringURL(pre, name, s string) RestAttr {
	return RestAttr{pre: pre, name: name, kind: restStringURL, s: s}
}

// RestURL is a dynamic trusted URL attribute: escaped, not filtered.
func RestURL(pre, name string, u URL) RestAttr {
	return RestAttr{pre: pre, name: name, kind: restURL, s: string(u)}
}

// RestBool is a dynamic boolean attribute: bare when true, omitted when
// false.
func RestBool(pre, name string, b bool) RestAttr {
	a := RestAttr{pre: pre, name: name, kind: restBool}
	if b {
		a.n = 1
	}
	return a
}

// RestInt is a dynamic integer attribute.
func RestInt(pre, name string, n int64) RestAttr {
	return RestAttr{pre: pre, name: name, kind: restInt, n: uint64(n)}
}

// RestUint is a dynamic unsigned integer attribute.
func RestUint(pre, name string, n uint64) RestAttr {
	return RestAttr{pre: pre, name: name, kind: restUint, n: n}
}

// RestFloat is a dynamic float64 attribute, written as Float writes it.
func RestFloat(pre, name string, f float64) RestAttr {
	return RestAttr{pre: pre, name: name, kind: restFloat, n: math.Float64bits(f)}
}

// RestFloat32 is a dynamic float32 attribute, written as Float32 writes it.
func RestFloat32(pre, name string, f float32) RestAttr {
	return RestAttr{pre: pre, name: name, kind: restFloat32, n: uint64(math.Float32bits(f))}
}

// RestJSON is a dynamic JSONValue attribute, written single-quoted as
// JSONAttr writes it; pos is the .orbit position of the value, for an
// encoding error.
func RestJSON(pre, name, pos string, v JSONValue) RestAttr {
	return RestAttr{pre: pre, name: name, kind: restJSON, s: pos, v: v.v}
}

// RestOf wraps an Attrs as pass-through attributes, for Go code that
// builds a component's Props. They're spread like Spread spreads them:
// sorted by name, with names checked when written. A "class" entry is the
// class merged into the element's class.
func RestOf(m Attrs) RestAttrs { return RestAttrs{m: m} }

// RestFrom converts the operand of a spread on a component tag
// (<Input {...x} />, alone on the tag) to the component's RestAttrs: an
// Attrs as RestOf does, a RestAttrs as is. Generated code calls it.
func RestFrom[T Attrs | RestAttrs](x T) RestAttrs {
	// A pointer in the interface, so that the value isn't boxed.
	switch p := any(&x).(type) {
	case *Attrs:
		return RestAttrs{m: *p}
	case *RestAttrs:
		return *p
	}
	return RestAttrs{}
}

// RestMerge merges the operand x of a spread on a component tag into own,
// the attributes written on the tag, which win for the same name. Merging
// an Attrs into attributes that have no merged part costs nothing; a
// RestAttrs costs one allocation, except when own holds only static
// attributes that x doesn't have (a forwarding caller's scope attribute).
// Generated code calls it.
func RestMerge[T Attrs | RestAttrs](own RestAttrs, x T) RestAttrs {
	switch p := any(&x).(type) {
	case *Attrs:
		return own.mergeAttrs(*p)
	case *RestAttrs:
		return own.merge(*p)
	}
	return own
}

func (a RestAttrs) mergeAttrs(m Attrs) RestAttrs {
	if len(m) == 0 {
		return a
	}
	if a.m == nil && a.next == nil {
		a.m = m
		return a
	}
	return a.merge(RestAttrs{m: m})
}

func (a RestAttrs) merge(x RestAttrs) RestAttrs {
	if x.empty() {
		return a
	}
	if a.empty() {
		return x
	}
	if a.class == "" && a.dyn == nil && a.m == nil && a.next == nil && x.extra == "" && !x.hasAnyOf(a.tail) {
		x.extra = a.tail
		return x
	}
	if a.next != nil {
		x = a.next.merge(x)
	}
	n := new(RestAttrs)
	*n = x
	a.next = n
	return a
}

func (a *RestAttrs) empty() bool {
	return a.class == "" && a.tail == "" && a.extra == "" && a.dyn == nil && len(a.m) == 0 && a.next == nil
}

// hasAnyOf reports whether a has one of the attributes of the static run s.
func (a *RestAttrs) hasAnyOf(s string) bool {
	for s != "" {
		var name string
		name, _, _, s = nextStatic(s)
		if name != "" && a.Has(name) {
			return true
		}
	}
	return false
}

// Has reports whether name (in any ASCII case) is among the attributes,
// class included. A false or nil value doesn't count.
func (a RestAttrs) Has(name string) bool {
	_, ok := a.Get(name)
	return ok
}

// Get returns the value of the attribute name (in any ASCII case) as it
// would be written, unescaped: "" for a bare attribute, the joined classes
// for class. ok is false when there is no such attribute, or its value is
// false or nil.
func (a RestAttrs) Get(name string) (value string, ok bool) {
	if strings.EqualFold(name, "class") {
		s := a.Class()
		return s, s != ""
	}
	for p := &a; p != nil; p = p.next {
		if v, ok := p.getLocal(name); ok {
			return v, true
		}
	}
	return "", false
}

// Class returns the joined class string: the one written on the tag, or
// else a merged spread's.
func (a RestAttrs) Class() string {
	for p := &a; p != nil; p = p.next {
		if p.class != "" {
			return p.class
		}
		if v, ok := p.m["class"]; ok && v != nil {
			if s := mapClass(v); s != "" {
				return s
			}
		}
	}
	return ""
}

// mapClass is the class string of an Attrs "class" entry.
func mapClass(v any) string {
	switch v := v.(type) {
	case string:
		return v
	case []string:
		return strings.Join(v, " ")
	}
	if s, ok := classValue(v); ok {
		return s
	}
	return fmt.Sprint(v)
}

// getLocal looks name up in p's own attributes and its Attrs, not in the
// merged RestAttrs.
func (p *RestAttrs) getLocal(name string) (string, bool) {
	for i := range p.dyn {
		d := &p.dyn[i]
		if v, ok := staticGet(d.pre, name); ok {
			return v, true
		}
		if strings.EqualFold(d.name, name) {
			if v, ok := d.value(); ok {
				return v, true
			}
		}
	}
	if v, ok := staticGet(p.tail, name); ok {
		return v, true
	}
	if v, ok := staticGet(p.extra, name); ok {
		return v, true
	}
	for k, v := range p.m {
		if strings.EqualFold(k, name) && !strings.EqualFold(k, "class") {
			if s, ok := anyValue(name, v); ok {
				return s, true
			}
		}
	}
	return "", false
}

// hasLocal reports whether p's own attributes or its Attrs set name.
func (p *RestAttrs) hasLocal(name string) bool {
	_, ok := p.getLocal(name)
	return ok
}

// value is d's value as written, unescaped; ok is false when it isn't
// written.
func (d *RestAttr) value() (string, bool) {
	switch d.kind {
	case restString, restURL:
		return d.s, true
	case restStringURL:
		if isUnsafeURL(d.s) {
			return invalidURL, true
		}
		return d.s, true
	case restBool:
		return "", d.n != 0
	case restInt:
		return strconv.FormatInt(int64(d.n), 10), true
	case restUint:
		return strconv.FormatUint(d.n, 10), true
	case restFloat:
		return formatFloat(math.Float64frombits(d.n), 64), true
	case restFloat32:
		return formatFloat(float64(math.Float32frombits(uint32(d.n))), 32), true
	case restJSON:
		b, err := json.Marshal(d.v)
		if err != nil {
			return "", false
		}
		return string(b), true
	}
	return anyValue(d.name, d.v)
}

// formatFloat formats f as Writer.Float and Float32 do.
func formatFloat(f float64, bits int) string {
	var buf bytes.Buffer
	w := NewWriter(&buf)
	if bits == 32 {
		w.Float32(float32(f))
	} else {
		w.Float(f)
	}
	return buf.String()
}

// anyValue is the value Attr writes for v, unescaped.
func anyValue(name string, v any) (string, bool) {
	switch v := v.(type) {
	case nil:
		return "", false
	case bool:
		return "", v
	case JSONValue:
		b, err := json.Marshal(v.v)
		if err != nil {
			return "", false
		}
		return string(b), true
	case URL:
		return string(v), true
	}
	var buf bytes.Buffer
	w := NewWriter(&buf)
	w.Attr(name, v)
	s := buf.String()
	if s == "" {
		return "", false // a named false bool
	}
	_, val, ok := strings.Cut(s, `="`)
	if !ok {
		return "", true // bare
	}
	return html.UnescapeString(strings.TrimSuffix(val, `"`)), true
}

// nextStatic reads the first attribute of a pre-escaped static run
// (` name="value"` or ` name`): its name, its value (escaped), whether it
// has one, and the rest of the run.
func nextStatic(s string) (name, value string, hasValue bool, rest string) {
	i := 0
	for i < len(s) && s[i] == ' ' {
		i++
	}
	j := i
	for j < len(s) && s[j] != ' ' && s[j] != '=' {
		j++
	}
	name = s[i:j]
	if j+1 < len(s) && s[j] == '=' && s[j+1] == '"' {
		end := strings.IndexByte(s[j+2:], '"')
		if end < 0 {
			return name, s[j+2:], true, ""
		}
		return name, s[j+2 : j+2+end], true, s[j+2+end+1:]
	}
	return name, "", false, s[j:]
}

// staticGet looks name up in a pre-escaped static run.
func staticGet(s, name string) (string, bool) {
	for s != "" {
		n, v, hasValue, rest := nextStatic(s)
		if n != "" && strings.EqualFold(n, name) {
			if !hasValue {
				return "", true
			}
			return html.UnescapeString(v), true
		}
		s = rest
	}
	return "", false
}

// SpreadRest writes the pass-through attributes in a, for {...props.Attrs}
// on an HTML element: the call site's attributes in source order (each
// dynamic one by the Attr rules: escaped, URL attributes scheme-filtered,
// false and nil omitted), the caller's scope attribute, then any merged
// spread's attributes that the call site doesn't set. With withClass, it
// also writes a class attribute holding their class (none when it's
// empty); generated code passes false when the element has its own class,
// and writes the class there with RestClass.
func (w *Writer) SpreadRest(a *RestAttrs, withClass bool) {
	if w.err != nil {
		return
	}
	for i := range a.dyn {
		d := &a.dyn[i]
		w.buf.WriteString(d.pre)
		w.restDyn(d)
	}
	w.buf.WriteString(a.tail)
	w.buf.WriteString(a.extra)
	if a.m != nil || a.next != nil {
		w.restMerged(a)
	}
	if withClass {
		if s := a.Class(); hasClass(s) {
			w.ClassStart()
			w.Class(s)
			w.ClassEnd()
		}
	}
}

// RestClass writes the class of a inside an element's class value, as
// Class does: each class preceded by a space, nothing when it's empty.
func (w *Writer) RestClass(a *RestAttrs) {
	if a.class != "" || a.m == nil && a.next == nil {
		w.Class(a.class)
		return
	}
	w.Class(a.Class())
}

// restDyn writes one dynamic attribute.
func (w *Writer) restDyn(d *RestAttr) {
	switch d.kind {
	case restString, restURL:
		w.attrValue(d.name, d.s, false)
	case restStringURL:
		w.attrValue(d.name, d.s, true)
	case restBool:
		if d.n != 0 {
			w.buf.WriteByte(' ')
			w.buf.WriteString(d.name)
		}
	case restInt:
		w.attrOpen(d.name)
		w.Int(int64(d.n))
		w.buf.WriteByte('"')
	case restUint:
		w.attrOpen(d.name)
		w.Uint(d.n)
		w.buf.WriteByte('"')
	case restFloat:
		w.attrOpen(d.name)
		w.Float(math.Float64frombits(d.n))
		w.buf.WriteByte('"')
	case restFloat32:
		w.attrOpen(d.name)
		w.Float32(math.Float32frombits(uint32(d.n)))
		w.buf.WriteByte('"')
	case restJSON:
		w.buf.WriteByte(' ')
		w.buf.WriteString(d.name)
		w.buf.WriteString(`='`)
		w.JSONAttr(d.s, JSONValue{v: d.v})
		w.buf.WriteByte('\'')
	default:
		w.attr(d.name, d.v, isURLAttr(d.name))
	}
}

// restMerged writes the merged parts of a: its Attrs, then each merged
// RestAttrs, skipping the names set before them (and class, which is
// written apart).
func (w *Writer) restMerged(a *RestAttrs) {
	w.restMap(a, a, a.m)
	for p := a.next; p != nil && w.err == nil; p = p.next {
		for i := range p.dyn {
			d := &p.dyn[i]
			w.restStatics(a, p, d.pre)
			if !shadowed(a, p, d.name) {
				w.restDyn(d)
			}
		}
		w.restStatics(a, p, p.tail)
		w.restStatics(a, p, p.extra)
		w.restMap(a, p, p.m)
	}
}

// shadowed reports whether name is set by one of the parts from top down
// to (not including) p.
func shadowed(top, p *RestAttrs, name string) bool {
	for q := top; q != nil && q != p; q = q.next {
		if q.hasLocal(name) {
			return true
		}
	}
	return false
}

// restStatics writes the attributes of the static run s of part p that
// aren't shadowed.
func (w *Writer) restStatics(top, p *RestAttrs, s string) {
	if top == p {
		w.buf.WriteString(s)
		return
	}
	for s != "" {
		start := s
		name, _, _, rest := nextStatic(s)
		if name != "" && !shadowed(top, p, name) {
			w.buf.WriteString(start[:len(start)-len(rest)])
		}
		s = rest
	}
}

// restMap writes the entries of m (part p's Attrs) sorted by name, minus
// class and the names set before them (by p's own attributes too).
func (w *Writer) restMap(top, p *RestAttrs, m Attrs) {
	if len(m) == 0 {
		return
	}
	var arr [16]string
	names := arr[:0]
	for name := range m {
		if !validAttrName(name) {
			w.setErr(fmt.Errorf("orbit: invalid attribute name %q in spread", name))
			return
		}
		if strings.EqualFold(name, "class") {
			continue
		}
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		if shadowed(top, p, name) || p.ownSets(name) {
			continue
		}
		w.attr(name, m[name], isURLAttr(name))
	}
}

// ownSets reports whether p's own attributes (not its Attrs) set name.
func (p *RestAttrs) ownSets(name string) bool {
	if p.m == nil {
		return false
	}
	q := *p
	q.m, q.next = nil, nil
	return q.hasLocal(name)
}

// MarshalJSON encodes the attributes as a JSON object of name → value as
// written (unescaped), or true for a bare attribute, so a Props with a
// RestAttrs passes to a server:defer component. A URL value loses its
// trust: it's filtered after the round trip.
func (a RestAttrs) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	first := true
	add := func(name, value string, bare bool) {
		if !first {
			b.WriteByte(',')
		}
		first = false
		k, _ := json.Marshal(name)
		b.Write(k)
		b.WriteByte(':')
		if bare {
			b.WriteString("true")
			return
		}
		v, _ := json.Marshal(value)
		b.Write(v)
	}
	if c := a.Class(); hasClass(c) {
		add("class", c, false)
	}
	seen := map[string]bool{"class": true}
	var statics func(s string)
	statics = func(s string) {
		for s != "" {
			name, v, hasValue, rest := nextStatic(s)
			s = rest
			if name == "" || seen[strings.ToLower(name)] {
				continue
			}
			seen[strings.ToLower(name)] = true
			add(name, html.UnescapeString(v), !hasValue)
		}
	}
	for p := &a; p != nil; p = p.next {
		for i := range p.dyn {
			d := &p.dyn[i]
			statics(d.pre)
			if seen[strings.ToLower(d.name)] {
				continue
			}
			v, ok := d.value()
			if !ok {
				continue
			}
			seen[strings.ToLower(d.name)] = true
			add(d.name, v, d.bare())
		}
		statics(p.tail)
		statics(p.extra)
		names := make([]string, 0, len(p.m))
		for name := range p.m {
			names = append(names, name)
		}
		slices.Sort(names)
		for _, name := range names {
			if !validAttrName(name) {
				return nil, fmt.Errorf("orbit: invalid attribute name %q in RestAttrs", name)
			}
			if seen[strings.ToLower(name)] {
				continue
			}
			v, ok := anyValue(name, p.m[name])
			if !ok {
				continue
			}
			seen[strings.ToLower(name)] = true
			add(name, v, isBare(p.m[name]))
		}
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

// bare reports whether d is written as a bare attribute.
func (d *RestAttr) bare() bool {
	switch d.kind {
	case restBool:
		return true
	case restAny:
		return isBare(d.v)
	}
	return false
}

// isBare reports whether Attr writes v as a bare attribute: a true bool,
// or a named bool that is true.
func isBare(v any) bool {
	if b, ok := v.(bool); ok {
		return b
	}
	rv := reflect.ValueOf(v)
	return rv.IsValid() && rv.Kind() == reflect.Bool && rv.Bool()
}

// UnmarshalJSON decodes the object MarshalJSON writes: each string value
// becomes a static attribute (escaped), true a bare one; false and null
// are dropped. Names are checked as Spread checks them, and URL attributes
// are scheme-filtered.
func (a *RestAttrs) UnmarshalJSON(data []byte) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	if tok == nil {
		*a = RestAttrs{}
		return nil
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return errors.New("orbit: RestAttrs must be a JSON object")
	}
	var out RestAttrs
	var tail bytes.Buffer
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		name, _ := tok.(string)
		if !validAttrName(name) {
			return fmt.Errorf("orbit: invalid attribute name %q in RestAttrs", name)
		}
		tok, err = dec.Token()
		if err != nil {
			return err
		}
		switch v := tok.(type) {
		case string:
			if strings.EqualFold(name, "class") {
				out.class = v
				continue
			}
			if isURLAttr(name) && isUnsafeURL(v) {
				v = invalidURL // a URL's trust doesn't survive the round trip
			}
			tail.WriteByte(' ')
			tail.WriteString(name)
			tail.WriteString(`="`)
			escapeString(&tail, v)
			tail.WriteByte('"')
		case bool:
			if v {
				tail.WriteByte(' ')
				tail.WriteString(name)
			}
		case nil:
		default:
			return fmt.Errorf("orbit: RestAttrs attribute %q must be a string, true, false or null", name)
		}
	}
	if _, err := dec.Token(); err != nil {
		return err
	}
	out.tail = tail.String()
	*a = out
	return nil
}
