package format

// <script> and <style> content, formatted with bun fmt (PLAN.md §6.1).

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"

	"github.com/evanw/esbuild/pkg/api"

	"github.com/orbit-z/orbit/internal/compiler"
)

// bunSlots bounds the bun fmt processes running at once, across files.
var bunSlots = make(chan struct{}, runtime.GOMAXPROCS(0))

// jsTypes are the script type values the HTML spec treats as JavaScript,
// plus module.
var jsTypes = map[string]bool{
	"module": true, "application/ecmascript": true, "application/javascript": true,
	"application/x-ecmascript": true, "application/x-javascript": true, "text/ecmascript": true,
	"text/javascript": true, "text/javascript1.0": true, "text/javascript1.1": true,
	"text/javascript1.2": true, "text/javascript1.3": true, "text/javascript1.4": true,
	"text/javascript1.5": true, "text/jscript": true, "text/livescript": true,
	"text/x-ecmascript": true, "text/x-javascript": true,
}

// blockExt returns the extension a block is formatted as ("ts", "js",
// "css"), or "" to leave it as it is.
func blockExt(n compiler.Node) string {
	switch n := n.(type) {
	case *compiler.Style:
		return "css"
	case *compiler.Script:
		if n.JSON {
			return ""
		}
		if n.Processed {
			return "ts"
		}
		t := n.Attr("type")
		if t == nil {
			return "js"
		}
		if t.IsStatic() && jsTypes[strings.ToLower(strings.TrimSpace(t.Value))] {
			return "js"
		}
	}
	return ""
}

// countBlocks returns the number of script and style blocks Bun would
// format.
func (fx *fileFmt) countBlocks() int {
	n := 0
	for _, r := range fx.regions {
		if (r.kind == rScript || r.kind == rStyle) && blockExt(r.node) != "" && strings.TrimSpace(fx.src[r.span.Start:r.span.End]) != "" {
			n++
		}
	}
	return n
}

// block formats one script or style. The content keeps its indentation: its
// common leading whitespace is removed before Bun and added back after.
// Content that starts on the tag's line moves to its own lines, one unit in
// from the tag; the closing tag goes on its own line at the tag's
// indentation.
func (fx *fileFmt) block(r *region) {
	ext := blockExt(r.node)
	content := fx.src[r.span.Start:r.span.End]
	if ext == "" || strings.TrimSpace(content) == "" {
		return
	}
	tag := compiler.TagOf(r.node)
	tagIndent := r.indent
	c := lf(content)
	lines := strings.Split(c, "\n")
	// The last line, if blank, is the closing tag's indentation.
	if len(lines) > 1 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	// origLine[i] is the content line (0-based) that input line i comes
	// from, and cut[i] how many bytes were removed from its start.
	var in []string
	var origLine, cut []int
	var k string
	if strings.TrimSpace(lines[0]) != "" {
		// Content on the tag's line.
		first := strings.TrimLeft(lines[0], " \t")
		in = append(in, first)
		origLine, cut = append(origLine, 0), append(cut, len(lines[0])-len(first))
		rest := lines[1:]
		kr := commonIndent(rest)
		for i, l := range rest {
			in = append(in, dedent(l, kr))
			origLine, cut = append(origLine, i+1), append(cut, len(l)-len(dedent(l, kr)))
		}
		k = tagIndent + fx.unit
	} else {
		body := lines[1:]
		k = commonIndent(body)
		for i, l := range body {
			in = append(in, dedent(l, k))
			origLine, cut = append(origLine, i+1), append(cut, len(l)-len(dedent(l, k)))
		}
	}
	input := strings.Join(in, "\n") + "\n"
	name := fmt.Sprintf("%s.%d.%s", fx.opts.Path, r.index, ext)
	out, line, col, msg, err := runBun(fx.opts, name, input)
	if err != nil {
		off := r.span.Start
		if line > 0 && line <= len(origLine) {
			off = offsetAt(fx.src, r.span.Start, origLine[line-1]+1, cut[line-1]+col)
		}
		fx.bunErrs = append(fx.bunErrs, errorAt(off, fmt.Sprintf("%s: %s", regionName(r.kind), msg)))
		return
	}
	out = strings.TrimSuffix(out, "\n")
	outLines := strings.Split(out, "\n")
	for i, l := range outLines {
		if strings.TrimSpace(l) != "" {
			outLines[i] = k + l
		}
	}
	newContent := "\n" + strings.Join(outLines, "\n") + "\n" + tagIndent
	if strings.Contains(newContent, "</"+tag.Name) || !sameMinified(ext, c, newContent) {
		fx.warns = append(fx.warns, warning(compiler.Span{Start: tag.StartTag.Start, End: tag.StartTag.End},
			"formatting this "+regionName(r.kind)+" would change its code; left as is (please report this)"))
		return
	}
	r.out, r.done = newContent, true
}

