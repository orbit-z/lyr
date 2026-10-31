package project

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/orbit-z/lyr/internal/compiler"
)

func TestRouteFor(t *testing.T) {
	tests := []struct {
		path  string
		role  compiler.Role
		route string
	}{
		{"src/pages/index.lyr", compiler.RolePage, "/"},
		{"src/pages/about.lyr", compiler.RolePage, "/about"},
		{"src/pages/blog/index.lyr", compiler.RolePage, "/blog"},
		{"src/pages/blog/[slug].lyr", compiler.RolePage, "/blog/[slug]"},
		{"src/pages/blog/[slug]/index.lyr", compiler.RolePage, "/blog/[slug]"},
		{"src/pages/docs/[...rest].lyr", compiler.RolePage, "/docs/[...rest]"},
		{"src/pages/404.lyr", compiler.RolePage, "404"},
		{"src/pages/blog/404.lyr", compiler.RolePage, "/blog/404"},
		{"src/pages/index/index.lyr", compiler.RolePage, "/index"},
		// As in Astro, a leading "_" on the file or a directory keeps it out
		// of routing: it's a component.
		{"src/pages/_lyr/stats.lyr", compiler.RoleComponent, ""},
		{"src/pages/_Draft.lyr", compiler.RoleComponent, ""},
		{"src/pages/blog/_parts/Byline.lyr", compiler.RoleComponent, ""},
		{"src/pages/blog/x_y.lyr", compiler.RolePage, "/blog/x_y"},
		{"src/components/Card.lyr", compiler.RoleComponent, ""},
		{"src/pagesx/a.lyr", compiler.RoleComponent, ""},
	}
	for _, tt := range tests {
		role, route := RouteFor(tt.path)
		if role != tt.role || route != tt.route {
			t.Errorf("RouteFor(%s) = %v %q, want %v %q", tt.path, role, route, tt.role, tt.route)
		}
	}
}

func TestCheckRoute(t *testing.T) {
	tests := []struct{ pattern, want string }{
		{"/", ""},
		{"404", ""},
		{"/blog/[slug]", ""},
		{"/docs/[...path]", ""},
		{"/[a]/[b]/[...c]", ""},
		{"/blog/post-[id]", `route segment "post-[id]" mixes text and a parameter; use a whole-segment parameter such as [id].lyr and parse it in the page`},
		{"/[lang]-[version]", `route segment "[lang]-[version]" mixes text and a parameter; use a whole-segment parameter such as [lang].lyr and parse it in the page`},
		{"/post-[id]/edit", `route segment "post-[id]" mixes text and a parameter; use a whole-segment parameter such as [id]/ and parse it in the page`},
		{"/x-[...rest]", `route segment "x-[...rest]" mixes text and a parameter; use a whole-segment parameter such as [rest].lyr and parse it in the page`},
		{"/[...path]/edit", `rest parameter [...path] must be the last segment of the route; use a whole-segment parameter such as [path] instead`},
		{"/_lyr/stats", `route /_lyr/stats is under /_lyr/, which Lyr reserves for its assets and islands; rename the page or its directory`},
		{"/_lyr", `route /_lyr is under /_lyr/, which Lyr reserves for its assets and islands; rename the page or its directory`},
		{"/[slug", `invalid route pattern "/[slug": segment "[slug" has unbalanced brackets`},
		{"/a]", `invalid route pattern "/a]": segment "a]" has unbalanced brackets`},
		{"/[]", `invalid route pattern "/[]": segment "[]" has an empty parameter name`},
	}
	for _, tt := range tests {
		if got := checkRoute(tt.pattern); got != tt.want {
			t.Errorf("checkRoute(%s) = %q, want %q", tt.pattern, got, tt.want)
		}
	}
}

// frontErrors loads a project of .lyr files (no Go build) and returns its
// diagnostics.
func frontErrors(t *testing.T, files map[string]string) []string {
	t.Helper()
	p := newTestProject(t, files)
	proj, err := Load(p.Dir)
	if err != nil {
		t.Fatal(err)
	}
	proj.Front()
	return diagStrings(proj.Diagnostics)
}

