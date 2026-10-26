package compiler

import (
	"context"
	"errors"
	gotoken "go/token"
	"go/types"
	"reflect"
	"testing"
)

func TestCheckerPreload(t *testing.T) {
	p := newTyProject()
	units := p.units(t)
	want := []string{tyPostsPath, RuntimePath}

	t.Run("covering", func(t *testing.T) {
		l := tyLoader()
		l.fail = map[string]bool{testModule + "/internal/unused": true}
		c := NewChecker(l)
		c.generate = newTyGen(units...).generate
		if got := c.LoadedPaths(); got != nil {
			t.Errorf("LoadedPaths before Check = %v", got)
		}
		// A superset, with a path whose import would fail: it's never
		// imported, so it isn't reported.
		c.Preload([]string{RuntimePath, testModule + "/internal/unused", tyPostsPath})
		res := tyCheck(t, c, units)
		if l.loads != 1 || !reflect.DeepEqual(l.paths[0], []string{tyPostsPath, testModule + "/internal/unused", RuntimePath}) {
			t.Errorf("loads = %d %v, want the preload only", l.loads, l.paths)
		}
		if tyChecked(res) != "true true true true " || len(res.Warnings) > 0 {
			t.Errorf("checked %s, warnings %v", tyChecked(res), res.Warnings)
		}
		if got := c.LoadedPaths(); !reflect.DeepEqual(got, want) {
			t.Errorf("LoadedPaths = %v, want %v", got, want)
		}

		// orbit dev after a .go edit: Invalidate, then preload the last
		// load's paths; the Update that needs a load takes it.
		c.Invalidate()
		c.Preload(c.LoadedPaths())
		res, err := c.Update(context.Background(), units, nil)
		if err != nil {
			t.Fatal(err)
		}
		if l.loads != 2 || !reflect.DeepEqual(l.paths[1], want) || tyChecked(res) != "true true true true " {
			t.Errorf("after Invalidate: loads = %d %v, checked %s", l.loads, l.paths, tyChecked(res))
		}

		// An Update that needs no load drops a preload.
		c.Preload(want)
		if _, err := c.Update(context.Background(), units[:1], nil); err != nil {
			t.Fatal(err)
		}
		if c.pre != nil {
			t.Error("Update kept the preload")
		}
	})

	t.Run("missing a path", func(t *testing.T) {
		l := tyLoader()
		c := NewChecker(l)
		c.generate = newTyGen(units...).generate
		c.Preload([]string{RuntimePath})
		<-c.pre.done // tyTestLoader isn't safe for concurrent use
		res := tyCheck(t, c, units)
		if l.loads != 2 || !reflect.DeepEqual(l.paths[1], want) {
			t.Errorf("loads = %d %v, want the preload and a full load", l.loads, l.paths)
		}
		if tyChecked(res) != "true true true true " {
			t.Errorf("checked %s", tyChecked(res))
		}
	})

	t.Run("failed preload", func(t *testing.T) {
		l := tyLoader()
		l.err = errors.New("go.mod is broken")
		c := NewChecker(l)
		c.generate = newTyGen(units...).generate
		c.Preload(want)
		res := tyCheck(t, c, units)
		if l.loads != 1 || tyChecked(res) != "false false false false " || len(res.Warnings) != 1 {
			t.Errorf("loads = %d, checked %s, warnings %v", l.loads, tyChecked(res), res.Warnings)
		}
	})

	t.Run("cancelled while waiting", func(t *testing.T) {
		l := &blockingLoader{started: make(chan struct{})}
		c := NewChecker(l)
		c.Preload(want)
		<-l.started
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := c.Check(ctx, units); !errors.Is(err, context.Canceled) {
			t.Errorf("Check = %v, want context.Canceled", err)
		}
		<-l.stopped // the preload's context was cancelled too
	})
}

// blockingLoader's Load blocks until its context is done.
type blockingLoader struct {
	started chan struct{}
	stopped chan struct{}
}

func (l *blockingLoader) Load(ctx context.Context, _ *gotoken.FileSet, _ []string) (types.Importer, error) {
	l.stopped = make(chan struct{})
	close(l.started)
	<-ctx.Done()
	close(l.stopped)
	return nil, ctx.Err()
}
