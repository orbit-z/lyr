// Package linktest imports only a decl package (no lyr_gen/app), as a user
// test might, to check the unlinked-Render panic.
package linktest

import (
	"bytes"
	"testing"

	a "example.com/blog/lyr_gen/decl/components/a"
	lyr "example.com/blog/lyrrt"
)

func TestUnlinked(t *testing.T) {
	defer func() { t.Logf("recovered: %v", recover()) }()
	var buf bytes.Buffer
	_ = a.Render(&lyr.Ctx{}, lyr.NewWriter(&buf), a.Props{}, lyr.Slots{})
	t.Fatal("no panic")
}
