package dev

import (
	"bufio"
	"bytes"
	"fmt"
	"html"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// posLine matches a "file:line:col: message" line of Orbit's or the go
// command's cleaned-up output.
var posLine = regexp.MustCompile(`^([^\s:][^:]*):(\d+):(\d+): (.*)$`)

// maxFrames is how many errors get a code frame.
const maxFrames = 10

// overlayHTML renders the error overlay page for f (PLAN.md §7.1): the
// message, each error's file:line:col with a code frame read from root,
// and the server's stderr. It loads the reload script, so fixing the error
// reloads the page.
func overlayHTML(root string, f *failure) []byte {
	var b bytes.Buffer
	b.WriteString(`<!DOCTYPE html>
<html lang="en"><head><meta charset="utf-8"><title>Orbit: ` + html.EscapeString(f.Title) + `</title>
<meta name="viewport" content="width=device-width, initial-scale=1">
<style>
:root{color-scheme:dark}
body{margin:0;background:#1b1d23;color:#e6e6e6;font:14px/1.5 ui-sans-serif,system-ui,sans-serif}
main{max-width:960px;margin:0 auto;padding:32px 20px}
h1{margin:0 0 4px;font-size:20px;color:#ff6b6b}
p.hint{margin:0 0 24px;color:#9aa0a6}
.err{margin:0 0 24px;padding:16px;background:#23262e;border-left:4px solid #ff6b6b;border-radius:4px}
.msg{margin:0;font:13px/1.5 ui-monospace,SFMono-Regular,Menlo,monospace;white-space:pre-wrap;word-break:break-word}
.pos{color:#7cc7ff}
pre.frame{margin:12px 0 0;padding:12px;overflow-x:auto;background:#15171c;border-radius:4px;font:13px/1.5 ui-monospace,SFMono-Regular,Menlo,monospace}
.frame .hl{color:#fff;background:#3a2326;display:inline-block;min-width:100%}
.frame .caret{color:#ff6b6b}
.ln{color:#6b717c;user-select:none}
pre.out{margin:0;padding:12px;overflow-x:auto;background:#15171c;border-radius:4px;font:13px/1.5 ui-monospace,SFMono-Regular,Menlo,monospace}
</style></head>
<body><main>
<h1>` + html.EscapeString(f.Title) + `</h1>
<p class="hint">Fix the error and save: this page reloads by itself. The same messages are in the terminal running orbit dev.</p>
`)
	frames := 0
	sc := bufio.NewScanner(strings.NewReader(f.Text))
	sc.Buffer(nil, 1<<20)
	var other []string
	flush := func() {
		if len(other) == 0 {
			return
		}
		b.WriteString(`<div class="err"><pre class="msg">`)
		b.WriteString(html.EscapeString(strings.Join(other, "\n")))
		b.WriteString("</pre></div>\n")
		other = nil
	}
	for sc.Scan() {
		line := sc.Text()
		m := posLine.FindStringSubmatch(line)
		if m == nil {
			if strings.HasPrefix(line, "# ") {
				continue // go build's "# package" header
			}
			other = append(other, line)
			continue
		}
		flush()
		b.WriteString(`<div class="err"><pre class="msg"><span class="pos">`)
		b.WriteString(html.EscapeString(m[1] + ":" + m[2] + ":" + m[3]))
		b.WriteString("</span>: ")
		b.WriteString(html.EscapeString(m[4]))
		b.WriteString("</pre>")
		if frames < maxFrames {
			ln, _ := strconv.Atoi(m[2])
			col, _ := strconv.Atoi(m[3])
			if fr := codeFrame(root, m[1], ln, col); fr != "" {
				frames++
				b.WriteString(`<pre class="frame">`)
				b.WriteString(fr)
				b.WriteString("</pre>")
			}
		}
		b.WriteString("</div>\n")
	}
	flush()
	if f.Output != "" {
		b.WriteString(`<h2 style="font-size:15px;margin:24px 0 8px">Your server's output</h2><pre class="out">`)
		b.WriteString(html.EscapeString(strings.TrimRight(f.Output, "\n")))
		b.WriteString("</pre>\n")
	}
	b.WriteString("</main>\n" + reloadTag + "\n</body></html>\n")
	return b.Bytes()
}

// codeFrame returns the HTML of lines line-2 … line+2 of the file at the
// project-relative path rel, with line highlighted and a caret under col
// (a 1-based byte column); "" when the file can't be read or has no such
// line, or rel leaves the project.
func codeFrame(root, rel string, line, col int) string {
	if line < 1 || filepath.IsAbs(rel) || strings.HasPrefix(filepath.ToSlash(filepath.Clean(rel)), "../") {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		return ""
	}
	lines := strings.Split(string(data), "\n")
	if line > len(lines) {
		return ""
	}
	first, last := max(1, line-2), min(len(lines), line+2)
	width := len(strconv.Itoa(last))
	var b strings.Builder
	for n := first; n <= last; n++ {
		text := strings.TrimRight(lines[n-1], "\r")
		text = strings.ReplaceAll(text, "\t", "    ")
		num := fmt.Sprintf("%*d │ ", width, n)
		if n == line {
			b.WriteString(`<span class="hl"><span class="ln">` + html.EscapeString("> "+num) + "</span>" + html.EscapeString(text) + "</span>\n")
			// The caret: the line's prefix up to col, with tabs expanded
			// like the text and every other character as one column.
			prefix := lines[n-1]
			if c := col - 1; c >= 0 && c <= len(prefix) {
				prefix = prefix[:c]
			}
			pad := strings.Count(prefix, "\t")*4 + len([]rune(strings.ReplaceAll(prefix, "\t", "")))
			b.WriteString(`<span class="ln">` + strings.Repeat(" ", width+5) + `</span><span class="caret">` + strings.Repeat(" ", pad) + "^</span>\n")
			continue
		}
		b.WriteString(`<span class="ln">` + html.EscapeString("  "+num) + "</span>" + html.EscapeString(text) + "\n")
	}
	return b.String()
}
