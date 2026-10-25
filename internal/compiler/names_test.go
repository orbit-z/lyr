package compiler

import (
	"bytes"
	"strings"
	"testing"

	"github.com/bertrandkhe/orbit"
)

func TestPackageFor(t *testing.T) {
	tests := []struct {
		in, rel, name string
	}{
		{"src/components/Card.orbit", "components/card", "card"},
		{"src/components/my-card.orbit", "components/my_card", "my_card"},
		{"src/components/my.card.orbit", "components/my_card", "my_card"},
		{"src/pages/index.orbit", "pages/index", "index"},
		{"src/pages/blog/[slug].orbit", "pages/blog/slug_param", "slug_param"},
		{"src/pages/docs/[...rest].orbit", "pages/docs/rest_rest", "rest_rest"},
		{"src/pages/[lang]/about.orbit", "pages/lang_param/about", "about"},
		{"src/pages/[post-id].orbit", "pages/post_id_param", "post_id_param"},
		{"src/pages/404.orbit", "pages/p_404", "p_404"},
		{"src/components/default.orbit", "components/p_default", "p_default"},
		{"src/components/type.orbit", "components/p_type", "p_type"},
		{"src/main/x.orbit", "p_main/x", "x"},
		{"src/components/init.orbit", "components/p_init", "p_init"},
		{"src/internal/x.orbit", "p_internal/x", "x"},
		{"src/vendor/testdata/X.orbit", "p_vendor/p_testdata/x", "x"},
		{"src/components/_private.orbit", "components/p__private", "p__private"},
		{"src/components/Café.orbit", "components/caf_", "caf_"},
		{"src/layouts/Base.orbit", "layouts/base", "base"},
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
	for _, bad := range []string{"components/Card.orbit", "src/x.go", "src//x.orbit", "src/../x.orbit", "src/a/./b.orbit"} {
		if _, err := PackageFor(bad); err == nil {
			t.Errorf("PackageFor(%q): no error", bad)
		}
	}
}

func TestGenPackagePaths(t *testing.T) {
	p, err := PackageFor("src/components/Card.orbit")
	if err != nil {
		t.Fatal(err)
	}
	const mod = "example.com/blog"
	checks := map[string]string{
		p.ImplName():          "card_impl",
		p.DeclFile():          "orbit_gen/decl/components/card/decl.go",
		p.ImplFile():          "orbit_gen/impl/components/card/render.go",
		p.DeclImportPath(mod): "example.com/blog/orbit_gen/decl/components/card",
		p.ImplImportPath(mod): "example.com/blog/orbit_gen/impl/components/card",
		DirectiveFile(p.ImplFile(), "src/components/Card.orbit"):                                     "../../../../src/components/Card.orbit",
		DirectiveFile("orbit_gen/decl/pages/blog/slug_param/decl.go", "src/pages/blog/[slug].orbit"): "../../../../../src/pages/blog/[slug].orbit",
	}
	for got, want := range checks {
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	}
}

func TestComponentID(t *testing.T) {
	id := ComponentID("src/components/Comments.orbit")
	name, hash, ok := strings.Cut(id, "-")
	if !ok || name != "Comments" || len(hash) != 8 || hash != ScopeHash("src/components/Comments.orbit") {
		t.Errorf("ComponentID = %q", id)
	}
	if ScopeHash("src/a.orbit") == ScopeHash("src/b.orbit") {
		t.Error("ScopeHash doesn't depend on the path")
	}
	// sha256("src/components/Card.orbit")[:4], fixed so IDs never change.
	if got := ScopeHash("src/components/Card.orbit"); got != scopeHashCard {
		t.Errorf("ScopeHash(Card) = %s, want %s", got, scopeHashCard)
	}
}

const scopeHashCard = "c039c8bb"

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
	for name, want := range map[string]bool{"orbit__w": true, "Orbit__Render": true, "orbit": false, "orbit_x": false, "Render": false} {
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
		orbit.NewWriter(&buf).Attr(name, "javascript:alert(1)")
		filtered := strings.Contains(buf.String(), "about:invalid")
		if IsURLAttr(name) != filtered {
			t.Errorf("IsURLAttr(%q) = %v, runtime filters: %v", name, IsURLAttr(name), filtered)
		}
	}
}
