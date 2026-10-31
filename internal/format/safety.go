package format

// The safety check (PLAN.md §6.1): a formatted region must keep its code,
// and the formatted file its template tree.

import (
	"fmt"
	goconstant "go/constant"
	goscanner "go/scanner"
	gotoken "go/token"
	"slices"
	"strings"

	"github.com/orbit-z/lyr/internal/compiler"
)

type goTok struct {
	tok gotoken.Token
	lit string
}

// goTokens scans Go source s, comments excluded.
func goTokens(s string) []goTok {
	fset := gotoken.NewFileSet()
	file := fset.AddFile("", -1, len(s))
	var sc goscanner.Scanner
	sc.Init(file, []byte(s), nil, 0)
	var out []goTok
	for {
		_, tok, lit := sc.Scan()
		out = append(out, goTok{tok, lit})
		if tok == gotoken.EOF {
			return out
		}
	}
}

// normalize drops the semicolons that don't matter: before a ) or }, at
// the end and repeated ones (auto and explicit semicolons are equal).
func normalize(toks []goTok) []goTok {
	var out []goTok
	for i, t := range toks {
		if t.tok == gotoken.SEMICOLON {
			if len(out) == 0 || out[len(out)-1].tok == gotoken.SEMICOLON || out[len(out)-1].tok == gotoken.LBRACE || out[len(out)-1].tok == gotoken.LPAREN {
				continue
			}
			if n := toks[i+1].tok; n == gotoken.RPAREN || n == gotoken.RBRACE || n == gotoken.EOF || n == gotoken.SEMICOLON {
				continue
			}
			t.lit = ";"
		}
		out = append(out, t)
	}
	return out
}

// imports removes the import declarations at depth 0 from toks and returns
// their specs as a sorted set ("name path"), since gofmt sorts and dedupes
// them.
func imports(toks []goTok) (rest []goTok, specs []string) {
	depth := 0
	for i := 0; i < len(toks); i++ {
		t := toks[i]
		switch t.tok {
		case gotoken.LPAREN, gotoken.LBRACE, gotoken.LBRACK:
			depth++
		case gotoken.RPAREN, gotoken.RBRACE, gotoken.RBRACK:
			depth--
		}
		if t.tok != gotoken.IMPORT || depth != 0 {
			rest = append(rest, t)
			continue
		}
		grouped := i+1 < len(toks) && toks[i+1].tok == gotoken.LPAREN
		j := i + 1
		if grouped {
			j++
		}
		name := ""
		for ; j < len(toks); j++ {
			switch u := toks[j]; u.tok {
			case gotoken.IDENT, gotoken.PERIOD:
				name = u.lit
				if u.tok == gotoken.PERIOD {
					name = "."
				}
				continue
			case gotoken.STRING:
				specs = append(specs, name+" "+u.lit)
				name = ""
				continue
			case gotoken.SEMICOLON:
				if grouped {
					continue
				}
			case gotoken.RPAREN:
				if grouped {
					j++ // past the ")"
				}
			}
			break
		}
		// Skip the declaration's semicolon too.
		if j < len(toks) && toks[j].tok == gotoken.SEMICOLON {
			j++
		}
		i = j - 1
	}
	slices.Sort(specs)
	return rest, slices.Compact(specs)
}

// sameGo reports whether Go code a and b have the same tokens: comments
// excluded, semicolons normalized, number literals compared by value and,
// for the frontmatter, imports compared as a set.
func sameGo(a, b string, frontmatter bool) bool {
	// gofmt can drop a comment (an empty // before a block); keep the
	// region as written then.
	if countComments(a) != countComments(b) {
		return false
	}
	ta, tb := normalize(goTokens(a)), normalize(goTokens(b))
	if frontmatter {
		var ia, ib []string
		ta, ia = imports(ta)
		tb, ib = imports(tb)
		if !slices.Equal(ia, ib) {
			return false
		}
		ta, tb = normalize(ta), normalize(tb)
	}
	if len(ta) != len(tb) {
		return false
	}
	for i := range ta {
		x, y := ta[i], tb[i]
		if x.tok != y.tok {
			return false
		}
		switch x.tok {
		case gotoken.INT, gotoken.FLOAT, gotoken.IMAG:
			if x.lit != y.lit {
				cx := goconstant.MakeFromLiteral(x.lit, x.tok, 0)
				cy := goconstant.MakeFromLiteral(y.lit, y.tok, 0)
				if cx.Kind() == goconstant.Unknown || !goconstant.Compare(cx, gotoken.EQL, cy) {
					return false
				}
			}
		case gotoken.SEMICOLON:
		default:
			if x.lit != y.lit {
				return false
			}
		}
	}
	return true
}

// treeSig serializes what formatting must not change in a template: node
// kinds, tag names, attributes with their static values, text, comments.
// Go code and script and style content are compared on their own.
func treeSig(f *compiler.File) string {
	var b strings.Builder
	fmt.Fprintf(&b, "fm %v %v\n", f.Frontmatter.Present, f.Frontmatter.Closed)
	var walk func(n compiler.Node, depth int)
	walk = func(n compiler.Node, depth int) {
		fmt.Fprintf(&b, "%d %T", depth, n)
		switch n := n.(type) {
		case *compiler.Text:
			fmt.Fprintf(&b, " %q", n.Value)
		case *compiler.Comment:
			fmt.Fprintf(&b, " %q", n.Body.Text)
		case *compiler.Doctype:
			fmt.Fprintf(&b, " %q", n.Body.Text)
		case *compiler.HTMLComment:
			fmt.Fprintf(&b, " %q", n.Body.Text)
		case *compiler.Branch:
			fmt.Fprintf(&b, " else=%v", n.Else)
		case *compiler.Case:
			fmt.Fprintf(&b, " default=%v", n.Default)
		}
		if t := compiler.TagOf(n); t != nil {
			fmt.Fprintf(&b, " <%s self=%v", t.Name, t.SelfClosing)
			for _, a := range t.Attrs {
				fmt.Fprintf(&b, " %s:%s", a.Kind, a.Name)
				if a.IsStatic() {
					fmt.Fprintf(&b, "=%q", a.Value)
				}
			}
		}
		b.WriteByte('\n')
		for _, c := range compiler.ChildNodes(n) {
			walk(c, depth+1)
		}
	}
	walk(f.Template, 0)
	return b.String()
}

// countComments returns the number of comments in Go source s.
func countComments(s string) int {
	if !strings.Contains(s, "/") {
		return 0
	}
	fset := gotoken.NewFileSet()
	file := fset.AddFile("", -1, len(s))
	var sc goscanner.Scanner
	sc.Init(file, []byte(s), nil, goscanner.ScanComments)
	n := 0
	for {
		_, tok, _ := sc.Scan()
		if tok == gotoken.EOF {
			return n
		}
		if tok == gotoken.COMMENT {
			n++
		}
	}
}
