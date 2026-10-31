package lsp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"testing/iotest"
	"time"
)

func frame(body string) string {
	return "Content-Length: " + itoa(len(body)) + "\r\n\r\n" + body
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }

func TestReadMessage(t *testing.T) {
	a, b := `{"jsonrpc":"2.0","method":"a"}`, `{"jsonrpc":"2.0","id":1,"result":"é✓"}`
	stream := frame(a) + "Content-Type: application/vscode-jsonrpc; charset=utf-8\r\n" + frame(b)
	// One byte per Read: every header and body read is partial.
	r := bufio.NewReader(iotest.OneByteReader(strings.NewReader(stream)))
	for _, want := range []string{a, b} {
		got, err := readMessage(r)
		if err != nil || string(got) != want {
			t.Fatalf("readMessage = %q, %v; want %q", got, err, want)
		}
	}
	if _, err := readMessage(r); err != io.EOF {
		t.Errorf("at the end: %v, want io.EOF", err)
	}
	// Header names are case-insensitive.
	if got, err := readMessage(bufio.NewReader(strings.NewReader("content-length: 2\r\n\r\n{}"))); err != nil || string(got) != "{}" {
		t.Errorf("lowercase header: %q, %v", got, err)
	}
}

func TestReadMessageErrors(t *testing.T) {
	for _, tc := range []struct{ name, in string }{
		{"no colon", "Content-Length 5\r\n\r\nhello"},
		{"bad length", "Content-Length: x\r\n\r\n"},
		{"negative", "Content-Length: -1\r\n\r\n"},
		{"too large", "Content-Length: 99999999999\r\n\r\n"},
		{"missing length", "Content-Type: x\r\n\r\n{}"},
		{"short body", "Content-Length: 10\r\n\r\n{}"},
		{"truncated header", "Content-Length: 2\r\n"},
	} {
		if _, err := readMessage(bufio.NewReader(strings.NewReader(tc.in))); err == nil || err == io.EOF {
			t.Errorf("%s: err = %v, want an error", tc.name, err)
		}
	}
}

func TestConnCall(t *testing.T) {
	// Two Conns wired back to back: a Call on one is answered by the
	// other's handler, and a notification arrives in order.
	ar, bw := io.Pipe()
	br, aw := io.Pipe()
	a, b := newConn(ar, aw), newConn(br, bw)
	notes := make(chan string, 4)
	go b.Run(func(m *message) {
		switch {
		case m.isRequest() && m.Method == "echo":
			b.Reply(m.ID, m.Params, nil)
		case m.isRequest():
			b.Reply(m.ID, nil, &rpcError{Code: codeMethodNotFound, Message: "no"})
		default:
			notes <- m.Method
		}
	})
	go a.Run(func(*message) {})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	a.Notify("first", nil)
	res, err := a.Call(ctx, "echo", map[string]any{"x": "Héllo ✓"})
	if err != nil || string(res) != `{"x":"Héllo ✓"}` {
		t.Errorf("echo = %s, %v", res, err)
	}
	if _, err := a.Call(ctx, "nope", nil); err == nil || err.(*rpcError).Code != codeMethodNotFound {
		t.Errorf("nope: err = %v", err)
	}
	if got := <-notes; got != "first" {
		t.Errorf("notification %q", got)
	}
	// A null result is delivered as null.
	res, err = a.Call(ctx, "echo", nil)
	if err != nil || string(res) != "null" {
		t.Errorf("echo nil = %q, %v", res, err)
	}
}

