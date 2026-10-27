package orbit

import (
	"errors"
	"strconv"
	"strings"
	"testing"
)

// dynRender renders <Tag a="1">kids</Tag> as generated code does.
func dynRender(name string, content bool) (string, error) {
	return render(func(w *Writer) {
		t := w.TagOpen("src/x.orbit:3:2", "Tag", name, content)
		if t.Attrs() {
			w.Raw(` a="1">`)
		}
		if content {
			w.Raw("kids")
		}
		w.TagClose(t)
	})
}

func TestDynTag(t *testing.T) {
	tests := []struct {
		name    string
		content bool
		want    string
		err     string
	}{
		{"div", true, `<div a="1">kids</div>`, ""},
		{"a", false, `<a a="1"></a>`, ""},
		{"H2", true, `<H2 a="1">kids</H2>`, ""}, // as given
		{"my-el", true, `<my-el a="1">kids</my-el>`, ""},
		{"svg:path", true, `<svg:path a="1">kids</svg:path>`, ""},
		{"x.y_z9", true, `<x.y_z9 a="1">kids</x.y_z9>`, ""},
		{"img", false, `<img a="1">`, ""},
		{"BR", false, `<BR a="1">`, ""},
		{Fragment, true, "kids", ""},
		{Fragment, false, "", ""},
		{"img", true, "", "src/x.orbit:3:2: dynamic tag <img> is a void element and can't have children"},
		{"Input", true, "", "src/x.orbit:3:2: dynamic tag <Input> is a void element and can't have children"},
		{"", true, "", "src/x.orbit:3:2: dynamic tag Tag is empty; use orbit.Fragment for no element"},
		{"1h", true, "", `src/x.orbit:3:2: dynamic tag Tag has an invalid element name "1h"`},
		{"a b", true, "", `src/x.orbit:3:2: dynamic tag Tag has an invalid element name "a b"`},
		{"a>", true, "", `src/x.orbit:3:2: dynamic tag Tag has an invalid element name "a>"`},
		{"-a", true, "", `src/x.orbit:3:2: dynamic tag Tag has an invalid element name "-a"`},
		{"é", true, "", `src/x.orbit:3:2: dynamic tag Tag has an invalid element name "é"`},
		{"#fragmentx", true, "", `src/x.orbit:3:2: dynamic tag Tag has an invalid element name "#fragmentx"`},
	}
	for _, name := range []string{"script", "style", "textarea", "title", "head", "xmp", "iframe", "noembed", "noframes", "plaintext"} {
		for _, n := range []string{name, strings.ToUpper(name), strings.ToUpper(name[:1]) + name[1:]} {
			tests = append(tests, struct {
				name    string
				content bool
				want    string
				err     string
			}{n, false, "", "src/x.orbit:3:2: <" + n + "> can't be a dynamic tag"})
		}
	}
	for _, tt := range tests {
		got, err := dynRender(tt.name, tt.content)
		if tt.err != "" {
			if err == nil || err.Error() != tt.err {
				t.Errorf("%q: err = %v, want %s", tt.name, err, tt.err)
			}
			if got != "" {
				t.Errorf("%q: wrote %q after an error", tt.name, got)
			}
			continue
		}
		if err != nil || got != tt.want {
			t.Errorf("%q: got %q, %v; want %q", tt.name, got, err, tt.want)
		}
	}
	// Without a position, the error is Orbit's.
	_, err := render(func(w *Writer) { w.TagOpen("", "Tag", "", false) })
	if err == nil || err.Error() != "orbit: dynamic tag Tag is empty; use orbit.Fragment for no element" {
		t.Errorf("no position: %v", err)
	}
	// A positioned error passes through ErrorAt unchanged.
	_, err = dynRender("script", false)
	if e := ErrorAt("src/y.orbit:1:1", err); e != err {
		t.Errorf("ErrorAt rewrapped %v", e)
	}
	// After an earlier error, nothing is written.
	got, _ := render(func(w *Writer) {
		w.setErr(errors.New("x"))
		tg := w.TagOpen("", "Tag", "div", true)
		if tg.Attrs() {
			w.Raw(">")
		}
		w.TagClose(tg)
	})
	if got != "" {
		t.Errorf("after an error: %q", got)
	}
}

// TestDynTagAllocs: a dynamic tag costs no allocation, a name built by
// concatenation included (TagOpen's error path mustn't make it escape).
func TestDynTagAllocs(t *testing.T) {
	w := NewWriter(getBuffer())
	level := 3
	names := []string{"a", "button", Fragment}
	i := 0
	n := testing.AllocsPerRun(1000, func() {
		w.buf.Reset()
		for _, name := range []string{"h" + strconv.Itoa(level), names[i%len(names)]} {
			tg := w.TagOpen("src/x.orbit:3:2", "Tag", name, true)
			if tg.Attrs() {
				w.Raw(` class="x">`)
			}
			w.Raw("kids")
			w.TagClose(tg)
		}
		i++
	})
	if n != 0 {
		t.Errorf("%v allocations per render, want 0", n)
	}
	if w.Err() != nil {
		t.Fatal(w.Err())
	}
}

// BenchmarkDynTag measures PLAN.md §2.8's cost table: a dynamic tag vs the
// static markup of an {if}/{else}.
func BenchmarkDynTag(b *testing.B) {
	w := NewWriter(getBuffer())
	link := false
	b.Run("static-if", func(b *testing.B) {
		for b.Loop() {
			w.buf.Reset()
			if link {
				w.Raw(`<a class="btn">`)
				w.Raw("Save")
				w.Raw("</a>")
			} else {
				w.Raw(`<button class="btn">`)
				w.Raw("Save")
				w.Raw("</button>")
			}
			link = !link
		}
	})
	b.Run("button", func(b *testing.B) {
		for b.Loop() {
			w.buf.Reset()
			name := "button"
			if link {
				name = "a"
			}
			tg := w.TagOpen("src/x.orbit:3:2", "Tag", name, true)
			if tg.Attrs() {
				w.Raw(` class="btn">`)
			}
			w.Raw("Save")
			w.TagClose(tg)
			link = !link
		}
	})
	level := 2
	b.Run("concat", func(b *testing.B) {
		for b.Loop() {
			w.buf.Reset()
			tg := w.TagOpen("src/x.orbit:3:2", "Heading", "h"+strconv.Itoa(level), true)
			if tg.Attrs() {
				w.Raw(` class="h">`)
			}
			w.Raw("Title")
			w.TagClose(tg)
		}
	})
	b.Run("fragment", func(b *testing.B) {
		for b.Loop() {
			w.buf.Reset()
			name := "div"
			if link {
				name = Fragment
			}
			tg := w.TagOpen("src/x.orbit:3:2", "Wrap", name, true)
			if tg.Attrs() {
				w.Raw(` class="w">`)
			}
			w.Raw("<label>x</label>")
			w.TagClose(tg)
			link = !link
		}
	})
}
