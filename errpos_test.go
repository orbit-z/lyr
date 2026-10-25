package orbit

import (
	"errors"
	"fmt"
	"testing"
)

func TestErrorAt(t *testing.T) {
	const pos = "src/pages/x.orbit:3:2"
	sentinel := errors.New("boom")
	redirect := &RedirectError{Location: "/login", Code: 302}
	wrappedNF := fmt.Errorf("lookup: %w", ErrNotFound)
	already := ErrorAt("src/components/Card.orbit:1:1", sentinel)

	if got := ErrorAt(pos, nil); got != nil {
		t.Errorf("ErrorAt(nil) = %v, want nil", got)
	}
	for _, err := range []error{ErrNotFound, redirect, already} {
		if got := ErrorAt(pos, err); got != err {
			t.Errorf("ErrorAt(%v) = %v, want it unchanged", err, got)
		}
	}

	err := ErrorAt(pos, sentinel)
	if got, want := err.Error(), pos+": boom"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
	if !errors.Is(err, sentinel) {
		t.Error("errors.Is(wrapped, sentinel) = false")
	}
	if errors.Unwrap(err) != sentinel {
		t.Error("Unwrap doesn't return the original error")
	}

	// A wrapped ErrNotFound isn't control flow at the top level, so it gets
	// the position, but errors.Is still finds it (the site renders a 404).
	err = ErrorAt(pos, wrappedNF)
	if !errors.Is(err, ErrNotFound) {
		t.Error("errors.Is(ErrNotFound) lost through ErrorAt")
	}
	var re *RedirectError
	if !errors.As(ErrorAt(pos, fmt.Errorf("x: %w", redirect)), &re) || re != redirect {
		t.Error("errors.As(*RedirectError) lost through ErrorAt")
	}
}

var errorAtSink error

func TestErrorAtAllocs(t *testing.T) {
	sentinel := errors.New("boom")
	redirect := &RedirectError{Location: "/", Code: 302}
	for name, err := range map[string]error{"nil": nil, "ErrNotFound": ErrNotFound, "redirect": redirect} {
		if n := testing.AllocsPerRun(100, func() { errorAtSink = ErrorAt("x.orbit:1:1", err) }); n != 0 {
			t.Errorf("ErrorAt(%s) allocates %v times, want 0", name, n)
		}
	}
	if n := testing.AllocsPerRun(100, func() { errorAtSink = ErrorAt("x.orbit:1:1", sentinel) }); n != 1 {
		t.Errorf("ErrorAt(failure) allocates %v times, want 1", n)
	}
}
