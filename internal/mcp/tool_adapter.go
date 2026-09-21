package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/wislist/mini-opencode/internal/agent"
)

type ToolAdapter struct {
	client Client
	def    ToolDef
	// server, when set, namespaces the exposed tool name as
	// "<server>__<tool>" so an MCP tool can never silently shadow a built-in
	// tool with the same name. The upstream server still sees def.Name.
	server string
}

func NewToolAdapter(client Client, def ToolDef) *ToolAdapter {
	return &ToolAdapter{client: client, def: def}
}

// NewNamespacedToolAdapter builds an adapter whose exposed name is prefixed
// with the owning server name.
func NewNamespacedToolAdapter(client Client, def ToolDef, server string) *ToolAdapter {
	return &ToolAdapter{client: client, def: def, server: server}
}

// ExposedName is the tool name the agent runtime sees.
func (t *ToolAdapter) ExposedName() string {
	if t.server == "" {
		return t.def.Name
	}
	return t.server + "__" + t.def.Name
}

func (t *ToolAdapter) Definition() agent.ToolDefinition {
	schema := map[string]any{
		"type":       "object",
		"properties": map[string]any{},
	}
	if len(t.def.InputSchema) > 0 {
		_ = json.Unmarshal(t.def.InputSchema, &schema)
	}

	description := t.def.Description
	if t.server != "" {
		description = strings.TrimSpace(fmt.Sprintf("[mcp:%s] %s", t.server, description))
	}
	return agent.ToolDefinition{
		Name:        t.ExposedName(),
		Description: description,
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
