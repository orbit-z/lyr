package main

import (
	"compress/gzip"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
)

// Lyr leaves compression to the server around it (see docs/server.md,
// "Wrapping with middleware"); this is the docs site's own, standard
// library only.

// minGzipSize is the smallest body worth compressing, when its length is
// known: below it, gzip's header and checksum outweigh the savings.
const minGzipSize = 512

var gzipPool = sync.Pool{New: func() any {
	w, _ := gzip.NewWriterLevel(io.Discard, gzip.DefaultCompression)
	return w
}}

// gzipped compresses text responses (HTML, CSS, JS, SVG, XML, plain text)
// with gzip for clients that accept it. It adds Vary: Accept-Encoding to
// every such response, compressed or not, so caches keep the two apart.
// It leaves alone responses to Range requests (static files answer them
// with byte ranges of the uncompressed file), statuses other than 200,
// bodies already encoded, and small bodies. A compressed response loses
// its Content-Length, and its ETag becomes weak, since the bytes differ
// from the file's.
func gzipped(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gw := &gzipWriter{ResponseWriter: w, accept: acceptsGzip(r) && r.Header.Get("Range") == "", head: r.Method == http.MethodHead}
		defer gw.close()
		next.ServeHTTP(gw, r)
	})
}

type gzipWriter struct {
	http.ResponseWriter
	accept  bool // the request accepts gzip and isn't a Range request
	head    bool // a HEAD request: GET's headers, no body (and so no gzip stream)
	discard bool
	decided bool
	gz      *gzip.Writer // nil when not compressing
}

func (g *gzipWriter) WriteHeader(code int) {
	if !g.decided && code >= 200 { // 1xx responses (103 Early Hints) come before the real one
		g.decided = true
		g.decide(code)
	}
	g.ResponseWriter.WriteHeader(code)
}

func (g *gzipWriter) decide(code int) {
	h := g.Header()
	if !compressible(h.Get("Content-Type")) {
		return
	}
	h.Add("Vary", "Accept-Encoding")
	if !g.accept || code != http.StatusOK || h.Get("Content-Encoding") != "" {
		return
	}
	if n, err := strconv.Atoi(h.Get("Content-Length")); err == nil && n < minGzipSize {
		return
	}
	h.Del("Content-Length")
	h.Set("Content-Encoding", "gzip")
	if et := h.Get("Etag"); et != "" && !strings.HasPrefix(et, "W/") {
		h.Set("Etag", "W/"+et)
	}
	if g.head {
		g.discard = true
		return
	}
	g.gz = gzipPool.Get().(*gzip.Writer)
	g.gz.Reset(g.ResponseWriter)
}

func (g *gzipWriter) Write(b []byte) (int, error) {
	if !g.decided {
		if g.Header().Get("Content-Type") == "" {
			g.Header().Set("Content-Type", http.DetectContentType(b))
		}
		g.WriteHeader(http.StatusOK)
	}
	if g.discard {
		return len(b), nil
	}
	if g.gz != nil {
		return g.gz.Write(b)
	}
	return g.ResponseWriter.Write(b)
}

// Unwrap lets http.ResponseController reach the connection's writer.
func (g *gzipWriter) Unwrap() http.ResponseWriter { return g.ResponseWriter }

func (g *gzipWriter) close() {
	if g.gz == nil {
		return
	}
	_ = g.gz.Close()
	g.gz.Reset(io.Discard)
	gzipPool.Put(g.gz)
	g.gz = nil
}

// acceptsGzip reports whether Accept-Encoding lists gzip with a nonzero
// quality.
func acceptsGzip(r *http.Request) bool {
	for _, v := range r.Header.Values("Accept-Encoding") {
		for part := range strings.SplitSeq(v, ",") {
			name, params, _ := strings.Cut(strings.TrimSpace(part), ";")
			if !strings.EqualFold(strings.TrimSpace(name), "gzip") {
				continue
			}
			q := strings.ReplaceAll(strings.TrimSpace(params), " ", "")
			return q != "q=0" && q != "q=0.0" && q != "q=0.00" && q != "q=0.000"
		}
	}
	return false
}

func compressible(contentType string) bool {
	mt, _, _ := strings.Cut(contentType, ";")
	mt = strings.ToLower(strings.TrimSpace(mt))
	switch {
	case strings.HasPrefix(mt, "text/"):
		return true
	case mt == "application/javascript", mt == "application/json",
		mt == "application/xml", mt == "image/svg+xml", mt == "application/manifest+json":
		return true
	}
	return false
}
