package lyr

import (
	"net/http"
	"os"
)

// RenderComponent renders one component as an HTML fragment into w, for
// example the response to an htmx request:
//
//	btn := likebutton.Props{Slug: r.PathValue("slug"), Likes: n}
//	if err := lyr.RenderComponent(w, r, likebutton.Render, btn); err != nil {
//		http.Error(w, "render failed", http.StatusInternalServerError)
//	}
//
// The fragment is buffered like a page. On success it's written once, with
// the status set by ctx.SetStatus (200 by default), the headers set through
// ctx.Header, Content-Type text/html; charset=utf-8 unless the component
// set one, and Content-Length. No <link> or <script> tags are inserted: a
// fragment has no <head>.
//
// If rendering fails, nothing has been written to w and the error is
// returned, so the caller can still send its own response. That includes a
// panic in the component, returned as an error that names the .lyr line,
// and the errors of ctx.Redirect and ErrNotFound, which a page would turn
// into a redirect or the 404 page.
//
// The component's locals are the ones attached to r with EnsureLocals or
// WithLocals; ctx.Site is nil.
func RenderComponent[P any](w http.ResponseWriter, r *http.Request, render func(*Ctx, *Writer, P, Slots) error, props P) error {
	c := acquireCtx(r, nil, os.Getenv("LYR_DEV") == "1")
	c.keys = defaultKeyring()
	if ch, ok := w.(cacheHolder); ok {
		c.cacheP = ch.lyrCache() // under Site.CacheHandler (PLAN.md §4h)
	}
	buf := getBuffer()
	wr := writerPool.Get().(*Writer)
	wr.reset(buf)
	c.w = wr
	wr.ctx, wr.limit = c, defaultConcurrencyLimit
	err := callComponent(render, c, wr, props)
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
		putBuffer(buf)
		releaseCtx(c)
		return err
	}
	h := w.Header()
	for k, v := range c.responseHeader() {
		h[k] = v
	}
	if _, ok := h["Content-Type"]; !ok {
		h["Content-Type"] = htmlContentType
	}
	h["Content-Length"] = contentLength(buf.Len())
	w.WriteHeader(c.responseStatus())
	if r.Method != http.MethodHead {
		w.Write(buf.Bytes())
	}
	putBuffer(buf)
	releaseCtx(c)
	return nil
}

// callComponent is callRender for a component's Render: a panic becomes a
// *panicError, except http.ErrAbortHandler, which is re-panicked.
func callComponent[P any](render func(*Ctx, *Writer, P, Slots) error, c *Ctx, w *Writer, props P) (err error) {
	defer func() {
		if p := recover(); p != nil {
			if p == http.ErrAbortHandler {
				panic(p)
			}
			err = recoveredPanic(p)
		}
	}()
	return render(c, w, props, Slots{})
}
