package lsp

import (
	_ "embed"
	"encoding/json"
	"sync"
)

// The HTML data for tag and attribute completion (PLAN.md §9 Attribute
// position): a trimmed, hand-curated list of the current elements of the
// WHATWG HTML standard with their element-specific attributes, the global
// attributes (no event handlers: Orbit warns on on* attributes) and a few
// ARIA ones. The descriptions are Orbit's own.

//go:embed htmldata.json
var htmlDataJSON []byte

type htmlTag struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Attributes  []string `json:"attributes"`
}

type htmlData struct {
	GlobalAttributes  []string  `json:"globalAttributes"`
	BooleanAttributes []string  `json:"booleanAttributes"`
	Tags              []htmlTag `json:"tags"`

	byName  map[string]*htmlTag
	boolean map[string]bool
}

var html = sync.OnceValue(func() *htmlData {
	var d htmlData
	if err := json.Unmarshal(htmlDataJSON, &d); err != nil {
		panic("lsp: htmldata.json: " + err.Error())
	}
	d.byName = make(map[string]*htmlTag, len(d.Tags))
	for i := range d.Tags {
		d.byName[d.Tags[i].Name] = &d.Tags[i]
	}
	d.boolean = make(map[string]bool, len(d.BooleanAttributes))
	for _, a := range d.BooleanAttributes {
		d.boolean[a] = true
	}
	return &d
})
