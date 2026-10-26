//go:build unlinked

package unlinked_test

import (
	"testing"

	"github.com/orbit-z/orbit/orbittest"

	card "example.com/site/orbit_gen/decl/components/card"
)

// No import of orbit_gen/app: the test must fail with the not-linked hint.
func TestUnlinked(t *testing.T) {
	orbittest.Render(t, card.Render, card.Props{Title: "x"})
}
