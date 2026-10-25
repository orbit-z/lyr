package orbit

import (
	"errors"
	"fmt"
	"strings"
)

// notFoundPattern is the reserved Route.Pattern of src/pages/404.orbit.
// It has no leading slash, so it can never be a URL pattern: the router keeps
// that route aside (router.notFound) instead of putting it in the trie, so
// the 404 page has no URL of its own and /404 isn't routable.
const notFoundPattern = "404"

// segKind is the kind of one route segment. The order is the match priority.
type segKind uint8

const (
	segStatic segKind = iota // "new"
	segParam                 // "[slug]"
	segRest                  // "[...path]"
)

type routeSeg struct {
	kind segKind
	text string // static text, or the parameter name
}

// compiledRoute is a Route with its parsed pattern.
type compiledRoute struct {
	route *Route
	segs  []routeSeg
	names []string // param names in path order; nil when there are none
}

// node is one level of the segment trie. Children are tried in priority
// order: static, then [param], then [...rest].
type node struct {
	static map[string]*node // keyed by the decoded segment text
	param  *node            // the [param] child (names don't matter for matching)
	route  *compiledRoute   // a route that ends at this node
	rest   *compiledRoute   // a [...rest] route that ends here (matches 0+ segments)
}

// router matches request paths against the page routes.
type router struct {
	root     node
	notFound *Route // the 404.orbit route, or nil
}

// newRouter compiles routes into a segment trie. It returns every
// validation error (unsupported shapes, malformed patterns, duplicates),
// joined with errors.Join.
func newRouter(routes []Route) (*router, error) {
	rt := &router{}
	var errs []error
	seen := make(map[string]*Route, len(routes)) // shape key → first route with it
	for i := range routes {
		r := &routes[i]
		if r.Pattern == notFoundPattern {
			if rt.notFound != nil {
				errs = append(errs, duplicateError(rt.notFound, r))
				continue
			}
			rt.notFound = r
			continue
		}
		segs, err := parsePattern(r.Pattern, r.File)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		key := shapeKey(segs)
		if prev, ok := seen[key]; ok {
			errs = append(errs, duplicateError(prev, r))
			continue
		}
		seen[key] = r
		rt.insert(&compiledRoute{route: r, segs: segs, names: paramNames(segs)})
	}
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return rt, nil
}

func (rt *router) insert(cr *compiledRoute) {
	n := &rt.root
	for _, s := range cr.segs {
		switch s.kind {
		case segStatic:
			if n.static == nil {
				n.static = make(map[string]*node)
			}
			c := n.static[s.text]
			if c == nil {
				c = &node{}
				n.static[s.text] = c
			}
			n = c
		case segParam:
			if n.param == nil {
				n.param = &node{}
			}
			n = n.param
		case segRest:
			// A rest is always last (parsePattern checks it).
			n.rest = cr
			return
		}
	}
	n.route = cr
}

// paramNames returns the parameter names of segs in order, or nil.
func paramNames(segs []routeSeg) []string {
	var names []string
	for _, s := range segs {
		if s.kind != segStatic {
			names = append(names, s.text)
		}
	}
	return names
}

// shapeKey identifies the set of URLs a pattern matches: parameter names
// are dropped, so /blog/[slug] and /blog/[id] get the same key.
func shapeKey(segs []routeSeg) string {
	var b strings.Builder
	for _, s := range segs {
		b.WriteByte('/')
		switch s.kind {
		case segStatic:
			b.WriteString(s.text)
		case segParam:
			b.WriteString("[]")
		case segRest:
			b.WriteString("[...]")
		}
	}
	return b.String()
}

func duplicateError(first, second *Route) error {
	return fmt.Errorf("%s: route %s is already defined by %s; remove or rename one of them",
		second.File, second.Pattern, first.File)
}

