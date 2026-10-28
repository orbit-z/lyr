package orbit

import "errors"

// The pass-1 forms of a spread on a component tag (PLAN.md §2.11). Pass 1
// can't tell a struct spread (which sets props) from an attribute spread
// (an Attrs, AttrList or RestAttrs, merged into the RestAttrs field), so it
// emits these, which type-check for any operand; the typed pass replaces
// them with field assignments, or with RestFrom and RestMerge. They only
// run when the typed pass couldn't (export data couldn't be read, with a
// warning): a struct spread then fails the render at its .orbit position.
// The second argument is the callee's zero Props, which gives the typed
// pass the callee's type; pos is the spread's .orbit position and what is
// "{...x} on <Tag>".

// SpreadCheck is a spread on a component tag without a RestAttrs field, in
// pass 1: it records a positioned render error. Generated code calls it.
func SpreadCheck[P, T any](w *Writer, _ P, pos, what string, x T) {
	w.setErr(spreadSkipped(pos, what, any(&x), false))
}

// RestFromAny is RestFrom for any operand, in pass 1: an Attrs, AttrList
// or RestAttrs as RestFrom converts it; anything else records a positioned
// render error and spreads nothing. Generated code calls it.
func RestFromAny[P, T any](w *Writer, _ P, pos, what string, x T) RestAttrs {
	switch p := any(&x).(type) {
	case *Attrs:
		return RestAttrs{m: *p}
	case *AttrList:
		return RestAttrs{l: *p}
	case *RestAttrs:
		return *p
	}
	w.setErr(spreadSkipped(pos, what, nil, true))
	return RestAttrs{}
}

// RestMergeAny is RestMerge for any operand, in pass 1, with RestFromAny's
// error for an operand of another type. Generated code calls it.
func RestMergeAny[P, T any](w *Writer, _ P, pos, what string, own RestAttrs, x T) RestAttrs {
	switch p := any(&x).(type) {
	case *Attrs:
		return own.mergeAttrs(*p)
	case *AttrList:
		return own.mergeList(*p)
	case *RestAttrs:
		return own.merge(*p)
	}
	w.setErr(spreadSkipped(pos, what, nil, true))
	return own
}

// spreadSkipped is the render error of a spread that pass 1 can't render:
// p points to the operand (nil for a struct), rest tells whether the callee
// has a RestAttrs field.
func spreadSkipped(pos, what string, p any, rest bool) error {
	switch p.(type) {
	case *Attrs, *AttrList, *RestAttrs:
		if !rest {
			return &posError{pos: pos, err: errors.New(what + " needs an orbit.RestAttrs field in its Props, to pass HTML attributes through")}
		}
	}
	return &posError{pos: pos, err: errors.New(what + " sets props, which needs Orbit's type check; it was skipped (see the warning above)")}
}
