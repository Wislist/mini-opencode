package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
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
