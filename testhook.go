package lyr

import (
	"context"
	"net/http"
	"reflect"
	"strings"
	"time"
	"unsafe"

	"github.com/orbit-z/lyr/internal/testhook"
)

// The lyrtest bridge: package lyrtest renders through these hooks, so it
// uses the same pooled Ctx, Writer and buffers as servePage, and package
// lyr exports nothing test-only. A variable initializer (rather than an
// init func) keeps the setup next to the code it installs.
var _ = func() bool {
	testhook.Render = testRender
	testhook.SeedSession = testSeedSession
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
	c.w = wr
	wr.ctx, wr.limit = c, defaultConcurrencyLimit
	defer func() {
		if wr.grp != nil { // a panic: the children still read c
			groupOf(wr).wg.Wait()
			releaseHoles(wr)
		}
		wr.reset(nil)
		writerPool.Put(wr)
		putBuffer(buf)
		releaseCtx(c)
	}()
	err = render(c, wr)
	if err == nil {
		err = wr.err
	}
	if wr.grp != nil {
		err = join(wr, err)
		buf = wr.buf
	}
	if err != nil {
		return "", err
	}
	return buf.String(), nil
}

// testSessionConfig is the configuration of a session seeded by
// lyrtest.WithSession: its store has nothing, and it's never saved.
var testSessionConfig = &sessionConfig{store: nopSessionStore{}, cookie: SessionCookie{Name: "lyr-session", Path: "/"}}

type nopSessionStore struct{}

func (nopSessionStore) Get(context.Context, string) ([]byte, error)              { return nil, nil }
func (nopSessionStore) Set(context.Context, string, []byte, time.Duration) error { return nil }
func (nopSessionStore) Delete(context.Context, string) error                     { return nil }

// testSeedSession implements testhook.SeedSession. The seeded state is
// never pooled, so it can be bound into the test's Locals (attached ones
// here, one LocalsOf allocates in bindLocalsSession).
func testSeedSession(ctx any, d any, locals []any) {
	c := ctx.(*Ctx)
	if old, ok := c.session.(*sessionState); ok && old.ctx == c {
		releaseSession(old)
	}
	st := new(sessionState)
	st.self = st
	st.cfg, st.r = testSessionConfig, c.Request
	st.ctx = c
	st.data, st.id = d, "lyrtest"
	st.fl = sessLoaded | sessBacked | sessSeeded
	c.session = st
	for _, l := range locals {
		bindSeededSession(st, l)
	}
}

// bindSeededSession binds the lyr.Session field of the Locals l (a
// pointer to a struct) whose SessionData is the seeded data's type to st,
// a state seeded by lyrtest.WithSession. Tests only: it uses reflection.
func bindSeededSession(st *sessionState, l any) {
	v := reflect.ValueOf(l)
	if v.Kind() != reflect.Pointer || v.IsNil() || v.Elem().Kind() != reflect.Struct {
		return
	}
	want := reflect.TypeOf(st.data) // *D
	v = v.Elem()
	for i := 0; i < v.NumField(); i++ {
		ft := v.Type().Field(i).Type
		if ft.PkgPath() != sessionPkgPath || !strings.HasPrefix(ft.Name(), "Session[") {
			continue
		}
		m, ok := reflect.PointerTo(ft).MethodByName("Data")
		if !ok || m.Type.NumOut() != 2 || m.Type.Out(0) != want {
			continue
		}
		f := v.Field(i)
		if !f.CanAddr() {
			continue
		}
		*(**sessionState)(unsafe.Pointer(f.UnsafeAddr())) = st
	}
}

// sessionPkgPath is the import path of Session[D]'s package (this one).
var sessionPkgPath = reflect.TypeFor[Session[struct{}]]().PkgPath()
