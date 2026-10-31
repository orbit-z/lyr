package assets

import (
	"encoding/json"
	"fmt"
	"path"
	"path/filepath"
	"strings"
)

// sourceMap is a version 3 source map, as esbuild writes it.
type sourceMap struct {
	Version        int       `json:"version"`
	File           string    `json:"file,omitempty"`
	SourceRoot     string    `json:"sourceRoot,omitempty"`
	Sources        []string  `json:"sources"`
	SourcesContent []*string `json:"sourcesContent,omitempty"`
	Mappings       string    `json:"mappings"`
	Names          []string  `json:"names"`
}

// fixSourceMap rewrites a source map from esbuild (name is its path under
// /_lyr/) so that it doesn't depend on where the project was built:
//
//   - sources inside the project become root-relative URLs,
//     "/src/components/Card.lyr" (as Vite, and so Astro, writes them);
//   - a .lyr source's content becomes the real .lyr file. The block's
//     module was padded to its .lyr position (plan.go), so the map's
//     original lines and columns already point into that file.
func (b *esbuildBundler) fixSourceMap(name string, data []byte, p *plan) ([]byte, error) {
	var sm sourceMap
	if err := json.Unmarshal(data, &sm); err != nil {
		return nil, fmt.Errorf("esbuild source map %s: %w", name, err)
	}
	dir := path.Dir(path.Join(filepath.ToSlash(b.outdir), name))
	root := filepath.ToSlash(b.opts.Root)
	for i, src := range sm.Sources {
		if strings.Contains(src, ":") { // a virtual module, lyr-page:index.ts
			continue
		}
		abs := path.Join(dir, src)
		rel, ok := strings.CutPrefix(abs, root+"/")
		if !ok {
			continue
		}
		sm.Sources[i] = "/" + rel
		if strings.HasSuffix(rel, ".lyr") && i < len(sm.SourcesContent) {
			if text, ok := p.lyrSrc[rel]; ok {
				sm.SourcesContent[i] = &text
			}
		}
	}
	return json.Marshal(sm)
}
