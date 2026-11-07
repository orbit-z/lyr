package lyr

// deferAt and deferWhenAt call Defer and DeferWhen on a line that a line
// directive maps to a .lyr file, as generated code does.
func deferAt(c *Ctx, w *Writer, props any) error {
//line src/pages/d.lyr:4
	return Defer(c, w, "X-1", props, nil)
}

func deferWhenAt(c *Ctx, w *Writer, props any) error {
//line src/pages/d.lyr:9
	return DeferWhen(c, w, "X-1", ` client="idle"`, props, nil)
}
