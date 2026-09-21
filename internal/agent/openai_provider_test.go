package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestOpenAICompatibleProviderSendsMessagesAndTools(t *testing.T) {
	var got openAIChatRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Fatalf("authorization = %q", r.Header.Get("Authorization"))
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(`{
          "choices": [{
            "message": {
              "role": "assistant",
              "content": "",
              "tool_calls": [{
                "id": "call-1",
                "type": "function",
                "function": {"name": "read", "arguments": "{\"path\":\"main.go\"}"}
              }]
            }
          }]
        }`))
	}))
	defer server.Close()

	provider, err := NewOpenAICompatibleProvider(OpenAICompatibleConfig{
		BaseURL: server.URL,
		APIKey:  "test-key",
		Model:   "test-model",
	})
	if err != nil {
		t.Fatalf("NewOpenAICompatibleProvider() error = %v", err)
	}

	resp, err := provider.Complete(context.Background(), Request{
		SystemPrompt: "system rules",
		Messages:     []Message{{Role: RoleUser, Content: "read file"}},
		Tools: []ToolSpec{{
			Name:        "read",
			Description: "Read file",
			InputSchema: map[string]any{"type": "object"},
		}},
	})
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}

	if got.Model != "test-model" {
		t.Fatalf("model = %q", got.Model)
	}
	if len(got.Messages) != 2 || got.Messages[0].Role != "system" || got.Messages[1].Role != "user" {
		t.Fatalf("messages = %#v", got.Messages)
	}
	if len(got.Tools) != 1 || got.Tools[0].Function.Name != "read" {
		t.Fatalf("tools = %#v", got.Tools)
	}
	if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].Name != "read" {
		t.Fatalf("tool calls = %#v", resp.ToolCalls)
	}
}

func TestConvertOpenAIMessagesToolContentNeverEmpty(t *testing.T) {
	msgs := convertOpenAIMessages(Request{
		Messages: []Message{
			{Role: RoleAssistant, Content: "", ToolCalls: []ToolCall{{ID: "c1", Name: "glob", Arguments: json.RawMessage(`{}`)}}},
			{Role: RoleTool, ToolCallID: "c1", Content: ""},
			{Role: RoleTool, ToolCallID: "c2", Content: "   "},
			{Role: RoleTool, ToolCallID: "c3", Content: "real output"},
		},
	})
	// index 0 is the assistant message; tool messages follow
	if msgs[0].Role != "assistant" {
		t.Fatalf("msgs[0] role = %q", msgs[0].Role)
	}
	if msgs[1].Role != "tool" || msgs[1].Content == "" {
		t.Fatal("empty tool content was omitted; provider would reject missing content")
	}
	if msgs[2].Content == "" {
		t.Fatal("whitespace-only tool content was omitted")
	}
	if msgs[3].Content != "real output" {
		t.Fatalf("non-empty tool content changed: %q", msgs[3].Content)
	}
}

func TestOpenAICompatibleProviderCompleteStreamAccumulates(t *testing.T) {
	// A minimal SSE server that emits content deltas and a tool-call delta
	// spread across chunks, then [DONE].
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("response writer cannot flush")
		}
		chunks := []string{
			`data: {"choices":[{"delta":{"content":"Hello"}}]}` + "\n\n",
			`data: {"choices":[{"delta":{"content":", world"}}]}` + "\n\n",
			`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call-1","type":"function","function":{"name":"read"}}]}}]}` + "\n\n",
			`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"path\":\"a.go\"}"}}]}}]}` + "\n\n",
			`data: [DONE]` + "\n\n",
		}
		for _, c := range chunks {
			_, _ = w.Write([]byte(c))
			flusher.Flush()
		}
	}))
	defer server.Close()

	provider, err := NewOpenAICompatibleProvider(OpenAICompatibleConfig{
		BaseURL: server.URL,
		APIKey:  "test-key",
		Model:   "test-model",
	})
	if err != nil {
		t.Fatalf("NewOpenAICompatibleProvider() error = %v", err)
	}

	var deltas []string
	resp, err := provider.CompleteStream(context.Background(), Request{
		Messages: []Message{{Role: RoleUser, Content: "hi"}},
	}, func(delta string) { deltas = append(deltas, delta) })
	if err != nil {
		t.Fatalf("CompleteStream() error = %v", err)
	}

	// Content deltas arrive token-by-token.
	if len(deltas) != 2 || deltas[0] != "Hello" || deltas[1] != ", world" {
		t.Fatalf("deltas = %#v", deltas)
	}
	if resp.Content != "Hello, world" {
		t.Fatalf("accumulated content = %q", resp.Content)
	}
	// Tool call reassembled across chunks.
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("tool calls = %d, want 1", len(resp.ToolCalls))
	}
	tc := resp.ToolCalls[0]
	if tc.ID != "call-1" || tc.Name != "read" {
		t.Fatalf("tool call = %+v", tc)
	}
	if string(tc.Arguments) != `{"path":"a.go"}` {
		t.Fatalf("tool call arguments = %s", tc.Arguments)
	}
}

