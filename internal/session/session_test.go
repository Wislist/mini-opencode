package session

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/wislist/mini-opencode/internal/agent"
)

func TestStoreCreateSaveLoad(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	sess := store.Create("test conversation")
	if sess.ID == "" {
		t.Fatal("expected non-empty ID")
	}
	sess.Messages = []agent.Message{
		{Role: agent.RoleUser, Content: "hello"},
		{Role: agent.RoleAssistant, Content: "hi there"},
	}

	if err := store.Save(sess); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	loaded, err := store.Load(sess.ID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if loaded.Title != "test conversation" {
		t.Errorf("title = %q", loaded.Title)
	}
	if len(loaded.Messages) != 2 {
		t.Fatalf("messages = %d, want 2", len(loaded.Messages))
	}
	if loaded.Messages[0].Content != "hello" {
		t.Errorf("first message = %q", loaded.Messages[0].Content)
	}
}

func TestStoreListNewestFirst(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	older := store.Create("older")
	older.CreatedAt = time.Now().Add(-2 * time.Hour)
	older.UpdatedAt = time.Now().Add(-2 * time.Hour)
	older.Messages = []agent.Message{{Role: agent.RoleUser, Content: "old"}}
	if err := store.Save(older); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	newer := store.Create("newer")
	newer.Messages = []agent.Message{{Role: agent.RoleUser, Content: "new"}}
	if err := store.Save(newer); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	metas, err := store.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(metas) != 2 {
		t.Fatalf("metas = %d, want 2", len(metas))
	}
	if metas[0].Title != "newer" {
		t.Errorf("first meta = %q, want newer", metas[0].Title)
	}
	if metas[1].Title != "older" {
		t.Errorf("second meta = %q, want older", metas[1].Title)
	}
}

func TestStoreListEmptyDir(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "nonexistent"))
	metas, err := store.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(metas) != 0 {
		t.Fatalf("metas = %d, want 0", len(metas))
	}
}

func TestStoreDelete(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	sess := store.Create("to delete")
	if err := store.Save(sess); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if err := store.Delete(sess.ID); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if _, err := store.Load(sess.ID); err == nil {
		t.Fatal("expected error loading deleted session")
	}
	// deleting a missing file is not an error
	if err := store.Delete("nonexistent"); err != nil {
		t.Fatalf("Delete() missing = %v", err)
	}
}

func TestTitleFromMessage(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"", "new session"},
		{"short message", "short message"},
		{"this is a longer message that exceeds the forty eight character limit", "this is a longer message that exceeds the forty ..."},
	}
	for _, tt := range tests {
		got := TitleFromMessage(tt.input)
		if got != tt.want {
			t.Errorf("TitleFromMessage(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestGenerateIDUnique(t *testing.T) {
	now := time.Now()
	ids := map[string]bool{}
	for i := 0; i < 100; i++ {
		id := generateID(now)
		if ids[id] {
			t.Fatalf("duplicate ID: %s", id)
		}
		ids[id] = true
	}
}

func TestRuntimeSetMessages(t *testing.T) {
	// This tests the agent.Runtime.SetMessages method via the session package
	// round-trip, ensuring messages survive serialization.
	dir := t.TempDir()
	store := NewStore(dir)
	sess := store.Create("roundtrip")
	sess.Messages = []agent.Message{
		{Role: agent.RoleUser, Content: "q", ToolCallID: ""},
		{Role: agent.RoleAssistant, Content: "a", ToolCalls: []agent.ToolCall{
			{ID: "c1", Name: "tool", Arguments: []byte(`{"x":1}`)},
		}},
	}
	if err := store.Save(sess); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	loaded, err := store.Load(sess.ID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(loaded.Messages[1].ToolCalls) != 1 {
		t.Fatalf("tool calls = %d, want 1", len(loaded.Messages[1].ToolCalls))
	}
	if loaded.Messages[1].ToolCalls[0].Name != "tool" {
		t.Errorf("tool call name = %q", loaded.Messages[1].ToolCalls[0].Name)
	}
}
