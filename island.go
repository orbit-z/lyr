package orbit

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"runtime"
	"strings"
)

// Deferred islands: server:defer placeholders and their endpoint (PLAN.md
// §3.4, §5b "Deferred island requests").

// Island is one entry of the server:defer island table, created by
// NewIsland in the generated orbit_gen/app.
type Island struct {
	id     string
	file   string
	render func(c *Ctx, w *Writer, props []byte) error // decode, then render
}

// NewIsland is one entry of the island table that the generated
// orbit_gen/app passes to NewSite: a component used with server:defer
// somewhere in the project. id is its component ID, file its .orbit path.
// On an island request, the opened props are decoded into a P with
// encoding/json (unknown fields are ignored, missing ones stay zero) and
// render is called with no slots.
func NewIsland[P any](id, file string, render func(*Ctx, *Writer, P, Slots) error) Island {
	return Island{id: id, file: file, render: func(c *Ctx, w *Writer, data []byte) error {
		var p P
		if err := json.Unmarshal(data, &p); err != nil {
			return &islandPropsError{err}
		}
		return callComponent(render, c, w, p)
	}}
}

// islandPropsError is opened props that don't decode into the component's
// Props: the island request gets a 400.
type islandPropsError struct{ err error }

func (e *islandPropsError) Error() string { return "orbit: island props: " + e.err.Error() }

// islandPrefix is the reserved path of island requests:
// /_orbit/island/<buildID>/<id>.
const islandPrefix = "/_orbit/island/"

// Defer is generated at each server:defer call site: it encodes props as
// JSON, seals them with the site's island key and writes the placeholder
//
//	<orbit-island src="/_orbit/island/<buildID>/<id>" props="<token>">fallback</orbit-island>
//
// fallback may be nil. The same props, component and build always give the
// same token. A JSON encoding error is the render error, at the .orbit
// position of the call site.
func Defer(c *Ctx, w *Writer, id string, props any, fallback Renderer) error {
	if w.err != nil {
		return w.err
	}
	data, err := json.Marshal(props)
	if err != nil {
		err = fmt.Errorf("orbit: <%s server:defer>: props can't be encoded as JSON: %w", islandName(id), err)
		if _, file, line, ok := runtime.Caller(1); ok && strings.HasSuffix(file, ".orbit") {
			err = &posError{pos: fmt.Sprintf("%s:%d", relativeFile(file), line), err: err}
		}
		w.setErr(err)
		return err
	}
	kr := c.keys
	if kr == nil {
		kr = defaultKeyring()
	}
	b := w.buf
	b.WriteString(`<orbit-island src="/_orbit/island/`)
	b.WriteString(kr.buildID)
	b.WriteByte('/')
	escapeAttrString(b, id)
	b.WriteString(`" props="`)
	// The token is base64url: nothing in it needs escaping.
	b.Grow(len(data)*4/3 + 64)
	b.Write(sealerOf(kr).AppendSeal(b.AvailableBuffer(), id, kr.buildID, data))
	b.WriteString(`">`)
	if fallback != nil {
		if err := fallback(w); err != nil {
			w.setErr(err)
			return err
		}
	}
	w.Raw("</orbit-island>")
	return w.err
}

// islandName is the component name of a component ID ("Comments" for
// "Comments-9b1e44a2").
func islandName(id string) string {
	if i := strings.LastIndexByte(id, '-'); i > 0 {
		return id[:i]
	}
	return id
}

// Shared, read-only header values for island responses (cap == len).
var (
	islandAllow        = []string{"GET, HEAD, POST"}
	islandCacheControl = []string{"private, no-store"}
	islandRobots       = []string{"noindex"}
	textPlain          = []string{"text/plain; charset=utf-8"}
)

// maxIslandBody is the largest POST body of an island request.
const maxIslandBody = 1 << 20

