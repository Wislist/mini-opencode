package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
)

type Tool interface {
	Definition() ToolDefinition
	Run(ctx context.Context, input ToolInput) (ToolOutput, error)
}

type ToolDefinition struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
	Prompt      string         `json:"prompt,omitempty"`
	Behavior    ToolBehavior   `json:"behavior"`
}

type ToolSpec = ToolDefinition

type ToolBehavior struct {
	Dangerous            bool `json:"dangerous"`
	RequiresConfirmation bool `json:"requires_confirmation"`
	SupportsBackground   bool `json:"supports_background"`
	ReadOnly             bool `json:"read_only"`
}

type ToolInput struct {
	CallID    string          `json:"call_id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type ToolOutput struct {
	Content  string         `json:"content"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

type ToolRegistry struct {
	// mu guards the tool map and the name order. The registry is written once
	// during construction, but Specs() and Definition() are read from the run
	// goroutine and from the UI, so the lazy sort below has to be safe.
	mu     sync.RWMutex
	tools  map[string]Tool
	order  []string
	sorted bool
	// policy is set once before any run and never mutated afterwards.
	policy PermissionPolicy
}

func NewToolRegistry() *ToolRegistry {
	return &ToolRegistry{tools: map[string]Tool{}}
}

func (r *ToolRegistry) SetPermissionPolicy(policy PermissionPolicy) {
	r.policy = policy
}

func (r *ToolRegistry) Register(tool Tool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	spec := tool.Definition()
	if spec.Name == "" {
		return fmt.Errorf("tool name is required")
	}
	if _, ok := r.tools[spec.Name]; ok {
		return fmt.Errorf("tool already registered: %s", spec.Name)
	}
	r.tools[spec.Name] = tool
	r.order = append(r.order, spec.Name)
	r.sorted = false
	return nil
}

// sortedNames returns the registration order in name order, sorting lazily.
// Callers must hold mu.
func (r *ToolRegistry) sortedNames() []string {
	if !r.sorted {
		sort.Strings(r.order)
		r.sorted = true
	}
	return r.order
}

func (r *ToolRegistry) Specs() []ToolSpec {
	r.mu.Lock()
	defer r.mu.Unlock()
	names := r.sortedNames()
	specs := make([]ToolSpec, 0, len(names))
	for _, name := range names {
		specs = append(specs, r.tools[name].Definition())
	}
	return specs
}

// Definition returns the registered definition for a tool by name.
func (r *ToolRegistry) Definition(name string) (ToolDefinition, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	tool, ok := r.tools[name]
	if !ok {
		return ToolDefinition{}, false
	}
	return tool.Definition(), true
}

func (r *ToolRegistry) Run(ctx context.Context, call ToolCall) ToolResult {
	return r.run(ctx, call, false)
}

func (r *ToolRegistry) RunApproved(ctx context.Context, call ToolCall) ToolResult {
	return r.run(ctx, call, true)
}

func (r *ToolRegistry) run(ctx context.Context, call ToolCall, approved bool) ToolResult {
	r.mu.RLock()
	tool, ok := r.tools[call.Name]
	policy := r.policy
	r.mu.RUnlock()
	if !ok {
		return ToolResult{
			ToolCallID: call.ID,
			Name:       call.Name,
			Error:      "unknown tool",
		}
	}
	definition := tool.Definition()
	if policy != nil {
		decision := policy.Check(ctx, call, definition)
		switch decision.Action {
		case PermissionDeny:
			return ToolResult{
				ToolCallID: call.ID,
				Name:       call.Name,
				Error:      "permission denied: " + decision.Reason,
				Metadata: map[string]any{
					"permission": string(PermissionDeny),
					"reason":     decision.Reason,
				},
			}
		case PermissionConfirm:
			if approved {
				break
			}
			return ToolResult{
				ToolCallID: call.ID,
				Name:       call.Name,
				Error:      "permission confirmation required: " + decision.Reason,
				Metadata: map[string]any{
					"permission": string(PermissionConfirm),
					"reason":     decision.Reason,
				},
			}
		}
	}

	out, err := tool.Run(ctx, ToolInput{
		CallID:    call.ID,
		Name:      call.Name,
		Arguments: call.Arguments,
	})
	if err != nil {
		return ToolResult{
			ToolCallID: call.ID,
			Name:       call.Name,
			Error:      err.Error(),
		}
	}
	return ToolResult{
		ToolCallID: call.ID,
		Name:       call.Name,
		Content:    out.Content,
		Metadata:   out.Metadata,
	}
}
