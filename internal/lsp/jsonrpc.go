package lsp

// A minimal JSON-RPC 2.0 peer over a byte stream with LSP's Content-Length
// framing (PLAN.md §9 Transport). The same Conn talks to the editor (Orbit
// is the server) and to gopls (Orbit is the client). Payloads stay
// json.RawMessage: the server decodes only the fields it rewrites.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
)

// maxMessage bounds a message body, so a corrupt header can't make the
// reader allocate gigabytes.
const maxMessage = 1 << 28

// JSON-RPC and LSP error codes.
const (
	codeParseError       = -32700
	codeInvalidRequest   = -32600
	codeMethodNotFound   = -32601
	codeInvalidParams    = -32602
	codeInternalError    = -32603
	codeRequestCancelled = -32800
)

// message is any JSON-RPC message: a request (ID and Method), a
// notification (Method only) or a response (ID, and Result or Error).
type message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

func (m *message) isRequest() bool      { return m.Method != "" && len(m.ID) > 0 }
func (m *message) isNotification() bool { return m.Method != "" && len(m.ID) == 0 }
func (m *message) isResponse() bool     { return m.Method == "" && len(m.ID) > 0 }

// rpcError is a JSON-RPC error object.
type rpcError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *rpcError) Error() string { return fmt.Sprintf("jsonrpc error %d: %s", e.Code, e.Message) }

// errClosed is returned by calls on a closed connection.
var errClosed = errors.New("lsp: connection closed")

// readMessage reads one framed message body. Header names are
// case-insensitive; headers other than Content-Length (Content-Type) are
// ignored. It returns io.EOF only at a clean end between messages.
func readMessage(r *bufio.Reader) ([]byte, error) {
	length := -1
	first := true
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			if err == io.EOF && first && line == "" {
				return nil, io.EOF
			}
			if err == io.EOF {
				return nil, io.ErrUnexpectedEOF
			}
			return nil, err
		}
		first = false
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		name, value, ok := strings.Cut(line, ":")
		if !ok {
			return nil, fmt.Errorf("lsp: bad header line %q", line)
		}
		if strings.EqualFold(strings.TrimSpace(name), "Content-Length") {
			n, err := strconv.Atoi(strings.TrimSpace(value))
			if err != nil || n < 0 {
				return nil, fmt.Errorf("lsp: bad Content-Length %q", strings.TrimSpace(value))
			}
			if n > maxMessage {
				return nil, fmt.Errorf("lsp: message of %d bytes is too large", n)
			}
			length = n
		}
	}
	if length < 0 {
		return nil, errors.New("lsp: message without Content-Length")
	}
	body := make([]byte, length)
	if _, err := io.ReadFull(r, body); err != nil {
		if err == io.EOF {
			err = io.ErrUnexpectedEOF
		}
		return nil, err
	}
	return body, nil
}

// writeMessage writes one framed message.
func writeMessage(w io.Writer, body []byte) error {
	var b bytes.Buffer
	b.Grow(len(body) + 32)
	b.WriteString("Content-Length: ")
	b.WriteString(strconv.Itoa(len(body)))
	b.WriteString("\r\n\r\n")
	b.Write(body)
	_, err := w.Write(b.Bytes())
	return err
}

// Conn is one JSON-RPC peer. Outgoing messages go through an unbounded
// queue drained by one writer goroutine, so sending never blocks (holding
// a lock while writing to a peer that is itself blocked writing to us
// would deadlock) and messages leave in the order they were sent.
type Conn struct {
	r *bufio.Reader
	w io.Writer

	qmu    sync.Mutex
	qcond  *sync.Cond
	queue  [][]byte
	closed bool
	wdone  chan struct{}

	mu      sync.Mutex
	next    int64
	pending map[string]chan *message
	done    chan struct{} // closed when the read loop ends
}

// newConn returns a Conn reading r and writing w. Call Run to read.
func newConn(r io.Reader, w io.Writer) *Conn {
	c := &Conn{r: bufio.NewReaderSize(r, 64<<10), w: w, pending: map[string]chan *message{},
		done: make(chan struct{}), wdone: make(chan struct{})}
	c.qcond = sync.NewCond(&c.qmu)
	go c.writeLoop()
	return c
}

