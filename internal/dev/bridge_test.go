package dev

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestPipelineBridgeTypes: an edit that only changes the type of an
// lyr.JSON argument rewrites the component's .d.ts (PLAN.md §5c "When
// it's regenerated") without an esbuild rebuild, since the script and the
// module esbuild sees for "./Liked.lyr" don't change.
func TestPipelineBridgeTypes(t *testing.T) {
	needE2E(t)
	dir := fixture(t)
	writeFile(t, dir, "src/components/Liked.lyr", "<liked-box data-v={lyr.JSON(1)}></liked-box>\n<script>\n  import { data } from \"./Liked.lyr\";\n  console.log(data(document.body, \"v\"));\n</script>\n")
	edit(t, dir, "src/pages/index.lyr", "\tSlow \"../components/Slow.lyr\"\n", "\tSlow \"../components/Slow.lyr\"\n\tLiked \"../components/Liked.lyr\"\n")
	edit(t, dir, "src/pages/index.lyr", "  <Card post=\"one\" />\n", "  <Card post=\"one\" />\n  <Liked />\n")
	tp := newTestPipeline(t, dir)
	if out := tp.run(t); out.Bin == "" || !out.Assets {
		t.Fatalf("first run: %+v", out)
	}
	dts := filepath.Join(dir, "lyr_gen/types/src/components/Liked.lyr.d.ts")
	before, err := os.ReadFile(dts)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(before), "v: number;") {
		t.Fatalf("Liked.lyr.d.ts:\n%s", before)
	}

	tp.reset()
	edit(t, dir, "src/components/Liked.lyr", "lyr.JSON(1)", `lyr.JSON("one")`)
	out := tp.run(t, "src/components/Liked.lyr")
	if st := tp.got(); slices.Contains(st, "esbuild") || out.Assets || !slices.Contains(st, "check") {
		t.Errorf("type-only edit: stages %v, %+v", st, out)
	}
	after, err := os.ReadFile(dts)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(after), "v: string;") {
		t.Errorf("Liked.lyr.d.ts after the edit:\n%s", after)
	}
}
