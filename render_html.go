package orbit

import "errors"

// errHeadInCapture is the render error of a </head> inside RenderHTML.
var errHeadInCapture = errors.New("orbit: </head> can't be inside orbit.RenderHTML; keep the document head outside the captured content")

// RenderHTML renders r and returns its output as HTML, for example to keep a
// slot's markup in a fragment cache and write it later with set:html:
//
//	html, err := cache.Get(props.Key, func() (orbit.HTML, error) {
//		return orbit.RenderHTML(ctx, slots.Default)
//	})
//
// The output is exactly what r would write in place. A nil r (no slot
// content) returns "". The render's error is returned, not kept by the page,
// and its partial output is dropped; a panic isn't recovered here.
//
// The capture continues the component stack of the writer rendering ctx's
// request, so the depth limit counts through captures, and captures can
// nest. A </head> inside the capture is a render error: the page's asset
// tags are written at its own </head>, never into captured HTML.
//
// server:concurrent components inside the capture still render in their own
// goroutines; RenderHTML waits for them before it returns, so the HTML is
// complete.
func RenderHTML(ctx *Ctx, r Renderer) (HTML, error) {
	if r == nil {
		return "", nil
	}
	buf := getBuffer()
	wr := writerPool.Get().(*Writer)
	wr.reset(buf)
	wr.capture = true
	var parent *Writer
	if ctx != nil {
		parent = ctx.w
		if parent != nil {
			wr.stack = append(wr.stack, parent.stack...)
			wr.limit = parent.limit
		}
		ctx.w = wr
		wr.ctx = ctx
	}
	defer func() {
		if ctx != nil {
			ctx.w = parent
		}
		if wr.grp != nil { // a panic: the children still use the buffers
			groupOf(wr).wg.Wait()
			releaseHoles(wr)
		}
		wr.reset(nil)
		writerPool.Put(wr)
		putBuffer(buf)
	}()
	err := r(wr)
	if err == nil {
		err = wr.err
	}
	// The capture joins its own server:concurrent components, so its HTML
	// is complete (PLAN.md §3.6).
	if wr.grp != nil {
		err = join(wr, err)
		buf = wr.buf
	}
	if err != nil {
		return "", err
	}
	return HTML(buf.String()), nil
}