func (c *Conn) writeLoop() {
	defer close(c.wdone)
	for {
		c.qmu.Lock()
		for len(c.queue) == 0 && !c.closed {
			c.qcond.Wait()
		}
		if len(c.queue) == 0 {
			c.qmu.Unlock()
			return
		}
		q := c.queue
		c.queue = nil
		c.qmu.Unlock()
		for _, b := range q {
			if err := writeMessage(c.w, b); err != nil {
				c.qmu.Lock()
				c.closed, c.queue = true, nil
				c.qmu.Unlock()
				return
			}
		}
	}
}

// send queues a message.
func (c *Conn) send(m *message) {
	m.JSONRPC = "2.0"
	b, err := json.Marshal(m)
	if err != nil {
		return // only from a bad RawMessage, which callers never build
	}
	c.qmu.Lock()
	if !c.closed {
		c.queue = append(c.queue, b)
		c.qcond.Signal()
	}
	c.qmu.Unlock()
}

// Close stops the writer after the queued messages are written.
func (c *Conn) Close() {
	c.qmu.Lock()
	c.closed = true
	c.qcond.Signal()
	c.qmu.Unlock()
	<-c.wdone
}

// marshalParams turns params into raw JSON; nil stays absent.
func marshalParams(params any) json.RawMessage {
	switch p := params.(type) {
	case nil:
		return nil
	case json.RawMessage:
		return p
	}
	b, err := json.Marshal(params)
	if err != nil {
		return nil
	}
	return b
}

// Notify sends a notification.
func (c *Conn) Notify(method string, params any) {
	c.send(&message{Method: method, Params: marshalParams(params)})
}

// Reply answers a request: with err when it's non-nil, else with result
// (null when result is nil).
func (c *Conn) Reply(id json.RawMessage, result any, err *rpcError) {
	m := &message{ID: id}
	if err != nil {
		m.Error = err
	} else if b := marshalParams(result); b != nil {
		m.Result = b
	} else {
		m.Result = json.RawMessage("null")
	}
	c.send(m)
}

// Call sends a request and waits for its response. When ctx ends first, it
// sends $/cancelRequest and returns ctx.Err(). An error response is
// returned as a *rpcError.
func (c *Conn) Call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	c.mu.Lock()
	c.next++
	id := json.RawMessage(strconv.FormatInt(c.next, 10))
	ch := make(chan *message, 1)
	c.pending[string(id)] = ch
	c.mu.Unlock()
	select {
	case <-c.done:
		c.forget(id)
		return nil, errClosed
	default:
	}
	c.send(&message{ID: id, Method: method, Params: marshalParams(params)})
	select {
	case m := <-ch:
		if m.Error != nil {
			return nil, m.Error
		}
		return m.Result, nil
	case <-ctx.Done():
		c.forget(id)
		c.Notify("$/cancelRequest", map[string]json.RawMessage{"id": id})
		return nil, ctx.Err()
	case <-c.done:
		c.forget(id)
		return nil, errClosed
	}
}

func (c *Conn) forget(id json.RawMessage) {
	c.mu.Lock()
	delete(c.pending, string(id))
	c.mu.Unlock()
}

// Run reads messages until the stream ends, delivering responses to their
// Call and handing requests and notifications to handle, in order, on the
// reading goroutine (handle must not block for long; it starts goroutines
// for requests that wait). It returns nil at a clean end of the stream.
func (c *Conn) Run(handle func(*message)) error {
	defer close(c.done)
	for {
		body, err := readMessage(c.r)
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		var m message
		if err := json.Unmarshal(body, &m); err != nil {
			continue // not JSON: nothing to answer (no id)
		}
		switch {
		case m.isResponse():
			c.mu.Lock()
			ch := c.pending[string(m.ID)]
			delete(c.pending, string(m.ID))
			c.mu.Unlock()
			if ch != nil {
				if m.Result == nil && m.Error == nil {
					m.Result = json.RawMessage("null")
				}
				ch <- &m
			}
		case m.Method != "":
			handle(&m)
		}
	}
}

// Done is closed when Run returns.
func (c *Conn) Done() <-chan struct{} { return c.done }
