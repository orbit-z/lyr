package orbit

import (
	"net/http"

	"github.com/orbit-z/orbit/internal/testhook"
)

// The orbittest bridge: package orbittest renders through these hooks, so it
// uses the same pooled Ctx, Writer and buffers as servePage, and package
// orbit exports nothing test-only. A variable initializer (rather than an
// init func) keeps the setup next to the code it installs.
var _ = func() bool {
	testhook.Render = testRender
	return true
}()

// testRender implements testhook.Render.
func testRender(r *http.Request, names, vals []string, render func(ctx, w any) error) (html string, err error) {
	c := acquireCtx(r, nil, false)
	c.paramNames, c.paramVals = names, vals
	// The fixed test island key and build ID 0000000000000000, so
	// server:defer placeholders are the same on every run.
	c.keys = testKeyring()
	buf := getBuffer()
	wr := writerPool.Get().(*Writer)
	wr.reset(buf)
	defer func() {
		wr.reset(nil)
		writerPool.Put(wr)
		putBuffer(buf)
		releaseCtx(c)
	}()
	err = render(c, wr)
	if err == nil {
		err = wr.err
	}
	if err != nil {
		return "", err
	}
	return buf.String(), nil
}
