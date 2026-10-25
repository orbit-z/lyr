// Command blog renders the spike's index page to stdout.
package main

import (
	"bytes"
	"fmt"
	"os"

	"example.com/blog/orbit_gen/app"
	orbit "example.com/blog/orbitrt"
)

func main() {
	for _, r := range app.Routes {
		var buf bytes.Buffer
		w := orbit.NewWriter(&buf)
		if err := r.Render(&orbit.Ctx{}, w, nil); err != nil {
			fmt.Fprintln(os.Stderr, "render error:", err)
			os.Exit(1)
		}
		fmt.Printf("<!-- route %s (%s, prerender=%v) -->\n", r.Pattern, r.File, r.Prerender)
		os.Stdout.Write(buf.Bytes())
	}
}
