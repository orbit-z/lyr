package project

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/orbit-z/lyr/internal/compiler"
)

// The .d.ts side table (PLAN.md §9b): every declaration line of the
// emitted types points at its source, a frontmatter type through the
// source map into the .lyr file, a user package's type at its .go file,
// and a key at its data-* attribute.
var typeSourcesFiles = map[string]string{
	"internal/posts/post.go": `package posts

// Post is a blog post.
type Post struct {
	Title string ` + "`json:\"title\"`" + `
}
`,
	"src/components/Like.lyr": `---
import "example.com/blog/internal/posts"

type like struct {
	Slug  string ` + "`json:\"slug\"`" + `
	Likes int    ` + "`json:\"likes\"`" + `
	Post  posts.Post ` + "`json:\"post\"`" + `
}
v := like{Slug: "a"}
---
<like-button data-like={lyr.JSON(v)}></like-button>
`,
	"src/pages/index.lyr": `---
import Like "../components/Like.lyr"
---
<Like />
`,
}

func TestTypeSources(t *testing.T) {
	needGo(t)
	p := newTestProject(t, typeSourcesFiles)
	proj, err := Load(p.Dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOFLAGS", "-mod=mod")
	t.Setenv("GOWORK", "off")
	proj.Front()
	if len(proj.Diagnostics) > 0 {
		t.Fatalf("diagnostics: %v", diagStrings(proj.Diagnostics))
	}
	check := func(t *testing.T, files []compiler.TSFile) {
		t.Helper()
		src := proj.Files[0].Src // src/components/Like.lyr sorts first
		if proj.Files[0].Path != "src/components/Like.lyr" {
			t.Fatalf("first file %s", proj.Files[0].Path)
		}
		byPath := map[string]compiler.TSFile{}
		for _, f := range files {
			byPath[f.Path] = f
		}
		lineOf := func(f compiler.TSFile, needle string) int {
			t.Helper()
			for i, l := range strings.Split(string(f.Content), "\n") {
				if strings.Contains(l, needle) {
					return i
				}
			}
			t.Fatalf("%s: no line with %q:\n%s", f.Path, needle, f.Content)
			return -1
		}
		goDTS := byPath["lyr_gen/types/go.d.ts"]
		lyrAt := func(f compiler.TSFile, needle, want string) {
			t.Helper()
			s, ok := f.Sources[lineOf(f, needle)]
			if !ok || !s.Lyr || s.File != "src/components/Like.lyr" || s.Start < 0 || s.End > len(src) {
				t.Fatalf("%s %q: source %+v", f.Path, needle, s)
			}
			if got := src[s.Start:s.End]; got != want {
				t.Errorf("%s %q: source covers %q, want %q", f.Path, needle, got, want)
			}
		}
		lyrAt(goDTS, "export interface like", "like")
		lyrAt(goDTS, "likes: number", "Likes")
		s := goDTS.Sources[lineOf(goDTS, "title: string")]
		if s.Lyr || filepath.Base(s.File) != "post.go" || s.Line != 5 || s.Name != "Title" {
			t.Errorf("title: source %+v, want post.go:5, Title", s)
		}
		unit := byPath["lyr_gen/types/src/components/Like.lyr.d.ts"]
		lyrAt(unit, "like: ", "data-like")
		lyrAt(unit, "export declare function data", "data-like")
	}
	t.Run("full typed pass", func(t *testing.T) {
		c := proj.Clone(compiler.Strict)
		c.Front()
		if _, err := c.Typecheck(context.Background(), compiler.NewChecker(&compiler.GoListLoader{Dir: p.Dir})); err != nil {
			t.Fatal(err)
		}
		check(t, c.TypeFiles())
	})
	t.Run("data scope", func(t *testing.T) {
		c := proj.Clone(compiler.Strict)
		c.Front()
		mc := NewMarkdownChecker(&compiler.GoListLoader{Dir: p.Dir})
		n, _, err := mc.TypecheckScope(context.Background(), c, CheckScope{Data: true})
		if err != nil {
			t.Fatal(err)
		}
		if n != 1 {
			t.Errorf("checked %d files, want 1 (Like.lyr; the page has no data)", n)
		}
		check(t, c.TypeFiles())
		// Again, through Update.
		c = proj.Clone(compiler.Strict)
		c.Front()
		if _, _, err := mc.TypecheckScope(context.Background(), c, CheckScope{Data: true}); err != nil {
			t.Fatal(err)
		}
		check(t, c.TypeFiles())
	})
}
