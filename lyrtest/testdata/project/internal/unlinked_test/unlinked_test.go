//go:build unlinked

package unlinked_test

import (
	"testing"

	"github.com/orbit-z/lyr/lyrtest"

	card "example.com/site/lyr_gen/decl/components/card"
)

// No import of lyr_gen/app: the test must fail with the not-linked hint.
func TestUnlinked(t *testing.T) {
	lyrtest.Render(t, card.Render, card.Props{Title: "x"})
}
