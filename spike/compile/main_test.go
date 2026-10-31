package main

import (
	"bytes"
	"strings"
	"testing"

	"example.com/blog/lyr_gen/app"
	a "example.com/blog/lyr_gen/decl/components/a"
	lyr "example.com/blog/lyrrt"
)

func TestIndexTerminates(t *testing.T) {
	var buf bytes.Buffer
	if err := app.Routes[0].Render(&lyr.Ctx{}, lyr.NewWriter(&buf), nil); err != nil {
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
	err := a.Render(&lyr.Ctx{}, lyr.NewWriter(&buf), a.Props{Item: a.Item{Name: "x", Count: 5000}, Depth: 4}, lyr.Slots{})
	if err == nil || !strings.Contains(err.Error(), "render depth exceeded 1000") {
		t.Fatalf("err = %v", err)
	}
	t.Log(err)
}
