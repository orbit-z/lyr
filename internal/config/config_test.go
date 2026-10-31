package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/orbit-z/lyr/internal/compiler"
	"github.com/orbit-z/lyr/internal/markdown"
)

func TestParse(t *testing.T) {
	tests := []struct {
		json string
		want Config
		err  string
	}{
		{`{}`, Config{CompressHTML: compiler.CompressJSX}, ""}, // the default is "jsx", Astro v7's
		{`{"site": "https://example.com", "server": "./cmd/web", "embed": true, "compressHTML": "jsx"}`,
			Config{Site: "https://example.com", Server: "./cmd/web", Embed: true, CompressHTML: compiler.CompressJSX}, ""},
		{`{"compressHTML": "html"}`, Config{CompressHTML: compiler.CompressHTML}, ""},
		{`{"compressHTML": false}`, Config{CompressHTML: compiler.CompressNone}, ""},
		{`{"compressHTML": null, "site": null}`, Config{CompressHTML: compiler.CompressJSX}, ""},
		{`{"unknown": 1, "Site": "https://x.dev"}`, Config{Site: "https://x.dev"}, ""}, // encoding/json matches keys case-insensitively
		{`{"compressHTML": true}`, Config{CompressHTML: compiler.CompressHTML}, ""},
		{`{"compressHTML": "none"}`, Config{}, `lyr.json: compressHTML must be "jsx", "html" (or true) or false, not "none"`},
		{`{"site": "blog.example.com"}`, Config{}, `lyr.json: site "blog.example.com" isn't an absolute URL; include the scheme and host, such as "https://example.com"`},
		{`{"site": "/blog"}`, Config{}, `lyr.json: site "/blog" isn't an absolute URL; include the scheme and host, such as "https://example.com"`},
		{`{"site": 3}`, Config{}, `lyr.json: site must be a string`},
		{`{"server": ""}`, Config{}, `lyr.json: server must not be empty; remove it to use ./cmd/server`},
		{`{"server": "/abs/cmd"}`, Config{}, `lyr.json: server "/abs/cmd" must be a path relative to the project root, such as "./cmd/server"`},
		{`{"embed": "yes"}`, Config{}, `lyr.json: embed must be true or false`},
		{`{"sourcemap": true}`, Config{Sourcemap: true}, ""},
		{`{"sourcemap": "inline"}`, Config{}, `lyr.json: sourcemap must be true or false`},
		{"{\n  \"site\": \"https://x.dev\",\n}", Config{}, `lyr.json:3:1: invalid character '}' looking for beginning of object key string`},
		{`[]`, Config{}, `lyr.json: must be a JSON object`},
		{`{"watch": ["../*.md", "content/**"]}`, Config{Watch: []string{"../*.md", "content/**"}}, ""},
		{`{"watch": null}`, Config{}, ""},
		{`{"watch": "content/**"}`, Config{}, `lyr.json: watch must be an array of strings`},
		{`{"watch": [1]}`, Config{}, `lyr.json: watch must be an array of strings`},
		{`{"watch": [""]}`, Config{}, `lyr.json: watch[0] "": must not be empty`},
		{`{"watch": ["a", "/etc/*.conf"]}`, Config{}, `lyr.json: watch[1] "/etc/*.conf": must be a slash-separated path relative to the project root, such as "content/**"`},
		{`{"watch": ["content/[a-"]}`, Config{}, `lyr.json: watch[0] "content/[a-": segment "[a-": syntax error in pattern`},
		// The trailingSlash key (PLAN.md §4e).
		{`{"trailingSlash": "never"}`, Config{}, ""},
		{`{"trailingSlash": "always"}`, Config{TrailingSlashAlways: true}, ""},
		{`{"trailingSlash": null}`, Config{}, ""},
		{`{"trailingSlash": "ignore"}`, Config{}, `lyr.json: trailingSlash must be "never" or "always", not "ignore"; Lyr has no "ignore" mode: each page has one URL`},
		{`{"trailingSlash": "Always"}`, Config{}, `lyr.json: trailingSlash must be "never" or "always", not "Always"`},
		{`{"trailingSlash": true}`, Config{}, `lyr.json: trailingSlash must be "never" or "always", not true`},
		{`{"trailingSlash": 1}`, Config{}, `lyr.json: trailingSlash must be "never" or "always", not 1`},
		// The markdown key (PLAN.md §8).
		{`{"markdown": {"gfm": true, "smartypants": true, "syntaxHighlight": {"themes": {"light": "github", "dark": "github-dark"}, "css": true}}}`,
			Config{Markdown: markdown.Options{Light: "github", Dark: "github-dark"}}, ""},
		{`{"markdown": {"gfm": false, "smartypants": false, "syntaxHighlight": false}}`,
			Config{Markdown: markdown.Options{NoGFM: true, NoSmartypants: true, NoHighlight: true}}, ""},
		{`{"markdown": {"syntaxHighlight": {"themes": {"light": "monokai", "dark": null}, "css": false}}}`,
			Config{Markdown: markdown.Options{Light: "monokai", SingleTheme: true, NoCSS: true}}, ""},
		{`{"markdown": {"syntaxHighlight": true}}`, Config{}, ""},
		{`{"markdown": null}`, Config{}, ""},
		{`{"markdown": true}`, Config{}, `lyr.json: markdown must be an object`},
		{`{"markdown": {"gfm": "yes"}}`, Config{}, `lyr.json: markdown.gfm must be true or false`},
		{`{"markdown": {"syntaxHighlight": "shiki"}}`, Config{}, `lyr.json: markdown.syntaxHighlight must be false or an object such as {"themes": {"light": "github", "dark": "github-dark"}}`},
		{`{"markdown": {"syntaxHighlight": {"css": 1}}}`, Config{}, `lyr.json: markdown.syntaxHighlight.css must be true or false`},
		{`{"markdown": {"syntaxHighlight": {"themes": {"light": 3}}}}`, Config{}, `lyr.json: markdown.syntaxHighlight.themes.light must be a theme name`},
	}
	for _, tt := range tests {
		c, err := Parse([]byte(tt.json))
		if tt.err != "" {
			if err == nil || err.Error() != tt.err {
				t.Errorf("%s: error %v, want %q", tt.json, err, tt.err)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: %v", tt.json, err)
			continue
		}
		if !reflect.DeepEqual(*c, tt.want) {
			t.Errorf("%s: got %+v, want %+v", tt.json, *c, tt.want)
		}
	}
	_, err := Parse([]byte(`{"markdown": {"syntaxHighlight": {"themes": {"dark": "githbu"}}}}`))
	if err == nil || !strings.HasPrefix(err.Error(), `lyr.json: markdown.syntaxHighlight.themes.dark: unknown theme "githbu"; the themes are abap, `) {
		t.Errorf("unknown theme: %v", err)
	}
}

func TestLoadMissing(t *testing.T) {
	c, err := Load(t.TempDir())
	if err != nil || !reflect.DeepEqual(*c, Config{}) {
		t.Fatalf("Load without lyr.json = %+v, %v", c, err)
	}
}

func TestServerEntry(t *testing.T) {
	dir := t.TempDir()
	c := &Config{}
	if pkg, gen := c.ServerEntry(dir); pkg != GeneratedServer || !gen {
		t.Errorf("no cmd/server: %s %v", pkg, gen)
	}
	if err := os.MkdirAll(filepath.Join(dir, "cmd", "server"), 0o755); err != nil {
		t.Fatal(err)
	}
	if pkg, gen := c.ServerEntry(dir); pkg != DefaultServer || gen {
		t.Errorf("cmd/server: %s %v", pkg, gen)
	}
	c.Server = "./cmd/web"
	if pkg, gen := c.ServerEntry(dir); pkg != "./cmd/web" || gen {
		t.Errorf("server set: %s %v", pkg, gen)
	}
}

func TestModulePath(t *testing.T) {
	tests := []struct {
		gomod, want, err string
	}{
		{"module example.com/blog\n\ngo 1.27\n", "example.com/blog", ""},
		{"// a comment\nmodule example.com/blog // trailing\n", "example.com/blog", ""},
		{"go 1.27\n\n  module\t\"example.com/quoted\"\r\n", "example.com/quoted", ""},
		{"module `example.com/raw`\n", "example.com/raw", ""},
		{"module (\n\t// c\n\texample.com/block\n)\n", "example.com/block", ""},
		{"module ( example.com/inline )\n", "example.com/inline", ""},
		{"module example.com/x//comment\n", "example.com/x", ""},
		{"// module example.com/commented\nmodule real.dev/m\n", "real.dev/m", ""},
		{"require example.com/a v1.0.0\n", "", "go.mod: no module directive"},
		{"module \"unterminated\n", "", "go.mod:1: unterminated quoted string"},
		{"module a b\n", "", "go.mod:1: usage: module module/path"},
		{"module \"\"\n", "", "go.mod:1: empty module path"},
	}
	for _, tt := range tests {
		got, err := ModulePath([]byte(tt.gomod))
		if tt.err != "" {
			if err == nil || err.Error() != tt.err {
				t.Errorf("%q: error %v, want %q", tt.gomod, err, tt.err)
			}
			continue
		}
		if err != nil || got != tt.want {
			t.Errorf("%q: got %q, %v; want %q", tt.gomod, got, err, tt.want)
		}
	}
}

func TestFindRoot(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "src", "pages")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module m\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := FindRoot(sub)
	if err != nil || got != dir {
		t.Errorf("FindRoot = %q, %v; want %q", got, err, dir)
	}
}
