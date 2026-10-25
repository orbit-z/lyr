package main

import (
	"bytes"
	"strings"
	"testing"

	"example.com/blog/orbit_gen/app"
	a "example.com/blog/orbit_gen/decl/components/a"
	orbit "example.com/blog/orbitrt"
)

func TestIndexTerminates(t *testing.T) {
	var buf bytes.Buffer
	if err := app.Routes[0].Render(&orbit.Ctx{}, orbit.NewWriter(&buf), nil); err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(buf.String(), `<div class="a">`); got != 9 {
		t.Fatalf("A rendered %d times, want 9", got)
	}
	if got := strings.Count(buf.String(), `<section class="b">`); got != 6 {
		t.Fatalf("B rendered %d times, want 6", got)
	}
}

func TestDepthGuard(t *testing.T) {
	var buf bytes.Buffer
	// <Self> recursion 5000 deep: the always-on guard must stop it at 1000.
	err := a.Render(&orbit.Ctx{}, orbit.NewWriter(&buf), a.Props{Item: a.Item{Name: "x", Count: 5000}, Depth: 4}, orbit.Slots{})
	if err == nil || !strings.Contains(err.Error(), "render depth exceeded 1000") {
		t.Fatalf("err = %v", err)
	}
	t.Log(err)
}
