package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

// The remote transports are exercised against fake servers built from
// net/http/httptest only: these tests must never touch the network.

const (
	testRemoteToken = "test-remote-token"
	testTenant      = "acme"
)

// testRPCRequest is the JSON-RPC request a fake server decodes off the wire.
type testRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

// rpcAnswer is one fake answer: either a result or a JSON-RPC error.
type rpcAnswer struct {
	result json.RawMessage
	rpcErr *jsonRPCError
}

// fakeMCP answers the three methods the client under test sends, so the http
// and sse harnesses share one definition of "a well-behaved server".
func fakeMCP(req testRPCRequest) rpcAnswer {
	switch req.Method {
	case "initialize":
		return rpcAnswer{result: json.RawMessage(
			`{"protocolVersion":"2024-11-05","capabilities":{},"serverInfo":{"name":"fake","version":"1.0.0"}}`)}
	case "tools/list":
		return rpcAnswer{result: json.RawMessage(
			`{"tools":[{"name":"echo","description":"echo the input back","inputSchema":{"type":"object"}}]}`)}
	case "tools/call":
		var params struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		_ = json.Unmarshal(req.Params, &params)
		args := string(params.Arguments)
		if args == "" {
			args = "null"
		}
		result, err := json.Marshal(map[string]any{
			"content": []map[string]any{{"type": "text", "text": params.Name + " " + args}},
		})
		if err != nil {
			panic(err)
		}
		return rpcAnswer{result: result}
	}
	return rpcAnswer{rpcErr: &jsonRPCError{Code: -32601, Message: "method not found: " + req.Method}}
}

func replyBytes(req testRPCRequest, ans rpcAnswer) ([]byte, error) {
	return json.Marshal(jsonRPCResponse{JSONRPC: "2.0", ID: req.ID, Result: ans.result, Error: ans.rpcErr})
}

// httpHarness is a fake streamable-HTTP MCP server.
type httpHarnessConfig struct {
	// sessionID is returned in the initialize response header.
	sessionID string
	// requireSession makes every request after initialize assert the session
	// header, the way a real server binds state to one session.
	requireSession bool
	// sseResponse delivers POST replies as an SSE stream instead of JSON.
	sseResponse bool
	// notifyFirst prepends unrelated frames (non-JSON and a foreign id) to an
	// SSE reply.
	notifyFirst bool
	// sseNoResult sends only unrelated frames, so no reply is ever matched.
	sseNoResult bool
	// failStatus makes every POST fail with this status.
	failStatus int
	failBody   string
	// invalidBody answers 200 with a body that is not a JSON-RPC response.
	invalidBody string
	contentType string
	// callError answers tools/call with a JSON-RPC error.
	callError *jsonRPCError
	// delay stalls the handler before answering.
	delay time.Duration
	// block stalls the handler until the channel is closed (or until the
	// caller's context is done).
	block chan struct{}
	// releaseOnDelete closes block when a DELETE arrives.
	releaseOnDelete bool
}

type httpHarness struct {
	t   *testing.T
	cfg httpHarnessConfig
	srv *httptest.Server

	mu            sync.Mutex
	requests      []testRPCRequest
	headers       []http.Header
	deleteHeaders []http.Header
	releaseOnce   sync.Once
}

func newHTTPHarness(t *testing.T, cfg httpHarnessConfig) *httpHarness {
	t.Helper()
	h := &httpHarness{t: t, cfg: cfg}
	h.srv = httptest.NewServer(h)
	t.Cleanup(h.srv.Close)
	return h
}

func (h *httpHarness) url() string { return h.srv.URL }

func (h *httpHarness) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	cfg := h.cfg
	body, _ := io.ReadAll(r.Body)

	if r.Method == http.MethodDelete {
		h.mu.Lock()
		h.deleteHeaders = append(h.deleteHeaders, r.Header.Clone())
		h.mu.Unlock()
		if cfg.releaseOnDelete && cfg.block != nil {
			h.releaseOnce.Do(func() { close(cfg.block) })
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}

	h.mu.Lock()
	h.headers = append(h.headers, r.Header.Clone())
	h.mu.Unlock()

	if r.Method != http.MethodPost {
		http.Error(w, "want POST", http.StatusMethodNotAllowed)
		return
	}

	var req testRPCRequest
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "bad request body", http.StatusBadRequest)
		return
	}
	h.mu.Lock()
	h.requests = append(h.requests, req)
	h.mu.Unlock()

	if cfg.requireSession && req.Method != "initialize" {
		if got := r.Header.Get("Mcp-Session-Id"); got != cfg.sessionID {
			h.t.Errorf("%s carried Mcp-Session-Id %q, want %q", req.Method, got, cfg.sessionID)
		}
	}
	if cfg.block != nil {
		select {
		case <-cfg.block:
		case <-r.Context().Done():
			return
		}
	}
	if cfg.delay > 0 {
		select {
		case <-time.After(cfg.delay):
		case <-r.Context().Done():
			return
		}
	}
	if cfg.failStatus != 0 {
		w.WriteHeader(cfg.failStatus)
		_, _ = io.WriteString(w, cfg.failBody)
		return
	}
	if req.Method == "initialize" && cfg.sessionID != "" {
		w.Header().Set("Mcp-Session-Id", cfg.sessionID)
	}
	if cfg.invalidBody != "" {
		ct := cfg.contentType
		if ct == "" {
			ct = "application/json"
		}
		w.Header().Set("Content-Type", ct)
		_, _ = io.WriteString(w, cfg.invalidBody)
		return
	}

	ans := fakeMCP(req)
	if req.Method == "tools/call" && cfg.callError != nil {
		ans = rpcAnswer{rpcErr: cfg.callError}
	}
	reply, err := replyBytes(req, ans)
	if err != nil {
		http.Error(w, "encode reply: "+err.Error(), http.StatusInternalServerError)
		return
	}

	if cfg.sseResponse {
		contentType := cfg.contentType
		if contentType == "" {
			contentType = "text/event-stream"
		}
		w.Header().Set("Content-Type", contentType)
		w.WriteHeader(http.StatusOK)
		if cfg.notifyFirst {
			_, _ = io.WriteString(w, "event: message\ndata: {this is not json}\n\n")
			_, _ = io.WriteString(w, "event: message\ndata: {\"jsonrpc\":\"2.0\",\"id\":99999,\"result\":{\"tools\":[]}}\n\n")
		}
		if !cfg.sseNoResult {
			_, _ = fmt.Fprintf(w, "event: message\ndata: %s\n\n", reply)
		}
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(reply)
}

