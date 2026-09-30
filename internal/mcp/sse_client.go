package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
)

// sseClient speaks the legacy HTTP+SSE transport: one long-lived GET carries
// every reply, while requests are POSTed to an endpoint that the stream
// advertises.
//
// The reader goroutine and the callers are deliberately decoupled. Replies for
// concurrent calls arrive interleaved on a single stream, so the reader only
// routes frames into per-request channels and each caller waits for its own.
// That split is what allows several tool calls in flight at once: parsing in
// the caller would serialise them, and waiting in the reader would let one
// caller that gave up block every other one.
type sseClient struct {
	cfg     ServerConfig
	baseURL *url.URL
	http    *http.Client
	cancel  context.CancelFunc

	nextID atomic.Int64

	mu        sync.Mutex
	endpoint  string
	streamErr error
	closed    bool
	pending   map[int64]chan jsonRPCResponse

	// ready is closed once the endpoint is known or the stream has died, and
	// dead once the stream has died. They are separate so a caller waiting for
	// the handshake is not woken by the failure of someone else's call, and they
	// exist as channels so no waiter ever has to hold the lock while blocked.
	ready     chan struct{}
	dead      chan struct{}
	readyOnce sync.Once
	deadOnce  sync.Once
}

func newSSEClient(cfg ServerConfig) (*sseClient, error) {
	base, err := url.Parse(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("mcp sse url %q: %w", cfg.URL, err)
	}
	c := &sseClient{
		cfg:     cfg,
		baseURL: base,
		http:    &http.Client{},
		pending: map[int64]chan jsonRPCResponse{},
		ready:   make(chan struct{}),
		dead:    make(chan struct{}),
	}
	// The stream belongs to the transport, not to any single request: it is
	// opened once here and torn down by Close. Deriving it from a caller's
	// context would let the first cancelled request kill the session that the
	// other in-flight calls are still using.
	ctx, cancel := context.WithCancel(context.Background())
	c.cancel = cancel
	go c.readStream(ctx)
	return c, nil
}

func (c *sseClient) Initialize(ctx context.Context) error {
	_, err := c.call(ctx, "initialize", initializeParams())
	return err
}

func (c *sseClient) ListTools(ctx context.Context) ([]ToolDef, error) {
	raw, err := c.call(ctx, "tools/list", map[string]any{})
	if err != nil {
		return nil, err
	}

	var result toolListResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("mcp parse tools/list: %w", err)
	}
	return result.Tools, nil
}

func (c *sseClient) CallTool(ctx context.Context, name string, args json.RawMessage) (CallToolResult, error) {
	if len(args) == 0 {
		args = json.RawMessage("{}")
	}

	raw, err := c.call(ctx, "tools/call", map[string]any{
		"name":      name,
		"arguments": args,
	})
	if err != nil {
		return CallToolResult{}, err
	}

	var result CallToolResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return CallToolResult{}, fmt.Errorf("mcp parse tools/call: %w", err)
	}
	return result, nil
}

func (c *sseClient) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	id := c.nextID.Add(1)

	// One deadline covers the whole round trip: waiting for the endpoint, the
	// POST, and the reply on the stream. A per-step timeout would let a slow
	// server extend a single call without bound.
	reqCtx, cancel := context.WithTimeout(ctx, c.cfg.RequestTimeout())
	defer cancel()

	// The reply arrives on the stream, so the routing slot must exist before the
	// request is sent. Registering first is what makes a fast answer impossible
	// to lose.
	ch := make(chan jsonRPCResponse, 1)
	if err := c.register(id, ch); err != nil {
		return nil, c.fail(method, err)
	}
	defer c.forget(id)

	endpoint, err := c.waitEndpoint(reqCtx)
	if err != nil {
		return nil, c.fail(method, err)
	}

	body, err := json.Marshal(jsonRPCRequest{JSONRPC: "2.0", ID: id, Method: method, Params: params})
	if err != nil {
		return nil, c.fail(method, err)
	}

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, c.fail(method, err)
	}
	applyAuthHeaders(req, c.cfg)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, c.fail(method, remoteRequestError(reqCtx, err))
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		statusErr := remoteStatusError(resp)
		_ = resp.Body.Close()
		return nil, c.fail(method, statusErr)
	}
	// The POST body is only an acknowledgement; draining it lets the connection
	// be reused for the next call.
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, remoteErrorBodyLimit))
	_ = resp.Body.Close()

	raw, err := c.awaitResponse(reqCtx, ch)
	if err != nil {
		return nil, c.fail(method, err)
	}
	return raw, nil
}

