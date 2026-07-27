package agent

import "context"

type ToolService interface {
	ListTools() []ToolView
	RunTool(ctx context.Context, req ToolRunRequest) (<-chan ToolEvent, error)
}

type ToolView struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
	Behavior    ToolBehavior   `json:"behavior"`
}

type ToolRunRequest struct {
	Call     ToolCall `json:"call"`
	Approved bool     `json:"approved"`
}

type ToolEventType string

const (
	ToolEventStarted            ToolEventType = "tool_started"
	ToolEventFinished           ToolEventType = "tool_finished"
	ToolEventFailed             ToolEventType = "tool_failed"
	ToolEventPermissionRequired ToolEventType = "tool_permission_required"
	ToolEventPermissionDenied   ToolEventType = "tool_permission_denied"
)

type ToolEvent struct {
	Type   ToolEventType `json:"type"`
	Call   ToolCall      `json:"call"`
	Result *ToolResult   `json:"result,omitempty"`
	Error  error         `json:"-"`
}

type RegistryToolService struct {
	registry *ToolRegistry
}

func NewRegistryToolService(registry *ToolRegistry) *RegistryToolService {
	if registry == nil {
		registry = NewToolRegistry()
	}
	return &RegistryToolService{registry: registry}
}

func (s *RegistryToolService) ListTools() []ToolView {
	specs := s.registry.Specs()
	views := make([]ToolView, 0, len(specs))
	for _, spec := range specs {
		views = append(views, ToolView{
			Name:        spec.Name,
			Description: spec.Description,
			InputSchema: spec.InputSchema,
			Behavior:    spec.Behavior,
		})
	}
	return views
}

func (s *RegistryToolService) RunTool(ctx context.Context, req ToolRunRequest) (<-chan ToolEvent, error) {
	ch := make(chan ToolEvent, 4)
	go func() {
		defer close(ch)
		ch <- ToolEvent{Type: ToolEventStarted, Call: req.Call}

		var result ToolResult
		if req.Approved {
			result = s.registry.RunApproved(ctx, req.Call)
		} else {
			result = s.registry.Run(ctx, req.Call)
		}
		eventType := classifyToolResult(result)
		event := ToolEvent{Type: eventType, Call: req.Call, Result: &result}
		if result.Error != "" {
			event.Error = toolResultError(result.Error)
		}
		ch <- event
	}()
	return ch, nil
}

func classifyToolResult(result ToolResult) ToolEventType {
	if result.Metadata != nil {
		switch result.Metadata["permission"] {
		case string(PermissionConfirm):
			return ToolEventPermissionRequired
		case string(PermissionDeny):
			return ToolEventPermissionDenied
		}
	}
	if result.Error != "" {
		return ToolEventFailed
	}
	return ToolEventFinished
}

type toolResultError string

func (e toolResultError) Error() string { return string(e) }
