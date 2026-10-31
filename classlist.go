package lyr

import (
	"fmt"
	"reflect"
	"strings"
)

// The writes behind the class:list directive (PLAN.md §2.3). On an HTML
// element, codegen folds the static classes into the surrounding Raw
// constants and calls these methods for the rest; on a component, it joins
// the items into the class prop with JoinClasses. Classes are separated by
// HTML whitespace; runs of it collapse to one space, and empty items are
// dropped.

// isClassSpace reports whether c is ASCII whitespace as HTML defines it,
// which separates the classes of a class attribute.
func isClassSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\f' || c == '\r'
}

// For generated code. ClassStart begins a class attribute written by
// class:list when codegen can't tell whether it will hold a class: the
// first class written opens it with ` class="`, and ClassEnd closes it, or
// writes nothing when no class was written, so an empty list writes no
// attribute.
func (w *Writer) ClassStart() { w.clsPending = true }

// For generated code. ClassEnd ends a class attribute begun by ClassStart: the closing quote,
// or nothing when no class was written.
func (w *Writer) ClassEnd() {
	if w.clsPending {
		w.clsPending = false
		return
	}
	w.Raw(`"`)
}

// For generated code. Class writes the classes in s, HTML-escaped, each preceded by a space
// (or by ` class="` for the first class after ClassStart). Whitespace
// around and between them is normalized; an empty or blank s writes
// nothing.
func (w *Writer) Class(s string) {
	if w.err != nil {
		return
	}
	if classNormalized(s) { // the common case: one escape call
		w.classSep()
		escapeAttrString(w.buf, s)
		return
	}
	for i := 0; i < len(s); {
		for i < len(s) && isClassSpace(s[i]) {
			i++
		}
		j := i
		for j < len(s) && !isClassSpace(s[j]) {
			j++
		}
		if j > i {
			w.classSep()
			escapeAttrString(w.buf, s[i:j])
		}
		i = j
	}
}

// classSep writes what goes before a class: ` class="` for the first one
// after ClassStart, a space otherwise.
func (w *Writer) classSep() {
	if w.clsPending {
		w.clsPending = false
		w.buf.WriteString(` class="`)
		return
	}
	w.buf.WriteByte(' ')
}

// For generated code. Classes writes the classes of each string in ss, as Class does.
func (w *Writer) Classes(ss []string) {
	for _, s := range ss {
		w.Class(s)
	}
}

// For generated code. ClassAny writes a class:list item whose type codegen didn't know: a
// string kind as Class, a slice of a string kind as Classes. Any other
// type is a render error (lyr build reports it at the item first).
func (w *Writer) ClassAny(v any) {
	if w.err != nil {
		return
	}
	switch v := v.(type) {
	case string:
		w.Class(v)
	case []string:
		w.Classes(v)
	case optValue:
		if x, ok := v.optAny(); ok {
			w.ClassAny(x)
		}
	default:
		s, ok := classValue(v)
		if !ok {
			w.setErr(classTypeError(v))
			return
		}
		w.Class(s)
	}
}

// classValue converts a named string or string slice to its classes.
func classValue(v any) (string, bool) {
	rv := reflect.ValueOf(v)
	switch {
	case rv.Kind() == reflect.String:
		return rv.String(), true
	case rv.Kind() == reflect.Slice && rv.Type().Elem().Kind() == reflect.String:
		parts := make([]string, rv.Len())
		for i := range parts {
			parts[i] = rv.Index(i).String()
		}
		return strings.Join(parts, " "), true
	}
	return "", false
}

func classTypeError(v any) error {
	return fmt.Errorf("lyr: class:list item must be a string or []string, not %T", v)
}

// For generated code. JoinClasses joins the classes of parts with single spaces, dropping
// empty parts and normalizing whitespace: the class prop a component gets
// from class:list. When only one part has classes and it's already
// normalized, it is returned as is, without allocating.
func JoinClasses(parts ...string) string {
	only, n, clean := "", 0, true
	size := 0
	for _, p := range parts {
		if !hasClass(p) {
			continue
		}
		n++
		only = p
		size += len(p) + 1
		if clean && !classNormalized(p) {
			clean = false
		}
	}
	switch {
	case n == 0:
		return ""
	case n == 1 && clean:
		return only
	}
	var b strings.Builder
	b.Grow(size)
	for _, p := range parts {
		for i := 0; i < len(p); {
			for i < len(p) && isClassSpace(p[i]) {
				i++
			}
			j := i
			for j < len(p) && !isClassSpace(p[j]) {
				j++
			}
			if j > i {
				if b.Len() > 0 {
					b.WriteByte(' ')
				}
				b.WriteString(p[i:j])
			}
			i = j
		}
	}
	return b.String()
}

// hasClass reports whether s holds a class (anything but whitespace).
func hasClass(s string) bool {
	for i := 0; i < len(s); i++ {
		if !isClassSpace(s[i]) {
			return true
		}
	}
	return false
}

// classNormalized reports whether s is non-empty classes separated by
// single spaces.
func classNormalized(s string) bool {
	if s == "" || s[0] == ' ' || s[len(s)-1] == ' ' {
		return false
	}
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c == ' ' && s[i+1] == ' ':
			return false
		case c != ' ' && isClassSpace(c):
			return false
		}
	}
	return true
}

// For generated code. ClassIf returns s if cond is true, and "" otherwise: a keyed class:list
// item ("active": cond) in a component's class prop.
func ClassIf(cond bool, s string) string {
	if cond {
		return s
	}
	return ""
}

// For generated code. ClassString converts a class:list item whose type codegen didn't know to
// a string for JoinClasses: a string kind as is, a slice of a string kind
// joined with spaces. It panics for any other type (lyr build reports it
// at the item first).
func ClassString(v any) string {
	switch v := v.(type) {
	case string:
		return v
	case []string:
		return strings.Join(v, " ")
	case optValue:
		x, ok := v.optAny()
		if !ok {
			return ""
		}
		return ClassString(x)
	}
	s, ok := classValue(v)
	if !ok {
		panic(classTypeError(v))
	}
	return s
}
