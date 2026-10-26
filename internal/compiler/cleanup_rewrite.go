package compiler

// CleanGoOutput's implementation (PLAN.md §3.3 "Error output"). Owned by
// agent C.

import (
	"bytes"
	"go/ast"
	"path"
	"regexp"
	"strings"
)

func newErrorNames(module string, files []*Analysis) *ErrorNames {
	n := &ErrorNames{Module: module, Own: map[string]string{}, Aliases: map[string]map[string]string{},
		Imported: map[string][]string{}, Declared: map[string][]string{}}
	for _, a := range files {
		if a == nil {
			continue
		}
		for _, im := range a.GoImports {
			n.Imported[a.Info.Path] = append(n.Imported[a.Info.Path], importNames(im)...)
		}
		if pkg, err := PackageFor(a.Info.Path); err == nil {
			decl := pkg.DeclImportPath(module)
			n.Own[a.Info.Path] = decl
			names := []string{"Props", "Render", "Orbit__Render"}
			if a.Info.Role == RolePage {
				names = append(names, "Params")
			}
			for _, h := range a.Decls {
				if h.Place == PlaceDecl {
					names = append(names, h.Names...)
				}
			}
			n.Declared[decl] = names
		}
		for _, im := range a.OrbitImports {
			pkg, err := PackageFor(im.Resolved)
			if err != nil {
				continue
			}
			m := n.Aliases[a.Info.Path]
			if m == nil {
				m = map[string]string{}
				n.Aliases[a.Info.Path] = m
			}
			if _, dup := m[pkg.DeclImportPath(module)]; !dup {
				m[pkg.DeclImportPath(module)] = im.Alias
			}
		}
	}
	return n
}

var (
	// clPos is an .orbit position, with an optional "./" and an optional
	// related generated position in brackets.
	clPos = regexp.MustCompile(`(\./)?([^\s:"]*\.(?:orbit|md|markdown|mdown|mkdn|mkd|mdwn)):(\d+):(\d+)(\[[^\]\s]*\])?`)
	// clQuoted is a quoted package path qualifying a name.
	clQuoted = regexp.MustCompile(`"([^"\s]+)"\.([A-Za-z_][A-Za-z0-9_]*)`)
	// clQualified is pkg.Name, pkg a lowercase Go identifier.
	clQualified = regexp.MustCompile(`([a-z_][a-z0-9_]*)\.([A-Za-z_][A-Za-z0-9_]*)`)
)

// clFile is what one .orbit file calls the generated packages.
type clFile struct {
	byPath map[string]string // decl import path → alias ("" for its own)
	byName map[string]string // package name → alias; ambiguous names absent
	impl   string            // the own impl package's name
	// declared maps a package name to the names its decl package declares;
	// nil when ErrorNames.Declared is.
	declared map[string]map[string]bool
}

func (n *ErrorNames) file(orbit string) *clFile {
	own, ok := n.Own[orbit]
	if !ok {
		return nil
	}
	f := &clFile{byPath: map[string]string{own: ""}, byName: map[string]string{}}
	for p, alias := range n.Aliases[orbit] {
		f.byPath[p] = alias
	}
	count := map[string]int{}
	if n.Declared != nil {
		f.declared = map[string]map[string]bool{}
	}
	for p, alias := range f.byPath {
		name := path.Base(p)
		count[name]++
		f.byName[name] = alias
		if f.declared != nil {
			set := map[string]bool{}
			for _, d := range n.Declared[p] {
				set[d] = true
			}
			f.declared[name] = set
		}
	}
	// The own impl package (card_impl) qualifies impl names in vet output.
	f.impl = path.Base(own) + "_impl"
	count[f.impl]++
	f.byName[f.impl] = ""
	// A user import with a generated package's name makes it ambiguous.
	for _, name := range n.Imported[orbit] {
		count[name] += 2
	}
	for name, c := range count {
		if c > 1 {
			delete(f.byName, name)
		}
	}
	return f
}

// importNames are the package names a Go import may bind: its explicit
// name, else its path's last element (skipping a major-version element such
// as v2) and that element without a go- prefix or a -go or .go suffix. A
// wrong guess only leaves a name unrewritten.
func importNames(im GoImport) []string {
	if im.Name != "" {
		if im.Name == "_" || im.Name == "." {
			return nil
		}
		return []string{im.Name}
	}
	elems := strings.Split(im.Path, "/")
	base := elems[len(elems)-1]
	if len(elems) > 1 && len(base) > 1 && base[0] == 'v' && strings.Trim(base[1:], "0123456789") == "" {
		base = elems[len(elems)-2]
	}
	names := []string{base}
	trimmed := strings.TrimSuffix(strings.TrimSuffix(strings.TrimPrefix(base, "go-"), "-go"), ".go")
	if i := strings.IndexByte(trimmed, '.'); i > 0 {
		trimmed = trimmed[:i] // gopkg.in/yaml.v3
	}
	if trimmed != base {
		names = append(names, trimmed)
	}
	return names
}