func TestOpenAICompatibleProviderParsesUsage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{
          "choices": [{"message": {"role": "assistant", "content": "hi"}}],
          "usage": {"prompt_tokens": 11, "completion_tokens": 7, "total_tokens": 18}
        }`))
	}))
	defer server.Close()

	provider, err := NewOpenAICompatibleProvider(OpenAICompatibleConfig{
		BaseURL: server.URL, APIKey: "test-key", Model: "test-model",
	})
	if err != nil {
		t.Fatalf("NewOpenAICompatibleProvider() error = %v", err)
	}
	resp, err := provider.Complete(context.Background(), Request{
		Messages: []Message{{Role: RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if resp.Usage.PromptTokens != 11 || resp.Usage.CompletionTokens != 7 || resp.Usage.TotalTokens != 18 {
		t.Fatalf("usage = %+v", resp.Usage)
	}
}

func TestOpenAICompatibleProviderStreamParsesTrailingUsage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		for _, c := range []string{
			`data: {"choices":[{"delta":{"content":"hi"}}]}` + "\n\n",
			`data: {"choices":[],"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}}` + "\n\n",
			`data: [DONE]` + "\n\n",
		} {
			_, _ = w.Write([]byte(c))
			flusher.Flush()
		}
	}))
	defer server.Close()

	provider, err := NewOpenAICompatibleProvider(OpenAICompatibleConfig{
		BaseURL: server.URL, APIKey: "test-key", Model: "test-model",
	})
	if err != nil {
		t.Fatalf("NewOpenAICompatibleProvider() error = %v", err)
	}
	resp, err := provider.CompleteStream(context.Background(), Request{
		Messages: []Message{{Role: RoleUser, Content: "hi"}},
	}, nil)
	if err != nil {
		t.Fatalf("CompleteStream() error = %v", err)
	}
	if resp.Content != "hi" {
		t.Fatalf("content = %q", resp.Content)
	}
	if resp.Usage.TotalTokens != 5 || resp.Usage.PromptTokens != 3 {
		t.Fatalf("usage = %+v", resp.Usage)
	}
}

// A retryable status is retried, and a later success is returned transparently.
func TestOpenAICompatibleProviderRetriesRetryableStatus(t *testing.T) {
	var attempts int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":"slow down"}`))
			return
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`))
	}))
	defer server.Close()

	provider, err := NewOpenAICompatibleProvider(OpenAICompatibleConfig{
		BaseURL: server.URL, APIKey: "test-key", Model: "test-model", MaxRetries: 2,
	})
	if err != nil {
		t.Fatalf("NewOpenAICompatibleProvider() error = %v", err)
	}
	// Skip real backoff sleeping.
	provider.sleep = func(ctx context.Context, d time.Duration) error { return nil }

	resp, err := provider.Complete(context.Background(), Request{
		Messages: []Message{{Role: RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if resp.Content != "ok" {
		t.Fatalf("content = %q", resp.Content)
	}
	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2", attempts)
	}
}

// A non-retryable status fails immediately without burning retries.
func TestOpenAICompatibleProviderDoesNotRetryClientError(t *testing.T) {
	var attempts int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"bad request"}`))
	}))
	defer server.Close()

	provider, err := NewOpenAICompatibleProvider(OpenAICompatibleConfig{
		BaseURL: server.URL, APIKey: "test-key", Model: "test-model", MaxRetries: 3,
	})
	if err != nil {
		t.Fatalf("NewOpenAICompatibleProvider() error = %v", err)
	}
	provider.sleep = func(ctx context.Context, d time.Duration) error { return nil }

	if _, err := provider.Complete(context.Background(), Request{
		Messages: []Message{{Role: RoleUser, Content: "hi"}},
	}); err == nil {
		t.Fatal("Complete() error = nil, want failure")
	}
	if attempts != 1 {
		t.Fatalf("attempts = %d, want 1", attempts)
	}
}