// parsePattern validates a route pattern and splits it into segments.
// Errors name the file, without a line and column (they're about the file).
// The build-time CLI is meant to run the same checks.
func parsePattern(pattern, file string) ([]routeSeg, error) {
	malformed := func(reason string) error {
		return fmt.Errorf("%s: invalid route pattern %q: %s", file, pattern, reason)
	}
	if !strings.HasPrefix(pattern, "/") {
		return nil, malformed("must start with /")
	}
	if pattern == "/" {
		return nil, nil
	}
	if strings.HasSuffix(pattern, "/") {
		return nil, malformed("must not end with /")
	}
	parts := strings.Split(pattern[1:], "/")
	segs := make([]routeSeg, 0, len(parts))
	names := make(map[string]bool)
	for i, p := range parts {
		last := i == len(parts)-1
		if p == "" {
			return nil, malformed("empty segment")
		}
		s, err := parseSegment(p, last, file)
		if err != nil {
			return nil, err
		}
		if s == nil {
			return nil, malformed(fmt.Sprintf("segment %q has unbalanced brackets", p))
		}
		if s.kind != segStatic {
			if s.text == "" {
				return nil, malformed(fmt.Sprintf("segment %q has an empty parameter name", p))
			}
			if names[s.text] {
				return nil, malformed(fmt.Sprintf("parameter %q is used twice", s.text))
			}
			names[s.text] = true
		}
		if s.kind == segRest && !last {
			return nil, fmt.Errorf("%s: rest parameter %s must be the last segment of the route; use a whole-segment parameter such as [%s] instead",
				file, p, s.text)
		}
		segs = append(segs, *s)
	}
	return segs, nil
}

// parseSegment parses one pattern segment. It returns nil (and no error) for
// malformed brackets, and an error for a segment that mixes text and a
// parameter.
func parseSegment(p string, last bool, file string) (*routeSeg, error) {
	if !strings.ContainsAny(p, "[]") {
		return &routeSeg{kind: segStatic, text: p}, nil
	}
	if p[0] == '[' && p[len(p)-1] == ']' && !strings.ContainsAny(p[1:len(p)-1], "[]") {
		name := p[1 : len(p)-1]
		if rest, ok := strings.CutPrefix(name, "..."); ok {
			return &routeSeg{kind: segRest, text: rest}, nil
		}
		return &routeSeg{kind: segParam, text: name}, nil
	}
	// Text and a well-formed [name] in one segment: unsupported in the MVP.
	if open := strings.IndexByte(p, '['); open >= 0 {
		if end := strings.IndexByte(p[open:], ']'); end > 1 {
			name := p[open+1 : open+end]
			if !strings.ContainsAny(name, "[") {
				name = strings.TrimPrefix(name, "...")
				suggest := "[" + name + "].orbit"
				if !last {
					suggest = "[" + name + "]/"
				}
				return nil, fmt.Errorf("%s: route segment %q mixes text and a parameter; use a whole-segment parameter such as %s and parse it in the page",
					file, p, suggest)
			}
		}
	}
	return nil, nil
}

// match finds the route for an escaped request path (r.URL.EscapedPath())
// and appends its param values to vals, in the order of cr.names. It
// returns nil if no route matches. Paths with an empty segment (a trailing
// slash, or "//") never match; see redirectTarget.
//
// Pass a reused buffer as vals (Ctx keeps one) and match doesn't allocate,
// except to decode values that contain a %-escape.
func (rt *router) match(path string, vals []string) (cr *compiledRoute, out []string) {
	cr, rem := rt.find(path)
	if cr == nil || cr.names == nil {
		return cr, vals
	}
	return cr, cr.extract(rem, vals)
}

// find returns the route for path, and the path as lookup sees it.
func (rt *router) find(path string) (*compiledRoute, string) {
	if path == "" || path[0] != '/' {
		return nil, ""
	}
	rem := path
	if rem == "/" {
		rem = "" // the root has zero segments
	}
	return rt.root.lookup(rem), rem
}

// lookup walks the trie depth-first. rem is the unmatched rest of the path:
// "" when every segment is consumed, otherwise "/seg/...". Trying children
// in priority order and backtracking on failure gives the leftmost-segment
// priority rule: /blog/new beats /blog/[slug], but /blog/new/x can still
// fall back to /blog/[slug]/x.
func (n *node) lookup(rem string) *compiledRoute {
	if rem == "" {
		// A route ending here beats a rest matching zero segments.
		if n.route != nil {
			return n.route
		}
		return n.rest
	}
	seg, next := nextSegment(rem)
	if seg == "" {
		return nil // empty segment: nothing matches it
	}
	if n.static != nil {
		if c := n.staticChild(seg); c != nil {
			if m := c.lookup(next); m != nil {
				return m
			}
		}
	}
	if n.param != nil {
		if m := n.param.lookup(next); m != nil {
			return m
		}
	}
	if n.rest != nil && !hasEmptySegment(rem) {
		return n.rest
	}
	return nil
}

