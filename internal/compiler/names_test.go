package compiler

import (
	"bytes"
	"strings"
	"testing"

	"github.com/orbit-z/lyr"
)

func TestPackageFor(t *testing.T) {
	tests := []struct {
		in, rel, name string
	}{
		{"src/components/Card.lyr", "components/card", "card"},
		{"src/components/my-card.lyr", "components/my_card", "my_card"},
		{"src/components/my.card.lyr", "components/my_card", "my_card"},
		{"src/pages/index.lyr", "pages/index", "index"},
		{"src/pages/blog/[slug].lyr", "pages/blog/slug_param", "slug_param"},
		{"src/pages/docs/[...rest].lyr", "pages/docs/rest_rest", "rest_rest"},
		{"src/pages/[lang]/about.lyr", "pages/lang_param/about", "about"},
		{"src/pages/[post-id].lyr", "pages/post_id_param", "post_id_param"},
		{"src/pages/404.lyr", "pages/p_404", "p_404"},
		{"src/components/default.lyr", "components/p_default", "p_default"},
		{"src/components/type.lyr", "components/p_type", "p_type"},
		{"src/main/x.lyr", "p_main/x", "x"},
		{"src/components/init.lyr", "components/p_init", "p_init"},
		{"src/internal/x.lyr", "p_internal/x", "x"},
		{"src/vendor/testdata/X.lyr", "p_vendor/p_testdata/x", "x"},
		{"src/components/_private.lyr", "components/p__private", "p__private"},
		{"src/components/Café.lyr", "components/caf_", "caf_"},
		{"src/layouts/Base.lyr", "layouts/base", "base"},
		{"src/pages/blog/hello.md", "pages/blog/hello_md", "hello_md"},
		{"src/pages/404.md", "pages/p_404_md", "p_404_md"},
		{"src/content/intro.markdown", "content/intro_md", "intro_md"},
	}
	for _, tt := range tests {
		got, err := PackageFor(tt.in)
		if err != nil {
			t.Errorf("PackageFor(%q): %v", tt.in, err)
			continue
		}
		if got.Rel != tt.rel || got.Name != tt.name {
			t.Errorf("PackageFor(%q) = %+v, want {%s %s}", tt.in, got, tt.rel, tt.name)
		}
	}
	for _, bad := range []string{"components/Card.lyr", "src/x.go", "src//x.lyr", "src/../x.lyr", "src/a/./b.lyr"} {
		if _, err := PackageFor(bad); err == nil {
			t.Errorf("PackageFor(%q): no error", bad)
		}
	}
}

func TestGenPackagePaths(t *testing.T) {
	p, err := PackageFor("src/components/Card.lyr")
	if err != nil {
		t.Fatal(err)
	}
	const mod = "example.com/blog"
	checks := map[string]string{
		p.ImplName():          "card_impl",
		p.DeclFile():          "lyr_gen/decl/components/card/decl.go",
		p.ImplFile():          "lyr_gen/impl/components/card/render.go",
		p.DeclImportPath(mod): "example.com/blog/lyr_gen/decl/components/card",
		p.ImplImportPath(mod): "example.com/blog/lyr_gen/impl/components/card",
		DirectiveFile(p.ImplFile(), "src/components/Card.lyr"):                                   "../../../../src/components/Card.lyr",
		DirectiveFile("lyr_gen/decl/pages/blog/slug_param/decl.go", "src/pages/blog/[slug].lyr"): "../../../../../src/pages/blog/[slug].lyr",
	}
	for got, want := range checks {
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	}
}

func TestComponentID(t *testing.T) {
	id := ComponentID("src/components/Comments.lyr")
	name, hash, ok := strings.Cut(id, "-")
	if !ok || name != "Comments" || len(hash) != 8 || hash != ScopeHash("src/components/Comments.lyr") {
		t.Errorf("ComponentID = %q", id)
	}
	if id := ComponentID("src/content/intro.md"); !strings.HasPrefix(id, "intro-") || len(id) != len("intro-")+8 {
		t.Errorf("ComponentID of a .md = %q", id)
	}
	if ScopeHash("src/a.lyr") == ScopeHash("src/b.lyr") {
		t.Error("ScopeHash doesn't depend on the path")
	}
	// sha256("src/components/Card.lyr")[:4], fixed so IDs never change.
	if got := ScopeHash("src/components/Card.lyr"); got != scopeHashCard {
		t.Errorf("ScopeHash(Card) = %s, want %s", got, scopeHashCard)
	}
}

const scopeHashCard = "58b63f2f"

