package orbit

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Ordered spreads (PLAN.md §2.9): an AttrList is spread in list order,
// where an Attrs map is spread sorted by name. A builds each entry and
// keeps common value types unboxed, so a list built in a page costs no
// allocation.

// AttrList is an ordered list of attributes for a spread ({...list}):
// written in list order, each value by Attr's rules. Build its entries
// with A. It's a plain slice: append, range and the slices package work.
//
// Use it instead of Attrs when the order matters (byte parity with Astro,
// which keeps an object's insertion order, or a test asserting the HTML).
// A name that appears twice (in any ASCII case) is a render error.
type AttrList []AttrPair

// AttrPair is one attribute of an AttrList, built with A.
type AttrPair struct {
	name string
	kind pairKind
	s    string // string kinds
	n    uint64 // bool, int, uint and float bits
	v    any    // pairAny; a JSONValue's value
}

type pairKind uint8

const (
	pairAny pairKind = iota
	pairString
	pairURL
	pairHTML
	pairCSS
	pairJSON
	pairBool
	pairInt
	pairInt8
	pairInt16
	pairInt32
	pairInt64
	pairUint
	pairUint8
	pairUint16
	pairUint32
	pairUint64
	pairUintptr
	pairFloat32
	pairFloat64
	pairNone // a none Opt: written as nothing
)

// A returns the attribute name with value v, for an AttrList. Strings,
// bools, numbers, URL, HTML, CSS and JSONValue are stored without boxing,
// and so is an Opt of string, URL, HTML, CSS, JSONValue, bool, int, int64
// or float64 (stored as its value, or as nothing when none); any other
// type is stored as an any (boxed). The value is written as Attr
// writes it: nil and false omitted, true bare (or "true" on aria-* and the
// other true/false attributes), a URL attribute scheme-filtered unless v
// is a URL, everything escaped.
func A[T any](name string, v T) AttrPair {
	p := AttrPair{name: name}
	switch x := any(v).(type) {
	case string:
		p.kind, p.s = pairString, x
	case URL:
		p.kind, p.s = pairURL, string(x)
	case HTML:
		p.kind, p.s = pairHTML, string(x)
	case CSS:
		p.kind, p.s = pairCSS, string(x)
	case JSONValue:
		p.kind, p.v = pairJSON, x.v
	case bool:
		p.kind = pairBool
		if x {
			p.n = 1
		}
	case int:
		p.kind, p.n = pairInt, uint64(x)
	case int8:
		p.kind, p.n = pairInt8, uint64(x)
	case int16:
		p.kind, p.n = pairInt16, uint64(x)
	case int32:
		p.kind, p.n = pairInt32, uint64(x)
	case int64:
		p.kind, p.n = pairInt64, uint64(x)
	case uint:
		p.kind, p.n = pairUint, uint64(x)
	case uint8:
		p.kind, p.n = pairUint8, uint64(x)
	case uint16:
		p.kind, p.n = pairUint16, uint64(x)
	case uint32:
		p.kind, p.n = pairUint32, uint64(x)
	case uint64:
		p.kind, p.n = pairUint64, x
	case uintptr:
		p.kind, p.n = pairUintptr, uint64(x)
	case float32:
		p.kind, p.n = pairFloat32, uint64(math.Float32bits(x))
	case float64:
		p.kind, p.n = pairFloat64, math.Float64bits(x)
	// The common Opts, stored as their value (each case instantiates the
	// Opt type's methods in this package, so the list stays short).
	case Opt[string]:
		p.kind, p.s = optKind(x.ok, pairString), x.v
	case Opt[URL]:
		p.kind, p.s = optKind(x.ok, pairURL), string(x.v)
	case Opt[HTML]:
		p.kind, p.s = optKind(x.ok, pairHTML), string(x.v)
	case Opt[CSS]:
		p.kind, p.s = optKind(x.ok, pairCSS), string(x.v)
	case Opt[JSONValue]:
		p.kind, p.v = optKind(x.ok, pairJSON), x.v.v
	case Opt[bool]:
		p.kind = optKind(x.ok, pairBool)
		if x.v {
			p.n = 1
		}
	case Opt[int]:
		p.kind, p.n = optKind(x.ok, pairInt), uint64(x.v)
	case Opt[int64]:
		p.kind, p.n = optKind(x.ok, pairInt64), uint64(x.v)
	case Opt[float64]:
		p.kind, p.n = optKind(x.ok, pairFloat64), math.Float64bits(x.v)
	default:
		p.v = any(v)
	}
	return p
}

// optKind is k for a set Opt, pairNone for a none.
func optKind(ok bool, k pairKind) pairKind {
	if !ok {
		return pairNone
	}
	return k
}

// Name returns the attribute's name.
func (p AttrPair) Name() string { return p.name }

// Value returns the attribute's value as given to A (boxed). For an Opt of
// a type A stores unboxed, it's the Opt's value, or nil when none.
func (p AttrPair) Value() any {
	switch p.kind {
	case pairString:
		return p.s
	case pairURL:
		return URL(p.s)
	case pairHTML:
		return HTML(p.s)
	case pairCSS:
		return CSS(p.s)
	case pairJSON:
		return JSONValue{p.v}
	case pairBool:
		return p.n != 0
	case pairInt:
		return int(p.n)
	case pairInt8:
		return int8(p.n)
	case pairInt16:
		return int16(p.n)
	case pairInt32:
		return int32(p.n)
	case pairInt64:
		return int64(p.n)
	case pairUint:
		return uint(p.n)
	case pairUint8:
		return uint8(p.n)
	case pairUint16:
		return uint16(p.n)
	case pairUint32:
		return uint32(p.n)
	case pairUint64:
		return p.n
	case pairUintptr:
		return uintptr(p.n)
	case pairFloat32:
		return math.Float32frombits(uint32(p.n))
	case pairFloat64:
		return math.Float64frombits(p.n)
	}
	return p.v
}

