package project

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/bertrandkhe/orbit/internal/compiler"
)

func TestRouteFor(t *testing.T) {
	tests := []struct {
		path  string
		role  compiler.Role
		route string
	}{
		{"src/pages/index.orbit", compiler.RolePage, "/"},
		{"src/pages/about.orbit", compiler.RolePage, "/about"},
		{"src/pages/blog/index.orbit", compiler.RolePage, "/blog"},
		{"src/pages/blog/[slug].orbit", compiler.RolePage, "/blog/[slug]"},
		{"src/pages/blog/[slug]/index.orbit", compiler.RolePage, "/blog/[slug]"},
		{"src/pages/docs/[...rest].orbit", compiler.RolePage, "/docs/[...rest]"},
		{"src/pages/404.orbit", compiler.RolePage, "404"},
		{"src/pages/blog/404.orbit", compiler.RolePage, "/blog/404"},
		{"src/pages/index/index.orbit", compiler.RolePage, "/index"},
		{"src/pages/_orbit/stats.orbit", compiler.RolePage, "/_orbit/stats"},
		{"src/components/Card.orbit", compiler.RoleComponent, ""},
		{"src/pagesx/a.orbit", compiler.RoleComponent, ""},
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
		{"/blog/post-[id]", `route segment "post-[id]" mixes text and a parameter; use a whole-segment parameter such as [id].orbit and parse it in the page`},
		{"/[lang]-[version]", `route segment "[lang]-[version]" mixes text and a parameter; use a whole-segment parameter such as [lang].orbit and parse it in the page`},
		{"/post-[id]/edit", `route segment "post-[id]" mixes text and a parameter; use a whole-segment parameter such as [id]/ and parse it in the page`},
		{"/x-[...rest]", `route segment "x-[...rest]" mixes text and a parameter; use a whole-segment parameter such as [rest].orbit and parse it in the page`},
		{"/[...path]/edit", `rest parameter [...path] must be the last segment of the route; use a whole-segment parameter such as [path] instead`},
		{"/_orbit/stats", `route /_orbit/stats is under /_orbit/, which Orbit reserves for its assets and islands; rename the page or its directory`},
		{"/_orbit", `route /_orbit is under /_orbit/, which Orbit reserves for its assets and islands; rename the page or its directory`},
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

// frontErrors loads a project of .orbit files (no Go build) and returns its
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
		{"mixed segment", map[string]string{"src/pages/blog/post-[id].orbit": "x"}, []string{
			`src/pages/blog/post-[id].orbit: route segment "post-[id]" mixes text and a parameter; use a whole-segment parameter such as [id].orbit and parse it in the page`,
		}},
		{"rest not last", map[string]string{"src/pages/[...path]/edit.orbit": "x"}, []string{
			`src/pages/[...path]/edit.orbit: rest parameter [...path] must be the last segment of the route; use a whole-segment parameter such as [path] instead`,
		}},
		{"bad param names", map[string]string{"src/pages/[1st]/[é].orbit": "x"}, []string{
			`src/pages/[1st]/[é].orbit: route param [1st] can't become a Go field name; use letters, digits, - and _, starting with a letter`,
			`src/pages/[1st]/[é].orbit: route param [é] can't become a Go field name; use letters, digits, - and _, starting with a letter`,
		}},
		{"colliding params", map[string]string{"src/pages/[post-id]/[postId].orbit": "x", "src/pages/x/[id]/[id].orbit": "y"}, []string{
			`src/pages/[post-id]/[postId].orbit: route params [post-id] and [postId] both become params.PostID; rename one`,
			`src/pages/x/[id]/[id].orbit: route params [id] and [id] both become params.ID; rename one`,
		}},
		{"orbit prefix", map[string]string{"src/pages/_orbit/stats.orbit": "x"}, []string{
			`src/pages/_orbit/stats.orbit: route /_orbit/stats is under /_orbit/, which Orbit reserves for its assets and islands; rename the page or its directory`,
		}},
		{"duplicate routes", map[string]string{
			"src/pages/blog.orbit": "a", "src/pages/blog/index.orbit": "b",
			"src/pages/[slug].orbit": "c", "src/pages/[id].orbit": "d",
		}, []string{
			`src/pages/[slug].orbit: route /[slug] is already defined by src/pages/[id].orbit; remove or rename one of them`,
			`src/pages/blog/index.orbit: route /blog is already defined by src/pages/blog.orbit; remove or rename one of them`,
		}},
		{"package collision", map[string]string{
			"src/components/my-card.orbit": "a", "src/components/my_card.orbit": "b",
			"src/pages/[slug].orbit": "c", "src/pages/slug_param.orbit": "d",
		}, []string{
			`src/components/my_card.orbit: generates the same Go package as src/components/my-card.orbit (orbit_gen/decl/components/my_card); rename one of them`,
			`src/pages/slug_param.orbit: generates the same Go package as src/pages/[slug].orbit (orbit_gen/decl/pages/slug_param); rename one of them`,
		}},
		{"all errors, sorted", map[string]string{
			"src/pages/z-[id].orbit":    "x",
			"src/pages/a.orbit":         "---\nimport Card \"../components/Card.orbit\"\n---\n<Card psot={1} />\n",
			"src/components/Card.orbit": "---\ntype Props struct{ Post int }\n---\n<p>{props.Post}</p>\n",
			"src/pages/b.orbit":         "---\nfunc init() {}\n---\n<p>",
		}, []string{
			`src/pages/a.orbit:4:7: unknown prop "psot" on <Card> (props: post)`,
			`src/pages/b.orbit:2:6: func init isn't allowed in a component; use a package under internal/ for initialization`,
			`src/pages/b.orbit:4:1: <p> isn't closed; add </p>`,
			`src/pages/z-[id].orbit: route segment "z-[id]" mixes text and a parameter; use a whole-segment parameter such as [id].orbit and parse it in the page`,
		}},
		{"props cycle", map[string]string{
			"src/components/A.orbit": "---\nimport B \"./B.orbit\"\n\ntype Props struct{ Next *B.Props }\n---\n<B />\n",
			"src/components/B.orbit": "---\nimport C \"./C.orbit\"\n\ntype Props struct{ Next *C.Props }\n---\n<C />\n",
			"src/components/C.orbit": "---\nimport A \"./A.orbit\"\n\ntype Props struct{ Next *A.Props }\n---\n<A />\n",
			// D renders A (impl only): not on the cycle.
			"src/components/D.orbit": "---\nimport A \"./A.orbit\"\n---\n<A />\n",
		}, []string{
			`src/components/A.orbit:2:8: import cycle not allowed between component Props: src/components/A.orbit → src/components/B.orbit → src/components/C.orbit → src/components/A.orbit; declare the shared types in a package under internal/`,
		}},
		{"mutual recursion is fine", map[string]string{
			"src/components/A.orbit": "---\nimport B \"./B.orbit\"\n\ntype Props struct{ N int }\n---\n<B n={props.N} />\n",
			"src/components/B.orbit": "---\nimport A \"./A.orbit\"\n\ntype Props struct{ N int }\n---\n{if props.N > 0}<A n={props.N - 1} />{/if}\n",
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
		"src/pages/index.orbit":         "",
		"src/pages/blog/[slug].orbit":   "",
		"src/components/Card.orbit":     "",
		"src/.hidden/X.orbit":           "",
		"src/components/.Y.orbit":       "",
		"src/components/notes.txt":      "",
		"src/locals/locals.go":          "package locals\n",
		"shared/Shared.orbit":           "",
		"src/components/sub/Deep.orbit": "",
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
		"src/components/Card.orbit",
		"src/components/sub/Deep.orbit",
		"src/pages/blog/[slug].orbit",
		"src/pages/index.orbit",
		"src/shared/Shared.orbit",
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
		"orbit_gen/decl/components/old/decl.go":   compiler.GenHeader + "\n\npackage old\n",
		"orbit_gen/impl/components/old/render.go": compiler.GenHeader + "\n\npackage old_impl\n",
		"orbit_gen/decl/mixed/decl.go":            compiler.GenHeader + "\n\npackage mixed\n",
		"orbit_gen/decl/mixed/notes.go":           "package mixed // mine\n",
		// A user file without the marker, and orbit build's static copy.
		"orbit_gen/user/keep.go":              "package user\n",
		"orbit_gen/static/client/app.js":      "x",
		"orbit_gen/static/static.go":          compiler.GenHeader + "\n\npackage static\n",
		"orbit_gen/decl/components/a/decl.go": "old content",
	})
	files := []GenFile{
		{Path: "orbit_gen/decl/components/a/decl.go", Content: []byte(compiler.GenHeader + "\n\npackage a\n")},
		{Path: "orbit_gen/app/app.go", Content: []byte(compiler.GenHeader + "\n\npackage app\n")},
	}
	res, err := WriteGenerated(p.Dir, files)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"orbit_gen/app/app.go", "orbit_gen/decl/components/a/decl.go"}; !slices.Equal(res.Written, want) {
		t.Errorf("written %q, want %q", res.Written, want)
	}
	wantRemoved := []string{"orbit_gen/decl/components/old/decl.go", "orbit_gen/decl/mixed/decl.go", "orbit_gen/impl/components/old/render.go"}
	if !slices.Equal(res.Removed, wantRemoved) {
		t.Errorf("removed %q, want %q", res.Removed, wantRemoved)
	}
	for _, gone := range []string{"orbit_gen/decl/components/old", "orbit_gen/impl"} {
		if p.exists(gone) {
			t.Errorf("%s still exists", gone)
		}
	}
	for _, kept := range []string{"orbit_gen/decl/mixed/notes.go", "orbit_gen/user/keep.go", "orbit_gen/static/client/app.js", "orbit_gen/static/static.go"} {
		if !p.exists(kept) {
			t.Errorf("%s was removed", kept)
		}
	}

	// Unchanged files aren't rewritten: their mtime stays.
	name := filepath.Join(p.Dir, "orbit_gen", "app", "app.go")
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
		t.Errorf("writing outside orbit_gen/ succeeded")
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
