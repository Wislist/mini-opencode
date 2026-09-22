package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/wislist/mini-opencode/internal/agent"
	"github.com/wislist/mini-opencode/internal/memory"
)

const (
	// MemoryToolName is the tool the agent uses for durable, cross-session
	// notes. One tool with a mode switch rather than three tools keeps the
	// schema count down and makes the read/write split explicit to the model.
	MemoryToolName = "memory"
)

// MemoryTool reads and writes durable notes under
// <workDir>/.mini-opencode/memory/. Unlike the todo list, which tracks the
// current task and is cleared with the session, memory outlives the
// conversation: it is how a decision or a gotcha survives into the next one.
type MemoryTool struct {
	store        *memory.Store
	instructions string
	// defaultLimit bounds how many notes a search returns.
	defaultLimit int
}

// MemoryOptions configures the memory tool.
type MemoryOptions struct {
	// Store is the backing note store. A nil store disables the tool.
	Store *memory.Store
	// DefaultLimit is the recall size for a search; zero uses 5.
	DefaultLimit int
	// InstructionData carries the rendering data for the tool prompt.
	InstructionData InstructionData
}

// NewMemoryTool builds the memory tool. It returns nil when no store is
// configured, so callers can register it unconditionally.
func NewMemoryTool(options MemoryOptions) *MemoryTool {
	if options.Store == nil {
		return nil
	}
	if options.DefaultLimit <= 0 {
		options.DefaultLimit = 5
	}
	if options.InstructionData.MaxOutputLength == 0 {
		options.InstructionData = DefaultInstructionData()
	}
	instructions, _ := RenderToolInstructions(MemoryToolName, options.InstructionData)
	return &MemoryTool{
		store:        options.Store,
		instructions: instructions,
		defaultLimit: options.DefaultLimit,
	}
}

func (t *MemoryTool) Definition() agent.ToolDefinition {
	return agent.ToolDefinition{
		Name: MemoryToolName,
		Description: "Remember durable facts across conversations, or recall what was remembered before. " +
			"Use it for decisions, conventions, and gotchas that should outlive this session.",
		Prompt: t.instructions,
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action": map[string]any{
					"type":        "string",
					"enum":        []string{"write", "search", "read", "list", "delete"},
					"description": "write stores a note; search recalls relevant notes; read fetches one by name; list shows what exists; delete removes a note.",
				},
				"name": map[string]any{
					"type":        "string",
					"description": "Stable identifier for the note, used by read and delete. Derived from the title when omitted.",
				},
				"title": map[string]any{
					"type":        "string",
					"description": "Short human-readable title (write).",
				},
				"tags": map[string]any{
					"type":        "array",
					"description": "Labels that improve later recall (write).",
					"items":       map[string]any{"type": "string"},
				},
				"body": map[string]any{
					"type":        "string",
					"description": "The note itself, in Markdown (write).",
				},
				"query": map[string]any{
					"type":        "string",
					"description": "What to look for (search). An empty query returns the most recent notes.",
				},
				"limit": map[string]any{
					"type":        "integer",
					"description": "Maximum results to return (search).",
				},
			},
			"required": []string{"action"},
		},
		// Reading and writing notes never touches the workspace, so the tool is
		// safe in plan mode and alongside other reads.
		Behavior: agent.ToolBehavior{ReadOnly: true},
	}
}

type memoryArgs struct {
	Action string   `json:"action"`
	Name   string   `json:"name"`
	Title  string   `json:"title"`
	Tags   []string `json:"tags"`
	Body   string   `json:"body"`
	Query  string   `json:"query"`
	Limit  int      `json:"limit"`
}

func (t *MemoryTool) Run(_ context.Context, input agent.ToolInput) (agent.ToolOutput, error) {
	var args memoryArgs
	if err := json.Unmarshal(input.Arguments, &args); err != nil {
		return agent.ToolOutput{}, fmt.Errorf("memory: invalid args: %w", err)
	}
	switch strings.ToLower(strings.TrimSpace(args.Action)) {
	case "write":
		return t.runWrite(args)
	case "search":
		return t.runSearch(args)
	case "read":
		return t.runRead(args)
	case "list":
		return t.runList()
	case "delete":
		return t.runDelete(args)
	case "":
		return agent.ToolOutput{}, fmt.Errorf("memory: action is required")
	default:
		return agent.ToolOutput{}, fmt.Errorf("memory: unknown action %q", args.Action)
	}
}