func TestLineIndex(t *testing.T) {
	// é is 2 bytes / 1 unit, ✓ 3 bytes / 1 unit, 😀 4 bytes / 2 units.
	src := "a\n\"Héllo ✓\" 😀 x\r\nlast"
	x := newLineIndex(src)
	for _, tc := range []struct {
		off   int
		utf16 Position
		utf32 Position
	}{
		{0, Position{0, 0}, Position{0, 0}},
		{2, Position{1, 0}, Position{1, 0}},
		{strings.Index(src, "llo"), Position{1, 3}, Position{1, 3}},
		{strings.Index(src, "\" 😀"), Position{1, 8}, Position{1, 8}},
		{strings.Index(src, " x"), Position{1, 12}, Position{1, 11}},
		{strings.Index(src, "x"), Position{1, 13}, Position{1, 12}},
		{strings.Index(src, "last"), Position{2, 0}, Position{2, 0}},
		{len(src), Position{2, 4}, Position{2, 4}},
	} {
		if got := x.position(tc.off, encUTF16); got != tc.utf16 {
			t.Errorf("position(%d) = %v, want %v", tc.off, got, tc.utf16)
		}
		if got := x.offset(tc.utf16, encUTF16); got != tc.off {
			t.Errorf("offset(%v) = %d, want %d", tc.utf16, got, tc.off)
		}
		if got := x.position(tc.off, encUTF32); got != tc.utf32 {
			t.Errorf("utf-32 position(%d) = %v, want %v", tc.off, got, tc.utf32)
		}
		if got := x.offset(tc.utf32, encUTF32); got != tc.off {
			t.Errorf("utf-32 offset(%v) = %d, want %d", tc.utf32, got, tc.off)
		}
		p8 := x.position(tc.off, encUTF8)
		if got := x.offset(p8, encUTF8); got != tc.off {
			t.Errorf("utf-8 round trip of %d: %v → %d", tc.off, p8, got)
		}
	}
	emoji := strings.Index(src, "😀")
	// Half of a surrogate pair: the start of the rune.
	if got := x.offset(Position{1, 11}, encUTF16); got != emoji {
		t.Errorf("offset inside a surrogate pair = %d, want %d", got, emoji)
	}
	// Past the end of a line (before \r\n), and past the last line.
	if got := x.offset(Position{1, 99}, encUTF16); got != strings.Index(src, "\r\n") {
		t.Errorf("offset past the line end = %d", got)
	}
	if got := x.offset(Position{9, 0}, encUTF16); got != len(src) {
		t.Errorf("offset past the end = %d", got)
	}
}

func TestApplyChanges(t *testing.T) {
	text := "x := \"Héllo ✓\"\ny := 😀z\n"
	changes := []obj{
		decodeObj(jsonRaw(`{"range":{"start":{"line":1,"character":7},"end":{"line":1,"character":8}},"text":"Z"}`)),
		decodeObj(jsonRaw(`{"range":{"start":{"line":0,"character":13},"end":{"line":0,"character":13}},"text":"!"}`)),
	}
	if got, want := applyChanges(text, changes), "x := \"Héllo ✓!\"\ny := 😀Z\n"; got != want {
		t.Errorf("applyChanges = %q, want %q", got, want)
	}
	if got := applyChanges(text, []obj{decodeObj(jsonRaw(`{"text":"new"}`))}); got != "new" {
		t.Errorf("full change = %q", got)
	}
}

const mapCard = `---
type Props struct {
	Title   string
	IsOpen  bool
}
label := "Héllo ✓ " + props.Title
---
<p class={label}>{props.}</p>
`

const mapPage = `---
import Card "../components/Card.lyr"
---
<Card title="x" is-open />
`

// memWorkspace generates files in memory (no gopls, no disk).
func memWorkspace(t *testing.T, files map[string]string) *workspace {
	t.Helper()
	ws, err := loadWorkspace(writeProject(t, files))
	if err != nil {
		t.Fatal(err)
	}
	return ws
}

