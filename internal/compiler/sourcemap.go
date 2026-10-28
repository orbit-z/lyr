package compiler

// Source maps between a .orbit file and one generated Go file (PLAN.md §3.3,
// §9). Owned by agent B (milestone 4); the LSP (milestone 12) consumes it.

// SegmentKind says how a segment's two ranges correspond.
type SegmentKind uint8

const (
	// SegExact is a 1:1 byte copy (equal lengths): frontmatter statements
	// and declarations, imports, expressions, control headers, an import
	// alias. Offsets map by arithmetic.
	SegExact SegmentKind = iota
	// SegSpan maps a whole range to a whole range of a different length:
	// attribute name is-open → field key IsOpen, a tag name → its
	// Card.Render (or Render for <Self>), an .orbit import path → the decl
	// import path. Offsets clamp.
	SegSpan
	// SegEcho is a generated "_ = name" (Go range: the name). It maps to no
	// .orbit range (OrbitStart = OrbitEnd = -1); an LSP rename drops edits
	// there.
	SegEcho
	// SegSpread is a field assignment generated for a props spread,
	// orbit__p.F = orbit__s1.F (PLAN.md §2.11): it maps back like SegSpan,
	// to the whole {...x} (so a Go error there lands on the spread), but
	// GoOffset never picks it (the operand's own exact segment serves the
	// cursor), and an LSP rename drops edits there like an echo's (the next
	// generate re-emits it).
	SegSpread
)

// Segment is one mapped range. Orbit offsets are bytes into the .orbit
// file, Go offsets bytes into GenFile.Content; ends are exclusive.
type Segment struct {
	Kind                 SegmentKind
	OrbitStart, OrbitEnd int
	GoStart, GoEnd       int
}

// SourceMap holds the segments of one generated file.
type SourceMap struct {
	// Orbit is the project-relative .orbit path, Gen the module-relative
	// generated file (GenFile.Path).
	Orbit, Gen string
	// Segments are sorted by GoStart and don't overlap on the Go side.
	Segments []Segment
}

// GoOffset maps a .orbit offset to the generated file. It finds the segment
// whose orbit range contains off, end inclusive (so a cursor right after
// "props." maps); exact segments map by arithmetic, span segments clamp to
// the Go range, echo and spread segments never match. ok is false outside every
// segment.
func (m *SourceMap) GoOffset(off int) (goOff int, ok bool) {
	return m.goOffset(off)
}

// OrbitOffset maps a generated-file offset back to the .orbit file, with the
// same rules; an offset in an echo segment or in glue returns ok false.
func (m *SourceMap) OrbitOffset(goOff int) (off int, ok bool) {
	return m.orbitOffset(goOff)
}
