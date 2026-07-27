package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

type OpenAICompatibleConfig struct {
	BaseURL string
	APIKey  string
	Model   string
}

type OpenAICompatibleProvider struct {
	config OpenAICompatibleConfig
	client *http.Client
}

func NewOpenAICompatibleProvider(config OpenAICompatibleConfig) (*OpenAICompatibleProvider, error) {
	if config.BaseURL == "" {
		return nil, fmt.Errorf("base_url is required")
	}
	if config.APIKey == "" {
		return nil, fmt.Errorf("api key is required")
	}
	if config.Model == "" {
		return nil, fmt.Errorf("model is required")
	}
	return &OpenAICompatibleProvider{
		config: config,
		client: &http.Client{Timeout: 120 * time.Second},
	}, nil
}

func (p *OpenAICompatibleProvider) Complete(ctx context.Context, req Request) (AssistantResponse, error) {
	body := openAIChatRequest{
		Model:    p.config.Model,
		Messages: convertOpenAIMessages(req),
		Tools:    convertOpenAITools(req.Tools),
	}
	data, err := json.Marshal(body)
	if err != nil {
		return AssistantResponse{}, err
	}

	url := strings.TrimRight(p.config.BaseURL, "/") + "/chat/completions"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return AssistantResponse{}, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+p.config.APIKey)
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return AssistantResponse{}, err
	}
	defer resp.Body.Close()

	respData, err := io.ReadAll(resp.Body)
	if err != nil {
		return AssistantResponse{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return AssistantResponse{}, fmt.Errorf("provider error %d: %s", resp.StatusCode, strings.TrimSpace(string(respData)))
	}

	var out openAIChatResponse
	if err := json.Unmarshal(respData, &out); err != nil {
		return AssistantResponse{}, err
	}
	if len(out.Choices) == 0 {
		return AssistantResponse{}, fmt.Errorf("provider returned no choices")
	}
	msg := out.Choices[0].Message
	return AssistantResponse{
		Content:   msg.Content,
		ToolCalls: convertAgentToolCalls(msg.ToolCalls),
	}, nil
}

// CompleteStream requests a streaming completion (SSE) and invokes onDelta for
// each content token chunk as it arrives. Tool-call argument deltas are
// accumulated and returned in the final AssistantResponse.
func (p *OpenAICompatibleProvider) CompleteStream(ctx context.Context, req Request, onDelta func(string)) (AssistantResponse, error) {
	body := openAIChatRequest{
		Model:    p.config.Model,
		Messages: convertOpenAIMessages(req),
		Tools:    convertOpenAITools(req.Tools),
		Stream:   true,
	}
	data, err := json.Marshal(body)
	if err != nil {
		return AssistantResponse{}, err
	}

	url := strings.TrimRight(p.config.BaseURL, "/") + "/chat/completions"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return AssistantResponse{}, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+p.config.APIKey)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return AssistantResponse{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respData, _ := io.ReadAll(resp.Body)
		return AssistantResponse{}, fmt.Errorf("provider error %d: %s", resp.StatusCode, strings.TrimSpace(string(respData)))
	}

	var content strings.Builder
	toolCalls := map[int]*openAIToolCall{}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "[DONE]" {
			break
		}
		var chunk openAIStreamChunk
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			continue
		}
		if len(chunk.Choices) == 0 {
			continue
		}
		delta := chunk.Choices[0].Delta
		if delta.Content != "" {
			content.WriteString(delta.Content)
			if onDelta != nil {
				onDelta(delta.Content)
			}
		}
		for _, tc := range delta.ToolCalls {
			existing, ok := toolCalls[tc.Index]
			if !ok {
				toolCalls[tc.Index] = &openAIToolCall{
					ID:       tc.ID,
					Type:     tc.Type,
					Function: tc.Function,
				}
				continue
			}
			existing.Function.Name += tc.Function.Name
			existing.Function.Arguments += tc.Function.Arguments
			if tc.ID != "" {
				existing.ID = tc.ID
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return AssistantResponse{}, err
	}

	// Stable, ordered list of accumulated tool calls.
	indices := make([]int, 0, len(toolCalls))
	for i := range toolCalls {
		indices = append(indices, i)
	}
	sort.Ints(indices)
	calls := make([]openAIToolCall, 0, len(indices))
	for _, i := range indices {
		calls = append(calls, *toolCalls[i])
	}

	return AssistantResponse{
		Content:   content.String(),
		ToolCalls: convertAgentToolCalls(calls),
	}, nil
}

