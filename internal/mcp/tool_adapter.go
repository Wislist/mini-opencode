package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/wislist/mini-opencode/internal/agent"
)

type ToolAdapter struct {
	client Client
	def    ToolDef
}

func NewToolAdapter(client Client, def ToolDef) *ToolAdapter {
	return &ToolAdapter{client: client, def: def}
}

func (t *ToolAdapter) Definition() agent.ToolDefinition {
	schema := map[string]any{
		"type":       "object",
		"properties": map[string]any{},
	}
	if len(t.def.InputSchema) > 0 {
		_ = json.Unmarshal(t.def.InputSchema, &schema)
	}

	return agent.ToolDefinition{
		Name:        t.def.Name,
		Description: t.def.Description,
		InputSchema: schema,
		Behavior: agent.ToolBehavior{
			RequiresConfirmation: true,
		},
	}
}

func (t *ToolAdapter) Run(ctx context.Context, input agent.ToolInput) (agent.ToolOutput, error) {
	result, err := t.client.CallTool(ctx, t.def.Name, input.Arguments)
	if err != nil {
		return agent.ToolOutput{}, err
	}

	var parts []string
	for _, item := range result.Content {
		if item.Type == "text" {
			parts = append(parts, item.Text)
		}
	}
	output := strings.Join(parts, "\n")
	if result.IsError {
		if output == "" {
			output = "mcp tool returned an error"
		}
		return agent.ToolOutput{}, errors.New(output)
	}
	return agent.ToolOutput{Content: output}, nil
}
