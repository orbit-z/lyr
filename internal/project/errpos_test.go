package project

import (
	"strings"
	"testing"
)

// TestReturnedErrorPosition runs a generated site: an error returned from a
// page's frontmatter is logged with the .lyr position of its return (the
// compiler wraps each frontmatter return in lyr.ErrorAt), while
// ErrNotFound and a redirect returned the same way keep their meaning.
func TestReturnedErrorPosition(t *testing.T) {
	needGo(t)
	p := newTestProject(t, map[string]string{
		"src/pages/x.lyr": `---
import "errors"

if ctx.Param("") == "" {
	return errors.New("boom")
}
---
<p>never</p>
`,
		"src/pages/gone.lyr": "---\nreturn lyr.ErrNotFound\n---\n",
		"src/pages/away.lyr": "---\nreturn ctx.Redirect(\"/x\", 303)\n---\n",
		"src/pages/404.lyr":  "<p>custom 404</p>\n",
		"cmd/server/main.go": `package main

import (
	"fmt"
	"log"
	"net/http/httptest"
	"os"

	"example.com/blog/lyr_gen/app"
)

func main() {
	log.SetFlags(0)
	log.SetOutput(os.Stdout)
	h := app.New().Handler()
	for _, path := range []string{"/x", "/gone", "/away"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		fmt.Printf("%s %d %s %q\n", path, rec.Code, rec.Header().Get("Location"), rec.Body.String())
	}
}
`,
	})
	if res := p.generate(); res.HasErrors() {
		t.Fatal(joinLines(diagStrings(res.Diagnostics)))
	}
	out, err := p.goCmd("run", "./cmd/server")
	if err != nil {
		t.Fatalf("go run: %v\n%s", err, out)
	}
	for _, want := range []string{
		"lyr: GET /x: src/pages/x.lyr:5:2: boom\n/x 500 ",
		"/gone 404  \"<p>custom 404</p>",
		"/away 303 /x ",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}
