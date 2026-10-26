package dev

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestPipelineBridgeTypes: an edit that only changes the type of an
// orbit.JSON argument rewrites the component's .d.ts (PLAN.md §5c "When
// it's regenerated") without an esbuild rebuild, since the script and the
// module esbuild sees for "./Liked.orbit" don't change.
func TestPipelineBridgeTypes(t *testing.T) {
	needE2E(t)
	dir := fixture(t)
	writeFile(t, dir, "src/components/Liked.orbit", "<liked-box data-v={orbit.JSON(1)}></liked-box>\n<script>\n  import { data } from \"./Liked.orbit\";\n  console.log(data(document.body, \"v\"));\n</script>\n")
	edit(t, dir, "src/pages/index.orbit", "\tSlow \"../components/Slow.orbit\"\n", "\tSlow \"../components/Slow.orbit\"\n\tLiked \"../components/Liked.orbit\"\n")
	edit(t, dir, "src/pages/index.orbit", "  <Card post=\"one\" />\n", "  <Card post=\"one\" />\n  <Liked />\n")
	tp := newTestPipeline(t, dir)
	if out := tp.run(t); out.Bin == "" || !out.Assets {
		t.Fatalf("first run: %+v", out)
	}
	dts := filepath.Join(dir, "orbit_gen/types/src/components/Liked.orbit.d.ts")
	before, err := os.ReadFile(dts)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(before), "v: number;") {
		t.Fatalf("Liked.orbit.d.ts:\n%s", before)
	}

	tp.reset()
	edit(t, dir, "src/components/Liked.orbit", "orbit.JSON(1)", `orbit.JSON("one")`)
	out := tp.run(t, "src/components/Liked.orbit")
	if st := tp.got(); slices.Contains(st, "esbuild") || out.Assets || !slices.Contains(st, "check") {
		t.Errorf("type-only edit: stages %v, %+v", st, out)
	}
	after, err := os.ReadFile(dts)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(after), "v: string;") {
		t.Errorf("Liked.orbit.d.ts after the edit:\n%s", after)
	}
}
