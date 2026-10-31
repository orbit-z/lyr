package lyr

import (
	"bytes"
	"errors"
	"net/http"
	"sync"
	"sync/atomic"
)

// Concurrent rendering: server:concurrent call sites (PLAN.md §3.6).
//
// A marked component renders in its own goroutine, into its own pooled
// Writer, buffer and Ctx (a copy of the forking one). The forking Writer
// records a hole (its buffer offset and the child Writer) and keeps
// rendering. The entry point (a page, an island, RenderComponent, lyrtest,
// or a RenderHTML capture for its own content) joins once at the end: it
// waits for every goroutine of the render, then copies the buffers in
// output order into one, inserting the head tags at the first </head> in
// output order, takes the first error in output order, and merges the
// children's status and headers in output order.
//
// Only marked call sites touch this state: on the unmarked path a Writer
// has no group and no holes, and nothing here runs.

// defaultConcurrencyLimit is the per-render cap on marked components
// running at once, without WithConcurrencyLimit.
const defaultConcurrencyLimit = 16

// hole is a marked component's place in its parent's output.
type hole struct {
	off int     // offset in the parent's buffer
	w   *Writer // the child's Writer; its ctx is the child's Ctx
}

// group is the shared state of one render's goroutines: the only
// synchronized state of concurrent rendering.
type group struct {
	wg      sync.WaitGroup
	running atomic.Int32 // goroutines running now, for the cap
	aborted atomic.Bool  // a goroutine panicked with http.ErrAbortHandler
}

// groupOf returns w's group, or nil before the first fork. Writer.grp holds
// it as any, which keeps sync.WaitGroup and the atomics out of the type
// graph of every generated package (§4b).
func groupOf(w *Writer) *group {
	g, _ := w.grp.(*group)
	return g
}

var groupPool = sync.Pool{New: func() any { return new(group) }}

// errAborted is a child's error after it panicked with
// http.ErrAbortHandler; the join re-panics with http.ErrAbortHandler.
var errAborted = errors.New("lyr: render aborted (http.ErrAbortHandler)")

// testForkHook, when set (tests only), is called with +1 for each child
// Ctx/Writer/buffer set taken from the pools and -1 for each returned.
var testForkHook func(delta int)

// WithConcurrencyLimit sets how many server:concurrent components of one
// render may run at once, counted across nesting (default 16). Past the
// limit, a marked component renders inline, in order, on the goroutine
// that reaches it, with the same output. 0 renders every marked component
// inline; a negative n means no limit.
func WithConcurrencyLimit(n int) Option {
	return func(o *siteOptions) {
		o.concLimit = int32(max(min(n, 1<<30), -1))
		o.concSet = true
	}
}

// concurrencyLimit returns the Writer limit for a site's options.
func (o *siteOptions) concurrencyLimit() int32 {
	if !o.concSet {
		return defaultConcurrencyLimit
	}
	return o.concLimit
}

// For generated code. CtxOf is emitted at the start of slot content that
// uses the Ctx: it returns the Ctx of the goroutine that renders into w
// (slot content can run in a server:concurrent component's goroutine, with
// that component's Writer), or c when w has none, such as a Writer from
// NewWriter.
func CtxOf(w *Writer, c *Ctx) *Ctx {
	if w.ctx != nil {
		return w.ctx
	}
	return c
}

// For generated code. Concurrent is emitted at each server:concurrent call
// site. It starts
// render(c', w', props, slots) in its own goroutine, with its own Writer w'
// and Ctx c' (a copy of c), records the component's place in w's output
// and returns nil: the component's output and errors come out when the
// render is joined, in source order. It returns w's error, and starts
// nothing, when w has already failed.
//
// It renders inline, as a plain call, when w belongs to no Lyr render
// (NewWriter), past the concurrency limit (WithConcurrencyLimit), and
// inside an attribute value.
func Concurrent[P any](c *Ctx, w *Writer, render func(*Ctx, *Writer, P, Slots) error, props P, slots Slots) error {
	if w.err != nil {
		return w.err
	}
	cw := fork(w, c)
	if cw == nil {
		return render(c, w, props, slots)
	}
	go runConcurrent(cw, render, props, slots)
	return nil
}

// fork returns the Writer of a new child rendering at w's current offset,
// with its Ctx (a copy of c's request state), or nil to render inline.
func fork(w *Writer, c *Ctx) *Writer {
	if w.limit == 0 {
		return nil
	}
	g := groupOf(w)
	if g == nil {
		g = groupPool.Get().(*group)
		w.grp = g
	}
	if w.limit > 0 && g.running.Add(1) > w.limit {
		g.running.Add(-1)
		return nil
	}
	cc := ctxPool.Get().(*Ctx)
	h := cc.header
	*cc = Ctx{
		Request:    c.Request,
		URL:        c.URL,
		Site:       c.Site,
		paramNames: c.paramNames,
		paramVals:  c.paramVals, // the root Ctx's storage, released after the join
		locals:     c.locals,
		dev:        c.dev,
		keys:       c.keys,
		route:      c.route,
		header:     h,
	}
	pk := c.Cache()
	cc.cache.mode, cc.cache.site = pk.mode, pk.site
	cw := writerPool.Get().(*Writer)
	cw.reset(getBuffer())
	cw.stack = append(cw.stack, w.stack...)
	cw.ctx, cw.limit, cw.grp = cc, w.limit, g
	cw.capture = w.capture
	cc.w = cw
	w.holes = append(w.holes, hole{off: w.buf.Len(), w: cw})
	if testForkHook != nil {
		testForkHook(1)
	}
	g.wg.Add(1)
	return cw
}

