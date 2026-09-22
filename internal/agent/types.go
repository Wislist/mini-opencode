package agent

import "encoding/json"

type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

type Message struct {
	Role       Role       `json:"role"`
	Content    string     `json:"content"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
}

type ToolCall struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type ToolResult struct {
	ToolCallID string         `json:"tool_call_id"`
	Name       string         `json:"name"`
	Content    string         `json:"content"`
	Metadata   map[string]any `json:"metadata,omitempty"`
	Error      string         `json:"error,omitempty"`
}

type AssistantResponse struct {
	Content   string
	ToolCalls []ToolCall
	// Usage carries provider-reported token accounting for this response. It
	// stays the zero value when the provider does not report usage.
	Usage Usage
	// Warnings carries non-fatal provider problems (a malformed stream chunk,
	// a truncated response). The runtime surfaces them as events instead of
	// dropping them silently.
	Warnings []string
}

// Usage is provider-reported token accounting for a single completion.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// IsZero reports whether no usage was reported at all.
func (u Usage) IsZero() bool {
	return u.PromptTokens == 0 && u.CompletionTokens == 0 && u.TotalTokens == 0
}

// Add accumulates other into the receiver.
func (u Usage) Add(other Usage) Usage {
	return Usage{
		PromptTokens:     u.PromptTokens + other.PromptTokens,
		CompletionTokens: u.CompletionTokens + other.CompletionTokens,
		TotalTokens:      u.TotalTokens + other.TotalTokens,
	}
}

type EventType string

const (
	EventRunStarted             EventType = "run_started"
	EventTurnStarted            EventType = "turn_started"
	EventAssistantResponse      EventType = "assistant_response"
	EventAssistantDelta         EventType = "assistant_delta"
	EventToolCallStarted        EventType = "tool_call_started"
	EventToolCallFinished       EventType = "tool_call_finished"
	EventToolCallFailed         EventType = "tool_call_failed"
	EventToolPermissionRequired EventType = "tool_permission_required"
	EventToolPermissionDenied   EventType = "tool_permission_denied"
	EventRunFinished            EventType = "run_finished"
	EventRunFailed              EventType = "run_failed"
	EventHookDenied             EventType = "hook_denied"
	EventHookStopped            EventType = "hook_stopped"
	// EventUsage reports token accounting for the turn that just finished.
	EventUsage EventType = "usage"
	// EventPlanSubmitted fires when the agent submits a plan through the
	// exit_plan_mode tool; the UI approves or rejects it.
	EventPlanSubmitted EventType = "plan_submitted"
	// EventTodosChanged fires when the agent rewrote the todo list.
	EventTodosChanged EventType = "todos_changed"
	// EventContextCompacted fires when the runtime compacted the conversation
	// automatically because it approached the context window.
	EventContextCompacted EventType = "context_compacted"
	// EventBudgetExhausted fires when the turn budget runs out; the run then
	// also emits EventRunFailed.
	EventBudgetExhausted EventType = "budget_exhausted"
	// EventProviderWarning fires for a non-fatal provider problem, such as a
	// stream chunk that could not be parsed.
	EventProviderWarning EventType = "provider_warning"
	// EventRunRetry fires when a failed turn is retried without losing the
	// transcript.
	EventRunRetry EventType = "run_retry"
	// EventTodoContinuation fires when the run pushes itself forward to the
	// next outstanding task instead of stopping.
	EventTodoContinuation EventType = "todo_continuation"
	// EventTodoBlocked fires when the agent reports a blocker through
	// todo_blocked; it ends the todo chain.
	EventTodoBlocked EventType = "todo_blocked"
	// EventTodoChainStopped fires when the stall guard ends the chain.
	EventTodoChainStopped EventType = "todo_chain_stopped"
)

type Event struct {
	Type       EventType
	Turn       int
	Message    *Message
	ToolCall   *ToolCall
	ToolResult *ToolResult
	Error      error
	Delta      string
	// Usage is set on EventUsage.
	Usage *Usage
	// Plan is set on EventPlanSubmitted.
	Plan string
	// Todos is set on EventTodosChanged.
	Todos string
	// ContextTokens is set on EventContextCompacted: the context size that
	// triggered compaction.
	ContextTokens int
	// Attempt is set on EventRunRetry: the 1-based retry number.
	Attempt int
	// Todos is also set on todo events so the UI can refresh in place.
	// BlockedReason and BlockedNeeds are set on EventTodoBlocked.
	BlockedReason string
	BlockedNeeds  string
}