func TestFieldName(t *testing.T) {
	tests := map[string]string{
		"slug": "Slug", "path": "Path", "id": "ID", "post-id": "PostID", "post_id": "PostID",
		"postId": "PostID", "api-url": "APIURL", "is-open": "IsOpen", "isOpen": "IsOpen",
		"is_open": "IsOpen", "user-id": "UserID", "userId": "UserID", "title": "Title",
		"HTTPServer": "HTTPServer", "featured": "Featured", "a--b": "AB", "-x-": "X", "utf8": "UTF8",
		"json-ld": "JSONLd", "uri": "URI",
	}
	for in, want := range tests {
		if got := FieldName(in); got != want {
			t.Errorf("FieldName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLowerCamel(t *testing.T) {
	tests := map[string]string{
		"Title": "title", "IsOpen": "isOpen", "ID": "id", "URL": "url", "UserID": "userID",
		"HTTPServer": "httpServer", "Post": "post", "X": "x", "": "",
	}
	for in, want := range tests {
		if got := LowerCamel(in); got != want {
			t.Errorf("LowerCamel(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMatchProp(t *testing.T) {
	props := &PropsInfo{Declared: true, Listable: true, Fields: []PropField{
		{Name: "Post"}, {Name: "IsOpen"}, {Name: "ID"}, {Name: "UserID"},
	}}
	for attr, want := range map[string]string{
		"post": "Post", "is-open": "IsOpen", "isOpen": "IsOpen", "is_open": "IsOpen", "id": "ID",
		"user-id": "UserID", "userId": "UserID", "POST": "Post",
	} {
		if got, err := MatchProp(props, attr); err != nil || got != want {
			t.Errorf("MatchProp(%q) = %q, %v, want %q", attr, got, err, want)
		}
	}
	if _, err := MatchProp(props, "psot"); err != ErrNoField {
		t.Errorf("MatchProp(psot) err = %v, want ErrNoField", err)
	}
	props.Listable = false
	if got, err := MatchProp(props, "api-url"); err != nil || got != "APIURL" {
		t.Errorf("fallback MatchProp(api-url) = %q, %v", got, err)
	}
	if got, _ := MatchProp(props, "is-open"); got != "IsOpen" {
		t.Errorf("fallback MatchProp(is-open) = %q, want the listed field", got)
	}
}

func TestIsReservedIdent(t *testing.T) {
	for name, want := range map[string]bool{"lyr__w": true, "Lyr__Render": true, "lyr": false, "lyr_x": false, "Render": false} {
		if IsReservedIdent(name) != want {
			t.Errorf("IsReservedIdent(%q) != %v", name, want)
		}
	}
}

// TestIsURLAttrMatchesRuntime checks IsURLAttr against the runtime's own URL
// filtering, so the typed URL/String choice mirrors Attr.
func TestIsURLAttrMatchesRuntime(t *testing.T) {
	names := append(urlAttrs[:], "HREF", "Src", "class", "title", "id", "alt", "srcset", "xlink:title", "data-href")
	for _, name := range names {
		var buf bytes.Buffer
		lyr.NewWriter(&buf).Attr(name, "javascript:alert(1)")
		filtered := strings.Contains(buf.String(), "about:invalid")
		if IsURLAttr(name) != filtered {
			t.Errorf("IsURLAttr(%q) = %v, runtime filters: %v", name, IsURLAttr(name), filtered)
		}
	}
}

// TestIsTrueFalseAttrMatchesRuntime checks IsTrueFalseAttr against Attr's
// bool rule, so the typed Bool pieces mirror it (PLAN.md §2.6 (Q3)).
func TestIsTrueFalseAttrMatchesRuntime(t *testing.T) {
	names := append(trueFalseAttrs[:], "aria-pressed", "ARIA-HIDDEN", "aria-x", "aria", "aria-", "Draggable",
		"hidden", "data-x", "translate", "autocomplete", "autocorrect", "popover", "download", "open")
	for _, name := range names {
		var buf bytes.Buffer
		lyr.NewWriter(&buf).Attr(name, false)
		tf := buf.String() != ""
		if IsTrueFalseAttr(name) != tf {
			t.Errorf("IsTrueFalseAttr(%q) = %v, runtime writes %q", name, IsTrueFalseAttr(name), buf.String())
		}
	}
}

// TestIsUnsafeURLMatchesRuntime checks the build-time URL filter (folded
// lyr.If constants) against Attr's.
func TestIsUnsafeURLMatchesRuntime(t *testing.T) {
	for _, u := range []string{"javascript:x", " JavaScript:x", "java\tscript:x", "\x01vbscript:x", "VBSCRIPT:", "/ok", "javascript", "https://x", "data:x", "jav\nascript:"} {
		var buf bytes.Buffer
		lyr.NewWriter(&buf).Attr("href", u)
		if got := strings.Contains(buf.String(), invalidURL); got != isUnsafeURL(u) {
			t.Errorf("isUnsafeURL(%q) = %v, runtime filters: %v", u, isUnsafeURL(u), got)
		}
	}
}
