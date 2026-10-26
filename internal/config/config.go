// Package config loads a project's orbit.json (PLAN.md §8) and reads its
// module path from go.mod.
package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"

	"github.com/orbit-z/orbit/internal/compiler"
)

// FileName is the config file's name, at the project root.
const FileName = "orbit.json"

// DefaultServer is the server entry used when orbit.json has no "server" and
// the directory exists (PLAN.md §5b).
const DefaultServer = "./cmd/server"

// GeneratedServer is the package of the generated default server entry.
const GeneratedServer = "./" + compiler.GenDir + "/cmd/server"

// Config is a project's orbit.json. Every key is optional.
type Config struct {
	// Site is the absolute site URL ("https://example.com"), "" when unset.
	Site string
	// Server is the server entry package as written ("./cmd/server"), ""
	// when unset; see ServerEntry for the resolved one.
	Server string
	// Embed is "embed": true.
	Embed bool
	// CompressHTML is "compressHTML" ("html" by default; true means "html").
	CompressHTML compiler.Compression
	// Sourcemap is "sourcemap": true: orbit build writes external source
	// maps next to the bundles, not linked from them (PLAN.md §5).
	Sourcemap bool
	// Watch are the "watch" globs: extra files whose changes restart the
	// app in orbit dev (PLAN.md §7), relative to the project root.
	Watch []string
}

// Load reads dir/orbit.json. A missing file gives the defaults.
func Load(dir string) (*Config, error) {
	data, err := os.ReadFile(filepath.Join(dir, FileName))
	if errors.Is(err, fs.ErrNotExist) {
		return &Config{}, nil
	}
	if err != nil {
		return nil, err
	}
	return Parse(data)
}

// Parse parses the contents of an orbit.json. Errors start with "orbit.json"
// (and the line and column of a JSON syntax error). Unknown keys are ignored,
// as encoding/json ignores unknown fields.
func Parse(data []byte) (*Config, error) {
	var raw struct {
		Site         json.RawMessage `json:"site"`
		Server       json.RawMessage `json:"server"`
		Embed        json.RawMessage `json:"embed"`
		CompressHTML json.RawMessage `json:"compressHTML"`
		Sourcemap    json.RawMessage `json:"sourcemap"`
		Watch        json.RawMessage `json:"watch"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		var syn *json.SyntaxError
		if errors.As(err, &syn) {
			line, col := lineCol(data, syn.Offset)
			return nil, fmt.Errorf("%s:%d:%d: %v", FileName, line, col, syn)
		}
		var typ *json.UnmarshalTypeError
		if errors.As(err, &typ) && typ.Field == "" {
			return nil, fmt.Errorf("%s: must be a JSON object", FileName)
		}
		return nil, fmt.Errorf("%s: %v", FileName, err)
	}
	c := &Config{}
	if isSet(raw.Site) {
		if json.Unmarshal(raw.Site, &c.Site) != nil {
			return nil, fmt.Errorf("%s: site must be a string", FileName)
		}
		if err := checkSite(c.Site); err != nil {
			return nil, err
		}
	}
	if isSet(raw.Server) {
		if json.Unmarshal(raw.Server, &c.Server) != nil {
			return nil, fmt.Errorf("%s: server must be a string", FileName)
		}
		if c.Server == "" {
			return nil, fmt.Errorf("%s: server must not be empty; remove it to use %s", FileName, DefaultServer)
		}
		if filepath.IsAbs(c.Server) {
			return nil, fmt.Errorf("%s: server %q must be a path relative to the project root, such as %q", FileName, c.Server, DefaultServer)
		}
	}
	if isSet(raw.Embed) {
		if json.Unmarshal(raw.Embed, &c.Embed) != nil {
			return nil, fmt.Errorf("%s: embed must be true or false", FileName)
		}
	}
	if isSet(raw.Sourcemap) {
		if json.Unmarshal(raw.Sourcemap, &c.Sourcemap) != nil {
			return nil, fmt.Errorf("%s: sourcemap must be true or false", FileName)
		}
	}
	if isSet(raw.Watch) {
		if json.Unmarshal(raw.Watch, &c.Watch) != nil {
			return nil, fmt.Errorf("%s: watch must be an array of strings", FileName)
		}
		for i, p := range c.Watch {
			if err := CheckWatch(p); err != nil {
				return nil, fmt.Errorf("%s: watch[%d] %q: %v", FileName, i, p, err)
			}
		}
	}
	if isSet(raw.CompressHTML) {
		var v any
		_ = json.Unmarshal(raw.CompressHTML, &v)
		switch v {
		case "html", true: // true is Astro's spelling of "html"
			c.CompressHTML = compiler.CompressHTML
		case "jsx":
			c.CompressHTML = compiler.CompressJSX
		case false:
			c.CompressHTML = compiler.CompressNone
		default:
			return nil, fmt.Errorf(`%s: compressHTML must be "html" (or true), "jsx" or false, not %s`, FileName, bytes.TrimSpace(raw.CompressHTML))
		}
	}
	return c, nil
}

// isSet reports whether a key is present with a non-null value; null is
// treated as unset, as encoding/json does.
func isSet(m json.RawMessage) bool {
	return len(m) > 0 && string(m) != "null"
}

func checkSite(site string) error {
	u, err := url.Parse(site)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return fmt.Errorf("%s: site %q isn't an absolute URL; include the scheme and host, such as \"https://example.com\"", FileName, site)
	}
	return nil
}

// lineCol converts a byte offset into a 1-based line and byte column.
// json.SyntaxError.Offset is the number of bytes read before the error, so
// the error is at the byte before it.
func lineCol(data []byte, off int64) (line, col int) {
	off = max(0, min(off-1, int64(len(data))))
	line, col = 1, 1
	for _, b := range data[:off] {
		if b == '\n' {
			line, col = line+1, 1
		} else {
			col++
		}
	}
	return line, col
}

// ServerEntry returns the server entry package, relative to root with a
// "./" prefix: Server if set, else DefaultServer if that directory exists,
// else GeneratedServer with generated true (PLAN.md §5b).
func (c *Config) ServerEntry(root string) (pkg string, generated bool) {
	if c.Server != "" {
		return c.Server, false
	}
	if fi, err := os.Stat(filepath.Join(root, filepath.FromSlash(DefaultServer))); err == nil && fi.IsDir() {
		return DefaultServer, false
	}
	return GeneratedServer, true
}
