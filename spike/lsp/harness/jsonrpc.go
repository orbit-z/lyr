package main

// Minimal LSP client over stdio: Content-Length framing, json.RawMessage
// everywhere, a transcript of every message, and canned answers to
// server→client requests (recorded so FINDINGS.md can list them).

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// goplsSettings is the answer to every workspace/configuration item.
var goplsSettings = map[string]any{}

// answerMode: "" (sensible answers), "null" (null result to everything), "error" (MethodNotFound to everything).
var answerMode string

type msg struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   json.RawMessage `json:"error,omitempty"`
}

type diagEvent struct {
	at      time.Time
	uri     string
	version int
	raw     json.RawMessage
	n       int
}

type client struct {
	cmd     *exec.Cmd
	w       io.WriteCloser
	wmu     sync.Mutex
	nextID  int
	mu      sync.Mutex
	pending map[string]chan msg
	start   time.Time
	log     *os.File
	logMax  int

	// server→client requests seen: method → count, and first params
	srvReqs   map[string]int
	srvFirst  map[string]json.RawMessage
	notifs    map[string]int
	diags     []diagEvent
	diagCond  *sync.Cond
	cfgAnswer func(params json.RawMessage) any
}

func startClient(goplsPath, dir, logPath string, env []string) (*client, error) {
	f, err := os.Create(logPath)
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(goplsPath)
	cmd.Dir = dir
	cmd.Env = env
	cmd.Stderr = f
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	c := &client{cmd: cmd, w: in, pending: map[string]chan msg{}, log: f, logMax: 1500,
		srvReqs: map[string]int{}, srvFirst: map[string]json.RawMessage{}, notifs: map[string]int{}}
	c.diagCond = sync.NewCond(&c.mu)
	c.start = time.Now()
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	go c.readLoop(bufio.NewReader(out))
	return c, nil
}

func (c *client) logf(format string, a ...any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	fmt.Fprintf(c.log, "[%7.3fs] ", time.Since(c.start).Seconds())
	fmt.Fprintf(c.log, format, a...)
	fmt.Fprintln(c.log)
}

func trim(b []byte, n int) string {
	s := string(b)
	if len(s) > n {
		return s[:n] + fmt.Sprintf("…(+%d bytes)", len(s)-n)
	}
	return s
}

func (c *client) send(m msg) {
	m.JSONRPC = "2.0"
	b, _ := json.Marshal(m)
	c.logf("--> %s", trim(b, c.logMax))
	c.wmu.Lock()
	defer c.wmu.Unlock()
	fmt.Fprintf(c.w, "Content-Length: %d\r\n\r\n", len(b))
	c.w.Write(b)
}

func (c *client) readLoop(r *bufio.Reader) {
	for {
		n := -1
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				c.logf("read error: %v", err)
				return
			}
			line = strings.TrimSpace(line)
			if line == "" {
				break
			}
			if v, ok := strings.CutPrefix(line, "Content-Length:"); ok {
				n, _ = strconv.Atoi(strings.TrimSpace(v))
			}
		}
		buf := make([]byte, n)
		if _, err := io.ReadFull(r, buf); err != nil {
			c.logf("read error: %v", err)
			return
		}
		var m msg
		if err := json.Unmarshal(buf, &m); err != nil {
			c.logf("bad json: %v", err)
			continue
		}
		c.logf("<-- %s", trim(buf, c.logMax))
		switch {
		case m.Method != "" && m.ID != nil:
			go c.answer(m)
		case m.Method != "":
			c.mu.Lock()
			c.notifs[m.Method]++
			if m.Method == "textDocument/publishDiagnostics" {
				var p struct {
					URI         string            `json:"uri"`
					Version     int               `json:"version"`
					Diagnostics []json.RawMessage `json:"diagnostics"`
				}
				json.Unmarshal(m.Params, &p)
				c.diags = append(c.diags, diagEvent{time.Now(), p.URI, p.Version, m.Params, len(p.Diagnostics)})
				c.diagCond.Broadcast()
			}
			c.mu.Unlock()
		default:
			c.mu.Lock()
			ch := c.pending[string(m.ID)]
			delete(c.pending, string(m.ID))
			c.mu.Unlock()
			if ch != nil {
				ch <- m
			}
		}
	}
}