func (t *MemoryTool) runWrite(args memoryArgs) (agent.ToolOutput, error) {
	note, err := t.store.Write(memory.Note{
		Name:  args.Name,
		Title: args.Title,
		Tags:  args.Tags,
		Body:  args.Body,
	})
	if err != nil {
		return agent.ToolOutput{}, fmt.Errorf("memory: %w", err)
	}
	return agent.ToolOutput{
		Content: fmt.Sprintf("remembered %q (%s)", note.Title, note.Name),
		Metadata: map[string]any{
			"memory_name": note.Name,
			// Signals the runtime that durable state changed, so a caller can
			// surface it the way todos are surfaced.
			"memory_written": true,
		},
	}, nil
}

func (t *MemoryTool) runSearch(args memoryArgs) (agent.ToolOutput, error) {
	limit := args.Limit
	if limit <= 0 {
		limit = t.defaultLimit
	}
	notes, err := t.store.Search(args.Query, limit)
	if err != nil {
		return agent.ToolOutput{}, fmt.Errorf("memory: %w", err)
	}
	if len(notes) == 0 {
		return agent.ToolOutput{Content: "no matching memories"}, nil
	}
	return agent.ToolOutput{
		Content:  renderNotes(notes),
		Metadata: map[string]any{"memory_count": len(notes)},
	}, nil
}

func (t *MemoryTool) runRead(args memoryArgs) (agent.ToolOutput, error) {
	note, err := t.store.Read(args.Name)
	if err != nil {
		return agent.ToolOutput{}, fmt.Errorf("memory: %w", err)
	}
	return agent.ToolOutput{Content: renderNotes([]memory.Note{note})}, nil
}

func (t *MemoryTool) runList() (agent.ToolOutput, error) {
	notes, err := t.store.List()
	if err != nil {
		return agent.ToolOutput{}, fmt.Errorf("memory: %w", err)
	}
	if len(notes) == 0 {
		return agent.ToolOutput{Content: "no memories stored yet"}, nil
	}
	var b strings.Builder
	for _, note := range notes {
		fmt.Fprintf(&b, "- %s: %s", note.Name, note.Title)
		if len(note.Tags) > 0 {
			fmt.Fprintf(&b, " [%s]", strings.Join(note.Tags, ", "))
		}
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "(%d notes)", len(notes))
	return agent.ToolOutput{
		Content:  b.String(),
		Metadata: map[string]any{"memory_count": len(notes)},
	}, nil
}

func (t *MemoryTool) runDelete(args memoryArgs) (agent.ToolOutput, error) {
	if err := t.store.Delete(args.Name); err != nil {
		return agent.ToolOutput{}, fmt.Errorf("memory: %w", err)
	}
	return agent.ToolOutput{Content: "forgot " + args.Name}, nil
}

// renderNotes formats notes for the model. The body is included because a
// recalled note is only useful if its content reaches the model, but the whole
// set stays bounded by the caller's limit.
func renderNotes(notes []memory.Note) string {
	var b strings.Builder
	for i, note := range notes {
		if i > 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "## %s\n", note.Title)
		if len(note.Tags) > 0 {
			fmt.Fprintf(&b, "tags: %s\n", strings.Join(note.Tags, ", "))
		}
		b.WriteString(note.Body)
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// MemoryRecallSection builds the system-prompt section describing memories
// relevant to a query. It is exported so the app assembly can inject recall
// without the agent package depending on the memory package.
func MemoryRecallSection(store *memory.Store, query string, limit int) string {
	if store == nil {
		return ""
	}
	notes, err := store.Search(query, limit)
	if err != nil || len(notes) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("<memories>\n")
	b.WriteString("Notes you saved in earlier sessions that may be relevant now. " +
		"Treat them as your own prior findings, and verify anything that may have changed.\n\n")
	for _, note := range notes {
		fmt.Fprintf(&b, "## %s\n", note.Title)
		if len(note.Tags) > 0 {
			fmt.Fprintf(&b, "tags: %s\n", strings.Join(note.Tags, ", "))
		}
		b.WriteString(note.Body)
		b.WriteString("\n\n")
	}
	b.WriteString("</memories>")
	return b.String()
}