// An explicit zero retry count disables retries even for retryable statuses.
func TestOpenAICompatibleProviderZeroRetriesIsHonored(t *testing.T) {
	var attempts int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	provider, err := NewOpenAICompatibleProvider(OpenAICompatibleConfig{
		BaseURL: server.URL, APIKey: "test-key", Model: "test-model",
		MaxRetries: 0, MaxRetriesSet: true,
	})
	if err != nil {
		t.Fatalf("NewOpenAICompatibleProvider() error = %v", err)
	}
	provider.sleep = func(ctx context.Context, d time.Duration) error { return nil }

	if _, err := provider.Complete(context.Background(), Request{
		Messages: []Message{{Role: RoleUser, Content: "hi"}},
	}); err == nil {
		t.Fatal("Complete() error = nil, want failure")
	}
	if attempts != 1 {
		t.Fatalf("attempts = %d, want 1", attempts)
	}
}

// A malformed chunk used to be skipped in silence. It must surface as a
// warning when the stream still produced content.
func TestOpenAICompatibleProviderReportsUnparsableChunks(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		for _, c := range []string{
			`data: {"choices":[{"delta":{"content":"ok"}}]}` + "\n\n",
			`data: {this is not json}` + "\n\n",
			`data: [DONE]` + "\n\n",
		} {
			_, _ = w.Write([]byte(c))
			flusher.Flush()
		}
	}))
	defer server.Close()

	provider, err := NewOpenAICompatibleProvider(OpenAICompatibleConfig{
		BaseURL: server.URL, APIKey: "test-key", Model: "test-model",
	})
	if err != nil {
		t.Fatalf("NewOpenAICompatibleProvider() error = %v", err)
	}
	resp, err := provider.CompleteStream(context.Background(), Request{
		Messages: []Message{{Role: RoleUser, Content: "hi"}},
	}, nil)
	if err != nil {
		t.Fatalf("CompleteStream() error = %v", err)
	}
	if resp.Content != "ok" {
		t.Fatalf("content = %q", resp.Content)
	}
	if len(resp.Warnings) != 1 || !strings.Contains(resp.Warnings[0], "unparsable stream chunk") {
		t.Fatalf("warnings = %#v", resp.Warnings)
	}
	if !strings.Contains(resp.Warnings[0], "{this is not json}") {
		t.Fatalf("warning does not quote the payload: %q", resp.Warnings[0])
	}
}

// A stream that yields nothing usable is an error, not a silent empty answer.
func TestOpenAICompatibleProviderFailsOnFullyUnparsableStream(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		_, _ = w.Write([]byte("data: garbage\n\n"))
		flusher.Flush()
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
		flusher.Flush()
	}))
	defer server.Close()

	provider, err := NewOpenAICompatibleProvider(OpenAICompatibleConfig{
		BaseURL: server.URL, APIKey: "test-key", Model: "test-model",
	})
	if err != nil {
		t.Fatalf("NewOpenAICompatibleProvider() error = %v", err)
	}
	_, err = provider.CompleteStream(context.Background(), Request{
		Messages: []Message{{Role: RoleUser, Content: "hi"}},
	}, nil)
	if err == nil {
		t.Fatal("CompleteStream() error = nil, want a visible failure")
	}
	if !strings.Contains(err.Error(), "unparsable chunk") {
		t.Fatalf("error = %v, want it to mention the unparsable chunk", err)
	}
}

// A stream that ends without content and without [DONE] is reported instead of
// being returned as an empty response.
func TestOpenAICompatibleProviderFailsOnEmptyStream(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.(http.Flusher).Flush()
	}))
	defer server.Close()

	provider, err := NewOpenAICompatibleProvider(OpenAICompatibleConfig{
		BaseURL: server.URL, APIKey: "test-key", Model: "test-model",
	})
	if err != nil {
		t.Fatalf("NewOpenAICompatibleProvider() error = %v", err)
	}
	_, err = provider.CompleteStream(context.Background(), Request{
		Messages: []Message{{Role: RoleUser, Content: "hi"}},
	}, nil)
	if err == nil {
		t.Fatal("CompleteStream() error = nil, want an empty-stream error")
	}
	if !strings.Contains(err.Error(), "no [DONE]") {
		t.Fatalf("error = %v, want it to mention the missing terminator", err)
	}
}
