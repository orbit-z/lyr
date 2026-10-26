package markdown

import (
	"fmt"
	"path"
	"strings"

	"github.com/orbit-z/orbit/internal/compiler"
)

// Doc is a parsed Markdown file.
type Doc struct {
	// Path is project-relative ("src/pages/blog/hello.md"), Src the file.
	Path  string
	Src   string
	Lines *compiler.LineIndex
	// Fields are the frontmatter keys other than layout, in order.
	Fields []*Field
	// Layout is the layout's resolved project-relative path ("" for none),
	// LayoutRaw the value as written and LayoutPos its offset.
	Layout    string
	LayoutRaw string
	LayoutPos int
	// HTML is the rendered body.
	HTML     string
	Headings []Heading
	// Links are the rewritten links to Markdown pages.
	Links []Link
	// Highlighted is set when a code block was highlighted by Chroma.
	Highlighted bool
	// Diagnostics are the frontmatter, layout and link errors, by offset.
	Diagnostics []compiler.Diagnostic
}

func (d *Doc) errorf(off int, format string, args ...any) {
	d.Diagnostics = append(d.Diagnostics, compiler.Diagnostic{Severity: compiler.SeverityError,
		Span: compiler.Span{Start: off, End: off}, Message: fmt.Sprintf(format, args...)})
}

// Parse parses the Markdown file at the project-relative path p: its
// frontmatter, its layout path (not checked against the project) and its
// body, converted to HTML with options o. resolve tells it about the
// project's other Markdown files, for links (nil: every link to a .md file
// is missing). It never fails: problems are d.Diagnostics.
func Parse(p, src string, o Options, resolve Resolver) *Doc {
	d := &Doc{Path: p, Src: src, Lines: compiler.NewLineIndex(src)}
	ySpan, block, unclosed := splitFrontmatter(src)
	body := []byte(src)
	if block.End > 0 {
		if unclosed {
			d.errorf(0, "the frontmatter has no closing --- line")
		} else {
			fp := &fmParser{src: src, lines: d.Lines}
			fields := fp.parse(ySpan)
			d.Diagnostics = append(d.Diagnostics, fp.diags...)
			d.setFields(fields)
		}
		// Blank the frontmatter out, keeping line breaks: goldmark sees
		// blank lines, and its offsets stay file offsets.
		for i := range block.End {
			if body[i] != '\n' {
				body[i] = ' '
			}
		}
	}
	if !unclosed {
		d.body(body, o, resolve)
	}
	return d
}

// setFields takes the layout key out of the frontmatter.
func (d *Doc) setFields(fields []*Field) {
	for _, f := range fields {
		if f.Key != "layout" {
			d.Fields = append(d.Fields, f)
			continue
		}
		v := f.Value
		if v.Kind == KindList || v.Kind == KindMap || v.Raw == "" {
			d.errorf(v.Pos, "layout must be the path of an .orbit file, such as ../layouts/Post.orbit")
			continue
		}
		d.LayoutRaw, d.LayoutPos = v.Raw, v.Pos
		switch {
		case !strings.HasPrefix(v.Raw, "./") && !strings.HasPrefix(v.Raw, "../"):
			d.errorf(v.Pos, "layout %s must be a path relative to this file, starting with ./ or ../", v.Raw)
		case path.Ext(v.Raw) != ".orbit":
			d.errorf(v.Pos, "layout %s must be an .orbit file", v.Raw)
		default:
			resolved := path.Join(path.Dir(d.Path), v.Raw)
			if !strings.HasPrefix(resolved, "src/") {
				d.errorf(v.Pos, "layout %s is outside src/", v.Raw)
			} else {
				d.Layout = resolved
			}
		}
	}
}