// commonIndent returns the longest leading whitespace shared by the
// non-blank lines.
func commonIndent(lines []string) string {
	k, have := "", false
	for _, l := range lines {
		if strings.TrimSpace(l) == "" {
			continue
		}
		ws := l[:len(l)-len(strings.TrimLeft(l, " \t"))]
		if !have {
			k, have = ws, true
			continue
		}
		n := 0
		for n < len(k) && n < len(ws) && k[n] == ws[n] {
			n++
		}
		k = k[:n]
	}
	return k
}

// dedent removes k from a non-blank line; blank lines are kept as they are.
func dedent(l, k string) string {
	if strings.TrimSpace(l) == "" {
		return l
	}
	return strings.TrimPrefix(l, k)
}

var bunAt = regexp.MustCompile(`(?m)^\s*at .*:(\d+):(\d+)\s*$`)

// runBun runs bun fmt on input. On failure it returns Bun's message and, if
// Bun gave one, the 1-based line and column in input.
func runBun(opts Options, name, input string) (out string, line, col int, msg string, err error) {
	ctx := opts.Context
	if ctx == nil {
		ctx = context.Background()
	}
	timeout := opts.Timeout
	if timeout == 0 {
		timeout = DefaultTimeout
	}
	select {
	case bunSlots <- struct{}{}:
	case <-ctx.Done():
		return "", 0, 0, ctx.Err().Error(), ctx.Err()
	}
	defer func() { <-bunSlots }()
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, opts.Bun, "fmt", "--no-script", "--stdin-filepath="+name)
	cmd.Dir = opts.BunDir
	cmd.Stdin = strings.NewReader(input)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err = cmd.Run()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return "", 0, 0, fmt.Sprintf("bun fmt timed out after %s", timeout), ctx.Err()
	}
	if err != nil {
		msg = "bun fmt failed: " + err.Error()
		for _, l := range strings.Split(stderr.String(), "\n") {
			if m, ok := strings.CutPrefix(strings.TrimSpace(l), "error: "); ok {
				msg = m
				break
			}
		}
		if m := bunAt.FindStringSubmatch(stderr.String()); m != nil {
			line, _ = strconv.Atoi(m[1])
			col, _ = strconv.Atoi(m[2])
		}
		return "", line, col, msg, err
	}
	return stdout.String(), 0, 0, "", nil
}

// sameMinified reports whether esbuild minifies both versions of a block to
// the same code (whitespace for scripts; whitespace and syntax for styles,
// since CSS printing keeps numbers as written).
func sameMinified(ext, a, b string) bool {
	opts := api.TransformOptions{MinifyWhitespace: true, LegalComments: api.LegalCommentsNone}
	switch ext {
	case "ts":
		opts.Loader = api.LoaderTS
	case "js":
		opts.Loader = api.LoaderJS
	case "css":
		opts.Loader = api.LoaderCSS
		opts.MinifySyntax = true
	}
	ra := api.Transform(a, opts)
	rb := api.Transform(b, opts)
	return len(ra.Errors) == 0 && len(rb.Errors) == 0 && bytes.Equal(ra.Code, rb.Code)
}
