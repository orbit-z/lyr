// Package markdown compiles Markdown files (PLAN.md §2.4): YAML frontmatter
// (go.yaml.in/yaml/v3), the body to HTML with goldmark (CommonMark, GFM,
// footnotes, smart punctuation, raw HTML), github-slugger heading IDs,
// relative .md link rewriting, and code blocks highlighted with Chroma
// classes when Lyr generates code; then the Go declaration and
// implementation packages of the file. It is part of the CLI only: user
// servers never link it.
package markdown

import (
	"fmt"
	"slices"
	"strings"

	"github.com/alecthomas/chroma/v2/styles"
)

// Default Chroma themes (PLAN.md §8).
const (
	DefaultLight = "github"
	DefaultDark  = "github-dark"
)

// Options are lyr.json's "markdown" key (PLAN.md §8). The zero value is
// the default: GFM and smart punctuation on, highlighting with the
// github / github-dark themes and their CSS.
type Options struct {
	NoGFM, NoSmartypants bool
	// NoHighlight is "syntaxHighlight": false.
	NoHighlight bool
	// Light and Dark are the Chroma theme names, "" for the defaults;
	// SingleTheme is "dark": null (the light theme everywhere).
	Light, Dark string
	SingleTheme bool
	// NoCSS is "css": false: token classes but no stylesheet.
	NoCSS bool
}

// LightTheme and DarkTheme return the theme names in use; DarkTheme is ""
// with SingleTheme.
func (o Options) LightTheme() string {
	if o.Light == "" {
		return DefaultLight
	}
	return o.Light
}

func (o Options) DarkTheme() string {
	switch {
	case o.SingleTheme:
		return ""
	case o.Dark == "":
		return DefaultDark
	}
	return o.Dark
}

// CheckTheme returns an error for a Chroma theme name that doesn't exist,
// listing the available ones.
func CheckTheme(name string) error {
	if _, ok := styles.Registry[strings.ToLower(name)]; ok {
		return nil
	}
	names := styles.Names()
	slices.Sort(names)
	return fmt.Errorf("unknown theme %q; the themes are %s", name, strings.Join(names, ", "))
}