func TestSourceMapRoundTrip(t *testing.T) {
	ws := memWorkspace(t, map[string]string{"src/components/Card.lyr": mapCard, "src/pages/index.lyr": mapPage})
	const card = "src/components/Card.lyr"
	_, impl := ws.genOf(card)

	// exact: every byte of "props.Title" maps both ways after non-ASCII.
	start := strings.Index(mapCard, "props.Title")
	for off := start; off <= start+len("props.Title"); off++ {
		g, goOff, ok := ws.toGo(card, off)
		if !ok || g != impl {
			t.Fatalf("toGo(%d): ok=%v", off, ok)
		}
		if back, ok := g.fromGo(goOff); !ok || back != off {
			t.Errorf("round trip of %d: %d → %d, %v", off, goOff, back, ok)
		}
	}
	// The cursor at the end of "props." maps inside, right after it.
	end := strings.Index(mapCard, "props.}") + len("props.")
	g, goOff, ok := ws.toGo(card, end)
	if !ok || !strings.HasSuffix(string(g.content[:goOff]), "props.") {
		t.Errorf("end of props.: ok=%v, Go text before %q", ok, string(g.content[max(goOff-10, 0):goOff]))
	}
	// A Props field (decl file).
	decl, _ := ws.genOf(card)
	field := strings.Index(mapCard, "IsOpen")
	if g, goOff, ok := ws.toGo(card, field); !ok || g != decl || string(decl.content[goOff:goOff+6]) != "IsOpen" {
		t.Errorf("Props field: %v", ok)
	}

	// span: the attribute is-open maps to the whole field key IsOpen, and
	// offsets past the key's length clamp.
	const page = "src/pages/index.lyr"
	_, pimpl := ws.genOf(page)
	attr := strings.Index(mapPage, "is-open")
	g, goOff, ok = ws.toGo(page, attr)
	if !ok || g != pimpl || string(g.content[goOff:goOff+6]) != "IsOpen" {
		t.Fatalf("span start: ok=%v", ok)
	}
	if _, last, _ := ws.toGo(page, attr+len("is-open")); last != goOff+len("IsOpen") {
		t.Errorf("span end %d, want %d (clamped)", last, goOff+len("IsOpen"))
	}
	if back, ok := g.fromGo(goOff + 5); !ok || back != attr+5 {
		t.Errorf("span reverse = %d, %v; want %d", back, ok, attr+5)
	}

	// echo: the generated "_ = label" maps to nothing.
	echo := strings.Index(string(impl.content), "_ = label")
	if echo < 0 {
		t.Fatal("no echo line")
	}
	if _, ok := impl.fromGo(echo + len("_ = ")); ok {
		t.Error("an echo segment maps to the .lyr file")
	}
	// Glue maps to nothing.
	if _, ok := impl.fromGo(strings.Index(string(impl.content), "lyr__w.Enter")); ok {
		t.Error("glue maps to the .lyr file")
	}
	// Template text isn't Go.
	if _, _, ok := ws.toGo(card, strings.Index(mapCard, "<p")); ok {
		t.Error("template markup maps to Go")
	}

	// Through the LSP edge: a UTF-16 position after "Héllo ✓ " converts
	// to the right byte offset and back.
	lines := ws.lines[card]
	p := posAt(mapCard, start)
	if lines.offset(p, encUTF16) != start || lines.position(start, encUTF16) != p {
		t.Errorf("UTF-16 edge: %v ↔ %d", p, start)
	}
	m := mapper{ws: ws, enc: encUTF16}
	_, goOff, _ = ws.toGo(card, start)
	r := impl.lines.rangeOf(goOff, goOff+len("props.Title"), encUTF16)
	uri, out, _, ok := m.location(impl.uri, r)
	if !ok || uriToPath(uri) != ws.absLyr(card) || rangeText(mapCard, out) != "props.Title" {
		t.Errorf("location mapping: %v %v %q", ok, out, rangeText(mapCard, out))
	}
}

func TestClassify(t *testing.T) {
	ws := memWorkspace(t, map[string]string{"src/components/Card.lyr": mapCard, "src/pages/index.lyr": mapPage})
	page := ws.byPath["src/pages/index.lyr"]
	card := ws.byPath["src/components/Card.lyr"]
	for _, tc := range []struct {
		f    string
		src  string
		at   string
		want regionKind
	}{
		{"card", mapCard, "label :=", regionGo},
		{"card", mapCard, "props.}", regionGo},
		{"card", mapCard, "label}", regionGo},
		{"card", mapCard, "class=", regionAttr},
		{"page", mapPage, "Card \"", regionImport},
		{"page", mapPage, "\"../components", regionImport},
		{"page", mapPage, "Card title", regionTag},
		{"page", mapPage, "title=", regionProp},
		{"page", mapPage, "is-open", regionProp},
	} {
		f := page
		if tc.f == "card" {
			f = card
		}
		if got := classify(f, strings.Index(tc.src, tc.at)+1).kind; got != tc.want {
			t.Errorf("classify %q = %d, want %d", tc.at, got, tc.want)
		}
	}
	if loc, ok := ws.nativeDefinition(page, classify(page, strings.Index(mapPage, "is-open"))); !ok || loc == nil ||
		rangeText(mapCard, loc.Range) != "IsOpen" {
		t.Errorf("definition on is-open: %v %v", loc, ok)
	}
}

