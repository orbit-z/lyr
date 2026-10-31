package config

import (
	"reflect"
	"strings"
	"testing"
)

func TestRouteRules(t *testing.T) {
	c, err := Parse([]byte(`{"routeRules": {
		"/blog/[...path]": {"maxAge": 300, "swr": 3600, "tags": ["blog"]},
		"/": {"maxAge": 60},
		"/about": {"tags": ["static", "pages"]},
		"/x": {"maxAge": 1e2, "swr": 0}
	}}`))
	if err != nil {
		t.Fatal(err)
	}
	want := []RouteRule{
		{Pattern: "/", MaxAge: 60},
		{Pattern: "/about", Tags: []string{"static", "pages"}},
		{Pattern: "/blog/[...path]", MaxAge: 300, SWR: 3600, Tags: []string{"blog"}},
		{Pattern: "/x", MaxAge: 100},
	}
	if !reflect.DeepEqual(c.RouteRules, want) {
		t.Errorf("rules %+v,\nwant %+v", c.RouteRules, want)
	}
	if c, err := Parse([]byte(`{"routeRules": null}`)); err != nil || c.RouteRules != nil {
		t.Errorf("null: %v %v", c.RouteRules, err)
	}

	for _, tt := range []struct{ json, err string }{
		{`{"routeRules": []}`, `lyr.json: routeRules must be an object from route pattern to options`},
		{`{"routeRules": {"/blog/[slug": {"maxAge": 1}}}`, `lyr.json: routeRules["/blog/[slug"]: segment "[slug" must be text, [param] or [...rest]`},
		{`{"routeRules": {"blog": {}}}`, `lyr.json: routeRules["blog"]: the pattern must start with /`},
		{`{"routeRules": {"/blog/": {}}}`, `lyr.json: routeRules["/blog/"]: the pattern must not end with /`},
		{`{"routeRules": {"/a//b": {}}}`, `lyr.json: routeRules["/a//b"]: the pattern has an empty segment`},
		{`{"routeRules": {"/_lyr/x": {}}}`, `lyr.json: routeRules["/_lyr/x"]: /_lyr/ paths are Lyr's own`},
		{`{"routeRules": {"/[...a]/b": {}}}`, `lyr.json: routeRules["/[...a]/b"]: rest parameter [...a] must be the last segment`},
		{`{"routeRules": {"/post-[id]": {}}}`, `lyr.json: routeRules["/post-[id]"]: segment "post-[id]" must be text`},
		{`{"routeRules": {"/[a]/[a]": {}}}`, `lyr.json: routeRules["/[a]/[a]"]: parameter "a" is used twice`},
		{`{"routeRules": {"/[]": {}}}`, `lyr.json: routeRules["/[]"]: segment "[]" has an empty parameter name`},
		{`{"routeRules": {"/b/[x]": {}, "/b/[y]": {}}}`, `lyr.json: routeRules["/b/[x]"] and routeRules["/b/[y]"] match the same paths`},
		{`{"routeRules": {"/": {"maxAge": -1}}}`, `lyr.json: routeRules["/"]: maxAge must not be negative, not -1`},
		{`{"routeRules": {"/": {"swr": 1.5}}}`, `lyr.json: routeRules["/"]: swr must be a whole number of seconds, not 1.5`},
		{`{"routeRules": {"/": {"maxAge": "1h"}}}`, `lyr.json: routeRules["/"]: maxAge must be a number of seconds, not "1h"`},
		{`{"routeRules": {"/": {"maxAge": 1e300}}}`, `lyr.json: routeRules["/"]: maxAge is too large`},
		{`{"routeRules": {"/": {"tags": "blog"}}}`, `lyr.json: routeRules["/"]: tags must be an array of strings`},
		{`{"routeRules": {"/": {"tags": ["a", ""]}}}`, `lyr.json: routeRules["/"]: tags[1] is empty`},
		{`{"routeRules": {"/": {"maxage": 1}}}`, `lyr.json: routeRules["/"]: unknown key "maxage"; a rule has "maxAge", "swr" and "tags"`},
		{`{"routeRules": {"/": 300}}`, `lyr.json: routeRules["/"]: must be an object`},
	} {
		_, err := Parse([]byte(tt.json))
		if err == nil || !strings.HasPrefix(err.Error(), tt.err) {
			t.Errorf("%s: error %v, want prefix %q", tt.json, err, tt.err)
		}
	}
}