// write writes the attribute by Attr's rules.
func (w *Writer) writePair(p *AttrPair) {
	switch p.kind {
	case pairString, pairHTML, pairCSS:
		w.attrValue(p.name, p.s, isURLAttr(p.name))
	case pairURL:
		w.attrValue(p.name, p.s, false)
	case pairJSON:
		w.attrJSON(p.name, JSONValue{p.v})
	case pairBool:
		w.attrBool(p.name, p.n != 0)
	case pairInt, pairInt8, pairInt16, pairInt32, pairInt64:
		w.attrOpen(p.name)
		w.Int(p.int())
		w.buf.WriteByte('"')
	case pairUint, pairUint8, pairUint16, pairUint32, pairUint64, pairUintptr:
		w.attrOpen(p.name)
		w.Uint(p.n)
		w.buf.WriteByte('"')
	case pairFloat32:
		w.attrOpen(p.name)
		w.Float32(math.Float32frombits(uint32(p.n)))
		w.buf.WriteByte('"')
	case pairFloat64:
		w.attrOpen(p.name)
		w.Float(math.Float64frombits(p.n))
		w.buf.WriteByte('"')
	case pairNone:
	default:
		w.attr(p.name, p.v, isURLAttr(p.name))
	}
}

// int is a signed integer kind's value, sign-extended from its own width.
func (p *AttrPair) int() int64 {
	switch p.kind {
	case pairInt8:
		return int64(int8(p.n))
	case pairInt16:
		return int64(int16(p.n))
	case pairInt32:
		return int64(int32(p.n))
	}
	return int64(p.n)
}

// SpreadList writes every attribute of l in list order, each by the Attr
// rules (URL attributes included). A name that doesn't match
// [A-Za-z_:][-A-Za-z0-9_:.]*, or that appears twice (in any ASCII case),
// is a render error, and nothing of l is written.
func (w *Writer) SpreadList(l AttrList) { w.spreadList("", l) }

func (w *Writer) spreadList(pos string, l AttrList) {
	if w.err != nil || len(l) == 0 {
		return
	}
	if err := checkList(pos, l); err != nil {
		w.setErr(err)
		return
	}
	for i := range l {
		w.writePair(&l[i])
	}
}

// checkList checks the names of l: valid, and each once. Its errors are
// built without fmt, so that the names don't escape.
//
//go:noinline
func checkList(pos string, l AttrList) error {
	for i := range l {
		name := l[i].name
		if !validAttrName(name) {
			return posErr(pos, "invalid attribute name "+strconv.Quote(name)+" in spread")
		}
		for j := range i {
			if len(l[j].name) == len(name) && strings.EqualFold(l[j].name, name) {
				return posErr(pos, "attribute "+strconv.Quote(name)+" twice in spread")
			}
		}
	}
	return nil
}

// SpreadOf writes the spread {...a} of an HTML element: an Attrs as Spread
// writes it (sorted by name), an AttrList as SpreadList does (in order).
// pos is the .orbit position of the spread, for a name error. Generated
// code calls it.
func SpreadOf[T Attrs | AttrList](w *Writer, pos string, a T) {
	// A pointer in the interface, so that the value isn't boxed.
	switch p := any(&a).(type) {
	case *Attrs:
		w.spread(pos, *p)
	case *AttrList:
		w.spreadList(pos, *p)
	}
}

// MarshalJSON encodes the list as a JSON object in list order, each name
// mapped to its value as written (unescaped), or true for a bare
// attribute; false and nil values are left out. So a Props with an
// AttrList passes to a server:defer component. A URL value loses its trust:
// it's filtered after the round trip.
func (l AttrList) MarshalJSON() ([]byte, error) {
	if l == nil {
		return []byte("null"), nil
	}
	if err := checkList("", l); err != nil {
		return nil, err
	}
	var b bytes.Buffer
	b.WriteByte('{')
	first := true
	for i := range l {
		p := &l[i]
		v := p.Value()
		s, ok := anyValue(p.name, v)
		if !ok {
			continue
		}
		if !first {
			b.WriteByte(',')
		}
		first = false
		k, _ := json.Marshal(p.name)
		b.Write(k)
		b.WriteByte(':')
		if isBare(p.name, v) {
			b.WriteString("true")
			continue
		}
		e, _ := json.Marshal(s)
		b.Write(e)
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

// UnmarshalJSON decodes the object MarshalJSON writes, in its order: a
// string value becomes a string attribute, true or false a bool one; null
// is dropped. Names are checked as SpreadList checks them.
func (l *AttrList) UnmarshalJSON(data []byte) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	if tok == nil {
		*l = nil
		return nil
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return errors.New("orbit: AttrList must be a JSON object")
	}
	out := AttrList{}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		name, _ := tok.(string)
		tok, err = dec.Token()
		if err != nil {
			return err
		}
		switch v := tok.(type) {
		case string:
			out = append(out, A(name, v))
		case bool:
			out = append(out, A(name, v))
		case nil:
		default:
			return fmt.Errorf("orbit: AttrList attribute %q must be a string, true, false or null", name)
		}
	}
	if _, err := dec.Token(); err != nil {
		return err
	}
	if err := checkList("", out); err != nil {
		return err
	}
	*l = out
	return nil
}
