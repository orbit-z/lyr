package lyr

import (
	"bytes"
	"encoding/json"
	"unsafe"
)

// The stored session format (PLAN.md §4i): a JSON object, compared and
// merged per top-level member. The scanner here reads the objects Lyr and
// encoding/json write; anything it can't read counts as entirely changed.

// jsonMember is one top-level member of a JSON object.
type jsonMember struct {
	key string // the unescaped name
	raw []byte // the quoted name, as written
	val []byte // the value, as written
}

// jsonMembers appends the top-level members of the JSON object b to dst.
// It reports false when b isn't an object it can read.
func jsonMembers(b []byte, dst []jsonMember) ([]jsonMember, bool) {
	i := skipWS(b, 0)
	if i >= len(b) || b[i] != '{' {
		return dst, false
	}
	i = skipWS(b, i+1)
	if i < len(b) && b[i] == '}' {
		return dst, true
	}
	for {
		if i >= len(b) || b[i] != '"' {
			return dst, false
		}
		end, esc, ok := skipString(b, i)
		if !ok {
			return dst, false
		}
		raw := b[i:end]
		var key string
		if esc {
			if key, ok = unquote(raw); !ok {
				return dst, false
			}
		} else if len(raw) > 2 {
			key = unsafe.String(&raw[1], len(raw)-2)
		}
		i = skipWS(b, end)
		if i >= len(b) || b[i] != ':' {
			return dst, false
		}
		i = skipWS(b, i+1)
		vend, ok := skipValue(b, i)
		if !ok {
			return dst, false
		}
		dst = append(dst, jsonMember{key: key, raw: raw, val: b[i:vend]})
		i = skipWS(b, vend)
		if i >= len(b) {
			return dst, false
		}
		switch b[i] {
		case ',':
			i = skipWS(b, i+1)
		case '}':
			return dst, true
		default:
			return dst, false
		}
	}
}

// unquote decodes a JSON string with escapes.
func unquote(raw []byte) (string, bool) {
	var s string
	if json.Unmarshal(raw, &s) != nil {
		return "", false
	}
	return s, true
}

func skipWS(b []byte, i int) int {
	for i < len(b) && (b[i] == ' ' || b[i] == '\t' || b[i] == '\n' || b[i] == '\r') {
		i++
	}
	return i
}

// skipString returns the end of the string starting at b[i] (a quote), and
// whether it has escapes.
func skipString(b []byte, i int) (end int, esc, ok bool) {
	for j := i + 1; j < len(b); j++ {
		switch b[j] {
		case '\\':
			esc = true
			j++
		case '"':
			return j + 1, esc, true
		}
	}
	return 0, false, false
}

// skipValue returns the end of the value starting at b[i].
func skipValue(b []byte, i int) (int, bool) {
	if i >= len(b) {
		return 0, false
	}
	switch b[i] {
	case '"':
		end, _, ok := skipString(b, i)
		return end, ok
	case '{', '[':
		depth := 0
		for j := i; j < len(b); j++ {
			switch b[j] {
			case '"':
				end, _, ok := skipString(b, j)
				if !ok {
					return 0, false
				}
				j = end - 1
			case '{', '[':
				depth++
			case '}', ']':
				depth--
				if depth == 0 {
					return j + 1, true
				}
			}
		}
		return 0, false
	}
	j := i
	for j < len(b) {
		switch b[j] {
		case ',', '}', ']', ' ', '\t', '\n', '\r':
			return j, j > i
		}
		j++
	}
	return j, j > i
}

func findMember(ms []jsonMember, key string) *jsonMember {
	for i := range ms {
		if ms[i].key == key {
			return &ms[i]
		}
	}
	return nil
}

// changedFields compares the new encoding n with the snapshot l per
// top-level field of ti, and returns which fields changed (by index in
// ti.fields; a field present in one only counts) and whether any did.
// Members of l that ti doesn't know never count. When either side can't
// be read, every field counts as changed.
func changedFields(ti *sessionType, n, l []byte, dst []bool) ([]bool, bool) {
	var nb, lb [16]jsonMember
	nm, ok1 := jsonMembers(n, nb[:0])
	lm, ok2 := jsonMembers(l, lb[:0])
	for range ti.fields {
		dst = append(dst, !(ok1 && ok2))
	}
	if !ok1 || !ok2 {
		return dst, true
	}
	anyChanged := false
	for i, f := range ti.fields {
		a, b := findMember(nm, f), findMember(lm, f)
		if (a == nil) != (b == nil) || a != nil && !bytes.Equal(a.val, b.val) {
			dst[i] = true
			anyChanged = true
		}
	}
	return dst, anyChanged
}

// mergeSession merges the new encoding n over the stored bytes s: the
// changed fields take n's value (or are removed when n doesn't have them),
// and every other member of s keeps the store's value. The object has n's
// key order, then s's other keys. With s nil (the stored session is gone),
// it holds only n's changed fields. The result is a new slice.
func mergeSession(ti *sessionType, s, n []byte, changed []bool) []byte {
	var nb, sb [16]jsonMember
	nm, ok := jsonMembers(n, nb[:0])
	if !ok || len(changed) != len(ti.fields) {
		return bytes.Clone(n)
	}
	var sm []jsonMember
	if s != nil {
		if sm, ok = jsonMembers(s, sb[:0]); !ok {
			return bytes.Clone(n) // unreadable stored bytes: the new data wins
		}
	}
	isChanged := func(key string) bool {
		i := ti.fieldIndex(key)
		return i >= 0 && changed[i]
	}
	out := make([]byte, 0, len(n)+len(s)+2)
	out = append(out, '{')
	first := true
	emit := func(raw, val []byte) {
		if !first {
			out = append(out, ',')
		}
		first = false
		out = append(out, raw...)
		out = append(out, ':')
		out = append(out, val...)
	}
	for _, m := range nm {
		if isChanged(m.key) {
			emit(m.raw, m.val)
		} else if sv := findMember(sm, m.key); sv != nil {
			emit(m.raw, sv.val)
		}
	}
	for _, m := range sm {
		if findMember(nm, m.key) != nil || isChanged(m.key) {
			continue
		}
		emit(m.raw, m.val)
	}
	return append(out, '}')
}
