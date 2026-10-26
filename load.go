package orbit

import (
	"context"
	"net/http"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"sync"
)

// Concurrent loads in a frontmatter: orbit.All (PLAN.md §4f).

// All runs each load concurrently and waits for all of them. Each load gets a
// context derived from c.Request.Context(), cancelled when a load fails (and
// when All returns). All returns the first error in time, or nil.
//
// A failing load's error is prefixed with the .orbit line of its func
// literal; errors.Is and errors.As still see the error, and ErrNotFound and
// *RedirectError are returned as is. A panic in a load cancels the others
// and is re-panicked on the calling goroutine once every load has returned,
// with the load's position, so it fails the request like a panic in the
// frontmatter itself.
//
// The first load runs on the calling goroutine; a single load runs inline,
// with the request's context. A nil c uses context.Background().
func All(c *Ctx, loads ...func(context.Context) error) error {
	parent := context.Background()
	if c != nil && c.Request != nil {
		parent = c.Request.Context()
	}
	switch len(loads) {
	case 0:
		return nil
	case 1:
		return loadError(loads[0], loads[0](parent))
	}
	rc, cancel := context.WithCancel(parent)
	g := &loadGroup{cancel: cancel}
	g.wg.Add(len(loads) - 1)
	for _, load := range loads[1:] {
		go g.goRun(rc, load)
	}
	g.run(rc, loads[0])
	g.wg.Wait()
	cancel()
	if g.panic != nil {
		panic(g.panic)
	}
	return g.err
}

// loadGroup is the shared state of one All call.
type loadGroup struct {
	wg     sync.WaitGroup
	cancel context.CancelFunc
	mu     sync.Mutex
	err    error // the first error in time
	panic  any   // the first panic in time: a *panicError or http.ErrAbortHandler
}

func (g *loadGroup) goRun(rc context.Context, load func(context.Context) error) {
	defer g.wg.Done()
	g.run(rc, load)
}

// run calls load, recording its error or recovered panic.
func (g *loadGroup) run(rc context.Context, load func(context.Context) error) {
	defer func() {
		if p := recover(); p != nil {
			if p != http.ErrAbortHandler {
				p = recoveredPanic(p)
			}
			g.fail(nil, p)
		}
	}()
	if err := load(rc); err != nil {
		g.fail(loadError(load, err), nil)
	}
}

// fail records the first error and the first panic, and cancels the loads.
func (g *loadGroup) fail(err error, p any) {
	g.mu.Lock()
	if err != nil && g.err == nil {
		g.err = err
	}
	if p != nil && g.panic == nil {
		g.panic = p
	}
	g.mu.Unlock()
	g.cancel()
}

// loadError prefixes a failing load's error with the .orbit line of the
// load's func literal (generated code carries line directives), the way
// ErrorAt does with a return's position: control flow and already positioned
// errors are returned as is, and so is the error of a load that isn't in a
// .orbit file. It runs on the error path only.
func loadError(load func(context.Context) error, err error) error {
	if err == nil {
		return nil
	}
	switch err.(type) {
	case *posError, *RedirectError:
		return err
	}
	if err == ErrNotFound {
		return err
	}
	pc := reflect.ValueOf(load).Pointer()
	fn := runtime.FuncForPC(pc)
	if fn == nil {
		return err
	}
	file, line := fn.FileLine(fn.Entry())
	if !strings.HasSuffix(file, ".orbit") {
		return err
	}
	return &posError{pos: relativeFile(file) + ":" + strconv.Itoa(line), err: err}
}