func TestProjectErrors(t *testing.T) {
	tests := []struct {
		name  string
		files map[string]string
		want  []string
	}{
		{"mixed segment", map[string]string{"src/pages/blog/post-[id].lyr": "x"}, []string{
			`src/pages/blog/post-[id].lyr: route segment "post-[id]" mixes text and a parameter; use a whole-segment parameter such as [id].lyr and parse it in the page`,
		}},
		{"rest not last", map[string]string{"src/pages/[...path]/edit.lyr": "x"}, []string{
			`src/pages/[...path]/edit.lyr: rest parameter [...path] must be the last segment of the route; use a whole-segment parameter such as [path] instead`,
		}},
		{"bad param names", map[string]string{"src/pages/[1st]/[é].lyr": "x"}, []string{
			`src/pages/[1st]/[é].lyr: route param [1st] can't become a Go field name; use letters, digits, - and _, starting with a letter`,
			`src/pages/[1st]/[é].lyr: route param [é] can't become a Go field name; use letters, digits, - and _, starting with a letter`,
		}},
		{"colliding params", map[string]string{"src/pages/[post-id]/[postId].lyr": "x", "src/pages/x/[id]/[id].lyr": "y"}, []string{
			`src/pages/[post-id]/[postId].lyr: route params [post-id] and [postId] both become params.PostID; rename one`,
			`src/pages/x/[id]/[id].lyr: route params [id] and [id] both become params.ID; rename one`,
		}},
		{"underscore files aren't routes", map[string]string{"src/pages/_lyr/stats.lyr": "x", "src/pages/_a.lyr": "y", "src/pages/_b.lyr": "z"}, nil},
		{"duplicate routes", map[string]string{
			"src/pages/blog.lyr": "a", "src/pages/blog/index.lyr": "b",
			"src/pages/[slug].lyr": "c", "src/pages/[id].lyr": "d",
		}, []string{
			`src/pages/[slug].lyr: route /[slug] is already defined by src/pages/[id].lyr; remove or rename one of them`,
			`src/pages/blog/index.lyr: route /blog is already defined by src/pages/blog.lyr; remove or rename one of them`,
		}},
		{"package collision", map[string]string{
			"src/components/my-card.lyr": "a", "src/components/my_card.lyr": "b",
			"src/pages/[slug].lyr": "c", "src/pages/slug_param.lyr": "d",
		}, []string{
			`src/components/my_card.lyr: generates the same Go package as src/components/my-card.lyr (lyr_gen/decl/components/my_card); rename one of them`,
			`src/pages/slug_param.lyr: generates the same Go package as src/pages/[slug].lyr (lyr_gen/decl/pages/slug_param); rename one of them`,
		}},
		{"all errors, sorted", map[string]string{
			"src/pages/z-[id].lyr":    "x",
			"src/pages/a.lyr":         "---\nimport Card \"../components/Card.lyr\"\n---\n<Card psot={1} />\n",
			"src/components/Card.lyr": "---\ntype Props struct{ Post int }\n---\n<p>{props.Post}</p>\n",
			"src/pages/b.lyr":         "---\nfunc init() {}\n---\n<p>",
		}, []string{
			`src/pages/a.lyr:4:7: unknown prop "psot" on <Card> (props: post; add a lyr.RestAttrs field to pass HTML attributes through)`,
			`src/pages/b.lyr:2:6: func init isn't allowed in a component; use a package under internal/ for initialization`,
			`src/pages/b.lyr:4:1: <p> isn't closed; add </p>`,
			`src/pages/z-[id].lyr: route segment "z-[id]" mixes text and a parameter; use a whole-segment parameter such as [id].lyr and parse it in the page`,
		}},
		{"props cycle", map[string]string{
			"src/components/A.lyr": "---\nimport B \"./B.lyr\"\n\ntype Props struct{ Next *B.Props }\n---\n<B />\n",
			"src/components/B.lyr": "---\nimport C \"./C.lyr\"\n\ntype Props struct{ Next *C.Props }\n---\n<C />\n",
			"src/components/C.lyr": "---\nimport A \"./A.lyr\"\n\ntype Props struct{ Next *A.Props }\n---\n<A />\n",
			// D renders A (impl only): not on the cycle.
			"src/components/D.lyr": "---\nimport A \"./A.lyr\"\n---\n<A />\n",
		}, []string{
			`src/components/A.lyr:2:8: import cycle not allowed between component Props: src/components/A.lyr → src/components/B.lyr → src/components/C.lyr → src/components/A.lyr; declare the shared types in a package under internal/`,
		}},
		{"mutual recursion is fine", map[string]string{
			"src/components/A.lyr": "---\nimport B \"./B.lyr\"\n\ntype Props struct{ N int }\n---\n<B n={props.N} />\n",
			"src/components/B.lyr": "---\nimport A \"./A.lyr\"\n\ntype Props struct{ N int }\n---\n{if props.N > 0}<A n={props.N - 1} />{/if}\n",
		}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := frontErrors(t, tt.files)
			if !slices.Equal(got, tt.want) {
				t.Errorf("got:\n%s\nwant:\n%s", joinLines(got), joinLines(tt.want))
			}
		})
	}
}

