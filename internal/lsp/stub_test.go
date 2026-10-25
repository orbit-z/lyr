package lsp

import (
	"encoding/json"
	"os"
)

// stubEnv makes the test binary act as a gopls stub (TestMain): one whose
// serverInfo says it was built with an old Go, for the refusal test.
const stubEnv = "ORBIT_LSP_TEST_GOPLS_STUB"

func runStubGopls() {
	c := newConn(os.Stdin, os.Stdout)
	info, _ := json.Marshal(map[string]any{"GoVersion": "go1.23.4", "Path": "golang.org/x/tools/gopls",
		"Main": map[string]any{"Path": "golang.org/x/tools/gopls", "Version": "v0.18.1"}, "Version": "v0.18.1"})
	exit := make(chan struct{})
	go c.Run(func(m *message) {
		switch m.Method {
		case "initialize":
			c.Reply(m.ID, map[string]any{"capabilities": map[string]any{"hoverProvider": true},
				"serverInfo": map[string]any{"name": "gopls", "version": string(info)}}, nil)
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
