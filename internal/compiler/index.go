package compiler

// The component index: what codegen (and later the LSP) knows about the
// other .lyr files of the project. The caller builds it (project
// discovery, milestone 5; tests before that) from every file's Analysis.

// ComponentIndex maps a project-relative .lyr path
// ("src/components/Card.lyr", as in LyrImport.Resolved) to its entry.
type ComponentIndex map[string]*ComponentInfo

// ComponentInfo is the cross-file view of one .lyr file.
type ComponentInfo struct {
	// Path is the project-relative .lyr path.
	Path string
	Role Role
	// Pkg locates its generated packages; calls import the decl package.
	Pkg GenPackage
	// Name is the base name without .lyr ("Card"): the alias the LSP
	// suggests when it adds an import.
	Name string
	// ID is its component ID (ComponentID), used by server:defer and
	// client:* call sites (milestone 8).
	ID string
	// Props is its Props, for attribute → field matching and the unknown-prop
	// pre-check (MatchProp).
	Props PropsInfo
	// Slots are the slot names its template declares, in order of first
	// appearance: "default" for <slot />, the name for <slot name="x">. For
	// LSP completion of slot="…"; codegen doesn't check them (Astro
	// doesn't).
	Slots []string
}

// NewComponentInfo builds the index entry of a parsed and analyzed file.
// Implemented by agent A.
func NewComponentInfo(f *File, a *Analysis) *ComponentInfo {
	return newComponentInfo(f, a)
}
