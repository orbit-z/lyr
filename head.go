package orbit

import (
	"bytes"
	"strings"
	"sync"
)

// Buffer pool for rendered responses.

const (
	// initialBufferSize is the capacity of a new pooled buffer, enough for a
	// typical page without growing.
	initialBufferSize = 8 << 10
	// maxPooledBuffer is the largest capacity returned to the pool. Bigger
	// buffers (from an unusually large page) are dropped, so one huge
	// response doesn't pin memory for the life of the process.
	maxPooledBuffer = 1 << 20
)

var bufferPool = sync.Pool{
	New: func() any { return bytes.NewBuffer(make([]byte, 0, initialBufferSize)) },
}

// getBuffer returns an empty buffer from the pool.
func getBuffer() *bytes.Buffer {
	return bufferPool.Get().(*bytes.Buffer)
}

// putBuffer returns buf to the pool. buf must not be used afterwards.
func putBuffer(buf *bytes.Buffer) {
	if buf.Cap() > maxPooledBuffer {
		return
	}
	buf.Reset()
	bufferPool.Put(buf)
}

// headTags returns the tags a page gets before its </head>: loader (the
// loader tags, or "") and then the <link> and <script> tags for assets, in
// asset order. Only "css" and "js" assets produce tags. "lazy" assets are
// loaded by the client loader (whose map is in loader), and other kinds are
// ignored. In production it runs once per route, in NewSite.
func headTags(loader string, assets []Asset) string {
	var tags strings.Builder
	tags.WriteString(loader)
	for _, a := range assets {
		switch a.Kind {
		case "css":
			tags.WriteString(`<link rel="stylesheet" href="`)
			tags.WriteString(attrEscape(a.URL))
			tags.WriteString(`">`)
		case "js":
			tags.WriteString(`<script type="module" src="`)
			tags.WriteString(attrEscape(a.URL))
			tags.WriteString(`"></script>`)
		}
	}
	return tags.String()
}

// attrEscape returns s HTML-escaped.
func attrEscape(s string) string {
	if firstEsc(s) < 0 {
		return s
	}
	var b bytes.Buffer
	escapeString(&b, s)
	return b.String()
}

// injectHead inserts tags (from headTags) into the rendered page in buf, for
// a page whose render never called Writer.HeadEnd: a page with a </head>
// gets its tags written there during the render, with no copy of the page.
// The tags go right after a leading <!DOCTYPE …> (optionally preceded by
// whitespace), so the doctype stays first and the page doesn't drop into
// quirks mode; otherwise at the start.
func injectHead(buf *bytes.Buffer, tags string) {
	if tags == "" {
		return
	}
	page := buf.Bytes()
	off := doctypeEnd(page)
	n := len(tags)
	oldLen := len(page)
	buf.WriteString(tags) // grow by n; the bytes are moved into place below
	b := buf.Bytes()
	copy(b[off+n:], b[off:oldLen])
	copy(b[off:], tags)
}

// doctypeEnd returns the offset just after a leading <!DOCTYPE …> in page
// (case-insensitive, after optional ASCII whitespace), or 0 if there is none.
func doctypeEnd(page []byte) int {
	i := 0
	for i < len(page) && isASCIISpace(page[i]) {
		i++
	}
	const doctype = "<!doctype"
	if len(page)-i < len(doctype) || !bytes.EqualFold(page[i:i+len(doctype)], []byte(doctype)) {
		return 0
	}
	end := bytes.IndexByte(page[i:], '>')
	if end < 0 {
		return 0
	}
	return i + end + 1
}

func isASCIISpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f'
}