func (h *httpHarness) recorded() ([]testRPCRequest, []http.Header) {
	h.mu.Lock()
	defer h.mu.Unlock()
	reqs := append([]testRPCRequest(nil), h.requests...)
	hdrs := make([]http.Header, len(h.headers))
	for i, hdr := range h.headers {
		hdrs[i] = hdr.Clone()
	}
	return reqs, hdrs
}

func (h *httpHarness) deletes() []http.Header {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]http.Header, len(h.deleteHeaders))
	for i, hdr := range h.deleteHeaders {
		out[i] = hdr.Clone()
	}
	return out
}

// requestParams returns the decoded params of the recorded request for method.
func (h *httpHarness) requestParams(t *testing.T, method string) json.RawMessage {
	t.Helper()
	reqs, _ := h.recorded()
	for _, req := range reqs {
		if req.Method == method {
			return req.Params
		}
	}
	t.Fatalf("no %s request reached the server", method)
	return nil
}

// sseHarnessConfig configures the fake legacy HTTP+SSE server.
type sseHarnessConfig struct {
	// endpointData is the payload of the endpoint event. "{url}" is replaced
	// with the server's own origin so absolute endpoints are expressible.
	endpointData string
	// endpointPath is the path the endpoint advertises (and the path the
	// harness serves).
	endpointPath string
	// endpointDelay postpones the endpoint event.
	endpointDelay time.Duration
	// noEndpoint ends the stream before advertising an endpoint.
	noEndpoint bool
	// chattyFirst sends an unnamed notification frame before the endpoint event.
	chattyFirst bool
	// closeAfterEndpoint ends the stream right after the endpoint event: the
	// rude server that dies while a request is in flight.
	closeAfterEndpoint bool
	// delay stalls POST handling.
	delay time.Duration
	// suppressReply accepts POSTs but never answers on the stream.
	suppressReply bool
	// callError answers tools/call with a JSON-RPC error.
	callError *jsonRPCError
}

type sseHarness struct {
	t    *testing.T
	cfg  sseHarnessConfig
	srv  *httptest.Server
	stop chan struct{}
	once sync.Once

	events chan string

	mu           sync.Mutex
	postPaths    []string
	postHeaders  []http.Header
	streamHeader http.Header
}

func newSSEHarness(t *testing.T, cfg sseHarnessConfig) *sseHarness {
	t.Helper()
	if cfg.endpointPath == "" {
		cfg.endpointPath = "/messages"
	}
	if cfg.endpointData == "" && !cfg.noEndpoint {
		cfg.endpointData = cfg.endpointPath + "?session=abc"
	}
	h := &sseHarness{
		t:      t,
		cfg:    cfg,
		stop:   make(chan struct{}),
		events: make(chan string, 32),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/sse", h.handleStream)
	mux.HandleFunc(cfg.endpointPath, h.handleEndpointPost)
	h.srv = httptest.NewServer(mux)
	t.Cleanup(h.Close)
	return h
}

// url is the SSE stream URL the client is configured with.
func (h *sseHarness) url() string { return h.srv.URL + "/sse" }

func (h *sseHarness) Close() {
	h.once.Do(func() {
		close(h.stop)
		h.srv.Close()
	})
}

func (h *sseHarness) handleStream(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		// A POST landing here means the client ignored the advertised endpoint.
		h.mu.Lock()
		h.postPaths = append(h.postPaths, r.URL.Path)
		h.mu.Unlock()
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "no flusher", http.StatusInternalServerError)
		return
	}

	h.mu.Lock()
	h.streamHeader = r.Header.Clone()
	h.mu.Unlock()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	if h.cfg.endpointDelay > 0 {
		select {
		case <-time.After(h.cfg.endpointDelay):
		case <-h.stop:
			return
		case <-r.Context().Done():
			return
		}
	}
	if !h.cfg.noEndpoint {
		if h.cfg.chattyFirst {
			// An unnamed frame before the endpoint must not be mistaken for it.
			_, _ = io.WriteString(w, "data: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/progress\"}\n\n")
			flusher.Flush()
		}
		data := strings.ReplaceAll(h.cfg.endpointData, "{url}", "http://"+r.Host)
		_, _ = fmt.Fprintf(w, "event: endpoint\ndata: %s\n\n", data)
		flusher.Flush()
	}
	if h.cfg.noEndpoint || h.cfg.closeAfterEndpoint {
		return
	}

	for {
		select {
		case ev := <-h.events:
			_, _ = io.WriteString(w, ev)
			flusher.Flush()
		case <-h.stop:
			return
		case <-r.Context().Done():
			return
		}
	}
}

