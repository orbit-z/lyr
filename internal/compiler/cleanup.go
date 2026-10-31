package compiler

// The CLI's cleanup of go build / go vet output (PLAN.md §3.3 "Error
// output"). Owned by agent C (milestone 4); the CLI (milestone 5) calls it.

// ErrorNames is what CleanGoOutput needs to rewrite generated names into the
// user's: for each .lyr file, its own decl package and the aliases it
// gives other components.
type ErrorNames struct {
	Module string
	// Own maps a project-relative .lyr path to its decl import path.
	Own map[string]string
	// Aliases maps a project-relative .lyr path to its .lyr imports:
	// decl import path → alias.
	Aliases map[string]map[string]string
	// Imported maps a project-relative .lyr path to the package names its
	// Go imports may bind (the explicit name, else the path's last element
	// and its usual variants). A bare generated package name that is also in
	// this list is ambiguous in that file's errors and is left as is.
	Imported map[string][]string
	// Declared maps a decl import path to the names its package declares
	// (Props, Render, Lyr__Render, a page's Params, the decl-placed
	// hoisted names). A bare pkg.X is rewritten only when X is one of them,
	// so a user package with a generated package's name, reached
	// transitively, keeps its names. Nil disables the check.
	Declared map[string][]string
}

// NewErrorNames builds the table from every file's Analysis (using
// PackageFor on Info.Path and LyrImport.Resolved).
func NewErrorNames(module string, files []*Analysis) *ErrorNames {
	return newErrorNames(module, files)
}

// CleanGoOutput rewrites go build / go vet output whose lines already point
// at .lyr files: it strips the "[<gen>:L:C]" related-position suffixes,
// and rewrites qualified generated names (card.Props, or the quoted
// "example.com/blog/lyr_gen/decl/components/card".Props) to the alias the
// reporting .lyr file uses (Card.Props), or to the bare name (Props,
// Params) in the component's own file. A name with no alias in the
// reporting file, or a bare package name that is ambiguous there (two
// generated packages, or a user import, with that name), is left as is. "in
// argument to lyr__rt.ErrorAt" (the wrapper around a frontmatter return's
// result) becomes "in return statement". Lines not
// about .lyr files pass through unchanged.
func CleanGoOutput(out []byte, names *ErrorNames) []byte {
	return cleanGoOutput(out, names)
}