type openAIChatRequest struct {
	Model    string          `json:"model"`
	Messages []openAIMessage `json:"messages"`
	Tools    []openAITool    `json:"tools,omitempty"`
	Stream   bool            `json:"stream,omitempty"`
}

type openAIMessage struct {
	Role       string           `json:"role"`
	Content    string           `json:"content,omitempty"`
	ToolCallID string           `json:"tool_call_id,omitempty"`
	ToolCalls  []openAIToolCall `json:"tool_calls,omitempty"`
}

type openAITool struct {
	Type     string             `json:"type"`
	Function openAIToolFunction `json:"function"`
}

type openAIToolFunction struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

type openAIToolCall struct {
	ID       string             `json:"id"`
	Type     string             `json:"type"`
	Function openAICallFunction `json:"function"`
}

type openAICallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// openAIStreamToolCall carries the index used to reassemble tool-call deltas
// across SSE chunks, plus the partial id/name/arguments.
type openAIStreamToolCall struct {
	Index    int                `json:"index"`
	ID       string             `json:"id"`
	Type     string             `json:"type"`
	Function openAICallFunction `json:"function"`
}

// openAIStreamDelta is the delta payload inside a streaming choice.
type openAIStreamDelta struct {
	Content    string                `json:"content"`
	ToolCalls  []openAIStreamToolCall `json:"tool_calls,omitempty"`
}

// openAIStreamChunk is one SSE data payload for a streaming completion.
type openAIStreamChunk struct {
	Choices []struct {
		Delta openAIStreamDelta `json:"delta"`
	} `json:"choices"`
}

type openAIChatResponse struct {
	Choices []struct {
		Message openAIMessage `json:"message"`
	} `json:"choices"`
}

func convertOpenAIMessages(req Request) []openAIMessage {
	messages := make([]openAIMessage, 0, len(req.Messages)+1)
	if req.SystemPrompt != "" {
		messages = append(messages, openAIMessage{Role: string(RoleSystem), Content: req.SystemPrompt})
	}
	for _, msg := range req.Messages {
		content := msg.Content
		// Tool messages must always carry non-empty content; providers
		// reject a tool message whose content field is missing. Fall back
		// to a placeholder when a tool returned no output (e.g. an empty
		// glob result) so the request stays well-formed.
		if msg.Role == RoleTool && strings.TrimSpace(content) == "" {
			content = "(no output)"
		}
		messages = append(messages, openAIMessage{
			Role:       string(msg.Role),
			Content:    content,
			ToolCallID: msg.ToolCallID,
			ToolCalls:  convertOpenAIToolCalls(msg.ToolCalls),
		})
	}
	return messages
}

func convertOpenAITools(specs []ToolSpec) []openAITool {
	tools := make([]openAITool, 0, len(specs))
	for _, spec := range specs {
		description := spec.Description
		if spec.Prompt != "" {
			description = strings.TrimSpace(description + "\n\n" + spec.Prompt)
		}
		tools = append(tools, openAITool{
			Type: "function",
			Function: openAIToolFunction{
				Name:        spec.Name,
				Description: description,
				Parameters:  spec.InputSchema,
			},
		})
	}
	return tools
}

func convertOpenAIToolCalls(calls []ToolCall) []openAIToolCall {
	out := make([]openAIToolCall, 0, len(calls))
	for _, call := range calls {
		args := string(call.Arguments)
		if args == "" {
			args = "{}"
		}
		out = append(out, openAIToolCall{
			ID:   call.ID,
			Type: "function",
			Function: openAICallFunction{
				Name:      call.Name,
				Arguments: args,
			},
		})
	}
	return out
}

func convertAgentToolCalls(calls []openAIToolCall) []ToolCall {
	out := make([]ToolCall, 0, len(calls))
	for _, call := range calls {
		args := call.Function.Arguments
		if args == "" {
			args = "{}"
		}
		out = append(out, ToolCall{
			ID:        call.ID,
			Name:      call.Function.Name,
			Arguments: json.RawMessage(args),
		})
	}
	return out
}