func (h *sseHarness) handleEndpointPost(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)

	h.mu.Lock()
	h.postPaths = append(h.postPaths, r.URL.Path)
	h.postHeaders = append(h.postHeaders, r.Header.Clone())
	h.mu.Unlock()

	if h.cfg.delay > 0 {
		select {
		case <-time.After(h.cfg.delay):
		case <-r.Context().Done():
			return
		}
	}

	var req testRPCRequest
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "bad request body", http.StatusBadRequest)
		return
	}

	// The legacy transport acknowledges the POST immediately; the answer
	// arrives on the event stream.
	w.WriteHeader(http.StatusAccepted)

	if h.cfg.suppressReply {
		return
	}
	ans := fakeMCP(req)
	if req.Method == "tools/call" && h.cfg.callError != nil {
		ans = rpcAnswer{rpcErr: h.cfg.callError}
	}
	reply, err := replyBytes(req, ans)
	if err != nil {
		return
	}
	select {
	case h.events <- fmt.Sprintf("event: message\ndata: %s\n\n", reply):
	case <-h.stop:
	case <-r.Context().Done():
	}
}

func (h *sseHarness) postedPaths() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.postPaths...)
}

func (h *sseHarness) postedHeaders() []http.Header {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]http.Header, len(h.postHeaders))
	for i, hdr := range h.postHeaders {
		out[i] = hdr.Clone()
	}
	return out
}

func (h *sseHarness) streamRequestHeader() http.Header {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.streamHeader == nil {
		return http.Header{}
	}
	return h.streamHeader.Clone()
}

// --- helpers ---------------------------------------------------------------