// serveIsland answers an island request; rest is the path after
// islandPrefix.
func (s *Site) serveIsland(w http.ResponseWriter, r *http.Request, rest string) {
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodPost:
	default:
		h := w.Header()
		h["Allow"] = islandAllow
		h["Content-Length"] = zeroLength
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	build, id, ok := strings.Cut(rest, "/")
	isl := s.islands[id]
	if !ok || build == "" || isl == nil {
		emptyNotFound(w)
		return
	}

	buf := getBuffer()
	defer func() { putBuffer(buf) }() // a join replaces buf
	var token string
	if r.Method == http.MethodPost {
		if _, err := buf.ReadFrom(http.MaxBytesReader(w, r.Body, maxIslandBody)); err != nil {
			var tooBig *http.MaxBytesError
			if errors.As(err, &tooBig) {
				emptyStatus(w, http.StatusRequestEntityTooLarge)
			} else {
				emptyStatus(w, http.StatusBadRequest)
			}
			return
		}
		token = string(bytes.TrimSpace(buf.Bytes()))
		buf.Reset()
	} else {
		token = queryValue(r.URL.RawQuery, "p")
	}
	props, err := sealerOf(s.keys).Open(id, build, token)
	if err != nil {
		emptyStatus(w, http.StatusBadRequest)
		return
	}

	c := s.acquire(r)
	defer releaseCtx(c)
	wr := writerPool.Get().(*Writer)
	wr.reset(buf)
	c.w = wr
	wr.ctx, wr.limit = c, s.limit
	err = isl.render(c, wr, props)
	if err == nil {
		err = wr.err
	}
	if wr.grp != nil {
		err = join(wr, err)
		buf = wr.buf
	}
	wr.reset(nil)
	writerPool.Put(wr)
	if err != nil {
		s.islandFailed(w, r, err, c.responseHeader())
		return
	}

	h := w.Header()
	for k, v := range c.responseHeader() {
		h[k] = v
	}
	if _, ok := h["Content-Type"]; !ok {
		h["Content-Type"] = htmlContentType
	}
	if _, ok := h["Cache-Control"]; !ok {
		h["Cache-Control"] = islandCacheControl
	}
	if _, ok := h["X-Robots-Tag"]; !ok {
		h["X-Robots-Tag"] = islandRobots
	}
	h["Content-Length"] = contentLength(buf.Len())
	w.WriteHeader(c.responseStatus())
	if r.Method != http.MethodHead {
		w.Write(buf.Bytes())
	}
}

// islandFailed answers an island render that returned err: props that
// don't decode give a 400, a redirect its 3xx, ErrNotFound an empty 404,
// anything else goes to the error handler, then a 500 (in dev, with the
// error text as the body).
func (s *Site) islandFailed(w http.ResponseWriter, r *http.Request, err error, head http.Header) {
	var re *RedirectError
	var pe *islandPropsError
	switch {
	case errors.As(err, &pe):
		emptyStatus(w, http.StatusBadRequest)
	case errors.As(err, &re):
		h := w.Header()
		for k, v := range head {
			h[k] = v
		}
		http.Redirect(w, r, re.Location, re.Code)
	case errors.Is(err, ErrNotFound):
		emptyNotFound(w)
	default:
		if s.onError != nil {
			h := w.Header()
			h["Cache-Control"] = islandCacheControl
			h["X-Robots-Tag"] = islandRobots
			if s.onError(w, r, err) {
				return
			}
		}
		log.Printf("orbit: %s %s: %v", r.Method, r.URL.Path, err)
		if !s.dev {
			emptyStatus(w, http.StatusInternalServerError)
			return
		}
		body := err.Error() + "\n"
		h := w.Header()
		h["Content-Type"] = textPlain
		h["Content-Length"] = contentLength(len(body))
		w.WriteHeader(http.StatusInternalServerError)
		if r.Method != http.MethodHead {
			w.Write([]byte(body))
		}
	}
}

// emptyStatus writes a response with status code and no body.
func emptyStatus(w http.ResponseWriter, code int) {
	w.Header()["Content-Length"] = zeroLength
	w.WriteHeader(code)
}

// queryValue returns the first value of key in a raw query string, decoded
// like url.ParseQuery, or "". It allocates only when the value is escaped.
func queryValue(raw, key string) string {
	for raw != "" {
		var pair string
		pair, raw, _ = strings.Cut(raw, "&")
		k, v, _ := strings.Cut(pair, "=")
		if k != key {
			continue
		}
		if strings.ContainsAny(v, "%+") {
			u, err := url.QueryUnescape(v)
			if err != nil {
				return ""
			}
			return u
		}
		return v
	}
	return ""
}
