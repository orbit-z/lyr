package lyr

import (
	"bytes"
	"errors"
	"math"
	"strings"
	"testing"
	"time"
)

// tlValues are piece values covering Expr's cases.
var tlValues = []any{
	nil, "a <b> & \"c\" 'd'", 42, int64(-7), uint8(3), 1.5, float32(0.1), math.Inf(1), true, false,
	[]byte("x<y"), HTML("<b>"), URL("javascript:x"), CSS("a:b"), errors.New("e<"), time.Duration(1500) * time.Millisecond,
	slug("s&s"), count(3), []string{"a", "b"}, []any{1, "<", nil}, point{1, 2}, Renderer(func(w *Writer) error { w.Raw("<r>"); return nil }),
	Some("o&"), None[int](),
}

func TestURLCheck(t *testing.T) {
	tests := []struct {
		pieces []string
		want   string
	}{
		{[]string{"/a", "b"}, "/ab"},
		{[]string{"java", "script:alert(1)"}, invalidURL},
		{[]string{"  JaVa\tScRiPt:x"}, invalidURL},
		{[]string{"vb", "script:", "x"}, invalidURL},
		{[]string{"java", "x"}, "javax"},
		{[]string{}, ""},
		{[]string{" \x01", "javascript:"}, invalidURL},
	}
	for _, tt := range tests {
		got, err := render(func(w *Writer) {
			w.Raw(`<a href="`)
			m := w.URLMark()
			for _, p := range tt.pieces {
				w.AttrValue(p)
			}
			w.URLCheck(m)
			w.Raw(`">`)
		})
		if err != nil || got != `<a href="`+tt.want+`">` {
			t.Errorf("%q: %q (%v), want %q", tt.pieces, got, err, tt.want)
		}
	}
	// A sticky error: nothing written, nothing checked.
	got, _ := render(func(w *Writer) {
		m := w.URLMark()
		w.setErr(errors.New("x"))
		w.URLCheck(m)
	})
	if got != "" {
		t.Errorf("after an error: %q", got)
	}
}

func TestClassBytes(t *testing.T) {
	for _, s := range []string{"a", " a  b ", "", "  ", "x\ty\nz", "a&<b>"} {
		want, _ := render(func(w *Writer) { w.ClassStart(); w.Class(s); w.ClassEnd() })
		got, _ := render(func(w *Writer) { w.ClassStart(); w.ClassBytes([]byte(s)); w.ClassEnd() })
		if got != want {
			t.Errorf("ClassBytes(%q) = %q, Class %q", s, got, want)
		}
	}
}

// TestPieces: AttrPiece and TextPiece write Expr's text of every value,
// escaped for their context (lyr.HTML and a Renderer's output too), and
// TemplateString and AppendAny the same text unescaped.
func TestPieces(t *testing.T) {
	for _, v := range tlValues {
		text, _ := render(func(w *Writer) { w.value(v, false) })
		attr, _ := render(func(w *Writer) { w.AttrPiece(v) })
		if want := attrValueEscape(text); attr != want {
			t.Errorf("AttrPiece(%#v) = %q, want %q", v, attr, want)
		}
		content, _ := render(func(w *Writer) { w.TextPiece(v) })
		if want := textEscape(text); content != want {
			t.Errorf("TextPiece(%#v) = %q, want %q", v, content, want)
		}
		if got := TemplateString("<", v, ">"); got != "<"+text+">" {
			t.Errorf("TemplateString(%#v) = %q, want %q", v, got, "<"+text+">")
		}
		if got := string(AppendAny([]byte("p"), v)); got != "p"+text {
			t.Errorf("AppendAny(%#v) = %q", v, got)
		}
	}
}

func attrValueEscape(s string) string {
	return strings.NewReplacer("\x00", "�", "&", "&amp;", `"`, "&quot;", "<", "&lt;", ">", "&gt;").Replace(s)
}

func textEscape(s string) string {
	return strings.NewReplacer("\x00", "�", "&", "&amp;", `"`, "&quot;", "'", "&#39;", "<", "&lt;", ">", "&gt;").Replace(s)
}

