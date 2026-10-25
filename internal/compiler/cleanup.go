package compiler

// The CLI's cleanup of go build / go vet output (PLAN.md §3.3 "Error
// output"). Owned by agent C (milestone 4); the CLI (milestone 5) calls it.

// ErrorNames is what CleanGoOutput needs to rewrite generated names into the
// user's: for each .orbit file, its own decl package and the aliases it
// gives other components.
type ErrorNames struct {
	Module string
	// Own maps a project-relative .orbit path to its decl import path.
	Own map[string]string
	// Aliases maps a project-relative .orbit path to its .orbit imports:
	// decl import path → alias.
	Aliases map[string]map[string]string
}

// NewErrorNames builds the table from every file's Analysis (using
// PackageFor on Info.Path and OrbitImport.Resolved).
func NewErrorNames(module string, files []*Analysis) *ErrorNames {
	panic("not implemented: milestone 4 (C)")
}

// CleanGoOutput rewrites go build / go vet output whose lines already point
// at .orbit files: it strips the "[<gen>:L:C]" related-position suffixes,
// and rewrites qualified generated names (card.Props, or the quoted
// "example.com/blog/orbit_gen/decl/components/card".Props) to the alias the
// reporting .orbit file uses (Card.Props), or to the bare name (Props,
// Params) in the component's own file. A name with no alias in the
// reporting file (or an ambiguous package name) is left as is. Lines not
// about .orbit files pass through unchanged.
func CleanGoOutput(out []byte, names *ErrorNames) []byte {
	panic("not implemented: milestone 4 (C)")
}