// newTestRemoteClient builds a remote client and closes it when the test ends.
func newTestRemoteClient(t *testing.T, cfg ServerConfig) Client {
	t.Helper()
	if cfg.Timeout == 0 {
		cfg.Timeout = 5 * time.Second
	}
	client, err := newRemoteClient(cfg)
	if err != nil {
		t.Fatalf("newRemoteClient(%s): %v", cfg.Transport(), err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func assertAuthAndProtocolHeaders(t *testing.T, hdr http.Header) {
	t.Helper()
	if got := hdr.Get("Authorization"); got != "Bearer "+testRemoteToken {
		t.Errorf("Authorization = %q, want the configured bearer token", got)
	}
	if got := hdr.Get("X-Tenant"); got != testTenant {
		t.Errorf("X-Tenant = %q, want the configured custom header", got)
	}
	accept := hdr.Get("Accept")
	if !strings.Contains(accept, "application/json") || !strings.Contains(accept, "text/event-stream") {
		t.Errorf("Accept = %q, want it to offer both json and an event stream", accept)
	}
	if got := hdr.Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got)
	}
}

// --- streamable HTTP -------------------------------------------------------

func TestHTTPClientHandshakeListAndCall(t *testing.T) {
	h := newHTTPHarness(t, httpHarnessConfig{})
	client := newTestRemoteClient(t, ServerConfig{Type: TransportHTTP, URL: h.url()})

	ctx := context.Background()
	if err := client.Initialize(ctx); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	tools, err := client.ListTools(ctx)
	if err != nil {
		t.Fatalf("ListTools() error = %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "echo" {
		t.Fatalf("tools = %#v", tools)
	}

	result, err := client.CallTool(ctx, "echo", json.RawMessage(`{"text":"hi"}`))
	if err != nil {
		t.Fatalf("CallTool() error = %v", err)
	}
	if result.IsError {
		t.Fatalf("CallTool() reported IsError: %#v", result)
	}
	if len(result.Content) != 1 || !strings.Contains(result.Content[0].Text, `{"text":"hi"}`) {
		t.Fatalf("CallTool() content = %#v", result.Content)
	}

	reqs, hdrs := h.recorded()
	want := []string{"initialize", "tools/list", "tools/call"}
	if len(reqs) != len(want) {
		t.Fatalf("server saw %d requests, want %d", len(reqs), len(want))
	}
	for i, method := range want {
		if reqs[i].Method != method {
			t.Errorf("request %d = %q, want %q", i, reqs[i].Method, method)
		}
		if reqs[i].JSONRPC != "2.0" {
			t.Errorf("request %d jsonrpc = %q", i, reqs[i].JSONRPC)
		}
		if reqs[i].ID != int64(i+1) {
			t.Errorf("request %d id = %d, want %d", i, reqs[i].ID, i+1)
		}
	}
	for i, hdr := range hdrs {
		accept := hdr.Get("Accept")
		if !strings.Contains(accept, "application/json") || !strings.Contains(accept, "text/event-stream") {
			t.Errorf("request %d Accept = %q, want both json and event-stream", i, accept)
		}
		if got := hdr.Get("Content-Type"); got != "application/json" {
			t.Errorf("request %d Content-Type = %q", i, got)
		}
	}
}

func TestHTTPClientCallToolSendsEmptyArgumentsObject(t *testing.T) {
	cases := []struct {
		name string
		args json.RawMessage
	}{
		{"nil", nil},
		{"empty slice", json.RawMessage("")},
		{"explicit object", json.RawMessage(`{"a":1}`)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHTTPHarness(t, httpHarnessConfig{})
			client := newTestRemoteClient(t, ServerConfig{Type: TransportHTTP, URL: h.url()})

			if _, err := client.CallTool(context.Background(), "echo", tc.args); err != nil {
				t.Fatalf("CallTool() error = %v", err)
			}
			var params struct {
				Name      string          `json:"name"`
				Arguments json.RawMessage `json:"arguments"`
			}
			if err := json.Unmarshal(h.requestParams(t, "tools/call"), &params); err != nil {
				t.Fatalf("params = %v", err)
			}
			if params.Name != "echo" {
				t.Errorf("params.name = %q, want echo", params.Name)
			}
			want := string(tc.args)
			if want == "" {
				want = "{}"
			}
			if string(params.Arguments) != want {
				t.Fatalf("params.arguments = %q, want %q", params.Arguments, want)
			}
		})
	}
}

func TestHTTPClientJSONRPCErrorResponse(t *testing.T) {
	h := newHTTPHarness(t, httpHarnessConfig{
		callError: &jsonRPCError{Code: -32602, Message: "invalid arguments"},
	})
	client := newTestRemoteClient(t, ServerConfig{Type: TransportHTTP, URL: h.url()})

	_, err := client.CallTool(context.Background(), "echo", json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("CallTool() = nil error, want the server-reported error")
	}
	if !strings.Contains(err.Error(), "-32602") || !strings.Contains(err.Error(), "invalid arguments") {
		t.Fatalf("CallTool() error = %q, want the code and message", err)
	}
}

func TestHTTPClientReadsSSEResponseBody(t *testing.T) {
	cases := []struct {
		name    string
		cfg     httpHarnessConfig
		wantErr bool
	}{
		{name: "plain frame", cfg: httpHarnessConfig{sseResponse: true}},
		{name: "unrelated frames first", cfg: httpHarnessConfig{sseResponse: true, notifyFirst: true}},
		{
			name: "content type with parameters",
			cfg:  httpHarnessConfig{sseResponse: true, contentType: "text/event-stream; charset=utf-8"},
		},
		{name: "no matching frame", cfg: httpHarnessConfig{sseResponse: true, sseNoResult: true}, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHTTPHarness(t, tc.cfg)
			client := newTestRemoteClient(t, ServerConfig{Type: TransportHTTP, URL: h.url()})

			ctx := context.Background()
			err := client.Initialize(ctx)
			if tc.wantErr {
				// No frame ever matches: the call must fail rather than return an
				// empty result as if the server had answered.
				if err == nil {
					t.Fatal("Initialize() = nil error, want a failure when no frame matches")
				}
				return
			}
			if err != nil {
				t.Fatalf("Initialize() error = %v", err)
			}
			tools, err := client.ListTools(ctx)
			if err != nil {
				t.Fatalf("ListTools() error = %v", err)
			}
			if len(tools) != 1 || tools[0].Name != "echo" {
				t.Fatalf("tools = %#v", tools)
			}
		})
	}
}

func TestHTTPClientPropagatesSessionAndAuthHeaders(t *testing.T) {
	h := newHTTPHarness(t, httpHarnessConfig{sessionID: "sess-42", requireSession: true})
	client := newTestRemoteClient(t, ServerConfig{
		Type:    TransportHTTP,
		URL:     h.url(),
		Token:   testRemoteToken,
		Headers: map[string]string{"X-Tenant": testTenant},
	})

	ctx := context.Background()
	if err := client.Initialize(ctx); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	if _, err := client.ListTools(ctx); err != nil {
		t.Fatalf("ListTools() error = %v", err)
	}
	if _, err := client.CallTool(ctx, "echo", json.RawMessage(`{}`)); err != nil {
		t.Fatalf("CallTool() error = %v", err)
	}

	_, hdrs := h.recorded()
	if len(hdrs) != 3 {
		t.Fatalf("server saw %d requests, want 3", len(hdrs))
	}
	for _, hdr := range hdrs {
		assertAuthAndProtocolHeaders(t, hdr)
	}
	if got := hdrs[0].Get("Mcp-Session-Id"); got != "" {
		t.Errorf("initialize carried a session id %q before the server issued one", got)
	}
	// requireSession makes the server reject (via t.Errorf) any later request
	// that drops the header; assert the value the client echoed back too.
	for i, hdr := range hdrs[1:] {
		if got := hdr.Get("Mcp-Session-Id"); got != "sess-42" {
			t.Errorf("request %d carried session %q, want sess-42", i+1, got)
		}
	}

	if err := client.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	deletes := h.deletes()
	if len(deletes) != 1 {
		t.Fatalf("server saw %d DELETEs, want 1", len(deletes))
	}
	if got := deletes[0].Get("Mcp-Session-Id"); got != "sess-42" {
		t.Errorf("DELETE carried session %q, want sess-42", got)
	}
	assertAuthAndProtocolHeaders(t, deletes[0])
}

