package compiler

import "strings"

// The runtime's escaping (escape.go at the repo root), for the values
// codegen writes at build time: they must be byte-identical to what the
// runtime would write (TestEscapersMatchRuntime checks every byte).
var (
	// textEscaper escapes text content, as Writer.String does: Astro's
	// html-escaper spelling.
	textEscaper = strings.NewReplacer("\x00", "�", `"`, "&quot;", "&", "&amp;", "'", "&#39;", "<", "&lt;", ">", "&gt;")
	// attrValueEscaper escapes a value for a double-quoted attribute, as
	// Writer.AttrString and Writer.Class do: & and " as in Astro, plus <
	// and >; ' stays.
	attrValueEscaper = strings.NewReplacer("\x00", "�", `"`, "&quot;", "&", "&amp;", "<", "&lt;", ">", "&gt;")
)
