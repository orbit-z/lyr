package main

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

var page = strings.Repeat("<p>Hello, Orbit.</p>\n", 200)

// buffered answers like an Orbit page: one write with a Content-Length.
var buffered = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Length", strconv.Itoa(len(page)))
	_, _ = io.WriteString(w, page)
})

// file answers like a static file: http.ServeContent, with an ETag.
var file = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Etag", `"abc"`)
	http.ServeContent(w, r, "app.js", time.Unix(0, 0), strings.NewReader(page))
})

func do(t *testing.T, h http.Handler, method string, hdr ...string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, "/", nil)
	for i := 0; i+1 < len(hdr); i += 2 {
		r.Header.Set(hdr[i], hdr[i+1])
	}
	rec := httptest.NewRecorder()
	gzipped(h).ServeHTTP(rec, r)
	return rec
}

func TestGzip(t *testing.T) {
	rec := do(t, buffered, "GET", "Accept-Encoding", "gzip, br")
	if rec.Header().Get("Content-Encoding") != "gzip" || rec.Header().Get("Content-Length") != "" || rec.Header().Get("Vary") != "Accept-Encoding" {
		t.Fatalf("headers: %v", rec.Header())
	}
	zr, err := gzip.NewReader(rec.Body)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := io.ReadAll(zr); string(b) != page {
		t.Errorf("body doesn't round-trip")
	}

	// No gzip: untouched, but still Vary.
	for _, ae := range []string{"", "br", "gzip;q=0"} {
		rec = do(t, buffered, "GET", "Accept-Encoding", ae)
		if rec.Header().Get("Content-Encoding") != "" || rec.Body.String() != page || rec.Header().Get("Vary") != "Accept-Encoding" {
			t.Errorf("Accept-Encoding %q: %v", ae, rec.Header())
		}
	}

	// Static files: weak ETag when compressed; ranges and 304s untouched.
	rec = do(t, file, "GET", "Accept-Encoding", "gzip")
	if rec.Header().Get("Content-Encoding") != "gzip" || rec.Header().Get("Etag") != `W/"abc"` {
		t.Errorf("file: %v", rec.Header())
	}
	rec = do(t, file, "GET", "Accept-Encoding", "gzip", "Range", "bytes=0-9")
	if rec.Code != http.StatusPartialContent || rec.Header().Get("Content-Encoding") != "" || rec.Body.Len() != 10 {
		t.Errorf("range: %d %v", rec.Code, rec.Header())
	}
	rec = do(t, file, "GET", "Accept-Encoding", "gzip", "If-None-Match", `W/"abc"`)
	if rec.Code != http.StatusNotModified || rec.Header().Get("Content-Encoding") != "" {
		t.Errorf("revalidation: %d %v", rec.Code, rec.Header())
	}

	// HEAD: the same headers as GET.
	rec = do(t, file, "HEAD", "Accept-Encoding", "gzip")
	if rec.Header().Get("Content-Encoding") != "gzip" || rec.Header().Get("Content-Length") != "" {
		t.Errorf("HEAD: %v", rec.Header())
	}

	// Small bodies and images aren't compressed.
	small := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("Content-Length", "3")
		_, _ = io.WriteString(w, "ok\n")
	})
	if rec = do(t, small, "GET", "Accept-Encoding", "gzip"); rec.Header().Get("Content-Encoding") != "" || rec.Body.String() != "ok\n" {
		t.Errorf("small: %v", rec.Header())
	}
	png := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(bytes.Repeat([]byte{1}, 2000))
	})
	if rec = do(t, png, "GET", "Accept-Encoding", "gzip"); rec.Header().Get("Content-Encoding") != "" || rec.Header().Get("Vary") != "" {
		t.Errorf("png: %v", rec.Header())
	}
}
