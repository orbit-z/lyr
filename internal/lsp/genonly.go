package lsp

import (
	"encoding/json"
	"strings"
)

// genOnlyDoc starts the doc comment of every runtime name that only
// generated code calls (package orbit's "# Generated code" section).
const genOnlyDoc = "For generated code."

// genOnlyNames are the runtime's generated-code-only names: package-level
// names and methods, by bare name, as gopls labels their completion items.
// TestGenOnlyNamesMatchRuntime keeps it equal to the names whose doc
// comment starts with genOnlyDoc.
var genOnlyNames = map[string]bool{
	// Package-level.
	"AppendAny": true, "AppendError": true, "AppendFloat": true, "AppendFloat32": true,
	"AppendStringer": true, "Asset": true, "ClassIf": true, "ClassString": true,
	"Concurrent": true, "CtxOf": true, "Defer": true, "DynTag": true, "ErrorAt": true,
	"Island": true, "JoinClasses": true, "LocalsOf": true, "NewIsland": true,
	"OptOf": true, "Rest": true, "RestAny": true, "RestAttr": true, "RestBool": true,
	"RestFloat": true, "RestFloat32": true, "RestFrom": true, "RestFromAny": true,
	"RestInt": true, "RestJSON": true, "RestMerge": true, "RestMergeAny": true,
	"RestOpt": true, "RestOptURL": true, "RestString": true, "RestStringURL": true,
	"RestURL": true, "RestUint": true, "Route": true, "RouteRule": true,
	"SetHTML": true, "SpreadCheck": true, "SpreadOf": true, "StringAs": true,
	"TB": true, "TemplateAs": true, "TemplateString": true, "WithLocalsCache": true,
	"WithLocalsRoute": true, "WithRouteRules": true, "Wrap": true, "WrapIf": true,
	// Methods: Writer, DynTag, TB.
	"AttrPiece": true, "Attrs": true, "Class": true, "ClassAny": true, "ClassBytes": true,
	"ClassEnd": true, "ClassStart": true, "Classes": true, "Enter": true, "HeadEnd": true,
	"Leave": true, "RestClass": true, "TagClose": true, "TagOpen": true, "TextPiece": true,
	"URLCheck": true, "URLMark": true,
	"Any": true, "B": true, "Bool": true, "Bytes": true, "Error": true, "Float": true,
	"Float32": true, "Int": true, "S": true, "String": true, "Stringer": true, "Uint": true,
}

// isGenOnlyItem reports whether a gopls completion item is one of the
// runtime's generated-code-only names: its label is one of genOnlyNames
// (a map lookup, so other items cost nothing more) and its documentation
// starts with genOnlyDoc, which tells the runtime's name from a user's
// name or field of the same spelling (a String method, a Route field). An
// item without documentation (gopls's completionDocumentation off) is kept.
func isGenOnlyItem(it obj) bool {
	if !genOnlyNames[it.str("label")] {
		return false
	}
	raw := it["documentation"]
	var doc string
	if json.Unmarshal(raw, &doc) != nil {
		var mc struct {
			Value string `json:"value"`
		}
		json.Unmarshal(raw, &mc)
		doc = mc.Value
	}
	return strings.HasPrefix(strings.TrimSpace(doc), genOnlyDoc)
}
