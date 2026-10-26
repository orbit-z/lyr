package compiler

// TEMPORARY stub of the Go → TS mapper and emitter (replaced when the
// mapper lands).

import (
	"go/types"
	"strings"
)

type tsNS struct{ Key, Base, Path string }

type tsMapper struct{ nsOf func(*types.Package) tsNS }

func newTSMapper(nsOf func(*types.Package) tsNS) *tsMapper { return &tsMapper{nsOf: nsOf} }

func (m *tsMapper) Map(t types.Type) (string, *jsonProblem) {
	switch u := t.Underlying().(type) {
	case *types.Basic:
		switch {
		case u.Info()&types.IsBoolean != 0:
			return "boolean", nil
		case u.Info()&types.IsString != 0:
			return "string", nil
		case u.Info()&types.IsNumeric != 0:
			return "number", nil
		}
	case *types.Signature:
		return "", &jsonProblem{"", t}
	}
	return "unknown", nil
}

func (m *tsMapper) Decls() []TSDecl { return nil }

type TSDecl struct {
	Ref, Name, GoPath string
	NS                tsNS
	TypeParams        []string
	Fields            []TSField
	Alias             string
	Interface         bool
}

type TSField struct {
	Name     string
	Optional bool
	Type     string
}

type UnitTypes struct {
	Path  string
	Data  []TSKey
	JSON  []TSKey
	Decls []TSDecl
}

type TSKey struct {
	Name  string
	Types []string
	Sites []string
}

type TSFile struct {
	Path    string
	Content []byte
}

func EmitTypes(units []*UnitTypes) []TSFile {
	files := []TSFile{{Path: GenDir + "/types/client.d.ts", Content: []byte(GenHeader + "\n")}, {Path: GenDir + "/types/go.d.ts", Content: []byte(GenHeader + "\n")}}
	for _, u := range units {
		if len(u.Data)+len(u.JSON) == 0 {
			continue
		}
		var b strings.Builder
		b.WriteString(GenHeader + "\nexport interface Data {\n")
		for _, k := range u.Data {
			b.WriteString("  " + k.Name + ": " + strings.Join(k.Types, " | ") + ";\n")
		}
		b.WriteString("}\n")
		files = append(files, TSFile{Path: GenDir + "/types/" + u.Path + ".d.ts", Content: []byte(b.String())})
	}
	return files
}
