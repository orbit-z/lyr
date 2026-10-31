package assets

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
)

// DevManifest is the dev manifest's file name, in the dev output directory
// next to _lyr/ (PLAN.md §5 Manifest). The app reads it from
// $LYR_STATIC with LYR_DEV=1.
const DevManifest = "manifest.json"

// WriteFiles writes the output files into dir (lyr build's staging
// client/_lyr), creating it.
func WriteFiles(dir string, files []File) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for _, f := range files {
		name := filepath.Join(dir, filepath.FromSlash(f.Name))
		if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(name, f.Contents, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// devManifest is the JSON form of the dev manifest. The runtime decodes the
// same shape (site.go).
type devManifest struct {
	Pages map[string][]devAsset `json:"pages"`
}

type devAsset struct {
	Kind string `json:"kind"`
	URL  string `json:"url"`
	ID   string `json:"id,omitempty"`
}

// WriteDev writes a dev build into devDir (.lyr/dev): the files into
// devDir/_lyr/, then devDir/manifest.json, atomically (a temporary file
// and a rename), then it removes the files of earlier builds. In that
// order, a page rendered at any moment links files that exist.
func WriteDev(devDir string, res *Result) error {
	out := filepath.Join(devDir, "_lyr")
	if err := os.MkdirAll(out, 0o755); err != nil {
		return err
	}
	keep := map[string]bool{}
	for _, f := range res.Files {
		keep[filepath.FromSlash(f.Name)] = true
		name := filepath.Join(out, filepath.FromSlash(f.Name))
		if old, err := os.ReadFile(name); err == nil && bytes.Equal(old, f.Contents) {
			continue // hashed names: usually unchanged
		}
		if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
			return err
		}
		if err := writeAtomic(name, f.Contents); err != nil {
			return err
		}
	}

	m := devManifest{Pages: map[string][]devAsset{}}
	for page, as := range res.Pages {
		for _, a := range as {
			m.Pages[page] = append(m.Pages[page], devAsset(a))
		}
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	if err := writeAtomic(filepath.Join(devDir, DevManifest), append(data, '\n')); err != nil {
		return err
	}

	var stale []string
	err = filepath.WalkDir(out, func(name string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(out, name)
		if err != nil {
			return err
		}
		if !keep[rel] {
			stale = append(stale, name)
		}
		return nil
	})
	if err != nil {
		return err
	}
	slices.Sort(stale)
	for _, name := range stale {
		if err := os.Remove(name); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

// writeAtomic writes data to name through a temporary file in the same
// directory and a rename, so readers see the old or the new file.
func writeAtomic(name string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(name), ".tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Chmod(tmp, 0o644); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, name); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}