func TestDiscover(t *testing.T) {
	p := newTestProject(t, map[string]string{
		"src/pages/index.lyr":         "",
		"src/pages/blog/[slug].lyr":   "",
		"src/components/Card.lyr":     "",
		"src/.hidden/X.lyr":           "",
		"src/components/.Y.lyr":       "",
		"src/components/notes.txt":    "",
		"src/locals/locals.go":        "package locals\n",
		"shared/Shared.lyr":           "",
		"src/components/sub/Deep.lyr": "",
	})
	// A symlinked directory is followed; a loop back to src isn't.
	if err := os.Symlink(filepath.Join(p.Dir, "shared"), filepath.Join(p.Dir, "src", "shared")); err != nil {
		t.Skip("symlinks unsupported:", err)
	}
	if err := os.Symlink(filepath.Join(p.Dir, "src"), filepath.Join(p.Dir, "src", "components", "loop")); err != nil {
		t.Fatal(err)
	}
	got, err := Discover(p.Dir)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"src/components/Card.lyr",
		"src/components/sub/Deep.lyr",
		"src/pages/blog/[slug].lyr",
		"src/pages/index.lyr",
		"src/shared/Shared.lyr",
	}
	if !slices.Equal(got, want) {
		t.Errorf("Discover = %q, want %q", got, want)
	}
	if _, err := Discover(t.TempDir()); err == nil {
		t.Errorf("Discover without src/ succeeded")
	}
}

