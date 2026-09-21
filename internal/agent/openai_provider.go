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
	// Timeout is the per-request timeout. Zero means DefaultProviderTimeout.
	Timeout time.Duration
	// MaxRetries is the number of retries after a retryable failure (network
	// error or HTTP 408/409/429/5xx). Zero means DefaultProviderRetries.
	MaxRetries int
	// MaxRetriesSet lets a caller explicitly ask for zero retries.
	MaxRetriesSet bool
}

const (
	// DefaultProviderTimeout bounds a single provider request.
	DefaultProviderTimeout = 120 * time.Second
	// DefaultProviderRetries is the retry count used when unset.
	DefaultProviderRetries = 2
	// maxProviderBackoff caps the exponential backoff between retries.
	maxProviderBackoff = 8 * time.Second
)

type OpenAICompatibleProvider struct {
	config OpenAICompatibleConfig
	client *http.Client
	// sleep is indirected so tests can run without real delays.
	sleep func(ctx context.Context, d time.Duration) error
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
	if config.Timeout <= 0 {
		config.Timeout = DefaultProviderTimeout
	}
	if !config.MaxRetriesSet {
		config.MaxRetries = DefaultProviderRetries
	}
	if config.MaxRetries < 0 {
		config.MaxRetries = 0
	}
	return &OpenAICompatibleProvider{
		config: config,
		client: &http.Client{Timeout: config.Timeout},
		sleep:  sleepWithContext,
	}, nil
}

// sleepWithContext waits for d or until ctx is done, whichever comes first.
func sleepWithContext(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// doWithRetry sends the request built by build(), retrying retryable failures
// with exponential backoff. The request body is rebuilt for every attempt so a
// retry never replays a consumed reader. It only retries request setup and the
// response status: a body that dies mid-stream is reported to the caller
// instead of being silently replayed.
func (p *OpenAICompatibleProvider) doWithRetry(ctx context.Context, build func() (*http.Request, error)) (*http.Response, error) {
	var lastErr error
	for attempt := 0; attempt <= p.config.MaxRetries; attempt++ {
		if attempt > 0 {
			backoff := backoffFor(attempt)
			if err := p.sleep(ctx, backoff); err != nil {
				return nil, lastErr
			}
		}
		req, err := build()
		if err != nil {
			return nil, err
		}
		resp, err := p.client.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return nil, err
			}
			lastErr = err
			continue
		}
		if !retryableStatus(resp.StatusCode) {
			return resp, nil
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		lastErr = &HTTPStatusError{
			StatusCode: resp.StatusCode,
			Body:       strings.TrimSpace(string(body)),
			Attempts:   attempt + 1,
		}
	}
	return nil, lastErr
}

// HTTPStatusError is a non-2xx provider response after retries are exhausted.
type HTTPStatusError struct {
	StatusCode int
	Body       string
	Attempts   int
}

func (e *HTTPStatusError) Error() string {
	msg := fmt.Sprintf("provider error %d", e.StatusCode)
	if e.Body != "" {
		msg += ": " + e.Body
	}
	if e.Attempts > 1 {
		msg += fmt.Sprintf(" (after %d attempts)", e.Attempts)
	}
	return msg
}

// retryableStatus reports whether a status code is worth retrying.
func retryableStatus(code int) bool {
	switch code {
	case http.StatusRequestTimeout, http.StatusConflict, http.StatusTooManyRequests:
		return true
	}
	return code >= 500
}

// backoffFor returns the exponential backoff for a 1-based attempt number.
func backoffFor(attempt int) time.Duration {
	d := 500 * time.Millisecond
	for i := 1; i < attempt; i++ {
		d *= 2
		if d >= maxProviderBackoff {
			return maxProviderBackoff
		}
	}
	return d
}

// newRequest builds a chat-completions request for the given payload.
func (p *OpenAICompatibleProvider) newRequest(ctx context.Context, data []byte) (*http.Request, error) {
	url := strings.TrimRight(p.config.BaseURL, "/") + "/chat/completions"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+p.config.APIKey)
	httpReq.Header.Set("Content-Type", "application/json")
	return httpReq, nil
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

	resp, err := p.doWithRetry(ctx, func() (*http.Request, error) {
		return p.newRequest(ctx, data)
	})
	if err != nil {
		return AssistantResponse{}, err
	}
	defer resp.Body.Close()

	respData, err := io.ReadAll(resp.Body)
	if err != nil {
		return AssistantResponse{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return AssistantResponse{}, &HTTPStatusError{
			StatusCode: resp.StatusCode,
			Body:       strings.TrimSpace(string(respData)),
			Attempts:   1,
		}
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
		Usage:     out.Usage.agentUsage(),
	}, nil
}