// answer replies to a server→client request with the minimal response.
func (c *client) answer(m msg) {
	c.mu.Lock()
	c.srvReqs[m.Method]++
	if _, ok := c.srvFirst[m.Method]; !ok {
		c.srvFirst[m.Method] = m.Params
	}
	c.mu.Unlock()
	switch answerMode {
	case "null":
		c.send(msg{ID: m.ID, Result: json.RawMessage("null")})
		return
	case "error":
		c.send(msg{ID: m.ID, Error: json.RawMessage(`{"code":-32601,"message":"method not found"}`)})
		return
	}
	var result any
	switch m.Method {
	case "workspace/configuration":
		var p struct {
			Items []json.RawMessage `json:"items"`
		}
		json.Unmarshal(m.Params, &p)
		if c.cfgAnswer != nil {
			result = c.cfgAnswer(m.Params)
		} else {
			res := make([]any, len(p.Items))
			for i := range res {
				res[i] = goplsSettings
			}
			result = res
		}
	case "workspace/applyEdit":
		result = map[string]any{"applied": false}
	default: // client/registerCapability, window/workDoneProgress/create, ...
		result = nil
	}
	b, _ := json.Marshal(result)
	c.send(msg{ID: m.ID, Result: b})
}

func (c *client) call(method string, params any) (json.RawMessage, time.Duration, error) {
	c.mu.Lock()
	c.nextID++
	id := json.RawMessage(strconv.Itoa(c.nextID))
	ch := make(chan msg, 1)
	c.pending[string(id)] = ch
	c.mu.Unlock()
	p, _ := json.Marshal(params)
	t0 := time.Now()
	c.send(msg{ID: id, Method: method, Params: p})
	select {
	case r := <-ch:
		d := time.Since(t0)
		if r.Error != nil {
			return nil, d, fmt.Errorf("%s: %s", method, r.Error)
		}
		return r.Result, d, nil
	case <-time.After(60 * time.Second):
		return nil, time.Since(t0), fmt.Errorf("%s: timeout", method)
	}
}

func (c *client) notify(method string, params any) {
	p, _ := json.Marshal(params)
	c.send(msg{Method: method, Params: p})
}

// waitDiag waits until a publishDiagnostics for uri arrives after index from
// (an index into c.diags) and pred accepts it. Returns the matching event.
func (c *client) waitDiag(from int, uri string, timeout time.Duration, pred func(diagEvent) bool) (diagEvent, bool) {
	deadline := time.Now().Add(timeout)
	timer := time.AfterFunc(timeout, func() { c.mu.Lock(); c.diagCond.Broadcast(); c.mu.Unlock() })
	defer timer.Stop()
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := from; ; {
		for ; i < len(c.diags); i++ {
			e := c.diags[i]
			if e.uri == uri && (pred == nil || pred(e)) {
				return e, true
			}
		}
		if time.Now().After(deadline) {
			return diagEvent{}, false
		}
		c.diagCond.Wait()
	}
}

func (c *client) diagMark() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.diags)
}

// lastDiags returns the latest publishDiagnostics per URI.
func (c *client) lastDiags() map[string]diagEvent {
	c.mu.Lock()
	defer c.mu.Unlock()
	m := map[string]diagEvent{}
	for _, e := range c.diags {
		m[e.uri] = e
	}
	return m
}

func (c *client) shutdown() {
	c.call("shutdown", nil)
	c.notify("exit", nil)
	done := make(chan struct{})
	go func() { c.cmd.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		c.cmd.Process.Kill()
	}
	c.log.Close()
}
