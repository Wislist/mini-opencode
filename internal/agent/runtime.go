package agent

import (
	"context"
	"fmt"
	"strings"
)

type Runtime struct {
	systemPrompt string
	provider     Provider
	tools        *ToolRegistry
	toolService  ToolService
	confirmer    PermissionConfirmer
	messages     []Message
	maxTurns     int
	hooks        HookChain
}

type PermissionConfirmer func(ctx context.Context, call ToolCall, result ToolResult) bool

type RuntimeOption func(*Runtime)

func NewRuntime(provider Provider, opts ...RuntimeOption) *Runtime {
	tools := NewToolRegistry()
	r := &Runtime{
		systemPrompt: defaultSystemPrompt,
		provider:     provider,
		tools:        tools,
		toolService:  NewRegistryToolService(tools),
		maxTurns:     100,
	}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

func WithSystemPrompt(prompt string) RuntimeOption {
	return func(r *Runtime) {
		if prompt != "" {
			r.systemPrompt = prompt
		}
	}
}

func WithMaxTurns(maxTurns int) RuntimeOption {
	return func(r *Runtime) {
		if maxTurns > 0 {
			r.maxTurns = maxTurns
		}
	}
}

func WithTool(tool Tool) RuntimeOption {
	return func(r *Runtime) {
		_ = r.tools.Register(tool)
	}
}

func WithToolService(service ToolService) RuntimeOption {
	return func(r *Runtime) {
		if service != nil {
			r.toolService = service
		}
	}
}

func WithPermissionPolicy(policy PermissionPolicy) RuntimeOption {
	return func(r *Runtime) {
		r.tools.SetPermissionPolicy(policy)
	}
}

func WithPermissionConfirmer(confirmer PermissionConfirmer) RuntimeOption {
	return func(r *Runtime) {
		r.confirmer = confirmer
	}
}

// SetMessages replaces the conversation history. It is used to restore a
// saved session into the runtime.
func (r *Runtime) SetMessages(messages []Message) {
	r.messages = make([]Message, len(messages))
	copy(r.messages, messages)
}

// WithHook appends a runtime lifecycle hook. Hooks fire before each tool
// call and after each turn; see the Hook interface for semantics.
func WithHook(hook Hook) RuntimeOption {
	return func(r *Runtime) {
		if hook != nil {
			r.hooks = append(r.hooks, hook)
		}
	}
}

func (r *Runtime) Messages() []Message {
	out := make([]Message, len(r.messages))
	copy(out, r.messages)
	return out
}

func (r *Runtime) Tools() []ToolView {
	return r.toolService.ListTools()
}

// ContextEstimate returns a rough token count for the full context the
// provider would see (system prompt + all messages). It uses the common
// ~4-char-per-token heuristic, which is good enough for a status display
// without pulling in a tokenizer dependency.
func (r *Runtime) ContextEstimate() int {
	total := len(r.systemPrompt)
	for _, msg := range r.Messages() {
		total += len(msg.Content)
		total += len(msg.ToolCallID)
		for _, call := range msg.ToolCalls {
			total += len(call.ID) + len(call.Name) + len(call.Arguments)
		}
	}
	return total / 4
}

func (r *Runtime) toolDefinition(name string) ToolDefinition {
	def, _ := r.tools.Definition(name)
	return def
}

// Compact asks the provider to summarize the current conversation and replaces
// the message history with a single summary message. The summaryPrompt is the
// instruction prompt (e.g. from the summary template) describing how to summarize.
// It returns the generated summary text.
func (r *Runtime) Compact(ctx context.Context, summaryPrompt string) (string, error) {
	if len(r.messages) == 0 {
		return "", nil
	}
	resp, err := r.provider.Complete(ctx, Request{
		SystemPrompt: summaryPrompt,
		Messages:     r.Messages(),
	})
	if err != nil {
		return "", err
	}
	summary := strings.TrimSpace(resp.Content)
	if summary == "" {
		return "", fmt.Errorf("provider returned empty summary")
	}
	r.messages = []Message{{
		Role: RoleUser,
		Content: "<conversation_summary>\n" + summary +
			"\n</conversation_summary>\n\nThe above summarizes our previous conversation. Continue from this context.",
	}}
	return summary, nil
}

func (r *Runtime) Run(ctx context.Context, input string, emit func(Event)) error {
	if emit == nil {
		emit = func(Event) {}
	}

	r.messages = append(r.messages, Message{Role: RoleUser, Content: input})
	emit(Event{Type: EventRunStarted})
	r.hooks.OnRunStart(ctx)

	for turn := 1; turn <= r.maxTurns; turn++ {
		emit(Event{Type: EventTurnStarted, Turn: turn})

		resp, err := r.provider.CompleteStream(ctx, Request{
			SystemPrompt: r.systemPrompt,
			Messages:     r.Messages(),
			Tools:        r.tools.Specs(),
		}, func(delta string) {
			emit(Event{Type: EventAssistantDelta, Turn: turn, Delta: delta})
		})
		if err != nil {
			emit(Event{Type: EventRunFailed, Turn: turn, Error: err})
			return err
		}

		msg := Message{
			Role:      RoleAssistant,
			Content:   resp.Content,
			ToolCalls: resp.ToolCalls,
		}
		r.messages = append(r.messages, msg)
		emit(Event{Type: EventAssistantResponse, Turn: turn, Message: &msg})

		if len(resp.ToolCalls) == 0 {
			emit(Event{Type: EventRunFinished, Turn: turn})
			return nil
		}

		for _, call := range resp.ToolCalls {
			call := call
			if decision := r.hooks.BeforeToolCall(ctx, call, r.toolDefinition(call.Name)); decision.Action != HookContinue {
				if decision.Action == HookStop {
					err := fmt.Errorf("run stopped by hook: %s", decision.Reason)
					emit(Event{Type: EventHookStopped, Turn: turn, ToolCall: &call, Error: err})
					emit(Event{Type: EventRunFailed, Turn: turn, ToolCall: &call, Error: err})
					return err
				}
				result := ToolResult{
					ToolCallID: call.ID,
					Name:       call.Name,
					Error:      "hook denied: " + decision.Reason,
					Metadata:   map[string]any{"hook": string(HookDeny)},
				}
				r.recordToolResult(&result)
				emit(Event{Type: EventHookDenied, Turn: turn, ToolCall: &call, ToolResult: &result, Error: fmt.Errorf("%s", decision.Reason)})
				continue
			}
			if err := r.runTool(ctx, turn, call, emit); err != nil {
				emit(Event{Type: EventRunFailed, Turn: turn, ToolCall: &call, Error: err})
				return err
			}
		}

		if decision := r.hooks.AfterTurn(ctx, turn, r.Messages()); decision.Action == HookStop {
			err := fmt.Errorf("run stopped by hook: %s", decision.Reason)
			emit(Event{Type: EventHookStopped, Turn: turn, Error: err})
			emit(Event{Type: EventRunFailed, Turn: turn, Error: err})
			return err
		}
	}

	err := fmt.Errorf("max turns reached: %d", r.maxTurns)
	emit(Event{Type: EventRunFailed, Turn: r.maxTurns, Error: err})
	return err
}

func (r *Runtime) runTool(ctx context.Context, turn int, call ToolCall, emit func(Event)) error {
	events, err := r.toolService.RunTool(ctx, ToolRunRequest{Call: call})
	if err != nil {
		return err
	}
	for toolEvent := range events {
		switch toolEvent.Type {
		case ToolEventStarted:
			emit(Event{Type: EventToolCallStarted, Turn: turn, ToolCall: &call})
		case ToolEventFinished:
			r.recordToolResult(toolEvent.Result)
			emit(Event{Type: EventToolCallFinished, Turn: turn, ToolCall: &call, ToolResult: toolEvent.Result})
		case ToolEventFailed:
			r.recordToolResult(toolEvent.Result)
			emit(Event{Type: EventToolCallFailed, Turn: turn, ToolCall: &call, ToolResult: toolEvent.Result, Error: toolEvent.Error})
		case ToolEventPermissionRequired:
			emit(Event{Type: EventToolPermissionRequired, Turn: turn, ToolCall: &call, ToolResult: toolEvent.Result, Error: toolEvent.Error})
			if toolEvent.Result == nil || r.confirmer == nil || !r.confirmer(ctx, call, *toolEvent.Result) {
				r.recordToolResult(toolEvent.Result)
				return nil
			}
			return r.runApprovedTool(ctx, turn, call, emit)
		case ToolEventPermissionDenied:
			r.recordToolResult(toolEvent.Result)
			emit(Event{Type: EventToolPermissionDenied, Turn: turn, ToolCall: &call, ToolResult: toolEvent.Result, Error: toolEvent.Error})
		}
	}
	return nil
}

func (r *Runtime) runApprovedTool(ctx context.Context, turn int, call ToolCall, emit func(Event)) error {
	events, err := r.toolService.RunTool(ctx, ToolRunRequest{Call: call, Approved: true})
	if err != nil {
		return err
	}
	for toolEvent := range events {
		switch toolEvent.Type {
		case ToolEventStarted:
			emit(Event{Type: EventToolCallStarted, Turn: turn, ToolCall: &call})
		case ToolEventFinished:
			r.recordToolResult(toolEvent.Result)
			emit(Event{Type: EventToolCallFinished, Turn: turn, ToolCall: &call, ToolResult: toolEvent.Result})
		case ToolEventFailed:
			r.recordToolResult(toolEvent.Result)
			emit(Event{Type: EventToolCallFailed, Turn: turn, ToolCall: &call, ToolResult: toolEvent.Result, Error: toolEvent.Error})
		case ToolEventPermissionDenied:
			r.recordToolResult(toolEvent.Result)
			emit(Event{Type: EventToolPermissionDenied, Turn: turn, ToolCall: &call, ToolResult: toolEvent.Result, Error: toolEvent.Error})
		case ToolEventPermissionRequired:
			r.recordToolResult(toolEvent.Result)
			emit(Event{Type: EventToolPermissionRequired, Turn: turn, ToolCall: &call, ToolResult: toolEvent.Result, Error: toolEvent.Error})
		}
	}
	return nil
}

func (r *Runtime) recordToolResult(result *ToolResult) {
	if result == nil {
		return
	}
	r.messages = append(r.messages, Message{
		Role:       RoleTool,
		ToolCallID: result.ToolCallID,
		Content:    resultMessageContent(*result),
	})
}

func resultMessageContent(result ToolResult) string {
	if result.Error != "" {
		return `{"error": "` + result.Error + `"}`
	}
	if strings.TrimSpace(result.Content) == "" {
		return "(no output)"
	}
	return result.Content
}

const defaultSystemPrompt = `You are mini-opencode, a local coding agent terminal.
Work step by step. Use tools when needed. Keep final answers concise.`
