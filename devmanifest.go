package orbit

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"sync"
)

// devManifestName is the dev manifest's name in $ORBIT_STATIC, written by
// orbit dev after every asset rebuild (PLAN.md §5 Manifest).
const devManifestName = "manifest.json"

// devManifest gives the page assets in dev (ORBIT_DEV=1 with ORBIT_STATIC
// set): they come from $ORBIT_STATIC/manifest.json instead of the compiled
// manifest, so a CSS/JS rebuild with new hashes needs no Go rebuild. The
// file is stat'ed on every page request and re-read when it changed. It is
// never used in production.
type devManifest struct {
	path string

	mu     sync.Mutex
	info   os.FileInfo // of the loaded file; nil when none is loaded
	pages  map[string][]Asset
	warned bool
}

// devManifestJSON is the file's shape (internal/assets.WriteDev).
type devManifestJSON struct {
	Pages map[string][]struct {
		Kind string `json:"kind"`
		URL  string `json:"url"`
		ID   string `json:"id"`
	} `json:"pages"`
}

func newDevManifest(staticDir string) *devManifest {
	return &devManifest{path: filepath.Join(staticDir, devManifestName)}
}

// assets returns the route's assets from the dev manifest, or the compiled
// ones when there is no readable manifest.
func (m *devManifest) assets(r *Route) []Asset {
	info, err := os.Stat(m.path)
	m.mu.Lock()
	defer m.mu.Unlock()
	if err != nil {
		m.info, m.pages = nil, nil
		return r.Assets
	}
	if m.info == nil || !os.SameFile(m.info, info) || !m.info.ModTime().Equal(info.ModTime()) || m.info.Size() != info.Size() {
		m.load(info)
	}
	if m.info == nil {
		return r.Assets
	}
	return m.pages[r.File]
}

// load reads the manifest. A file that doesn't parse keeps the previous
// one (orbit dev writes it atomically, so that is a bug), with one log line.
func (m *devManifest) load(info os.FileInfo) {
	data, err := os.ReadFile(m.path)
	var j devManifestJSON
	if err == nil {
		err = json.Unmarshal(data, &j)
	}
	if err != nil {
		if !m.warned {
			log.Printf("orbit: dev manifest %s: %v", m.path, err)
			m.warned = true
		}
		return
	}
	pages := make(map[string][]Asset, len(j.Pages))
	for file, as := range j.Pages {
		list := make([]Asset, len(as))
		for i, a := range as {
			list[i] = Asset{Kind: a.Kind, URL: a.URL, ID: a.ID}
		}
		pages[file] = list
	}
	m.info, m.pages, m.warned = info, pages, false
}
