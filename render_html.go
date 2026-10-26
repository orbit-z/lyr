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
		}
		ctx.w = wr
	}
	defer func() {
		if ctx != nil {
			ctx.w = parent
		}
		wr.reset(nil)
		writerPool.Put(wr)
		putBuffer(buf)
	}()
	err := r(wr)
	if err == nil {
		err = wr.err
	}
	if err != nil {
		return "", err
	}
	return HTML(buf.String()), nil
}
