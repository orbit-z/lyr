// Package posts is the blog's content: an in-memory store of posts, their
// likes and their comments. A real site would read these from a database.
package posts

import (
	"errors"
	"slices"
	"strings"
	"sync"
	"time"
)

// ErrNotFound is returned for an unknown slug.
var ErrNotFound = errors.New("posts: not found")

// Post is one blog post.
type Post struct {
	ID        int       `json:"id"`
	Slug      string    `json:"slug"`
	Title     string    `json:"title"`
	Summary   string    `json:"summary"`
	Body      string    `json:"-"` // trusted HTML, written by the authors
	Tags      []string  `json:"tags"`
	Published time.Time `json:"published"`
}

func date(s string) time.Time {
	t, err := time.Parse(time.DateOnly, s)
	if err != nil {
		panic(err)
	}
	return t
}

// all is sorted newest first.
var all = []Post{
	{
		ID: 4, Slug: "hello", Title: "Hello, Orbit",
		Summary:   "Why this blog renders every page on the server, in Go.",
		Body:      `<p>This blog is an <a href="https://github.com/orbit-z/orbit">Orbit</a> site: <code>.orbit</code> components with Go frontmatter, compiled to Go and rendered per request.</p><p>Scroll down for the share buttons and the comments, which load after the page.</p>`,
		Tags:      []string{"news"},
		Published: date("2026-10-20"),
	},
	{
		ID: 3, Slug: "go-templates", Title: "Go templates, typed",
		Summary:   "Template expressions are Go, checked by the Go compiler.",
		Body:      `<p>A typo in <code>{props.Titel}</code> fails the build at its line in the <code>.orbit</code> file.</p>`,
		Tags:      []string{"go", "guides"},
		Published: date("2026-10-12"),
	},
	{
		ID: 2, Slug: "safe-json", Title: "Why </script> can't break your JSON",
		Summary:   "set:json escapes what would end a script element early.",
		Body:      `<p>The search page embeds every post title with <code>set:json</code>, this one included.</p>`,
		Tags:      []string{"security"},
		Published: date("2026-10-05"),
	},
	{
		ID: 1, Slug: "server-defer", Title: "Deferring slow components",
		Summary:   "server:defer sends the page first and the slow part later.",
		Body:      `<p>Comments take a while to load, so the post page defers them.</p>`,
		Tags:      []string{"go", "performance"},
		Published: date("2026-09-28"),
	},
}

// All returns every post, newest first.
func All() ([]Post, error) { return all, nil }

// Find returns the post with the given slug, or ErrNotFound.
func Find(slug string) (Post, error) {
	for _, p := range all {
		if p.Slug == slug {
			return p, nil
		}
	}
	return Post{}, ErrNotFound
}

// Search returns the posts whose title or summary contains q, or that have
// q as a tag, ignoring case.
func Search(q string) []Post {
	q = strings.ToLower(strings.TrimSpace(q))
	if q == "" {
		return nil
	}
	var found []Post
	for _, p := range all {
		if strings.Contains(strings.ToLower(p.Title), q) ||
			strings.Contains(strings.ToLower(p.Summary), q) ||
			slices.Contains(p.Tags, q) {
			found = append(found, p)
		}
	}
	return found
}

var (
	mu    sync.Mutex
	likes = map[string]int{}
)

// Likes returns how many likes a post has.
func Likes(slug string) int {
	mu.Lock()
	defer mu.Unlock()
	return likes[slug]
}

// Like adds a like to a post and returns its new count, or ErrNotFound.
func Like(slug string) (int, error) {
	if _, err := Find(slug); err != nil {
		return 0, err
	}
	mu.Lock()
	defer mu.Unlock()
	likes[slug]++
	return likes[slug], nil
}
