package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// remoteErrorBodyLimit caps how much of a failing server's body is quoted back
// in an error. That text reaches the TUI and the logs, and a server is free to
// answer with megabytes.
const remoteErrorBodyLimit = 512

// remoteCloseTimeout bounds the best-effort session teardown, so a server that
// stopped answering cannot stall application shutdown.
const remoteCloseTimeout = 5 * time.Second

// newRemoteClient builds the client for the http and sse transports.
//
// It lives in its own file so the two remote transports can be implemented and
// tested without touching the stdio client or the manager.
func newRemoteClient(cfg ServerConfig) (Client, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	switch cfg.Transport() {
	case TransportHTTP:
		return newHTTPClient(cfg)
	case TransportSSE:
		return newSSEClient(cfg)
	}
	return nil, fmt.Errorf("unknown transport %q (use stdio, http or sse)", cfg.Type)
}

// httpClient speaks the streamable HTTP transport: every call is a POST whose
// response is either one JSON object or an SSE stream carrying it.
type httpClient struct {
	cfg  ServerConfig
	http *http.Client
	url  string

	nextID atomic.Int64

	// sessionMu guards sessionID. The server hands the id over in the response
	// to initialize, but any later response may carry a new one, and the tool
	// calls that have to present it run concurrently.
	sessionMu sync.Mutex
	sessionID string
}

func newHTTPClient(cfg ServerConfig) (*httpClient, error) {
	if _, err := url.Parse(cfg.URL); err != nil {
		return nil, fmt.Errorf("mcp http url %q: %w", cfg.URL, err)
	}
	return &httpClient{cfg: cfg, url: cfg.URL, http: &http.Client{}}, nil
}

func (c *httpClient) Initialize(ctx context.Context) error {
	_, err := c.call(ctx, "initialize", initializeParams())
	return err
}

func (c *httpClient) ListTools(ctx context.Context) ([]ToolDef, error) {
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

func (c *httpClient) CallTool(ctx context.Context, name string, args json.RawMessage) (CallToolResult, error) {
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

func (c *httpClient) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	id := c.nextID.Add(1)

	body, err := json.Marshal(jsonRPCRequest{JSONRPC: "2.0", ID: id, Method: method, Params: params})
	if err != nil {
		return nil, c.fail(method, err)
	}

	// The request timeout is derived from the caller's context, so the caller
	// stays in charge: whichever expires first wins, and a caller that is
	// cancelled mid-request is reported as a cancellation rather than a
	// transport failure.
	reqCtx, cancel := context.WithTimeout(ctx, c.cfg.RequestTimeout())
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, c.url, bytes.NewReader(body))
	if err != nil {
		return nil, c.fail(method, err)
	}
	applyAuthHeaders(req, c.cfg)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if session := c.session(); session != "" {
		req.Header.Set("Mcp-Session-Id", session)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, c.fail(method, remoteRequestError(reqCtx, err))
	}
	defer resp.Body.Close()

	if session := strings.TrimSpace(resp.Header.Get("Mcp-Session-Id")); session != "" {
		c.setSession(session)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, c.fail(method, remoteStatusError(resp))
	}

	// A streamable server may answer a POST with one JSON object or with an SSE
	// stream that carries the object, so the parser follows the content type
	// instead of assuming either.
	if isEventStream(resp.Header.Get("Content-Type")) {
		raw, err := readResponseFrame(reqCtx, resp.Body, id)
		if err != nil {
			return nil, c.fail(method, err)
		}
		return raw, nil
	}

	var rpcResp jsonRPCResponse
	if err := json.NewDecoder(resp.Body).Decode(&rpcResp); err != nil {
		return nil, c.fail(method, fmt.Errorf("decode response: %w", err))
	}
	if rpcResp.ID != id {
		return nil, c.fail(method, fmt.Errorf("response id %d does not match request id %d", rpcResp.ID, id))
	}
	if rpcResp.Error != nil {
		return nil, c.fail(method, rpcResponseError(rpcResp.Error))
	}
	return rpcResp.Result, nil
}