// runConcurrent renders one marked component in its goroutine. Its error,
// or a recovered panic, becomes w's error.
func runConcurrent[P any](w *Writer, render func(*Ctx, *Writer, P, Slots) error, props P, slots Slots) {
	g := groupOf(w)
	defer func() {
		if p := recover(); p != nil {
			if p == http.ErrAbortHandler {
				g.aborted.Store(true)
				w.setErr(errAborted)
			} else {
				w.setErr(recoveredPanic(p))
			}
		}
		if w.limit > 0 {
			g.running.Add(-1)
		}
		g.wg.Done()
	}()
	if err := render(w.ctx, w, props, slots); err != nil {
		w.setErr(err)
	}
}

// join finishes a render that forked (w.grp != nil): w is the entry
// point's Writer and err the render's own error. It waits for every
// goroutine, then returns the first error in output order (a child's error
// comes before w's own, which can only have happened after the holes it
// recorded). It merges the children's status and headers into w.ctx, in
// output order; on success it assembles the output into a new buffer,
// which replaces w.buf (the old one goes back to the pool). The children
// go back to their pools. If a goroutine panicked with
// http.ErrAbortHandler, join re-panics with it.
func join(w *Writer, err error) error {
	g := groupOf(w)
	g.wg.Wait()
	if e := holesErr(w); e != nil {
		err = e
	}
	if w.ctx != nil {
		mergeResponse(w, w.ctx)
	}
	if err == nil {
		out := getBuffer()
		out.Grow(assembledLen(w) + len(w.tags))
		assemble(w, out, &w.tags)
		putBuffer(w.buf)
		w.buf = out
	}
	releaseHoles(w)
	w.holes = w.holes[:0]
	w.grp = nil
	aborted := g.aborted.Load()
	g.aborted.Store(false)
	groupPool.Put(g)
	if aborted {
		panic(http.ErrAbortHandler)
	}
	return err
}

// holesErr returns the first error of w's holes in output order: a
// child's own holes come before its own error.
func holesErr(w *Writer) error {
	for _, h := range w.holes {
		if err := holesErr(h.w); err != nil {
			return err
		}
		if h.w.err != nil {
			return h.w.err
		}
	}
	return nil
}

// mergeResponse applies the children's ctx.SetStatus and ctx.Header()
// writes to root, in output order: a non-zero status replaces root's, and
// each header key replaces root's values for that key.
func mergeResponse(w *Writer, root *Ctx) {
	for _, h := range w.holes {
		cc := h.w.ctx
		if cc.status != 0 {
			root.status = cc.status
		}
		if cc.cache.state != 0 {
			mergeCache(root.Cache(), &cc.cache)
		}
		if len(cc.header) > 0 {
			rh := root.Header()
			for k, v := range cc.header {
				rh[k] = v
			}
		}
		mergeResponse(h.w, root)
	}
}

// assembledLen is the length of w's output with its holes filled.
func assembledLen(w *Writer) int {
	n := w.buf.Len()
	for _, h := range w.holes {
		n += assembledLen(h.w)
	}
	return n
}

// assemble copies w's output into out with its holes filled, recursively.
// While *tags is pending, they're inserted at the first HeadEnd offset in
// output order (a hole at the same offset as the HeadEnd was written before
// it).
func assemble(w *Writer, out *bytes.Buffer, tags *string) {
	b := w.buf.Bytes()
	pos := 0
	for _, h := range w.holes {
		if *tags != "" && w.head >= 0 && w.head < h.off {
			out.Write(b[pos:w.head])
			out.WriteString(*tags)
			*tags = ""
			pos = w.head
		}
		out.Write(b[pos:h.off])
		pos = h.off
		assemble(h.w, out, tags)
	}
	if *tags != "" && w.head >= 0 {
		out.Write(b[pos:w.head])
		out.WriteString(*tags)
		*tags = ""
		pos = w.head
	}
	out.Write(b[pos:])
}

// releaseHoles returns w's children, recursively, to their pools.
func releaseHoles(w *Writer) {
	for i, h := range w.holes {
		cw := h.w
		releaseHoles(cw)
		putBuffer(cw.buf)
		releaseCtx(cw.ctx)
		cw.reset(nil)
		writerPool.Put(cw)
		w.holes[i] = hole{}
		if testForkHook != nil {
			testForkHook(-1)
		}
	}
}