func TestScrubHover(t *testing.T) {
	in := "```go\nvar greeting string\n```\n\n---\n\nline ../../../../src/pages/index.lyr:4:1\n"
	if got := scrubHover(in); got != "```go\nvar greeting string\n```" {
		t.Errorf("scrubHover = %q", got)
	}
	in = "```go\nfield Post string\n```\n\n---\n\nPost is the title.\n\n\n---\n\n[`(card.Props).Post` on pkg.go.dev](https://pkg.go.dev/example.com/site/lyr_gen/decl/components/card#Props.Post)"
	if got := scrubHover(in); got != "```go\nfield Post string\n```\n\n---\n\nPost is the title." {
		t.Errorf("scrubHover = %q", got)
	}
}

func TestGoplsVersions(t *testing.T) {
	goV, v := goplsVersions(`{"GoVersion":"go1.23.4","Main":{"Version":"v0.18.1"},"Version":"v0.18.1"}`)
	if goV != "go1.23.4" || v != "v0.18.1" {
		t.Errorf("goplsVersions = %q, %q", goV, v)
	}
	if goV, _ := goplsVersions("gopls v0.30.0 built with go1.28.1 X:foo"); goV != "go1.28.1" {
		t.Errorf("text form: %q", goV)
	}
	ws := &workspace{goVersion: "1.27"}
	s := &Server{}
	if msg := s.refuseGopls(ws, "gopls", `{"GoVersion":"go1.26.9"}`); !strings.Contains(msg, installGopls) {
		t.Errorf("go1.26.9 for go 1.27 not refused: %q", msg)
	}
	for _, ok := range []string{`{"GoVersion":"go1.27.3"}`, `{"GoVersion":"go1.27"}`, `not json`} {
		if msg := s.refuseGopls(ws, "gopls", ok); msg != "" {
			t.Errorf("%s refused: %s", ok, msg)
		}
	}
}

func TestMapWorkspaceEdit(t *testing.T) {
	ws := memWorkspace(t, map[string]string{"src/components/Card.lyr": mapCard, "src/pages/index.lyr": mapPage})
	_, impl := ws.genOf("src/components/Card.lyr")
	m := mapper{ws: ws, enc: encUTF16}
	label := strings.Index(string(impl.content), "label :=")
	echo := strings.Index(string(impl.content), "_ = label") + len("_ = ")
	edit := map[string]any{"documentChanges": []any{map[string]any{
		"textDocument": map[string]any{"uri": impl.uri, "version": 3},
		"edits": []any{
			map[string]any{"range": impl.lines.rangeOf(label, label+5, encUTF16), "newText": "title"},
			map[string]any{"range": impl.lines.rangeOf(echo, echo+5, encUTF16), "newText": "title"},
		},
	}}}
	raw, _ := json.Marshal(edit)
	out, dropped := m.mapWorkspaceEdit(raw, true)
	if dropped != 1 {
		t.Errorf("dropped %d edits, want 1 (the echo line)", dropped)
	}
	var we struct {
		Changes map[string][]struct {
			Range Range `json:"range"`
		} `json:"changes"`
	}
	json.Unmarshal(out, &we)
	edits := we.Changes[ws.lyrURI("src/components/Card.lyr")]
	if len(edits) != 1 || rangeText(mapCard, edits[0].Range) != "label" || bytes.Contains(out, []byte("documentChanges")) {
		t.Errorf("mapped edit: %s", out)
	}
}