// Close tears the event stream down.
//
// It is repeatable and never fails: the POST-less transport has nothing to
// release server-side, and the only thing that matters locally is that nobody
// keeps waiting on a stream that will never answer again.
func (c *sseClient) Close() error {
	c.mu.Lock()
	alreadyClosed := c.closed
	c.closed = true
	c.mu.Unlock()
	if !alreadyClosed {
		// Waking the waiters here, instead of relying on the reader to notice the
		// cancelled request, makes Close deterministic: the reader may be parked
		// in a read that only unblocks once the server reacts.
		c.streamFailed(errors.New("client closed"))
		c.cancel()
	}
	return nil
}

// readStream owns the event stream for the transport's whole life: it resolves
// the endpoint, routes replies, and reports the stream's death to every waiter.
func (c *sseClient) readStream(ctx context.Context) {
	body, err := c.openStream(ctx)
	if err != nil {
		c.streamFailed(err)
		return
	}
	defer body.Close()

	events := newSSEReader(body)
	for events.next() {
		event := events.event()
		// The endpoint is the first event of a well-formed stream. A server that
		// leaves the event name off is still understood while the endpoint is
		// unknown, but only for data that can be a URL at all: an unnamed message
		// frame must never be mistaken for the endpoint and send later POSTs into
		// the void.
		if event.name == "endpoint" || (event.name == "" && !c.hasEndpoint() && looksLikeEndpoint(event.data)) {
			if err := c.setEndpoint(event.data); err != nil {
				c.streamFailed(err)
				return
			}
			continue
		}
		if event.name != "" && event.name != "message" {
			continue
		}
		c.dispatch(event.data)
	}
	if err := events.err(); err != nil {
		c.streamFailed(fmt.Errorf("read event stream: %w", err))
		return
	}
	// Losing the stream is terminal for the session: no reply can arrive any
	// more. Every waiter is failed now, rather than each one discovering it
	// alone after its own timeout.
	c.streamFailed(errors.New("event stream closed by server"))
}

func (c *sseClient) openStream(ctx context.Context) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL.String(), nil)
	if err != nil {
		return nil, err
	}
	applyAuthHeaders(req, c.cfg)
	req.Header.Set("Accept", "text/event-stream")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, remoteRequestError(ctx, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		defer resp.Body.Close()
		return nil, remoteStatusError(resp)
	}
	return resp.Body, nil
}

// setEndpoint resolves the advertised POST target. The endpoint is normally a
// path, which only means something relative to the stream URL -- the one
// absolute URL the server has given us.
func (c *sseClient) setEndpoint(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return errors.New("endpoint event carried no url")
	}
	ref, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("endpoint %q: %w", raw, err)
	}
	resolved := c.baseURL.ResolveReference(ref).String()

	c.mu.Lock()
	c.endpoint = resolved
	c.mu.Unlock()
	c.markReady()
	return nil
}

func (c *sseClient) hasEndpoint() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.endpoint != ""
}

// looksLikeEndpoint reports whether an unnamed frame's data could be the POST
// target: an absolute URL or an absolute path, which is all the legacy transport
// specifies.
func looksLikeEndpoint(data string) bool {
	data = strings.TrimSpace(data)
	return strings.HasPrefix(data, "/") ||
		strings.HasPrefix(data, "http://") ||
		strings.HasPrefix(data, "https://")
}

// dispatch routes one frame to the call that is waiting for it. Frames that are
// not JSON-RPC, or that answer nobody, are dropped: the stream is shared, so
// notifications and late replies are normal traffic, not errors.
func (c *sseClient) dispatch(data string) {
	var rpcResp jsonRPCResponse
	if err := json.Unmarshal([]byte(data), &rpcResp); err != nil {
		return
	}

	c.mu.Lock()
	ch, ok := c.pending[rpcResp.ID]
	if ok {
		delete(c.pending, rpcResp.ID)
	}
	c.mu.Unlock()

	if ok {
		// Buffered, so a caller that gave up between the lookup and this send
		// cannot block the reader: nobody reads the value and it is collected
		// with the channel.
		ch <- rpcResp
	}
}

func (c *sseClient) register(id int64, ch chan jsonRPCResponse) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return errors.New("client closed")
	}
	if c.streamErr != nil {
		return c.streamErr
	}
	c.pending[id] = ch
	return nil
}

func (c *sseClient) forget(id int64) {
	c.mu.Lock()
	delete(c.pending, id)
	c.mu.Unlock()
}

