// Command blog renders the spike's index page to stdout.
package main

import (
	"bytes"
	"fmt"
	"os"

	"example.com/blog/lyr_gen/app"
	lyr "example.com/blog/lyrrt"
)

func main() {
	for _, r := range app.Routes {
		var buf bytes.Buffer
		w := lyr.NewWriter(&buf)
		if err := r.Render(&lyr.Ctx{}, w, nil); err != nil {
			fmt.Fprintln(os.Stderr, "render error:", err)
			os.Exit(1)
		}
		fmt.Printf("<!-- route %s (%s, prerender=%v) -->\n", r.Pattern, r.File, r.Prerender)
		os.Stdout.Write(buf.Bytes())
	}
}