func TestHTTPClientOmitsAuthorizationWithoutToken(t *testing.T) {
	t.Setenv("MINI_OPENCODE_MCP_ABSENT_TOKEN", "")
	h := newHTTPHarness(t, httpHarnessConfig{})
	// Headers is nil and no token is configured: the request must not carry an
	// Authorization header, and nil headers must not panic.
	client := newTestRemoteClient(t, ServerConfig{
		Type:     TransportHTTP,
		URL:      h.url(),
		TokenEnv: "MINI_OPENCODE_MCP_ABSENT_TOKEN",
	})

	if err := client.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	_, hdrs := h.recorded()
	if len(hdrs) != 1 {
		t.Fatalf("server saw %d requests, want 1", len(hdrs))
	}
	if _, ok := hdrs[0]["Authorization"]; ok {
		t.Fatalf("Authorization was sent without a token: %q", hdrs[0].Get("Authorization"))
	}
}

func TestHTTPClientNon2xxStatus(t *testing.T) {
	t.Run("long body is truncated", func(t *testing.T) {
		h := newHTTPHarness(t, httpHarnessConfig{
			failStatus: http.StatusInternalServerError,
			failBody:   strings.Repeat("x", 4096),
		})
		client := newTestRemoteClient(t, ServerConfig{Type: TransportHTTP, URL: h.url()})

		err := client.Initialize(context.Background())
		if err == nil {
			t.Fatal("Initialize() = nil error, want the status code")
		}
		if !strings.Contains(err.Error(), "500") {
			t.Fatalf("error = %q, want the status code", err)
		}
		if len(err.Error()) > 1024 {
			t.Fatalf("error text is %d bytes: server bodies must be truncated before logging", len(err.Error()))
		}
		if !strings.Contains(err.Error(), "mcp http") || !strings.Contains(err.Error(), "initialize") {
			t.Fatalf("error = %q, want transport and method names", err)
		}
	})

	t.Run("empty body still names the status", func(t *testing.T) {
		h := newHTTPHarness(t, httpHarnessConfig{failStatus: http.StatusBadGateway})
		client := newTestRemoteClient(t, ServerConfig{Type: TransportHTTP, URL: h.url()})

		err := client.Initialize(context.Background())
		if err == nil || !strings.Contains(err.Error(), "502") {
			t.Fatalf("error = %v, want it to mention 502", err)
		}
	})
}

func TestHTTPClientInvalidResponseBody(t *testing.T) {
	cases := []struct {
		name string
		cfg  httpHarnessConfig
	}{
		{"json content type", httpHarnessConfig{invalidBody: "{not json"}},
		{"plain text body", httpHarnessConfig{invalidBody: "totally not json", contentType: "text/plain"}},
		{"empty json body", httpHarnessConfig{invalidBody: ""}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := tc.cfg
			if cfg.invalidBody == "" && cfg.contentType == "" {
				// An empty 200 body is not a JSON-RPC response either.
				cfg.invalidBody = " "
			}
			h := newHTTPHarness(t, cfg)
			client := newTestRemoteClient(t, ServerConfig{Type: TransportHTTP, URL: h.url()})

			if err := client.Initialize(context.Background()); err == nil {
				t.Fatal("Initialize() = nil error, want a parse failure")
			}
		})
	}
}

func TestHTTPClientCloseIsIdempotent(t *testing.T) {
	t.Run("before initialize", func(t *testing.T) {
		h := newHTTPHarness(t, httpHarnessConfig{})
		client, err := newRemoteClient(ServerConfig{Type: TransportHTTP, URL: h.url()})
		if err != nil {
			t.Fatalf("newRemoteClient() error = %v", err)
		}
		if err := client.Close(); err != nil {
			t.Errorf("first Close() error = %v", err)
		}
		if err := client.Close(); err != nil {
			t.Errorf("second Close() error = %v", err)
		}
		deletes := h.deletes()
		if len(deletes) != 2 {
			t.Fatalf("server saw %d DELETEs, want 2", len(deletes))
		}
		if got := deletes[0].Get("Mcp-Session-Id"); got != "" {
			t.Errorf("DELETE without a session carried %q", got)
		}
	})

	t.Run("repeated after initialize", func(t *testing.T) {
		h := newHTTPHarness(t, httpHarnessConfig{sessionID: "sess-close"})
		client, err := newRemoteClient(ServerConfig{Type: TransportHTTP, URL: h.url()})
		if err != nil {
			t.Fatalf("newRemoteClient() error = %v", err)
		}
		if err := client.Initialize(context.Background()); err != nil {
			t.Fatalf("Initialize() error = %v", err)
		}
		for i := 0; i < 3; i++ {
			if err := client.Close(); err != nil {
				t.Fatalf("Close() #%d error = %v", i+1, err)
			}
		}
		if n := len(h.deletes()); n != 3 {
			t.Fatalf("server saw %d DELETEs, want 3", n)
		}
	})
}