// streamFailed records why the stream ended and wakes everyone waiting on it.
// The first cause wins: Close and a server-side drop often race, and "client
// closed" is the more useful story when the application is the one shutting
// down.
func (c *sseClient) streamFailed(err error) {
	c.mu.Lock()
	if c.streamErr == nil {
		c.streamErr = err
	}
	c.mu.Unlock()
	c.markReady()
	c.deadOnce.Do(func() { close(c.dead) })
}

// streamFailure reports the recorded cause, or a default when the stream died
// without one.
func (c *sseClient) streamFailure() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.streamErr != nil {
		return c.streamErr
	}
	if c.closed {
		return errors.New("client closed")
	}
	return errors.New("event stream closed by server")
}

func (c *sseClient) markReady() {
	c.readyOnce.Do(func() { close(c.ready) })
}

// waitEndpoint blocks until the stream has advertised its endpoint. A caller
// that arrives first waits here, bounded by its own request deadline.
func (c *sseClient) waitEndpoint(ctx context.Context) (string, error) {
	if endpoint, err := c.endpointOrErr(); endpoint != "" || err != nil {
		return endpoint, err
	}

	select {
	case <-c.ready:
	case <-ctx.Done():
		return "", ctx.Err()
	}
	return c.endpointOrErr()
}

// endpointOrErr reports the endpoint, the reason it will never arrive, or
// ("", nil) while it is still pending.
func (c *sseClient) endpointOrErr() (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.endpoint != "" {
		return c.endpoint, nil
	}
	if c.streamErr != nil {
		return "", c.streamErr
	}
	if c.closed {
		return "", errors.New("client closed")
	}
	return "", nil
}

// awaitResponse waits for this call's reply, the death of the stream, or the
// caller's deadline. A dead stream is checked before the deadline because the
// answer can no longer arrive; the reply itself is checked first, since a frame
// delivered just before the stream died is still a valid answer.
func (c *sseClient) awaitResponse(ctx context.Context, ch <-chan jsonRPCResponse) (json.RawMessage, error) {
	deliver := func(rpcResp jsonRPCResponse) (json.RawMessage, error) {
		if rpcResp.Error != nil {
			return nil, rpcResponseError(rpcResp.Error)
		}
		return rpcResp.Result, nil
	}

	select {
	case rpcResp := <-ch:
		return deliver(rpcResp)
	case <-c.dead:
		select {
		case rpcResp := <-ch:
			return deliver(rpcResp)
		default:
		}
		return nil, c.streamFailure()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// fail tags an error with its transport and JSON-RPC method, matching the http
// client so /mcp reads the same for both.
func (c *sseClient) fail(method string, err error) error {
	return fmt.Errorf("mcp %s %s: %w", TransportSSE, method, err)
}

// sseEvent is one server-sent event: its data lines joined by newlines, plus the
// event name, which is how the legacy transport marks the endpoint frame.
type sseEvent struct {
	name string
	data string
}

// sseReader decodes text/event-stream framing for both transports.
//
// It implements only what MCP uses: "event" and "data". Everything else the
// format allows (comments, ids, retry hints, unknown fields) is ignorable by
// design, so unrecognised input is skipped rather than treated as corruption.
type sseReader struct {
	scanner *bufio.Scanner
	current sseEvent
	name    string
	data    []string
	scanErr error
}

func newSSEReader(r io.Reader) *sseReader {
	scanner := bufio.NewScanner(r)
	// A tool result is routinely larger than bufio's 64KiB line default, and a
	// split data line would silently corrupt the JSON it carries.
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	return &sseReader{scanner: scanner}
}

// next advances to the next event, reporting false at the end of the stream.
func (r *sseReader) next() bool {
	for r.scanner.Scan() {
		line := r.scanner.Text()
		if line == "" {
			if r.flush() {
				return true
			}
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue // comment / keep-alive
		}
		field, value, _ := strings.Cut(line, ":")
		value = strings.TrimPrefix(value, " ")
		switch field {
		case "event":
			r.name = value
		case "data":
			r.data = append(r.data, value)
		}
	}
	// A stream that ends without its trailing blank line still carries the last
	// event's data.
	if r.flush() {
		return true
	}
	r.scanErr = r.scanner.Err()
	return false
}

// flush turns the accumulated fields into an event, reporting whether there was
// one to dispatch.
func (r *sseReader) flush() bool {
	if r.name == "" && len(r.data) == 0 {
		return false
	}
	r.current = sseEvent{name: r.name, data: strings.Join(r.data, "\n")}
	r.name = ""
	r.data = r.data[:0]
	return true
}

func (r *sseReader) event() sseEvent { return r.current }

func (r *sseReader) err() error { return r.scanErr }
