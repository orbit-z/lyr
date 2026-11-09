package loader

import (
	"crypto/sha256"
	"encoding/hex"
	"path"
	"strings"
	"testing"
)

// TestFiles checks that each file's name carries the hash of its contents
// (a stale hand edit fails), and that the URLs name the files.
func TestFiles(t *testing.T) {
	if len(Files) != 2 {
		t.Fatalf("%d files, want 2", len(Files))
	}
	for _, f := range Files {
		sum := sha256.Sum256([]byte(f.Contents))
		base := path.Base(f.Path)
		ext := path.Ext(base)
		if want := hex.EncodeToString(sum[:4]); !strings.HasSuffix(strings.TrimSuffix(base, ext), "-"+want) || !strings.HasPrefix(f.Path, "loader/") {
			t.Errorf("%s: want loader/<name>-%s%s", f.Path, want, ext)
		}
	}
	if "/_lyr/"+Files[0].Path != LoaderURL || "/_lyr/"+Files[1].Path != WrappersURL {
		t.Errorf("URLs %s, %s don't name the files %s, %s", LoaderURL, WrappersURL, Files[0].Path, Files[1].Path)
	}
	if Files[1].Contents != WrappersCSS {
		t.Errorf("wrappers file = %q, want WrappersCSS", Files[1].Contents)
	}
}