func TestHTTPClientConcurrentCallsShareSession(t *testing.T) {
	h := newHTTPHarness(t, httpHarnessConfig{sessionID: "sess-concurrent", requireSession: true})
	client := newTestRemoteClient(t, ServerConfig{Type: TransportHTTP, URL: h.url()})

	if err := client.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	const calls = 8
	errs := make([]error, calls)
	var wg sync.WaitGroup
	for i := 0; i < calls; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = client.CallTool(context.Background(), "echo", json.RawMessage(fmt.Sprintf(`{"i":%d}`, i)))
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("call %d error = %v", i, err)
		}
	}
	reqs, _ := h.recorded()
	if len(reqs) != calls+1 {
		t.Fatalf("server saw %d requests, want %d", len(reqs), calls+1)
	}
}

func TestHTTPClientContextCanceled(t *testing.T) {
	t.Run("mid request", func(t *testing.T) {
		release := make(chan struct{})
		h := newHTTPHarness(t, httpHarnessConfig{block: release})
		defer close(release)

		client := newTestRemoteClient(t, ServerConfig{Type: TransportHTTP, URL: h.url(), Timeout: 5 * time.Second})
		ctx, cancel := context.WithCancel(context.Background())
		go func() {
			time.Sleep(50 * time.Millisecond)
			cancel()
		}()

		start := time.Now()
		err := client.Initialize(ctx)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Initialize() error = %v, want context.Canceled", err)
		}
		if elapsed := time.Since(start); elapsed > 2*time.Second {
			t.Fatalf("Initialize() took %v to notice cancellation", elapsed)
		}
	})

	t.Run("already canceled", func(t *testing.T) {
		h := newHTTPHarness(t, httpHarnessConfig{})
		client := newTestRemoteClient(t, ServerConfig{Type: TransportHTTP, URL: h.url()})

		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := client.Initialize(ctx); !errors.Is(err, context.Canceled) {
			t.Fatalf("Initialize() error = %v, want context.Canceled", err)
		}
		if reqs, _ := h.recorded(); len(reqs) != 0 {
			t.Fatalf("server saw %d requests, want none", len(reqs))
		}
	})
}

func TestHTTPClientRequestTimeout(t *testing.T) {
	h := newHTTPHarness(t, httpHarnessConfig{delay: 500 * time.Millisecond})
	client := newTestRemoteClient(t, ServerConfig{Type: TransportHTTP, URL: h.url(), Timeout: 50 * time.Millisecond})

	start := time.Now()
	err := client.Initialize(context.Background())
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Initialize() error = %v, want context.DeadlineExceeded", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("Initialize() took %v, want it bounded by the 50ms request timeout", elapsed)
	}
}

// --- legacy HTTP+SSE -------------------------------------------------------

func TestSSEClientEndToEnd(t *testing.T) {
	t.Run("relative endpoint", func(t *testing.T) {
		h := newSSEHarness(t, sseHarnessConfig{})
		client := newTestRemoteClient(t, ServerConfig{
			Type:    TransportSSE,
			URL:     h.url(),
			Token:   testRemoteToken,
			Headers: map[string]string{"X-Tenant": testTenant},
		})
		runSSESession(t, client, h)
	})

	t.Run("absolute endpoint", func(t *testing.T) {
		h := newSSEHarness(t, sseHarnessConfig{endpointData: "{url}/messages?session=abc"})
		client := newTestRemoteClient(t, ServerConfig{Type: TransportSSE, URL: h.url()})
		runSSESession(t, client, h)
	})

	t.Run("chatty stream before endpoint", func(t *testing.T) {
		h := newSSEHarness(t, sseHarnessConfig{chattyFirst: true})
		client := newTestRemoteClient(t, ServerConfig{Type: TransportSSE, URL: h.url()})
		runSSESession(t, client, h)
	})
}