// CompleteStream requests a streaming completion (SSE) and invokes onDelta for
// each content token chunk as it arrives. Tool-call argument deltas are
// accumulated and returned in the final AssistantResponse. Providers that
// report usage do so in a trailing chunk, requested through stream_options.
func (p *OpenAICompatibleProvider) CompleteStream(ctx context.Context, req Request, onDelta func(string)) (AssistantResponse, error) {
	body := openAIChatRequest{
		Model:         p.config.Model,
		Messages:      convertOpenAIMessages(req),
		Tools:         convertOpenAITools(req.Tools),
		Stream:        true,
		StreamOptions: &openAIStreamOptions{IncludeUsage: true},
	}
	data, err := json.Marshal(body)
	if err != nil {
		return AssistantResponse{}, err
	}

	resp, err := p.doWithRetry(ctx, func() (*http.Request, error) {
		httpReq, err := p.newRequest(ctx, data)
		if err != nil {
			return nil, err
		}
		httpReq.Header.Set("Accept", "text/event-stream")
		return httpReq, nil
	})
	if err != nil {
		return AssistantResponse{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respData, _ := io.ReadAll(resp.Body)
		return AssistantResponse{}, &HTTPStatusError{
			StatusCode: resp.StatusCode,
			Body:       strings.TrimSpace(string(respData)),
			Attempts:   1,
		}
	}

	var content strings.Builder
	var usage Usage
	var warnings []string
	var badChunks int
	toolCalls := map[int]*openAIToolCall{}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	sawDone := false
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "[DONE]" {
			sawDone = true
			break
		}
		var chunk openAIStreamChunk
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			// A malformed chunk used to be skipped silently, which turned a
			// broken stream into an empty answer. Record it so the caller can
			// see what happened, keeping only the first few payloads.
			badChunks++
			if len(warnings) < maxStreamWarnings {
				warnings = append(warnings, fmt.Sprintf("unparsable stream chunk: %s", truncatePayload(payload)))
			}
			continue
		}
		if !chunk.Usage.isZero() {
			usage = chunk.Usage.agentUsage()
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

	// A stream that produced nothing usable is an error, not an empty answer.
	if content.Len() == 0 && len(toolCalls) == 0 {
		switch {
		case badChunks > 0:
			return AssistantResponse{}, fmt.Errorf(
				"provider stream returned no usable content: %d unparsable chunk(s), first: %s",
				badChunks, truncatePayload(firstBadPayload(warnings)))
		case !sawDone:
			return AssistantResponse{}, fmt.Errorf("provider stream ended before any content arrived (no [DONE] marker)")
		}
	}
	if badChunks > len(warnings) {
		warnings = append(warnings, fmt.Sprintf("%d more unparsable stream chunk(s) omitted", badChunks-len(warnings)))
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
		Usage:     usage,
		Warnings:  warnings,
	}, nil
}

// maxStreamWarnings caps how many malformed chunks are reported.
const maxStreamWarnings = 3

// truncatePayload shortens a payload for an error or warning message.
func truncatePayload(payload string) string {
	payload = strings.TrimSpace(payload)
	const limit = 200
	if len(payload) <= limit {
		return payload
	}
	return payload[:limit] + "…"
}

// firstBadPayload extracts the payload text from the first recorded warning.
func firstBadPayload(warnings []string) string {
	if len(warnings) == 0 {
		return ""
	}
	const prefix = "unparsable stream chunk: "
	return strings.TrimPrefix(warnings[0], prefix)
}

type openAIChatRequest struct {
	Model         string               `json:"model"`
	Messages      []openAIMessage      `json:"messages"`
	Tools         []openAITool         `json:"tools,omitempty"`
	Stream        bool                 `json:"stream,omitempty"`
	StreamOptions *openAIStreamOptions `json:"stream_options,omitempty"`
}

// openAIStreamOptions asks the provider to report usage on a streaming request,
// which OpenAI-compatible servers otherwise omit.
type openAIStreamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

// openAIUsage is the usage payload of a response or trailing stream chunk.
type openAIUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

func (u openAIUsage) isZero() bool {
	return u.PromptTokens == 0 && u.CompletionTokens == 0 && u.TotalTokens == 0
}

func (u openAIUsage) agentUsage() Usage {
	total := u.TotalTokens
	if total == 0 {
		total = u.PromptTokens + u.CompletionTokens
	}
	return Usage{
		PromptTokens:     u.PromptTokens,
		CompletionTokens: u.CompletionTokens,
		TotalTokens:      total,
	}
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
	Content   string                 `json:"content"`
	ToolCalls []openAIStreamToolCall `json:"tool_calls,omitempty"`
}

// openAIStreamChunk is one SSE data payload for a streaming completion.
// openAIStreamChunk is one SSE data payload for a streaming completion. The
// trailing usage-only chunk has no choices.
type openAIStreamChunk struct {
	Choices []struct {
		Delta openAIStreamDelta `json:"delta"`
	} `json:"choices"`
	Usage openAIUsage `json:"usage"`
}

type openAIChatResponse struct {
	Choices []struct {
		Message openAIMessage `json:"message"`
	} `json:"choices"`
	Usage openAIUsage `json:"usage"`
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
