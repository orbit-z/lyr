// Package content loads Orbit's Markdown documentation (docs/*.md and
// docs/reference/*.md) and renders it to HTML once, for the pages of the site.
//
// The Markdown comes from one of two places:
//
//   - the docs directory on disk: $DOCS_DIR, or else the nearest parent
//     directory of the working directory that looks like Orbit's docs/
//     (it has README.md, getting-started.md and reference/cli.md);
//   - an embedded copy in md/, written by `go generate ./internal/content`.
//
// The embedded copy is used when it exists, except in `orbit dev`
// (ORBIT_DEV=1) or when DOCS_DIR is set. Reading from disk, the docs are
// reloaded whenever a .md file changes, so an edit shows on the next request.
package content

//go:generate go run ./gen

import (
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/orbit-z/orbit"
)

//go:embed all:md
var embedded embed.FS

// Heading is a heading of a page, for its table of contents.
type Heading struct {
	Level int
	ID    string
	Text  string
}

// Section is the text under one heading (or before the first one), for search.
type Section struct {
	Heading string // "" before the first heading
	ID      string // the heading's anchor, "" before the first heading
	Text    string
	lower   string
}

// Page is one rendered Markdown file.
type Page struct {
	Slug     string // "" for README.md, "getting-started", "reference/cli"
	URL      string // "/", "/getting-started", "/reference/cli"
	File     string // the file under docs/: "reference/cli.md"
	Title    string // the first h1
	Summary  string // the first paragraph, as text
	HTML     orbit.HTML
	TOC      []Heading // h2 and h3
	Sections []Section

	ids map[string]bool
}

// NavItem is one link of the sidebar.
type NavItem struct {
	Title string
	URL   string
	Desc  string
}

// NavGroup is a titled group of sidebar links.
type NavGroup struct {
	Title string
	Items []NavItem
}

// Docs is the whole loaded documentation.
type Docs struct {
	Pages []*Page
	Nav   []NavGroup
	// Problems lists broken links and anchors found while rendering,
	// as "file: message".
	Problems []string
	// Source says where the Markdown came from: a directory or "embedded".
	Source string

	bySlug map[string]*Page
	order  []*Page // pages in sidebar order, for previous/next links
}

// Page returns the page with the given slug, or nil.
func (d *Docs) Page(slug string) *Page { return d.bySlug[slug] }

// Neighbors returns the pages before and after p in the sidebar order
// (either may be nil).
func (d *Docs) Neighbors(p *Page) (prev, next *Page) {
	i := slices.Index(d.order, p)
	if i < 0 {
		return nil, nil
	}
	if i > 0 {
		prev = d.order[i-1]
	}
	if i+1 < len(d.order) {
		next = d.order[i+1]
	}
	return prev, next
}

// Load reads and renders every docs page from fsys, which holds the
// contents of docs/.
func Load(fsys fs.FS) (*Docs, error) {
	files, err := fs.Glob(fsys, "*.md")
	if err != nil {
		return nil, err
	}
	more, err := fs.Glob(fsys, "reference/*.md")
	if err != nil {
		return nil, err
	}
	files = append(files, more...)
	if len(files) == 0 {
		return nil, errors.New("content: no .md files found")
	}
	d := &Docs{bySlug: map[string]*Page{}}
	r := newRenderer(d)
	for _, f := range files {
		src, err := fs.ReadFile(fsys, f)
		if err != nil {
			return nil, err
		}
		p, err := r.render(f, src)
		if err != nil {
			return nil, fmt.Errorf("content: %s: %w", f, err)
		}
		d.Pages = append(d.Pages, p)
		d.bySlug[p.Slug] = p
	}
	r.checkLinks()
	d.buildNav(r.navLinks)
	return d, nil
}

// SlugOf maps a file under docs/ to its page slug: "reference/cli.md" is
// "reference/cli", "README.md" is "" and "DECISIONS.md" is "decisions".
func SlugOf(file string) string {
	s := strings.ToLower(strings.TrimSuffix(file, ".md"))
	if s == "readme" {
		return ""
	}
	return s
}