// TestTB: each TB method appends what the matching Writer method writes,
// unescaped.
func TestTB(t *testing.T) {
	var s fmtStringer = "str"
	e := errors.New("err")
	got := TB(nil).S("a").B([]byte("b")).Int(-1).Uint(2).Float(0.1).Float32(0.1).Bool(true).Stringer(s).Stringer(nil).Error(e).Error(nil).Any(count(5)).String()
	if want := "ab-120.10.1truestrerr5"; got != want {
		t.Errorf("TB = %q, want %q", got, want)
	}
	if b := TB(make([]byte, 0, 8)).S("x").Bytes(); string(b) != "x" {
		t.Errorf("Bytes = %q", b)
	}
}

type fmtStringer string

func (s fmtStringer) String() string { return string(s) }

func FuzzAppendFloat(f *testing.F) {
	for _, v := range []float64{0, 0.1, 1e21, -2.5, math.MaxFloat64, math.SmallestNonzeroFloat64, 1e-7} {
		f.Add(v)
	}
	f.Fuzz(func(t *testing.T, v float64) {
		want, _ := render(func(w *Writer) { w.Float(v) })
		if got := string(AppendFloat(nil, v)); got != want {
			t.Fatalf("AppendFloat(%v) = %q, Float %q", v, got, want)
		}
		want32, _ := render(func(w *Writer) { w.Float32(float32(v)) })
		if got := string(AppendFloat32(nil, float32(v))); got != want32 {
			t.Fatalf("AppendFloat32(%v) = %q, Float32 %q", v, got, want32)
		}
	})
}

func TestAppendStringerError(t *testing.T) {
	var np *nilStringer
	for _, s := range []fmtStringerI{fmtStringer("<x>"), np} {
		want, _ := render(func(w *Writer) { w.Raw(stringerString(s)) })
		if got := string(AppendStringer(nil, s)); got != want {
			t.Errorf("AppendStringer(%#v) = %q, want %q", s, got, want)
		}
	}
	if got := string(AppendError(nil, errors.New("<e>"))); got != "<e>" {
		t.Errorf("AppendError = %q", got)
	}
}

type fmtStringerI interface{ String() string }

type nilStringer struct{ s string }

func (n *nilStringer) String() string { return n.s }

func TestTemplateAs(t *testing.T) {
	type mySlug string
	if got := TemplateAs("", "a", 1, "b"); got != "a1b" {
		t.Errorf("string: %q", got)
	}
	if got := TemplateAs(mySlug(""), "s-", 2, ""); got != "s-2" {
		t.Errorf("named: %q", got)
	}
	if got := TemplateAs(any(nil), "x", true, ""); got != "xtrue" {
		t.Errorf("any: %#v", got)
	}
	if got := TemplateAs(URL(""), "/b/", 42, "", URL("/x"), ""); got != "/b/42/x" {
		t.Errorf("URL: %q", got)
	}
	if got := TemplateAs(HTML(""), "<i>", 1.5, "</i>"); got != "<i>1.5</i>" {
		t.Errorf("HTML: %q", got)
	}
	for name, f := range map[string]func(){
		"string piece into HTML":  func() { TemplateAs(HTML(""), "<i>", "<script>", "") },
		"Stringer piece into URL": func() { TemplateAs(URL(""), "/", time.Second, "") },
		"int field":               func() { TemplateAs(0, "x", 1, "") },
	} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("%s: no panic", name)
				}
			}()
			f()
		}()
	}
}

// TestTemplateLiteralAllocs: the lowered writes allocate nothing, and the
// stack-buffer TB chain one string.
func TestTemplateLiteralAllocs(t *testing.T) {
	w := NewWriter(new(bytes.Buffer))
	n, s := 300, "ab"
	if a := testing.AllocsPerRun(100, func() {
		w.buf.Reset()
		m := w.URLMark()
		w.AttrValue(s)
		w.Int(int64(n))
		w.URLCheck(m)
		w.ClassBytes(TB(make([]byte, 0, 64)).S("btn-").Int(int64(n)).Bytes())
	}); a != 0 {
		t.Errorf("writes: %v allocations", a)
	}
	var sink string
	if a := testing.AllocsPerRun(100, func() {
		sink = TB(make([]byte, 0, 64)).S("Chapter ").Int(int64(n)).String()
	}); a != 1 {
		t.Errorf("TB chain: %v allocations, want 1", a)
	}
	_ = sink
}
