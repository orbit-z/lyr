package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"unicode/utf16"
)

// A minimal LSP client: JSON-RPC 2.0 with Content-Length framing over the
// server's stdio. Responses are matched to calls by ID; the server's own
// requests and notifications go to the handlers.

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *rpcError) Error() string { return fmt.Sprintf("%d: %s", e.Code, e.Message) }

type wireMsg struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      *json.RawMessage `json:"id,omitempty"`
	Method  string           `json:"method,omitempty"`
	Params  json.RawMessage  `json:"params,omitempty"`
	Result  json.RawMessage  `json:"result,omitempty"`
	Error   *rpcError        `json:"error,omitempty"`
}

type reply struct {
	result json.RawMessage
	err    *rpcError
}

type client struct {
	wmu sync.Mutex
	w   io.Writer

	mu      sync.Mutex
	nextID  int
	pending map[int]chan reply
	closed  bool

	// onRequest answers a server→client request; onNotify sees a
	// notification. Both run on the reader goroutine.
	onRequest func(method string, params json.RawMessage) any
	onNotify  func(method string, params json.RawMessage)
}

func newClient(r io.Reader, w io.Writer, onRequest func(string, json.RawMessage) any, onNotify func(string, json.RawMessage)) *client {
	c := &client{w: w, pending: map[int]chan reply{}, onRequest: onRequest, onNotify: onNotify}
	go c.read(bufio.NewReaderSize(r, 1<<16))
	return c
}

func (c *client) write(m wireMsg) error {
	m.JSONRPC = "2.0"
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	c.wmu.Lock()
	defer c.wmu.Unlock()
	if _, err := fmt.Fprintf(c.w, "Content-Length: %d\r\n\r\n", len(b)); err != nil {
		return err
	}
	_, err = c.w.Write(b)
	return err
}

func marshal(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

// call sends a request and waits for its response.
func (c *client) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, errors.New("connection closed")
	}
	c.nextID++
	id := c.nextID
	ch := make(chan reply, 1)
	c.pending[id] = ch
	c.mu.Unlock()
	raw := json.RawMessage(strconv.Itoa(id))
	if err := c.write(wireMsg{ID: &raw, Method: method, Params: marshal(params)}); err != nil {
		return nil, err
	}
	select {
	case r, ok := <-ch:
		if !ok {
			return nil, errors.New("connection closed")
		}
		if r.err != nil {
			return nil, r.err
		}
		return r.result, nil
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, fmt.Errorf("%s: %w", method, ctx.Err())
	}
}

func (c *client) notify(method string, params any) error {
	return c.write(wireMsg{Method: method, Params: marshal(params)})
}

func (c *client) read(r *bufio.Reader) {
	defer func() {
		c.mu.Lock()
		c.closed = true
		for id, ch := range c.pending {
			close(ch)
			delete(c.pending, id)
		}
		c.mu.Unlock()
	}()
	for {
		n := -1
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			line = strings.TrimRight(line, "\r\n")
			if line == "" {
				break
			}
			if k, v, ok := strings.Cut(line, ":"); ok && strings.EqualFold(strings.TrimSpace(k), "Content-Length") {
				n, _ = strconv.Atoi(strings.TrimSpace(v))
			}
		}
		if n < 0 {
			return
		}
		body := make([]byte, n)
		if _, err := io.ReadFull(r, body); err != nil {
			return
		}
		var m wireMsg
		if json.Unmarshal(body, &m) != nil {
			continue
		}
		switch {
		case m.Method != "" && m.ID != nil: // server→client request
			res := c.onRequest(m.Method, m.Params)
			c.write(wireMsg{ID: m.ID, Result: marshal(res)})
		case m.Method != "":
			c.onNotify(m.Method, m.Params)
		case m.ID != nil:
			id, err := strconv.Atoi(string(*m.ID))
			if err != nil {
				continue
			}
			c.mu.Lock()
			ch := c.pending[id]
			delete(c.pending, id)
			c.mu.Unlock()
			if ch != nil {
				res := m.Result
				if res == nil {
					res = json.RawMessage("null")
				}
				ch <- reply{result: res, err: m.Error}
			}
		}
	}
}

// position is an LSP position (UTF-16 columns).
type position struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

// posAt is the UTF-16 position of byte offset off in text.
func posAt(text string, off int) position {
	line, start := 0, 0
	for i := 0; i < off; i++ {
		if text[i] == '\n' {
			line++
			start = i + 1
		}
	}
	n := 0
	for _, r := range text[start:off] {
		n += len(utf16.Encode([]rune{r}))
	}
	return position{Line: line, Character: n}
}

// find is the position of the first occurrence of needle in text, plus
// delta bytes; it panics when needle is missing (a generator change).
func find(text, needle string, delta int) position {
	i := strings.Index(text, needle)
	if i < 0 {
		panic(fmt.Sprintf("probe %q not found", needle))
	}
	return posAt(text, i+delta)
}
