package lsp

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"sync"
	"time"
)

// stubEnv makes the test binary act as a gopls stub (TestMain). "old": one
// whose serverInfo says it was built with an old Go, for the refusal test.
// "record:<file>": a current one that appends every message it receives
// (and its client's answer to a workspace/configuration it asks after
// initialized) to <file>, one JSON object per line, for the startup order
// test.
const stubEnv = "ORBIT_LSP_TEST_GOPLS_STUB"

// stubRecord is one line of a recording stub's file.
type stubRecord struct {
	Method string  `json:"method"`
	Params jsonRaw `json:"params,omitempty"`
}

func runStubGopls() {
	mode := os.Getenv(stubEnv)
	c := newConn(os.Stdin, os.Stdout)
	goVersion, version := "go1.23.4", "v0.18.1"
	var rec func(method string, params jsonRaw)
	if file, ok := strings.CutPrefix(mode, "record:"); ok {
		goVersion, version = "go1.27.3", "v0.24.0"
		var mu sync.Mutex
		rec = func(method string, params jsonRaw) {
			mu.Lock()
			defer mu.Unlock()
			b, _ := json.Marshal(stubRecord{method, params})
			f, err := os.OpenFile(file, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
			if err != nil {
				return
			}
			f.Write(append(b, '\n'))
			f.Close()
		}
	}
	info, _ := json.Marshal(map[string]any{"GoVersion": goVersion, "Path": "golang.org/x/tools/gopls",
		"Main": map[string]any{"Path": "golang.org/x/tools/gopls", "Version": version}, "Version": version})
	var folder string
	exit := make(chan struct{})
	go c.Run(func(m *message) {
		if rec != nil && m.Method != "" {
			rec(m.Method, m.Params)
		}
		switch m.Method {
		case "initialize":
			var p struct {
				WorkspaceFolders []struct{ URI string } `json:"workspaceFolders"`
			}
			json.Unmarshal(m.Params, &p)
			if len(p.WorkspaceFolders) > 0 {
				folder = p.WorkspaceFolders[0].URI
			}
			c.Reply(m.ID, map[string]any{"capabilities": map[string]any{"hoverProvider": true},
				"serverInfo": map[string]any{"name": "gopls", "version": string(info)}}, nil)
		case "initialized":
			if rec != nil {
				go func() {
					ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
					defer cancel()
					res, _ := c.Call(ctx, "workspace/configuration", map[string]any{
						"items": []any{map[string]any{"scopeUri": folder, "section": "gopls"}}})
					rec("answer:workspace/configuration", res)
				}()
			}
		case "exit":
			close(exit)
		default:
			if m.isRequest() {
				c.Reply(m.ID, nil, nil)
			}
		}
	})
	select {
	case <-exit:
	case <-c.Done():
	}
	c.Close()
}