// runSSESession drives one full handshake + tool call and checks that every
// POST went to the endpoint the stream advertised, never to the stream URL.
func runSSESession(t *testing.T, client Client, h *sseHarness) {
	t.Helper()
	ctx := context.Background()

	if err := client.Initialize(ctx); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	tools, err := client.ListTools(ctx)
	if err != nil {
		t.Fatalf("ListTools() error = %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "echo" {
		t.Fatalf("tools = %#v", tools)
	}
	result, err := client.CallTool(ctx, "echo", json.RawMessage(`{"text":"hi"}`))
	if err != nil {
		t.Fatalf("CallTool() error = %v", err)
	}
	if len(result.Content) != 1 || !strings.Contains(result.Content[0].Text, `{"text":"hi"}`) {
		t.Fatalf("CallTool() content = %#v", result.Content)
	}

	paths := h.postedPaths()
	if len(paths) != 3 {
		t.Fatalf("endpoint saw %d POSTs (%v), want 3", len(paths), paths)
	}
	for i, path := range paths {
		if path != "/messages" {
			t.Errorf("POST %d went to %q, want the advertised endpoint /messages", i, path)
		}
	}
	if got := h.streamRequestHeader().Get("Accept"); !strings.Contains(got, "text/event-stream") {
		t.Errorf("stream GET Accept = %q, want text/event-stream", got)
	}
	if _, ok := h.streamRequestHeader()["Content-Type"]; ok {
		t.Errorf("stream GET carried Content-Type %q, want none", h.streamRequestHeader().Get("Content-Type"))
	}
}

func TestSSEClientSendsAuthHeaders(t *testing.T) {
	h := newSSEHarness(t, sseHarnessConfig{})
	client := newTestRemoteClient(t, ServerConfig{
		Type:    TransportSSE,
		URL:     h.url(),
		Token:   testRemoteToken,
		Headers: map[string]string{"X-Tenant": testTenant},
	})

	if err := client.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	hdrs := h.postedHeaders()
	if len(hdrs) != 1 {
		t.Fatalf("endpoint saw %d POSTs, want 1", len(hdrs))
	}
	assertAuthAndProtocolHeaders(t, hdrs[0])
}

func TestSSEClientOmitsAuthorizationWithoutToken(t *testing.T) {
	t.Setenv("MINI_OPENCODE_MCP_ABSENT_TOKEN", "")
	h := newSSEHarness(t, sseHarnessConfig{})
	client := newTestRemoteClient(t, ServerConfig{
		Type:     TransportSSE,
		URL:      h.url(),
		TokenEnv: "MINI_OPENCODE_MCP_ABSENT_TOKEN",
	})

	if err := client.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	for i, hdr := range h.postedHeaders() {
		if _, ok := hdr["Authorization"]; ok {
			t.Fatalf("POST %d sent Authorization %q without a token", i, hdr.Get("Authorization"))
		}
	}
	if _, ok := h.streamRequestHeader()["Authorization"]; ok {
		t.Fatal("stream GET sent Authorization without a token")
	}
}

func TestSSEClientWaitsForEndpoint(t *testing.T) {
	h := newSSEHarness(t, sseHarnessConfig{endpointDelay: 150 * time.Millisecond})
	client := newTestRemoteClient(t, ServerConfig{Type: TransportSSE, URL: h.url(), Timeout: 5 * time.Second})

	start := time.Now()
	if err := client.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize() error = %v, want the call to wait for the endpoint event", err)
	}
	if elapsed := time.Since(start); elapsed < 100*time.Millisecond {
		t.Fatalf("Initialize() returned after %v, before the endpoint event was sent", elapsed)
	}
}

func TestSSEClientStreamClosedMidRequestFailsFast(t *testing.T) {
	h := newSSEHarness(t, sseHarnessConfig{closeAfterEndpoint: true})
	client := newTestRemoteClient(t, ServerConfig{Type: TransportSSE, URL: h.url(), Timeout: 5 * time.Second})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	start := time.Now()
	err := client.Initialize(ctx)
	if err == nil {
		t.Fatal("Initialize() = nil error, want the dead stream to fail the in-flight request")
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("Initialize() took %v: a closed stream must fail the request, not hang until the ctx timeout", elapsed)
	}
	if !strings.Contains(err.Error(), "sse") || !strings.Contains(err.Error(), "initialize") {
		t.Fatalf("error = %q, want transport and method names", err)
	}
}

func TestSSEClientStreamClosedBeforeEndpoint(t *testing.T) {
	h := newSSEHarness(t, sseHarnessConfig{noEndpoint: true})
	client := newTestRemoteClient(t, ServerConfig{Type: TransportSSE, URL: h.url(), Timeout: 5 * time.Second})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	start := time.Now()
	if err := client.Initialize(ctx); err == nil {
		t.Fatal("Initialize() = nil error, want a failure when no endpoint ever arrives")
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("Initialize() took %v, want a prompt failure", elapsed)
	}
	if paths := h.postedPaths(); len(paths) != 0 {
		t.Fatalf("endpoint received POSTs %v before being advertised", paths)
	}
}

func TestSSEClientContextCanceled(t *testing.T) {
	t.Run("mid request", func(t *testing.T) {
		h := newSSEHarness(t, sseHarnessConfig{suppressReply: true})
		client := newTestRemoteClient(t, ServerConfig{Type: TransportSSE, URL: h.url(), Timeout: 5 * time.Second})

		ctx, cancel := context.WithCancel(context.Background())
		go func() {
			time.Sleep(150 * time.Millisecond)
			cancel()
		}()

		start := time.Now()
		err := client.Initialize(ctx)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Initialize() error = %v, want context.Canceled", err)
		}
		if elapsed := time.Since(start); elapsed > 2*time.Second {
			t.Fatalf("Initialize() took %v to notice cancellation", elapsed)
		}
	})

	t.Run("already canceled", func(t *testing.T) {
		h := newSSEHarness(t, sseHarnessConfig{})
		client := newTestRemoteClient(t, ServerConfig{Type: TransportSSE, URL: h.url(), Timeout: 5 * time.Second})

		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := client.Initialize(ctx); !errors.Is(err, context.Canceled) {
			t.Fatalf("Initialize() error = %v, want context.Canceled", err)
		}
		if paths := h.postedPaths(); len(paths) != 0 {
			t.Fatalf("endpoint received POSTs %v for a canceled context", paths)
		}
	})
}

func TestSSEClientRequestTimeout(t *testing.T) {
	h := newSSEHarness(t, sseHarnessConfig{delay: 500 * time.Millisecond})
	client := newTestRemoteClient(t, ServerConfig{Type: TransportSSE, URL: h.url(), Timeout: 50 * time.Millisecond})

	start := time.Now()
	err := client.Initialize(context.Background())
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Initialize() error = %v, want context.DeadlineExceeded", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("Initialize() took %v, want it bounded by the 50ms request timeout", elapsed)
	}
}

