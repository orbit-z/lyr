package lsp

import (
	"strings"
	"testing"

	"github.com/orbit-z/lyr/internal/compiler"
)

// Blank imports and page partials in the editor (PLAN.md §4k): the path of
// a blank import goes to the file, its _ is nothing native, and a partial
// page's <style> warning is a live diagnostic.
func TestBlankImportAndPartial(t *testing.T) {
	const page = "---\nimport _ \"../components/Card.lyr\"\n\nconst partial = true\n---\n<p>x</p>\n<style>p{}</style>\n"
	ws := memWorkspace(t, map[string]string{"src/components/Card.lyr": mapCard, "src/pages/index.lyr": page})
	f := ws.byPath["src/pages/index.lyr"]
	reg := classify(f, strings.Index(page, `"../components`)+1)
	if reg.kind != regionImport {
		t.Fatalf("path: region %d", reg.kind)
	}
	if loc, ok := ws.nativeDefinition(f, reg); !ok || loc == nil || !strings.HasSuffix(loc.URI, "src/components/Card.lyr") {
		t.Errorf("definition on the path: %v %v", loc, ok)
	}
	if got := classify(f, strings.Index(page, "_ ")).kind; got == regionImport {
		t.Errorf("_ is classified as an import")
	}
	var msgs []string
	for _, d := range f.Analysis.Diagnostics {
		msgs = append(msgs, d.Message)
	}
	if len(msgs) != 1 || msgs[0] != compiler.MsgPartialStyle || f.Analysis.Diagnostics[0].Severity != compiler.SeverityWarning {
		t.Errorf("diagnostics %q", msgs)
	}
}