func TestWriteGenerated(t *testing.T) {
	p := newTestProject(t, map[string]string{
		// Stale generated files, in a directory that becomes empty and in
		// one that keeps a user file.
		"lyr_gen/decl/components/old/decl.go":   compiler.GenHeader + "\n\npackage old\n",
		"lyr_gen/impl/components/old/render.go": compiler.GenHeader + "\n\npackage old_impl\n",
		"lyr_gen/decl/mixed/decl.go":            compiler.GenHeader + "\n\npackage mixed\n",
		"lyr_gen/decl/mixed/notes.go":           "package mixed // mine\n",
		// A user file without the marker, and lyr build's static copy.
		"lyr_gen/user/keep.go":              "package user\n",
		"lyr_gen/static/client/app.js":      "x",
		"lyr_gen/static/static.go":          compiler.GenHeader + "\n\npackage static\n",
		"lyr_gen/decl/components/a/decl.go": "old content",
	})
	files := []GenFile{
		{Path: "lyr_gen/decl/components/a/decl.go", Content: []byte(compiler.GenHeader + "\n\npackage a\n")},
		{Path: "lyr_gen/app/app.go", Content: []byte(compiler.GenHeader + "\n\npackage app\n")},
	}
	res, err := WriteGenerated(p.Dir, files)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"lyr_gen/app/app.go", "lyr_gen/decl/components/a/decl.go"}; !slices.Equal(res.Written, want) {
		t.Errorf("written %q, want %q", res.Written, want)
	}
	wantRemoved := []string{"lyr_gen/decl/components/old/decl.go", "lyr_gen/decl/mixed/decl.go", "lyr_gen/impl/components/old/render.go"}
	if !slices.Equal(res.Removed, wantRemoved) {
		t.Errorf("removed %q, want %q", res.Removed, wantRemoved)
	}
	for _, gone := range []string{"lyr_gen/decl/components/old", "lyr_gen/impl"} {
		if p.exists(gone) {
			t.Errorf("%s still exists", gone)
		}
	}
	for _, kept := range []string{"lyr_gen/decl/mixed/notes.go", "lyr_gen/user/keep.go", "lyr_gen/static/client/app.js", "lyr_gen/static/static.go"} {
		if !p.exists(kept) {
			t.Errorf("%s was removed", kept)
		}
	}

	// Unchanged files aren't rewritten: their mtime stays.
	name := filepath.Join(p.Dir, "lyr_gen", "app", "app.go")
	old := mustStat(t, name).ModTime().Add(-time.Hour)
	if err := os.Chtimes(name, old, old); err != nil {
		t.Fatal(err)
	}
	res, err = WriteGenerated(p.Dir, files)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Written) != 0 || len(res.Removed) != 0 {
		t.Errorf("second write: written %q, removed %q", res.Written, res.Removed)
	}
	if !mustStat(t, name).ModTime().Equal(old) {
		t.Errorf("unchanged app.go was rewritten")
	}
	if _, err := WriteGenerated(p.Dir, []GenFile{{Path: "src/x.go"}}); err == nil {
		t.Errorf("writing outside lyr_gen/ succeeded")
	}
}

func mustStat(t *testing.T, name string) os.FileInfo {
	t.Helper()
	fi, err := os.Stat(name)
	if err != nil {
		t.Fatal(err)
	}
	return fi
}

func TestHasMarker(t *testing.T) {
	dir := t.TempDir()
	for content, want := range map[string]bool{
		compiler.GenHeader + "\n\npackage x\n": true,
		compiler.GenHeader:                     true,
		compiler.GenHeader + "\r\n":            true,
		compiler.GenHeader + " (not quite)\n":  false,
		"package x\n" + compiler.GenHeader:     false,
		"":                                     false,
	} {
		name := filepath.Join(dir, "f.go")
		if err := os.WriteFile(name, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		if got, err := hasMarker(name); err != nil || got != want {
			t.Errorf("hasMarker(%q) = %v, %v; want %v", content, got, err, want)
		}
	}
}

// TestClone: a clone has its own Files with the same sources and its own
// mode; going through Front leaves the original untouched.
func TestClone(t *testing.T) {
	tp := newTestProject(t, map[string]string{
		"src/pages/index.lyr": "---\nx := 1\n---\n<p>{x}</p>\n",
	})
	p, err := Load(tp.Dir)
	if err != nil {
		t.Fatal(err)
	}
	p.Options.Mode = compiler.Tolerant
	p.Front()
	c := p.Clone(compiler.Strict)
	if c.Options.Mode != compiler.Strict || p.Options.Mode != compiler.Tolerant {
		t.Fatalf("modes: clone %v, original %v", c.Options.Mode, p.Options.Mode)
	}
	if len(c.Files) != 1 || c.Files[0] == p.Files[0] || c.Files[0].Src != p.Files[0].Src || c.Files[0].Pass1 != nil {
		t.Fatalf("clone files: %+v", c.Files)
	}
	pass1 := p.Files[0].Pass1
	c.Files[0].Src = "---\ny := 2\n---\n<p>{y}</p>\n"
	c.Front()
	if p.Files[0].Pass1 != pass1 || strings.Contains(string(p.Files[0].Pass1.Impl.Content), "y := 2") {
		t.Error("Front on the clone changed the original")
	}
	if !strings.Contains(string(c.Files[0].Pass1.Impl.Content), "y := 2") {
		t.Error("the clone wasn't regenerated from its own source")
	}
}
