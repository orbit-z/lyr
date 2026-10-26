package lsp

// textDocument/formatting (PLAN.md §6.1): the whole document, with the
// same rules as orbit fmt, one TextEdit per changed region.

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sync"

	"github.com/orbit-z/orbit/internal/check"
	"github.com/orbit-z/orbit/internal/format"
)

// bunState is the Bun lookup, done once per session.
type bunState struct {
	once    sync.Once
	path    string
	notice  string
	noticed sync.Once
}

// TextEdit is an LSP text edit.
type TextEdit struct {
	Range   Range  `json:"range"`
	NewText string `json:"newText"`
}

// formatting answers textDocument/formatting. It needs no gopls and no
// generated code, so it doesn't wait for them.
func (s *Server) formatting(ctx context.Context, params jsonRaw) (any, *rpcError) {
	var p struct {
		TextDocument struct {
			URI string `json:"uri"`
		} `json:"textDocument"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &rpcError{Code: codeInvalidParams, Message: err.Error()}
	}
	s.mu.Lock()
	ws := s.ws
	var src, rel, root string
	ok := false
	if ws != nil {
		rel = ws.orbitPath(p.TextDocument.URI)
		if f := ws.byPath[rel]; rel != "" && f != nil {
			src, root, ok = f.Src, ws.root, true
		}
	}
	s.mu.Unlock()
	if !ok {
		return nil, nil // Markdown, or a file the server doesn't follow
	}
	s.bun.once.Do(func() {
		s.bun.path, s.bun.notice = check.FindBun(context.Background(), "", "orbit: <script> and <style> aren't formatted")
	})
	res := format.Regions(src, format.Options{
		Path:    filepath.ToSlash(rel),
		Bun:     s.bun.path,
		BunDir:  root,
		Context: ctx,
	})
	edits := []TextEdit{}
	if res.Skipped > 0 && s.bun.notice != "" {
		s.bun.noticed.Do(func() { s.showMessage(3, s.bun.notice) }) // Info
	}
	if len(res.Errors) > 0 && !res.BunFailed {
		return edits, nil // a syntax error: the diagnostic is already shown
	}
	x := newLineIndex(src)
	for _, e := range res.Edits {
		edits = append(edits, TextEdit{Range: x.rangeOf(e.Span.Start, e.Span.End, encUTF16), NewText: e.New})
	}
	return edits, nil
}
