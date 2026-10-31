package lyr

import (
	"bytes"
	"strings"
	"testing"
)

type classToken string

// TestClassWrites checks the element writes as codegen emits them: a
// known-open attribute (a static class first), and ClassStart/ClassEnd
// around items that may all be empty.
func TestClassWrites(t *testing.T) {
	tests := []struct {
		name string
		f    func(w *Writer)
		want string
	}{
		{"static first", func(w *Writer) {
			w.Raw(`<div class="a b`)
			w.Class("  c\t d\n")
			w.Raw(`">`)
		}, `<div class="a b c d">`},
		{"pending, empty", func(w *Writer) {
			w.Raw(`<div`)
			w.ClassStart()
			w.Class("")
			w.Class(" \n ")
			w.Classes(nil)
			w.ClassEnd()
			w.Raw(`>`)
		}, `<div>`},
		{"pending, opened", func(w *Writer) {
			w.Raw(`<div`)
			w.ClassStart()
			w.Class("")
			w.Classes([]string{" x ", "", "y  z"})
			w.ClassEnd()
			w.Raw(`>`)
		}, `<div class="x y z">`},
		{"pending, then static", func(w *Writer) {
			w.Raw(`<div`)
			w.ClassStart()
			w.Class("lit")
			w.Raw(` more">`)
		}, `<div class="lit more">`},
		{"escaped", func(w *Writer) {
			w.Raw(`<p`)
			w.ClassStart()
			w.Class(`a"b <c> &d`)
			w.ClassEnd()
			w.Raw(`>`)
		}, `<p class="a&quot;b &lt;c&gt; &amp;d">`},
		{"any", func(w *Writer) {
			w.Raw(`<p`)
			w.ClassStart()
			w.ClassAny("a")
			w.ClassAny([]string{"b"})
			w.ClassAny(classToken("c"))
			w.ClassAny([]classToken{"d", "e"})
			w.ClassEnd()
			w.Raw(`>`)
		}, `<p class="a b c d e">`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var b bytes.Buffer
			w := NewWriter(&b)
			tt.f(w)
			if err := w.Err(); err != nil {
				t.Fatal(err)
			}
			if b.String() != tt.want {
				t.Errorf("got %s, want %s", b.String(), tt.want)
			}
		})
	}
}

func TestClassAnyError(t *testing.T) {
	var b bytes.Buffer
	w := NewWriter(&b)
	w.ClassStart()
	w.ClassAny(3)
	if err := w.Err(); err == nil || !strings.Contains(err.Error(), "class:list item must be a string or []string, not int") {
		t.Errorf("err = %v", err)
	}
	defer func() {
		if r := recover(); r == nil {
			t.Error("ClassString(3) didn't panic")
		}
	}()
	ClassString(3)
}

func TestClassResetClearsPending(t *testing.T) {
	var b bytes.Buffer
	w := NewWriter(&b)
	w.ClassStart()
	w.reset(&b)
	w.Class("x")
	if b.String() != " x" {
		t.Errorf("got %q", b.String())
	}
}

func TestJoinClasses(t *testing.T) {
	tests := []struct {
		parts []string
		want  string
	}{
		{nil, ""},
		{[]string{"", " ", "\n"}, ""},
		{[]string{"a"}, "a"},
		{[]string{"a b"}, "a b"},
		{[]string{" a  b "}, "a b"},
		{[]string{"", "a\tb", ""}, "a b"},
		{[]string{"a", "", "b c", ClassIf(false, "d"), ClassIf(true, "e")}, "a b c e"},
		{[]string{ClassString([]string{"x", " y"}), ClassString(classToken("z"))}, "x y z"},
	}
	for _, tt := range tests {
		if got := JoinClasses(tt.parts...); got != tt.want {
			t.Errorf("JoinClasses(%q) = %q, want %q", tt.parts, got, tt.want)
		}
	}
}

// TestClassListAllocs: an element's class:list with static, conditional and
// dynamic items, as codegen writes it, allocates nothing; a component's
// class prop with one clean dynamic part doesn't either.
func TestClassListAllocs(t *testing.T) {
	buf := bytes.NewBuffer(make([]byte, 0, 1<<16))
	w := NewWriter(buf)
	hidden, active := true, false
	extra := "rounded  border"
	more := []string{"grid", "gap-2"}
	if n := testing.AllocsPerRun(100, func() {
		buf.Reset()
		classListElement(w, hidden, active, extra, more)
	}); n != 0 {
		t.Errorf("element: %v allocs per run, want 0", n)
	}
	if got := buf.String(); got != `<div class="appearance-none rounded-input hidden rounded border grid gap-2">` {
		t.Errorf("element wrote %s", got)
	}
	if n := testing.AllocsPerRun(100, func() {
		buf.Reset()
		classListStart(w, active, extra)
	}); n != 0 {
		t.Errorf("element with ClassStart: %v allocs per run, want 0", n)
	}
	cls := "card featured"
	var sink string
	if n := testing.AllocsPerRun(100, func() { sink = JoinClasses(ClassIf(false, "x"), cls) }); n != 0 {
		t.Errorf("JoinClasses with one clean part: %v allocs per run, want 0", n)
	}
	_ = sink
}

// classListElement is what codegen emits for
// <div class:list={"appearance-none rounded-input", "hidden": hidden, "active": active, extra, more}>.
func classListElement(w *Writer, hidden, active bool, extra string, more []string) {
	w.Raw(`<div class="appearance-none rounded-input`)
	if hidden {
		w.Raw(` hidden`)
	}
	if active {
		w.Raw(` active`)
	}
	w.Class(extra)
	w.Classes(more)
	w.Raw(`">`)
}

// classListStart is what codegen emits for
// <div class:list={"active": active, extra}>.
func classListStart(w *Writer, active bool, extra string) {
	w.Raw(`<div`)
	w.ClassStart()
	if active {
		w.Class("active")
	}
	w.Class(extra)
	w.ClassEnd()
	w.Raw(`>`)
}

func BenchmarkClassList(b *testing.B) {
	buf := bytes.NewBuffer(make([]byte, 0, 1<<16))
	w := NewWriter(buf)
	extra := "rounded  border"
	more := []string{"grid", "gap-2"}
	b.Run("element", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			buf.Reset()
			classListElement(w, true, false, extra, more)
		}
	})
	b.Run("element-start", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			buf.Reset()
			classListStart(w, true, extra)
		}
	})
	b.Run("static-attr", func(b *testing.B) {
		// The baseline: the same classes as one static attribute.
		b.ReportAllocs()
		for b.Loop() {
			buf.Reset()
			w.Raw(`<div class="appearance-none rounded-input hidden rounded border grid gap-2">`)
		}
	})
	b.Run("component-join", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			_ = JoinClasses("block", ClassIf(true, "hidden"), extra)
		}
	})
}