// Close releases the server-side session.
//
// It is deliberately best effort and safe to repeat: the DELETE is a courtesy
// that stops the server leaking session state, but shutting down must not fail
// because a server is already gone, and a client that never initialized has no
// session to release.
func (c *httpClient) Close() error {
	reqCtx, cancel := context.WithTimeout(context.Background(), remoteCloseTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodDelete, c.url, nil)
	if err != nil {
		return nil
	}
	applyAuthHeaders(req, c.cfg)
	// The MCP headers ride along so a strict server can route the DELETE the
	// same way it routed the POSTs.
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if session := c.session(); session != "" {
		req.Header.Set("Mcp-Session-Id", session)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, remoteErrorBodyLimit))
	_ = resp.Body.Close()
	return nil
}

func (c *httpClient) session() string {
	c.sessionMu.Lock()
	defer c.sessionMu.Unlock()
	return c.sessionID
}

func (c *httpClient) setSession(id string) {
	c.sessionMu.Lock()
	c.sessionID = id
	c.sessionMu.Unlock()
}

// fail tags an error with its transport and JSON-RPC method. /mcp renders this
// text verbatim, and "which call to which server broke" is the part a user
// needs; the cause stays wrapped so errors.Is still works.
func (c *httpClient) fail(method string, err error) error {
	return fmt.Errorf("mcp %s %s: %w", TransportHTTP, method, err)
}

// readResponseFrame pulls the JSON-RPC response for id out of an SSE stream,
// which is how both the streamable transport (a POST answered with an event
// stream) and the legacy transport receive replies.
//
// Frames that are notifications, that carry another request's id, or that are
// not JSON at all are skipped rather than reported: a server is allowed to
// interleave them, and failing a call over one would break a healthy session.
func readResponseFrame(ctx context.Context, stream io.Reader, id int64) (json.RawMessage, error) {
	events := newSSEReader(stream)
	for events.next() {
		event := events.event()
		if event.name != "" && event.name != "message" {
			continue
		}
		var rpcResp jsonRPCResponse
		if err := json.Unmarshal([]byte(event.data), &rpcResp); err != nil {
			continue
		}
		if rpcResp.ID != id {
			continue
		}
		if rpcResp.Error != nil {
			return nil, rpcResponseError(rpcResp.Error)
		}
		return rpcResp.Result, nil
	}
	if err := events.err(); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, fmt.Errorf("read event stream: %w", err)
	}
	return nil, fmt.Errorf("event stream ended without a response for id %d", id)
}

// initializeParams mirrors the stdio handshake so a server sees the same client
// identity whichever transport reached it.
func initializeParams() map[string]any {
	return map[string]any{
		"protocolVersion": protocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo": map[string]any{
			"name":    "mini-opencode",
			"version": "0.2.0",
		},
	}
}

// applyAuthHeaders copies the configured credentials onto req.
//
// The whole map is applied instead of setting a bearer here, because
// AuthHeaders already resolves the token and refuses to overwrite an explicit
// Authorization header: a server configured with a bespoke scheme (Basic, or a
// vendor header) must keep it.
func applyAuthHeaders(req *http.Request, cfg ServerConfig) {
	for key, value := range cfg.AuthHeaders() {
		req.Header.Set(key, value)
	}
}

// isEventStream reports whether a response body is an SSE stream. Servers append
// media type parameters, so this is a substring test on the type.
func isEventStream(contentType string) bool {
	return strings.Contains(strings.ToLower(contentType), "text/event-stream")
}

// remoteStatusError describes a non-2xx reply.
//
// Only the status and a truncated body are reported: request headers carry the
// token, and an error string is exactly the kind of text that ends up in logs.
func remoteStatusError(resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, remoteErrorBodyLimit+1))
	text := strings.TrimSpace(string(body))
	if len(text) > remoteErrorBodyLimit {
		// Truncating at a byte offset can split a multi-byte rune.
		text = strings.ToValidUTF8(text[:remoteErrorBodyLimit], "") + "…"
	}
	if text == "" {
		return fmt.Errorf("unexpected status %s", resp.Status)
	}
	return fmt.Errorf("unexpected status %s: %s", resp.Status, text)
}

// remoteRequestError prefers the context's error. net/http reports a cancelled
// or timed-out request as an opaque *url.Error, while callers want the
// context.Canceled / context.DeadlineExceeded they can match on.
func remoteRequestError(ctx context.Context, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	return err
}

// rpcResponseError keeps the stdio client's wording, so a failing tool reads the
// same whether its server is local or remote.
func rpcResponseError(e *jsonRPCError) error {
	return fmt.Errorf("mcp error %d: %s", e.Code, e.Message)
}