// buildNav makes the sidebar from the link lists of README.md, grouped by
// its h2 headings. Pages README.md doesn't link go in a last "More" group.
func (d *Docs) buildNav(links []navLink) {
	seen := map[*Page]bool{}
	if home := d.bySlug[""]; home != nil {
		d.order = append(d.order, home)
		seen[home] = true
	}
	for _, l := range links {
		p := d.bySlug[l.slug]
		if p == nil || seen[p] {
			continue
		}
		seen[p] = true
		if len(d.Nav) == 0 || d.Nav[len(d.Nav)-1].Title != l.group {
			d.Nav = append(d.Nav, NavGroup{Title: l.group})
		}
		g := &d.Nav[len(d.Nav)-1]
		g.Items = append(g.Items, NavItem{Title: l.title, URL: p.URL, Desc: l.desc})
		d.order = append(d.order, p)
	}
	var rest []NavItem
	for _, p := range d.Pages {
		if !seen[p] {
			rest = append(rest, NavItem{Title: p.Title, URL: p.URL})
			d.order = append(d.order, p)
		}
	}
	if len(rest) > 0 {
		d.Nav = append(d.Nav, NavGroup{Title: "More", Items: rest})
	}
}

// --- the shared instance -----------------------------------------------

var (
	mu      sync.Mutex
	current *Docs
	dir     string    // the disk directory in use, "" for the embedded copy
	stamp   time.Time // the newest modification time seen in dir
	loadErr error
)

// Get returns the documentation, loading it on first use. When it comes
// from disk, it's reloaded if a .md file changed since the last call.
func Get() (*Docs, error) {
	mu.Lock()
	defer mu.Unlock()
	if current == nil && loadErr == nil {
		current, loadErr = loadDefault()
		return current, loadErr
	}
	if dir != "" {
		if t := newest(dir); t.After(stamp) {
			if d, err := Load(os.DirFS(dir)); err == nil {
				d.Source = dir
				current, stamp = d, t
			}
		}
	}
	return current, loadErr
}

func loadDefault() (*Docs, error) {
	useDisk := os.Getenv("DOCS_DIR") != "" || os.Getenv("ORBIT_DEV") == "1"
	if !useDisk {
		if sub, err := fs.Sub(embedded, "md"); err == nil {
			if names, _ := fs.Glob(sub, "*.md"); len(names) > 0 {
				d, err := Load(sub)
				if d != nil {
					d.Source = "embedded"
				}
				return d, err
			}
		}
	}
	found, err := findDocsDir()
	if err != nil {
		return nil, err
	}
	dir = found
	stamp = newest(dir)
	d, err := Load(os.DirFS(dir))
	if d != nil {
		d.Source = dir
	}
	return d, err
}

// findDocsDir returns $DOCS_DIR, or the nearest parent of the working
// directory that holds Orbit's docs.
func findDocsDir() (string, error) {
	if d := os.Getenv("DOCS_DIR"); d != "" {
		return filepath.Abs(d)
	}
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for d := wd; ; d = filepath.Dir(d) {
		if isDocsDir(d) {
			return d, nil
		}
		if filepath.Dir(d) == d {
			return "", fmt.Errorf("content: no embedded docs and no docs directory above %s; run go generate ./internal/content or set DOCS_DIR", wd)
		}
	}
}

func isDocsDir(d string) bool {
	for _, f := range []string{"README.md", "getting-started.md", "reference/cli.md"} {
		if _, err := os.Stat(filepath.Join(d, f)); err != nil {
			return false
		}
	}
	return true
}

// newest returns the newest modification time of the .md files that Load reads.
func newest(dir string) time.Time {
	var t time.Time
	for _, pattern := range []string{"*.md", "reference/*.md"} {
		files, _ := filepath.Glob(filepath.Join(dir, filepath.FromSlash(pattern)))
		for _, f := range files {
			if fi, err := os.Stat(f); err == nil && fi.ModTime().After(t) {
				t = fi.ModTime()
			}
		}
	}
	return t
}

// pageDir is the directory of a docs file, for resolving relative links.
func pageDir(file string) string { return path.Dir(file) }