func TestSSEClientCloseUnblocksInFlightRequest(t *testing.T) {
	h := newSSEHarness(t, sseHarnessConfig{suppressReply: true})
	client, err := newRemoteClient(ServerConfig{Type: TransportSSE, URL: h.url(), Timeout: 30 * time.Second})
	if err != nil {
		t.Fatalf("newRemoteClient() error = %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- client.Initialize(context.Background()) }()

	// Give the call time to reach the response wait, then close underneath it.
	time.Sleep(100 * time.Millisecond)
	if err := client.Close(); err != nil {
		t.Errorf("Close() error = %v", err)
	}
	if err := client.Close(); err != nil {
		t.Errorf("second Close() error = %v", err)
	}

	select {
	case err := <-done:
		if err == nil {
			t.Error("Initialize() = nil error after Close, want a failure")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("in-flight Initialize() did not return after Close")
	}

	// A call started after Close must fail at once instead of waiting for a
	// stream that is gone.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	start := time.Now()
	if err := client.Initialize(ctx); err == nil {
		t.Error("Initialize() after Close = nil error, want a failure")
	} else if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("Initialize() after Close took %v, want an immediate failure", elapsed)
	}
}

func TestSSEClientConcurrentCalls(t *testing.T) {
	h := newSSEHarness(t, sseHarnessConfig{})
	client := newTestRemoteClient(t, ServerConfig{Type: TransportSSE, URL: h.url(), Timeout: 5 * time.Second})

	if err := client.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	const calls = 8
	errs := make([]error, calls)
	var wg sync.WaitGroup
	for i := 0; i < calls; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = client.CallTool(context.Background(), "echo", json.RawMessage(fmt.Sprintf(`{"i":%d}`, i)))
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("call %d error = %v", i, err)
		}
	}
	if paths := h.postedPaths(); len(paths) != calls+1 {
		t.Fatalf("endpoint saw %d POSTs, want %d", len(paths), calls+1)
	}
}

func TestSSEClientJSONRPCErrorResponse(t *testing.T) {
	h := newSSEHarness(t, sseHarnessConfig{callError: &jsonRPCError{Code: -32601, Message: "no such tool"}})
	client := newTestRemoteClient(t, ServerConfig{Type: TransportSSE, URL: h.url(), Timeout: 5 * time.Second})

	_, err := client.CallTool(context.Background(), "missing", json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("CallTool() = nil error, want the server-reported error")
	}
	if !strings.Contains(err.Error(), "-32601") || !strings.Contains(err.Error(), "no such tool") {
		t.Fatalf("CallTool() error = %q, want the code and message", err)
	}
}

// --- event stream framing --------------------------------------------------

func TestSSEReaderFraming(t *testing.T) {
	long := strings.Repeat("x", 200_000)
	cases := []struct {
		name   string
		stream string
		want   []sseEvent
	}{
		{"single data line", "data: a\n\n", []sseEvent{{data: "a"}}},
		{"data lines join with newline", "data: a\ndata: b\n\n", []sseEvent{{data: "a\nb"}}},
		{"no space after colon", "data:{\"a\":1}\n\n", []sseEvent{{data: `{"a":1}`}}},
		{"event name is kept", "event: endpoint\ndata: /messages\n\n", []sseEvent{{name: "endpoint", data: "/messages"}}},
		{"comments and unknown fields", ": keep-alive\nid: 7\nretry: 1000\ndata: y\n\n", []sseEvent{{data: "y"}}},
		{"blank frames are skipped", "\n\ndata: w\n\n", []sseEvent{{data: "w"}}},
		{"last event without a blank line", "data: z", []sseEvent{{data: "z"}}},
		{"several events", "data: 1\n\ndata: 2\n\n", []sseEvent{{data: "1"}, {data: "2"}}},
		{"empty stream", "", nil},
		{"data line longer than the scanner default", "data: " + long + "\n\n", []sseEvent{{data: long}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reader := newSSEReader(strings.NewReader(tc.stream))
			var got []sseEvent
			for reader.next() {
				got = append(got, reader.event())
			}
			if err := reader.err(); err != nil {
				t.Fatalf("err() = %v, want nil", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("events = %#v, want %#v", got, tc.want)
			}
		})
	}
}

func TestIsEventStream(t *testing.T) {
	cases := []struct {
		contentType string
		want        bool
	}{
		{"text/event-stream", true},
		{"text/event-stream; charset=utf-8", true},
		{"TEXT/EVENT-STREAM", true},
		{"application/json", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := isEventStream(tc.contentType); got != tc.want {
			t.Errorf("isEventStream(%q) = %v, want %v", tc.contentType, got, tc.want)
		}
	}
}

// --- construction boundaries ----------------------------------------------

func TestNewRemoteClientRejectsBadConfig(t *testing.T) {
	cases := []struct {
		name string
		cfg  ServerConfig
	}{
		{"http without url", ServerConfig{Type: TransportHTTP}},
		{"sse without url", ServerConfig{Type: TransportSSE, URL: "  "}},
		{"unknown transport", ServerConfig{Type: "websocket", URL: "ws://example.com"}},
		{"unparseable sse url", ServerConfig{Type: TransportSSE, URL: "http://[::1"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client, err := newRemoteClient(tc.cfg)
			if err == nil {
				_ = client.Close()
				t.Fatalf("newRemoteClient(%#v) = nil error, want failure", tc.cfg)
			}
		})
	}
}
