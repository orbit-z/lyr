package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
)

// RouteRule is one "routeRules" entry (PLAN.md §4h): default cache options
// for the request paths matching Pattern. Durations are whole seconds, as in
// Astro.
type RouteRule struct {
	Pattern string
	MaxAge  int64 // seconds
	SWR     int64 // seconds
	Tags    []string
}

// parseRouteRules parses "routeRules": an object from route pattern to
// {"maxAge", "swr", "tags"}. The rules come back sorted by pattern (the
// router's priority decides which applies, not the order).
func parseRouteRules(data json.RawMessage) ([]RouteRule, error) {
	var raw map[string]json.RawMessage
	if json.Unmarshal(data, &raw) != nil {
		return nil, fmt.Errorf(`%s: routeRules must be an object from route pattern to options, such as {"/blog/[slug]": {"maxAge": 300}}`, FileName)
	}
	rules := make([]RouteRule, 0, len(raw))
	shapes := map[string]string{}
	for pattern, body := range raw {
		fail := func(format string, args ...any) error {
			return fmt.Errorf("%s: routeRules[%s]: %s", FileName, strconv.Quote(pattern), fmt.Sprintf(format, args...))
		}
		shape, err := rulePatternShape(pattern)
		if err != nil {
			return nil, fail("%v", err)
		}
		if prev, ok := shapes[shape]; ok {
			a, b := min(prev, pattern), max(prev, pattern)
			return nil, fmt.Errorf("%s: routeRules[%s] and routeRules[%s] match the same paths; keep one", FileName, strconv.Quote(a), strconv.Quote(b))
		}
		shapes[shape] = pattern
		var fields map[string]json.RawMessage
		if json.Unmarshal(body, &fields) != nil || fields == nil {
			return nil, fail(`must be an object such as {"maxAge": 300, "swr": 3600, "tags": ["blog"]}`)
		}
		r := RouteRule{Pattern: pattern}
		for _, key := range sortedKeys(fields) {
			v := fields[key]
			switch key {
			case "maxAge", "swr":
				n, err := seconds(v)
				if err != nil {
					return nil, fail("%s %v", key, err)
				}
				if key == "maxAge" {
					r.MaxAge = n
				} else {
					r.SWR = n
				}
			case "tags":
				if json.Unmarshal(v, &r.Tags) != nil {
					return nil, fail("tags must be an array of strings")
				}
				if i := slices.Index(r.Tags, ""); i >= 0 {
					return nil, fail("tags[%d] is empty", i)
				}
			default:
				return nil, fail(`unknown key %q; a rule has "maxAge", "swr" and "tags"`, key)
			}
		}
		rules = append(rules, r)
	}
	slices.SortFunc(rules, func(a, b RouteRule) int { return strings.Compare(a.Pattern, b.Pattern) })
	return rules, nil
}

func sortedKeys(m map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// seconds parses a duration in whole, non-negative seconds.
func seconds(v json.RawMessage) (int64, error) {
	var f float64
	if json.Unmarshal(v, &f) != nil {
		return 0, fmt.Errorf("must be a number of seconds, not %s", bytes.TrimSpace(v))
	}
	switch {
	case f < 0:
		return 0, fmt.Errorf("must not be negative, not %s", bytes.TrimSpace(v))
	case f != math.Trunc(f):
		return 0, fmt.Errorf("must be a whole number of seconds, not %s", bytes.TrimSpace(v))
	case f > math.MaxInt64/1e9:
		return 0, fmt.Errorf("is too large: %s", bytes.TrimSpace(v))
	}
	return int64(f), nil
}

// rulePatternShape validates a rule pattern with the runtime router's
// rules (router.go parsePattern) and returns its shape: the pattern with
// parameter names dropped, so two rules matching the same paths are found.
func rulePatternShape(pattern string) (string, error) {
	if !strings.HasPrefix(pattern, "/") {
		return "", fmt.Errorf("the pattern must start with /")
	}
	if pattern == "/" {
		return "/", nil
	}
	if strings.HasSuffix(pattern, "/") {
		return "", fmt.Errorf("the pattern must not end with /")
	}
	parts := strings.Split(pattern[1:], "/")
	if parts[0] == "_lyr" {
		return "", fmt.Errorf("/_lyr/ paths are Lyr's own; rules never apply to them")
	}
	var shape strings.Builder
	names := map[string]bool{}
	for i, p := range parts {
		shape.WriteByte('/')
		if p == "" {
			return "", fmt.Errorf("the pattern has an empty segment")
		}
		if !strings.ContainsAny(p, "[]") {
			shape.WriteString(p)
			continue
		}
		if p[0] != '[' || p[len(p)-1] != ']' || strings.ContainsAny(p[1:len(p)-1], "[]") {
			return "", fmt.Errorf("segment %q must be text, [param] or [...rest]", p)
		}
		name, rest := strings.CutPrefix(p[1:len(p)-1], "...")
		switch {
		case name == "":
			return "", fmt.Errorf("segment %q has an empty parameter name", p)
		case names[name]:
			return "", fmt.Errorf("parameter %q is used twice", name)
		case rest && i != len(parts)-1:
			return "", fmt.Errorf("rest parameter %s must be the last segment", p)
		}
		names[name] = true
		if rest {
			shape.WriteString("[...]")
		} else {
			shape.WriteString("[]")
		}
	}
	return shape.String(), nil
}
