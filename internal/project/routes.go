package project

import (
	"fmt"
	"path"
	"strings"

	"github.com/orbit-z/lyr/internal/compiler"
)

// NotFoundFile is the page that renders not-found responses, and
// NotFoundPattern its reserved route pattern (the runtime's router keeps it
// out of the URL trie, so /404 isn't routable).
const (
	NotFoundFile    = "src/pages/404.lyr"
	NotFoundPattern = "404"
)

// RouteFor returns the role and route pattern of the .lyr file at the
// project-relative path p (PLAN.md §4): a file under src/pages is a page
// whose route is its path there without .lyr, with index as its
// directory's root ("/" for src/pages/index.lyr); src/pages/404.lyr gets
// the reserved pattern "404". As in Astro, a file under src/pages whose name,
// or the name of a directory on its path, starts with "_" isn't routed: it's
// a component. Anything else is a component with no route.
// The pattern isn't validated; see checkRoute.
func RouteFor(p string) (compiler.Role, string) {
	rest, ok := strings.CutPrefix(p, PagesDir+"/")
	if !ok {
		return compiler.RoleComponent, ""
	}
	for seg := range strings.SplitSeq(rest, "/") {
		if strings.HasPrefix(seg, "_") {
			return compiler.RoleComponent, ""
		}
	}
	if compiler.IsMarkdown(rest) {
		rest = strings.TrimSuffix(rest, path.Ext(rest)) // a Markdown page (PLAN.md §2.4)
	} else {
		rest = strings.TrimSuffix(rest, ".lyr")
	}
	if rest == "404" {
		return compiler.RolePage, NotFoundPattern
	}
	if rest == "index" {
		return compiler.RolePage, "/"
	}
	rest = strings.TrimSuffix(rest, "/index")
	return compiler.RolePage, "/" + rest
}

// checkRoute returns the problem with a page's route pattern, or "": a
// segment that mixes text and a parameter, a rest param that isn't last, a
// route under /_lyr/, unbalanced brackets or an empty param name. The
// messages match the runtime router's (router.go), which rejects the same
// patterns at startup. Param names that can't become Go fields, and params
// whose field names collide, are reported by compiler.Analyze.
func checkRoute(pattern string) string {
	if pattern == NotFoundPattern || pattern == "/" {
		return ""
	}
	parts := strings.Split(strings.TrimPrefix(pattern, "/"), "/")
	if parts[0] == "_lyr" {
		return fmt.Sprintf("route %s is under /_lyr/, which Lyr reserves for its assets and islands; rename the page or its directory", pattern)
	}
	for i, seg := range parts {
		last := i == len(parts)-1
		if !strings.ContainsAny(seg, "[]") {
			continue
		}
		if seg[0] == '[' && seg[len(seg)-1] == ']' && !strings.ContainsAny(seg[1:len(seg)-1], "[]") {
			name := seg[1 : len(seg)-1]
			rest, isRest := strings.CutPrefix(name, "...")
			if name == "" {
				return fmt.Sprintf("invalid route pattern %q: segment %q has an empty parameter name", pattern, seg)
			}
			if isRest && rest != "" && !last {
				return fmt.Sprintf("rest parameter %s must be the last segment of the route; use a whole-segment parameter such as [%s] instead", seg, rest)
			}
			continue
		}
		if open := strings.IndexByte(seg, '['); open >= 0 {
			if end := strings.IndexByte(seg[open:], ']'); end > 1 {
				if name := seg[open+1 : open+end]; !strings.Contains(name, "[") {
					name = strings.TrimPrefix(name, "...")
					suggest := "[" + name + "].lyr"
					if !last {
						suggest = "[" + name + "]/"
					}
					return fmt.Sprintf("route segment %q mixes text and a parameter; use a whole-segment parameter such as %s and parse it in the page", seg, suggest)
				}
			}
		}
		return fmt.Sprintf("invalid route pattern %q: segment %q has unbalanced brackets", pattern, seg)
	}
	return ""
}

// shapeKey identifies the URLs a pattern matches: param names are dropped,
// so /blog/[slug] and /blog/[id] are the same route.
func shapeKey(pattern string) string {
	if pattern == NotFoundPattern || pattern == "/" {
		return pattern
	}
	var b strings.Builder
	for _, seg := range strings.Split(strings.TrimPrefix(pattern, "/"), "/") {
		b.WriteByte('/')
		switch {
		case strings.HasPrefix(seg, "[...") && strings.HasSuffix(seg, "]"):
			b.WriteString("[...]")
		case strings.HasPrefix(seg, "[") && strings.HasSuffix(seg, "]"):
			b.WriteString("[]")
		default:
			b.WriteString(seg)
		}
	}
	return b.String()
}

// checkFiles reports the project-level file errors: invalid page routes,
// two pages with the same route, and two files whose sanitized generated
// package paths collide (PLAN.md §3.1, §4). files is sorted by path, so the
// error is on the later file and names the earlier one.
func checkFiles(files []*File) []Diagnostic {
	var ds []Diagnostic
	routes := map[string]*File{}
	pkgs := map[string]*File{}
	for _, f := range files {
		if prev, dup := pkgs[f.Pkg.Rel]; dup {
			ds = append(ds, fileError(f.Path, "generates the same Go package as %s (%s/decl/%s); rename one of them",
				prev.Path, compiler.GenDir, f.Pkg.Rel))
		} else {
			pkgs[f.Pkg.Rel] = f
		}
		if f.Info.Role != compiler.RolePage {
			continue
		}
		if msg := mdPageError(f); msg != "" {
			ds = append(ds, fileError(f.Path, "%s", msg))
			continue
		}
		if msg := checkRoute(f.Info.Route); msg != "" {
			ds = append(ds, fileError(f.Path, "%s", msg))
			continue
		}
		key := shapeKey(f.Info.Route)
		if prev, dup := routes[key]; dup {
			ds = append(ds, fileError(f.Path, "route %s is already defined by %s; remove or rename one of them", f.Info.Route, prev.Path))
			continue
		}
		routes[key] = f
	}
	return ds
}
