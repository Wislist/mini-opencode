package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/wislist/mini-opencode/internal/agent"
	"github.com/wislist/mini-opencode/internal/memory"
)

func newTestMemoryTool(t *testing.T) (*MemoryTool, *memory.Store) {
	t.Helper()
	store := memory.NewStore(t.TempDir())
	tool := NewMemoryTool(MemoryOptions{Store: store})
	if tool == nil {
		t.Fatal("NewMemoryTool() returned nil for a non-nil store")
	}
	return tool, store
}

func runMemory(t *testing.T, tool *MemoryTool, args map[string]any) agent.ToolOutput {
	t.Helper()
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	out, err := tool.Run(context.Background(), agent.ToolInput{Arguments: raw})
	if err != nil {
		t.Fatalf("Run(%v) error = %v", args, err)
	}
	return out
}

// TestMemoryToolNilStore verifies the tool can be registered unconditionally.
func TestMemoryToolNilStore(t *testing.T) {
	if tool := NewMemoryTool(MemoryOptions{}); tool != nil {
		t.Fatal("expected nil tool when no store is configured")
	}
}

func TestMemoryToolDefinitionIsReadOnly(t *testing.T) {
	tool, _ := newTestMemoryTool(t)
	def := tool.Definition()
	if def.Name != MemoryToolName {
		t.Fatalf("name = %q", def.Name)
	}
	// Memory must stay available in plan mode and never prompt for permission.
	if !def.Behavior.ReadOnly {
		t.Fatal("memory tool must be read-only")
	}
	if def.Behavior.Dangerous || def.Behavior.RequiresConfirmation {
		t.Fatal("memory tool must not require confirmation")
	}
	if strings.TrimSpace(def.Prompt) == "" {
		t.Fatal("memory tool has no instruction prompt")
	}
}

func TestMemoryToolWriteThenSearch(t *testing.T) {
	tool, store := newTestMemoryTool(t)

	out := runMemory(t, tool, map[string]any{
		"action": "write",
		"name":   "sqlite-pragmas",
		"title":  "SQLite tuning",
		"tags":   []string{"database"},
		"body":   "WAL mode plus busy_timeout keeps concurrent writes from failing.",
	})
	if !strings.Contains(out.Content, "sqlite-pragmas") {
		t.Fatalf("write content = %q", out.Content)
	}
	if written, _ := out.Metadata["memory_written"].(bool); !written {
		t.Fatalf("write did not flag memory_written: %#v", out.Metadata)
	}

	// The note must actually be on disk, not just reported.
	if _, err := store.Read("sqlite-pragmas"); err != nil {
		t.Fatalf("note not stored: %v", err)
	}

	found := runMemory(t, tool, map[string]any{"action": "search", "query": "sqlite tuning"})
	if !strings.Contains(found.Content, "WAL mode") {
		t.Fatalf("search content = %q", found.Content)
	}
}

func TestMemoryToolSearchMissIsNotAnError(t *testing.T) {
	tool, _ := newTestMemoryTool(t)
	out := runMemory(t, tool, map[string]any{"action": "search", "query": "nothing matches this"})
	if !strings.Contains(out.Content, "no matching memories") {
		t.Fatalf("content = %q", out.Content)
	}
}

func TestMemoryToolListAndDelete(t *testing.T) {
	tool, _ := newTestMemoryTool(t)
	runMemory(t, tool, map[string]any{"action": "write", "name": "alpha", "body": "first"})
	runMemory(t, tool, map[string]any{"action": "write", "name": "beta", "body": "second"})

	listed := runMemory(t, tool, map[string]any{"action": "list"})
	for _, want := range []string{"alpha", "beta"} {
		if !strings.Contains(listed.Content, want) {
			t.Fatalf("list missing %q: %q", want, listed.Content)
		}
	}

	runMemory(t, tool, map[string]any{"action": "delete", "name": "alpha"})
	after := runMemory(t, tool, map[string]any{"action": "list"})
	if strings.Contains(after.Content, "alpha") {
		t.Fatalf("alpha survived delete: %q", after.Content)
	}
}

// TestMemoryToolWriteOverwritesSameName documents that a correction replaces
// the note rather than creating a second, contradictory one.
func TestMemoryToolWriteOverwritesSameName(t *testing.T) {
	tool, store := newTestMemoryTool(t)
	runMemory(t, tool, map[string]any{"action": "write", "name": "fact", "body": "the old answer"})
	runMemory(t, tool, map[string]any{"action": "write", "name": "fact", "body": "the corrected answer"})

	notes, err := store.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(notes) != 1 {
		t.Fatalf("notes = %d, want 1 overwrite", len(notes))
	}
	if !strings.Contains(notes[0].Body, "corrected") {
		t.Fatalf("body = %q", notes[0].Body)
	}
}

func TestMemoryToolRejectsBadInput(t *testing.T) {
	tool, _ := newTestMemoryTool(t)

	raw := json.RawMessage(`{"action":"write"}`)
	if _, err := tool.Run(context.Background(), agent.ToolInput{Arguments: raw}); err == nil {
		t.Fatal("write with no body was accepted")
	}
	raw = json.RawMessage(`{"action":"nonsense"}`)
	if _, err := tool.Run(context.Background(), agent.ToolInput{Arguments: raw}); err == nil {
		t.Fatal("unknown action was accepted")
	}
	raw = json.RawMessage(`{}`)
	if _, err := tool.Run(context.Background(), agent.ToolInput{Arguments: raw}); err == nil {
		t.Fatal("missing action was accepted")
	}
	raw = json.RawMessage(`not json`)
	if _, err := tool.Run(context.Background(), agent.ToolInput{Arguments: raw}); err == nil {
		t.Fatal("malformed arguments were accepted")
	}
}

func TestMemoryRecallSection(t *testing.T) {
	store := memory.NewStore(t.TempDir())
	if _, err := store.Write(memory.Note{
		Name:  "tui-viewport",
		Title: "TUI viewport math",
		Body:  "Width is reduced by one to avoid writing the terminal's last cell.",
	}); err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	section := MemoryRecallSection(store, "tui viewport", 3)
	if !strings.Contains(section, "<memories>") {
		t.Fatalf("section missing wrapper: %q", section)
	}
	if !strings.Contains(section, "last cell") {
		t.Fatalf("section missing body: %q", section)
	}

	// A nil store and an unmatched query must both yield nothing, so the
	// caller can append unconditionally.
	if got := MemoryRecallSection(nil, "anything", 3); got != "" {
		t.Fatalf("nil store produced a section: %q", got)
	}
	if got := MemoryRecallSection(store, "completely unrelated zzzz", 3); got != "" {
		t.Fatalf("unmatched query produced a section: %q", got)
	}
}
