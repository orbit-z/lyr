package config

import (
	"encoding/json"
	"fmt"

	"github.com/orbit-z/orbit/internal/markdown"
)

// parseMarkdown parses the "markdown" key (PLAN.md §8): gfm, smartypants,
// and syntaxHighlight (false, or {"themes": {"light", "dark"}, "css"}).
// null means unset, except for themes.dark, where it means one theme.
func parseMarkdown(data json.RawMessage) (markdown.Options, error) {
	var o markdown.Options
	var raw struct {
		GFM             json.RawMessage `json:"gfm"`
		Smartypants     json.RawMessage `json:"smartypants"`
		SyntaxHighlight json.RawMessage `json:"syntaxHighlight"`
	}
	if json.Unmarshal(data, &raw) != nil {
		return o, fmt.Errorf("%s: markdown must be an object", FileName)
	}
	boolKey := func(m json.RawMessage, name string, off *bool) error {
		if !isSet(m) {
			return nil
		}
		var v bool
		if json.Unmarshal(m, &v) != nil {
			return fmt.Errorf("%s: markdown.%s must be true or false", FileName, name)
		}
		*off = !v
		return nil
	}
	if err := boolKey(raw.GFM, "gfm", &o.NoGFM); err != nil {
		return o, err
	}
	if err := boolKey(raw.Smartypants, "smartypants", &o.NoSmartypants); err != nil {
		return o, err
	}
	if !isSet(raw.SyntaxHighlight) {
		return o, nil
	}
	var on bool
	if json.Unmarshal(raw.SyntaxHighlight, &on) == nil {
		o.NoHighlight = !on // true is the default
		return o, nil
	}
	var sh struct {
		Themes json.RawMessage `json:"themes"`
		CSS    json.RawMessage `json:"css"`
	}
	if json.Unmarshal(raw.SyntaxHighlight, &sh) != nil {
		return o, fmt.Errorf(`%s: markdown.syntaxHighlight must be false or an object such as {"themes": {"light": "github", "dark": "github-dark"}}`, FileName)
	}
	css := true
	if isSet(sh.CSS) {
		if json.Unmarshal(sh.CSS, &css) != nil {
			return o, fmt.Errorf("%s: markdown.syntaxHighlight.css must be true or false", FileName)
		}
	}
	o.NoCSS = !css
	if !isSet(sh.Themes) {
		return o, nil
	}
	var themes map[string]json.RawMessage
	if json.Unmarshal(sh.Themes, &themes) != nil {
		return o, fmt.Errorf(`%s: markdown.syntaxHighlight.themes must be an object such as {"light": "github", "dark": "github-dark"}`, FileName)
	}
	theme := func(name string, into *string) error {
		m, ok := themes[name]
		if !ok || !isSet(m) {
			return nil
		}
		if json.Unmarshal(m, into) != nil {
			return fmt.Errorf("%s: markdown.syntaxHighlight.themes.%s must be a theme name", FileName, name)
		}
		if err := markdown.CheckTheme(*into); err != nil {
			return fmt.Errorf("%s: markdown.syntaxHighlight.themes.%s: %v", FileName, name, err)
		}
		return nil
	}
	if err := theme("light", &o.Light); err != nil {
		return o, err
	}
	if m, ok := themes["dark"]; ok && !isSet(m) {
		o.SingleTheme = true // "dark": null
	} else if err := theme("dark", &o.Dark); err != nil {
		return o, err
	}
	return o, nil
}
