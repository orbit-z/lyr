// Command server is the dev fixture's server entry: a custom /robots.txt
// and /pid route before the pages, a gzip middleware that compresses for
// clients that accept it, and a marker file written after a graceful
// shutdown (orbit.ListenAndServe returns nil after SIGINT).
package main

import (
	"compress/gzip"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/orbit-z/orbit"

	"example.com/devsite/orbit_gen/app"
)

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /robots.txt", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("custom robots"))
	})
	mux.HandleFunc("GET /pid", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(strconv.Itoa(os.Getpid())))
	})
	mux.Handle("/", app.New().Handler())
	if err := orbit.ListenAndServe(gzipped(mux)); err != nil {
		log.Fatal(err)
	}
	dir := filepath.Join(".orbit", "stopped")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, strconv.Itoa(os.Getpid())), []byte("SIGINT"), 0o644)
}

type gzipWriter struct {
	http.ResponseWriter
	zw *gzip.Writer
}

func (g gzipWriter) WriteHeader(code int) {
	g.Header().Del("Content-Length")
	g.ResponseWriter.WriteHeader(code)
}

func (g gzipWriter) Write(b []byte) (int, error) {
	g.Header().Del("Content-Length")
	return g.zw.Write(b)
}

// gzipped compresses every response for clients that accept gzip.
func gzipped(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Del("Content-Length")
		zw := gzip.NewWriter(w)
		defer zw.Close()
		next.ServeHTTP(gzipWriter{w, zw}, r)
	})
}
