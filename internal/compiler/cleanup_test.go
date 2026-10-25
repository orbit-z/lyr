package compiler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// tyErrorNames is the ErrorNames of the error fixture, built from the
// Analysis fields NewErrorNames reads.
func tyErrorNames() *ErrorNames {
	files := []*Analysis{
		{Info: FileInfo{Path: tyCard}},
		{Info: FileInfo{Path: tyUICard}},
		{Info: FileInfo{Path: tyOther}},
		{Info: FileInfo{Path: tyBroken}},
		{Info: FileInfo{Path: tyIndex, Role: RolePage}, OrbitImports: []OrbitImport{
			{Alias: "Card", Path: "../components/Card.orbit", Resolved: tyCard},
			{Alias: "UICard", Path: "../components/ui/Card.orbit", Resolved: tyUICard},
		}},
	}
	return NewErrorNames(testModule, files)
}

func TestNewErrorNames(t *testing.T) {
	n := tyErrorNames()
	if got, want := n.Own[tyUICard], testModule+"/orbit_gen/decl/components/ui/card"; got != want {
		t.Errorf("Own[%s] = %q, want %q", tyUICard, got, want)
	}
	if got := n.Aliases[tyIndex][testModule+"/orbit_gen/decl/components/card"]; got != "Card" {
		t.Errorf("Aliases[index][card] = %q, want Card", got)
	}
	if len(n.Aliases[tyCard]) != 0 {
		t.Errorf("Card has aliases %v, want none", n.Aliases[tyCard])
	}
}

// TestCleanGoOutputGolden cleans the real go build and go vet output of the
// error fixture (PLAN.md §3.3 position test).
func TestCleanGoOutputGolden(t *testing.T) {
	m := newGenModule(t)
	for _, u := range tyErrorUnits(t) {
		m.writeOutput(u.Pass1)
	}
	build, vet := m.buildErrors()
	names := tyErrorNames()
	got := "-- go build --\n" + sortPackages(string(CleanGoOutput([]byte(build), names))) +
		"-- go vet --\n" + sortPackages(string(CleanGoOutput([]byte(vet), names)))
	golden := filepath.Join("testdata", "typed", "cleanup.golden")
	if *update {
		if err := os.MkdirAll(filepath.Dir(golden), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("%v (run with -update)", err)
	}
	if got != string(want) {
		t.Errorf("cleaned output differs from %s:\n--- raw build:\n%s\n--- raw vet:\n%s\n--- got:\n%s", golden, build, vet, got)
	}
	// Spot checks of the rules, so the golden can't silently regress.
	for _, s := range []string{
		"src/components/Card.orbit:6:16: cannot use props (variable of struct type Props) as string value",
		"src/pages/index.orbit:7:22: cannot use x (variable of struct type Card.Props) as UICard.Props value",
		"\tsrc/components/Card.orbit:4:6: other declaration of helper",
		"\nsrc/components/Card.orbit:5:6: helper redeclared in this block",
		"\nsrc/components/Other.orbit:4:13: fmt.Printf format %d",
	} {
		if !strings.Contains(got, s) {
			t.Errorf("cleaned output lacks %q", s)
		}
	}
	for _, s := range []string{"./src", "[/", `".Props`, "card.Props", "vet: "} {
		if strings.Contains(got, s) {
			t.Errorf("cleaned output still contains %q", s)
		}
	}
}

func TestCleanGoOutput(t *testing.T) {
	names := tyErrorNames()
	// A user package named like a decl package, and a second alias of the
	// same package name, for the ambiguity rules.
	names.Aliases[tyOther] = map[string]string{
		testModule + "/orbit_gen/decl/components/card":    "Card",
		testModule + "/orbit_gen/decl/components/ui/card": "UICard",
	}
	tests := []struct{ name, in, want string }{
		{"generated package header dropped", "# example.com/blog/orbit_gen/impl/pages/index\nvet: ./src/pages/index.orbit:3:1: x\n# example.com/blog/orbit_gen/decl/components/card [example.com/blog/orbit_gen/decl/components/card.test]\n",
			"src/pages/index.orbit:3:1: x\n"},
		{"brackets in the path", "./src/pages/blog/[slug].orbit:3:13: x\n./src/pages/[...path].orbit:2:1: y ./src/pages/[id].orbit:7:6[/abs/orbit_gen/decl/pages/id_param/decl.go:10:53]\n",
			"src/pages/blog/[slug].orbit:3:13: x\nsrc/pages/[...path].orbit:2:1: y src/pages/[id].orbit:7:6\n"},
		{"not about orbit", "# example.com/blog/internal/posts\ninternal/posts/p.go:3:1: card.Props\n",
			"# example.com/blog/internal/posts\ninternal/posts/p.go:3:1: card.Props\n"},
		{"own name", "./src/components/Card.orbit:6:16: cannot use p (variable of type card.Props) as card.Render value\n",
			"src/components/Card.orbit:6:16: cannot use p (variable of type Props) as Render value\n"},
		{"own impl", "src/components/Card.orbit:3:6: card_impl.helper is unused, card_impl.Item\n",
			"src/components/Card.orbit:3:6: helper is unused, Item\n"},
		{"alias by package name", "./src/pages/index.orbit:4:2: x (type index.Params) is not card.Props\n",
			"src/pages/index.orbit:4:2: x (type Params) is not card.Props\n"},
		{"ambiguous package name kept", "./src/components/Other.orbit:4:2: card.Props\n",
			"src/components/Other.orbit:4:2: card.Props\n"},
		{"quoted with alias", `./src/components/Other.orbit:4:2: "example.com/blog/orbit_gen/decl/components/ui/card".Props` + "\n",
			"src/components/Other.orbit:4:2: UICard.Props\n"},
		{"quoted without alias kept", `src/components/Card.orbit:4:2: "example.com/blog/orbit_gen/decl/components/ui/card".Props` + "\n",
			`src/components/Card.orbit:4:2: "example.com/blog/orbit_gen/decl/components/ui/card".Props` + "\n"},
		{"related position", "./src/components/A.orbit:7:6[/abs/x/orbit_gen/decl/components/a/decl.go:10:53]: other\n",
			"src/components/A.orbit:7:6: other\n"},
		{"continuation", "./src/components/Card.orbit:6:16: cannot use\n\thave (card.Props)\n\twant (string)\nnext\n\tcard.Props\n",
			"src/components/Card.orbit:6:16: cannot use\n\thave (Props)\n\twant (string)\nnext\n\tcard.Props\n"},
		{"vet prefix", "vet: ./src/components/Card.orbit:5:6: helper redeclared\n",
			"src/components/Card.orbit:5:6: helper redeclared\n"},
		{"inside a path or selector", "./src/components/Card.orbit:1:1: x.card.Props and a/card.Props and mycard.Props\n",
			"src/components/Card.orbit:1:1: x.card.Props and a/card.Props and mycard.Props\n"},
		{"run from another dir", "../src/components/Card.orbit:1:1: card.Props\n",
			"../src/components/Card.orbit:1:1: Props\n"},
		{"no trailing newline", "./src/components/Card.orbit:1:1: card.Props", "src/components/Card.orbit:1:1: Props"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := string(CleanGoOutput([]byte(tt.in), names)); got != tt.want {
				t.Errorf("got\n%s\nwant\n%s", got, tt.want)
			}
		})
	}
}