func (n *ErrorNames) lookup(orbit string) (string, bool) {
	if _, ok := n.Own[orbit]; ok {
		return orbit, true
	}
	for p := range n.Own {
		if strings.HasSuffix(orbit, "/"+p) {
			return p, true
		}
	}
	return "", false
}

func qualify(alias, name string) string {
	if alias == "" {
		return name
	}
	return alias + "." + name
}

func cleanGoOutput(out []byte, names *ErrorNames) []byte {
	if names == nil {
		names = &ErrorNames{}
	}
	lines := bytes.SplitAfter(out, []byte("\n"))
	var b bytes.Buffer
	var cur *clFile // the file the last error line was about
	files := map[string]*clFile{}
	for _, raw := range lines {
		line := string(raw)
		// go build's "# <pkg>" header names a generated package, which the
		// user never wrote; its errors already point at .orbit files.
		if isGenHeader(line, names.Module) {
			cur = nil
			continue
		}
		loc := clPos.FindStringSubmatchIndex(line)
		if loc == nil {
			// A continuation line (tab-indented) belongs to the last error.
			if cur == nil || !strings.HasPrefix(line, "\t") {
				cur = nil
				b.WriteString(line)
				continue
			}
			b.WriteString(rewriteNames(line, cur))
			continue
		}
		// The reporting file is the line's first .orbit position.
		orbit := line[loc[4]:loc[5]]
		if p, ok := names.lookup(orbit); ok {
			f, seen := files[p]
			if !seen {
				f = names.file(p)
				files[p] = f
			}
			cur = f
		} else {
			cur = nil
		}
		// "vet: " in front of a type error, so build and vet read alike.
		if loc[0] == len("vet: ") && strings.HasPrefix(line, "vet: ") {
			line = line[len("vet: "):]
		}
		line = clPos.ReplaceAllString(line, "$2:$3:$4")
		// A frontmatter return's result is the argument of the generated
		// ErrorAt wrapper: say what the user wrote.
		line = strings.ReplaceAll(line, " in argument to "+NameRuntime+".ErrorAt", " in return statement")
		if cur != nil {
			line = rewriteNames(line, cur)
		}
		b.WriteString(line)
	}
	return b.Bytes()
}

// rewriteNames rewrites the qualified generated names of one line, outside
// its positions.
func rewriteNames(line string, f *clFile) string {
	line = clQuoted.ReplaceAllStringFunc(line, func(m string) string {
		sm := clQuoted.FindStringSubmatch(m)
		if alias, ok := f.byPath[sm[1]]; ok {
			return qualify(alias, sm[2])
		}
		return m
	})
	var b strings.Builder
	last := 0
	for _, loc := range clQualified.FindAllStringSubmatchIndex(line, -1) {
		if loc[0] > 0 {
			// Not part of a longer name, path or selector chain.
			switch c := line[loc[0]-1]; {
			case c == '.' || c == '/' || c == '"' || c == '-' || c == '_' ||
				'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9':
				continue
			}
		}
		qual, name := line[loc[2]:loc[3]], line[loc[4]:loc[5]]
		alias, ok := f.byName[qual]
		switch {
		case !ok:
			continue
		case qual == f.impl:
		case f.declared != nil:
			if !f.declared[qual][name] {
				continue // not generated: a user package with that name
			}
		case !ast.IsExported(name):
			continue
		}
		b.WriteString(line[last:loc[0]])
		b.WriteString(qualify(alias, name))
		last = loc[1]
	}
	b.WriteString(line[last:])
	return b.String()
}

// isGenHeader reports whether line is the "# <import path>" header go build
// and go vet print before a package's errors, for a package under the
// module's orbit_gen/ (with module "", any path with an orbit_gen element).
func isGenHeader(line, module string) bool {
	path, ok := strings.CutPrefix(strings.TrimRight(line, "\r\n"), "# ")
	if !ok {
		return false
	}
	path, _, _ = strings.Cut(path, " ") // "# pkg [pkg.test]"
	if module != "" {
		return path == module+"/orbit_gen" || strings.HasPrefix(path, module+"/orbit_gen/")
	}
	return strings.Contains("/"+path+"/", "/orbit_gen/")
}
