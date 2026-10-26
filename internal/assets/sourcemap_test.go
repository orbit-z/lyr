package assets

import (
	"encoding/json"
	"strings"
	"testing"
)

// segment is one decoded source map mapping (all 0-based).
type segment struct {
	genLine, genCol, src, line, col int
}

const b64 = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"

// decodeMappings decodes a source map's mappings string.
func decodeMappings(t *testing.T, mappings string) []segment {
	t.Helper()
	var out []segment
	var src, line, col int
	for gl, group := range strings.Split(mappings, ";") {
		gc := 0
		for _, seg := range strings.Split(group, ",") {
			if seg == "" {
				continue
			}
			var vals []int
			v, shift := 0, 0
			for _, c := range seg {
				d := strings.IndexRune(b64, c)
				if d < 0 {
					t.Fatalf("bad VLQ %q", seg)
				}
				v |= (d & 31) << shift
				if d&32 != 0 {
					shift += 5
					continue
				}
				if v&1 != 0 {
					v = -(v >> 1)
				} else {
					v >>= 1
				}
				vals = append(vals, v)
				v, shift = 0, 0
			}
			gc += vals[0]
			if len(vals) >= 4 {
				src += vals[1]
				line += vals[2]
				col += vals[3]
				out = append(out, segment{gl, gc, src, line, col})
			}
		}
	}
	return out
}

// lookup returns the original position of the generated position of the
// first occurrence of needle in gen.
func lookup(t *testing.T, gen string, sm *sourceMap, needle string) (source string, line, col int) {
	t.Helper()
	i := strings.Index(gen, needle)
	if i < 0 {
		t.Fatalf("%q not in output:\n%s", needle, gen)
	}
	gl := strings.Count(gen[:i], "\n")
	gc := i - (strings.LastIndex(gen[:i], "\n") + 1)
	for _, s := range decodeMappings(t, sm.Mappings) {
		if s.genLine == gl && s.genCol == gc {
			return sm.Sources[s.src], s.line + 1, s.col + 1
		}
	}
	t.Fatalf("no mapping at %d:%d for %q", gl, gc, needle)
	return "", 0, 0
}

func readMap(t *testing.T, res *Result, url string) *sourceMap {
	t.Helper()
	f := res.File(url + ".map")
	if f == nil {
		t.Fatalf("no %s.map", url)
	}
	var sm sourceMap
	if err := json.Unmarshal(f.Contents, &sm); err != nil {
		t.Fatal(err)
	}
	return &sm
}

func TestSourceMaps(t *testing.T) {
	dir := project(t, basicFiles)
	for _, tt := range []struct {
		name   string
		opts   Options
		linked bool
	}{
		{"dev", Options{Root: dir, Dev: true}, true},
		{"prod", Options{Root: dir, Sourcemap: true}, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			res := bundle(t, tt.opts, basicInput(t, dir))
			noErrors(t, res)
			idx := res.Pages["src/pages/index.orbit"]
			css, js := idx[0].URL, idx[1].URL
			for _, url := range []string{css, js} {
				gen := string(res.File(url).Contents)
				if got := strings.Contains(gen, "sourceMappingURL="+url+".map"); got != tt.linked {
					t.Errorf("%s linked = %v, want %v:\n%s", url, got, tt.linked, gen)
				}
			}

			gen := string(res.File(js).Contents)
			sm := readMap(t, res, js)
			// Card.orbit line 11: "  console.log(greet(\"card\"), n);"
			src, line, col := lookup(t, gen, sm, "console.log(")
			if src != "/src/components/Card.orbit" || line != 11 || col != 3 {
				t.Errorf("console.log maps to %s:%d:%d, want /src/components/Card.orbit:11:3", src, line, col)
			}
			// util.ts, shared with blog/[slug], is a chunk (PLAN.md §5e)
			// whose map points at the .ts file. Line 2: "  return `hi ${name}`;"
			if len(idx) != 3 || idx[2].Kind != "modulepreload" {
				t.Fatalf("index assets = %+v", idx)
			}
			chunk := idx[2].URL
			if got := strings.Contains(string(res.File(chunk).Contents), "sourceMappingURL="+chunk+".map"); got != tt.linked {
				t.Errorf("%s linked = %v, want %v", chunk, got, tt.linked)
			}
			if src, line, _ := lookup(t, string(res.File(chunk).Contents), readMap(t, res, chunk), "`hi "); src != "/src/scripts/util.ts" || line != 2 {
				t.Errorf("template literal maps to %s:%d", src, line)
			}
			if strings.Contains(gen, "`hi ") {
				t.Errorf("shared util.ts is in the page entry:\n%s", gen)
			}
			for i, s := range sm.Sources {
				if s == "/src/components/Card.orbit" && (i >= len(sm.SourcesContent) || *sm.SourcesContent[i] != basicFiles["src/components/Card.orbit"]) {
					t.Errorf("sourcesContent of Card.orbit isn't the file")
				}
				if strings.Contains(s, "?") || strings.Contains(s, "@") {
					t.Errorf("source %q keeps a block suffix or page qualifier", s)
				}
				if strings.Contains(s, dir) || strings.HasPrefix(s, "..") {
					t.Errorf("source %q depends on the build directory", s)
				}
			}

			cgen := string(res.File(css).Contents)
			csm := readMap(t, res, css)
			if !tt.opts.Dev {
				return
			}
			// Card.orbit line 6: "  .card { color: red }"
			if src, line, col := lookup(t, cgen, csm, ".card"); src != "/src/components/Card.orbit" || line != 6 || col != 3 {
				t.Errorf(".card maps to %s:%d:%d", src, line, col)
			}
		})
	}
}