// extract appends the param values of a path that matched cr to vals. The
// trie already chose the route, so this is a single forward pass with no
// backtracking.
func (cr *compiledRoute) extract(rem string, vals []string) []string {
	for _, s := range cr.segs {
		if s.kind == segRest {
			if rem != "" {
				rem = rem[1:] // drop the leading slash
			}
			return append(vals, pathUnescape(rem))
		}
		seg, next := nextSegment(rem)
		if s.kind == segParam {
			vals = append(vals, pathUnescape(seg))
		}
		rem = next
	}
	return vals
}

// nextSegment splits "/a/b" into "a" and "/b". rem must start with '/'.
func nextSegment(rem string) (seg, next string) {
	rem = rem[1:]
	if i := strings.IndexByte(rem, '/'); i >= 0 {
		return rem[:i], rem[i:]
	}
	return rem, ""
}

// hasEmptySegment reports whether "/a/b" has an empty segment ("//" or a
// trailing "/").
func hasEmptySegment(rem string) bool {
	return strings.Contains(rem, "//") || strings.HasSuffix(rem, "/")
}

// staticChild returns the static child for the escaped segment seg. Static
// keys are decoded text, so an escaped seg is decoded first, on the stack.
func (n *node) staticChild(seg string) *node {
	if strings.IndexByte(seg, '%') < 0 {
		return n.static[seg]
	}
	var buf [128]byte
	if b, ok := appendUnescape(buf[:0], seg); ok && len(b) <= len(buf) {
		return n.static[string(b)] // no allocation for a map lookup
	}
	return n.static[pathUnescape(seg)]
}

// pathUnescape decodes a path segment like ServeMux does for PathValue (and
// like url.PathUnescape): an invalid escape leaves the text as it is. Text
// without a % is returned as is, with no allocation.
func pathUnescape(s string) string {
	if strings.IndexByte(s, '%') < 0 {
		return s
	}
	b, ok := appendUnescape(make([]byte, 0, len(s)), s)
	if !ok {
		return s
	}
	return string(b)
}

// appendUnescape appends s with its %XX escapes decoded to dst. It reports
// false if s has an invalid escape.
func appendUnescape(dst []byte, s string) ([]byte, bool) {
	for i := 0; i < len(s); i++ {
		if s[i] != '%' {
			dst = append(dst, s[i])
			continue
		}
		if i+2 >= len(s) || !isHex(s[i+1]) || !isHex(s[i+2]) {
			return dst, false
		}
		dst = append(dst, unhex(s[i+1])<<4|unhex(s[i+2]))
		i += 2
	}
	return dst, true
}

func isHex(c byte) bool {
	return '0' <= c && c <= '9' || 'a' <= c && c <= 'f' || 'A' <= c && c <= 'F'
}

func unhex(c byte) byte {
	switch {
	case c <= '9':
		return c - '0'
	case c >= 'a':
		return c - 'a' + 10
	}
	return c - 'A' + 10
}

// redirectTarget returns the 308 Location for a trailing-slash path whose
// slash-less form matches a page: "/blog/hello/" → "/blog/hello", keeping
// the query. "/" and paths under /_orbit/ are never redirected; otherwise
// ok is false and the request should reach next untouched.
func (rt *router) redirectTarget(path, rawQuery string) (string, bool) {
	if len(path) < 2 || path[len(path)-1] != '/' || strings.HasPrefix(path, "/_orbit/") {
		return "", false
	}
	target := path[:len(path)-1]
	// "//evil.example/" must never become the protocol-relative
	// "//evil.example"; match rejects empty segments, but be explicit.
	if strings.HasPrefix(target, "//") {
		return "", false
	}
	if cr, _ := rt.find(target); cr == nil {
		return "", false
	}
	if rawQuery != "" {
		target += "?" + rawQuery
	}
	return target, true
}
