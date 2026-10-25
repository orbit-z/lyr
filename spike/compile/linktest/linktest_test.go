// Package linktest imports only a decl package (no orbit_gen/app), as a user
// test might, to check the unlinked-Render panic.
package linktest

import (
	"bytes"
	"testing"

	a "example.com/blog/orbit_gen/decl/components/a"
	orbit "example.com/blog/orbitrt"
)

func TestUnlinked(t *testing.T) {
	defer func() { t.Logf("recovered: %v", recover()) }()
	var buf bytes.Buffer
	_ = a.Render(&orbit.Ctx{}, orbit.NewWriter(&buf), a.Props{}, orbit.Slots{})
	t.Fatal("no panic")
}
