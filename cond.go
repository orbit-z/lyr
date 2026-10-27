package orbit

// If returns Some(v) when cond is true and None otherwise. As an attribute
// value, none omits the attribute; in content, it writes nothing. Both
// arguments are evaluated, as for any call: guard a nil pointer in the
// frontmatter or use {if} instead.
//
// Generated code writes a call that is the whole value of an HTML element
// attribute or of a content {expr} as an if statement around the typed
// write of v, with the same output and no boxing (PLAN.md §2.6).
func If[T any](cond bool, v T) Opt[T] {
	if cond {
		return Opt[T]{v: v, ok: true}
	}
	return Opt[T]{}
}

// Cond returns a when cond is true and b otherwise. Both are evaluated.
func Cond[T any](cond bool, a, b T) T {
	if cond {
		return a
	}
	return b
}
