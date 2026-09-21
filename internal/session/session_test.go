package session

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

func TestStoreArchiveExportsJSONAndDeletesActiveSession(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	sess := store.Create("archive me")
	sess.Messages = []agent.Message{{Role: agent.RoleUser, Content: "please save this"}}
	if err := store.Save(sess); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	path, err := store.Archive(sess.ID)
	if err != nil {
		t.Fatalf("Archive() error = %v", err)
	}
	if _, err := store.Load(sess.ID); err == nil {
		t.Fatal("expected archived session to be removed from active SQLite storage")
	}
	if path != filepath.Join(dir, ".mini-opencode", "sessions", sess.ID+".json") {
		t.Fatalf("archive path = %q", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("archive file missing: %v", err)
	}
	var archived Session
	if err := json.Unmarshal(data, &archived); err != nil {
		t.Fatalf("archive JSON invalid: %v", err)
	}
	if archived.ID != sess.ID || archived.Messages[0].Content != "please save this" {
		t.Fatalf("archive content = %#v", archived)
	}
}

func TestStoreListArchivesExpiredSessions(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	now := time.Now()

	old := store.Create("expired")
	old.CreatedAt = now.Add(-20 * 24 * time.Hour)
	old.UpdatedAt = now.Add(-16 * 24 * time.Hour)
	old.Messages = []agent.Message{{Role: agent.RoleUser, Content: "old message"}}

	fresh := store.Create("fresh")
	fresh.CreatedAt = now.Add(-2 * time.Hour)
	fresh.UpdatedAt = now.Add(-1 * time.Hour)
	fresh.Messages = []agent.Message{{Role: agent.RoleUser, Content: "fresh message"}}

	store.mu.Lock()
	if err := store.ensureLocked(); err != nil {
		store.mu.Unlock()
		t.Fatalf("ensureLocked() error = %v", err)
	}
	if err := store.saveLocked(old); err != nil {
		store.mu.Unlock()
		t.Fatalf("saveLocked(old) error = %v", err)
	}
	if err := store.saveLocked(fresh); err != nil {
		store.mu.Unlock()
		t.Fatalf("saveLocked(fresh) error = %v", err)
	}
	store.mu.Unlock()

	metas, err := store.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(metas) != 1 || metas[0].ID != fresh.ID {
		t.Fatalf("active metas = %#v, want only fresh", metas)
	}
	if _, err := os.Stat(filepath.Join(dir, ".mini-opencode", "sessions", old.ID+".json")); err != nil {
		t.Fatalf("expired session was not archived: %v", err)
	}
	if _, err := store.Load(old.ID); err == nil {
		t.Fatal("expected expired session to be deleted from SQLite")
	}
}

func TestStoreImportsLegacyJSONOnlyOnce(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	legacy := &Session{
		ID:        "legacy-1",
		Title:     "legacy",
		CreatedAt: time.Now().Add(-time.Hour),
		UpdatedAt: time.Now(),
		Messages:  []agent.Message{{Role: agent.RoleUser, Content: "legacy message"}},
	}
	if err := os.MkdirAll(store.archiveDir, 0700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	data, err := json.MarshalIndent(legacy, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(store.archiveDir, legacy.ID+".json"), data, 0600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	metas, err := store.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(metas) != 1 || metas[0].ID != legacy.ID {
		t.Fatalf("legacy import metas = %#v", metas)
	}
	if _, err := store.Archive(legacy.ID); err != nil {
		t.Fatalf("Archive() error = %v", err)
	}
	metas, err = store.List()
	if err != nil {
		t.Fatalf("List() after archive error = %v", err)
	}
	if len(metas) != 0 {
		t.Fatalf("legacy JSON was re-imported after archive: %#v", metas)
	}
}

func TestStoreUsesCrushStyleSchemaAndTypedMessageParts(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	sess := store.Create("typed parts")
	sess.ParentSessionID = ""
	sess.PromptTokens = 10
	sess.CompletionTokens = 20
	sess.Cost = 0.03
	sess.Todos = `[{"title":"ship","done":false}]`
	sess.Messages = []agent.Message{
		{Role: agent.RoleUser, Content: "hello"},
		{Role: agent.RoleAssistant, Content: "using tool", ToolCalls: []agent.ToolCall{{ID: "call-1", Name: "read", Arguments: []byte(`{"path":"a.go"}`)}}},
		{Role: agent.RoleTool, ToolCallID: "call-1", Content: "file contents"},
	}
	if err := store.Save(sess); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	if _, err := os.Stat(filepath.Join(dir, ".mini-opencode", "sessions.db")); err != nil {
		t.Fatalf("SQLite DB missing: %v", err)
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	var messageCount int
	var todos string
	if err := store.db.QueryRow(`SELECT message_count, todos FROM sessions WHERE id = ?`, sess.ID).Scan(&messageCount, &todos); err != nil {
		t.Fatalf("session row query error = %v", err)
	}
	if messageCount != 3 {
		t.Fatalf("message_count = %d, want 3", messageCount)
	}
	if todos != sess.Todos {
		t.Fatalf("todos = %q, want %q", todos, sess.Todos)
	}
	var partsRaw string
	if err := store.db.QueryRow(`SELECT parts FROM messages WHERE session_id = ? AND position = 1`, sess.ID).Scan(&partsRaw); err != nil {
		t.Fatalf("message row query error = %v", err)
	}
	var parts []ContentPart
	if err := json.Unmarshal([]byte(partsRaw), &parts); err != nil {
		t.Fatalf("parts JSON invalid: %v", err)
	}
	if len(parts) != 2 || parts[0].Type != "text" || parts[1].Type != "tool_use" {
		t.Fatalf("parts = %#v, want text + tool_use", parts)
	}
}

func TestStoreDeleteCascadesMessagesFilesAndReadFiles(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	sess := store.Create("cascade")
	sess.Messages = []agent.Message{{Role: agent.RoleUser, Content: "read a.go"}}
	if err := store.Save(sess); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if err := store.SaveFileSnapshot(sess.ID, "a.go", 1, []byte("v1")); err != nil {
		t.Fatalf("SaveFileSnapshot() error = %v", err)
	}
	if err := store.RecordReadFile(sess.ID, "a.go"); err != nil {
		t.Fatalf("RecordReadFile() error = %v", err)
	}
	if err := store.Delete(sess.ID); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}

	store.mu.Lock()
	defer store.mu.Unlock()
	for _, table := range []string{"messages", "files", "read_files"} {
		var count int
		if err := store.db.QueryRow(`SELECT COUNT(*) FROM `+table+` WHERE session_id = ?`, sess.ID).Scan(&count); err != nil {
			t.Fatalf("count %s error = %v", table, err)
		}
		if count != 0 {
			t.Fatalf("%s count = %d, want 0", table, count)
		}
	}
}

func TestStorePublishesCreateUpdateDeleteEvents(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	events, cancel := store.Subscribe()
	defer cancel()

	sess := store.Create("events")
	if err := store.Save(sess); err != nil {
		t.Fatalf("Save(create) error = %v", err)
	}
	assertSessionEvent(t, events, EventCreated, sess.ID)

	sess.Messages = []agent.Message{{Role: agent.RoleUser, Content: "hello"}}
	if err := store.Save(sess); err != nil {
		t.Fatalf("Save(update) error = %v", err)
	}
	assertSessionEvent(t, events, EventUpdated, sess.ID)

	if err := store.Delete(sess.ID); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	assertSessionEvent(t, events, EventDeleted, sess.ID)
}

func assertSessionEvent(t *testing.T, events <-chan Event, typ EventType, id string) {
	t.Helper()
	select {
	case event := <-events:
		if event.Type != typ || event.SessionID != id {
			t.Fatalf("event = %#v, want %s %s", event, typ, id)
		}
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for %s event", typ)
	}
}

func TestForkCopiesTranscriptAndRecordsParent(t *testing.T) {
	store := NewStore(t.TempDir())
	src := store.Create("fix the parser")
	src.Messages = []agent.Message{
		{Role: agent.RoleUser, Content: "help me"},
		{Role: agent.RoleAssistant, Content: "sure"},
	}
	src.Todos = `[{"content":"ship","status":"pending"}]`
	src.PromptTokens = 42
	src.CompletionTokens = 7
	if err := store.Save(src); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	fork, err := store.Fork(src.ID, "")
	if err != nil {
		t.Fatalf("Fork() error = %v", err)
	}
	if fork.ID == src.ID {
		t.Fatal("fork reused the source id")
	}
	if fork.ParentSessionID != src.ID {
		t.Fatalf("parent = %q, want %q", fork.ParentSessionID, src.ID)
	}
	if fork.Title != "fix the parser (branch)" {
		t.Fatalf("title = %q", fork.Title)
	}
	if len(fork.Messages) != 2 || fork.Messages[0].Content != "help me" {
		t.Fatalf("messages = %#v", fork.Messages)
	}
	if fork.PromptTokens != 42 || fork.CompletionTokens != 7 {
		t.Fatalf("token totals not inherited: %+v", fork)
	}

	// The fork is persisted and independently editable: appending to it must
	// not touch the source session.
	fork.Messages = append(fork.Messages, agent.Message{Role: agent.RoleUser, Content: "another angle"})
	if err := store.Save(fork); err != nil {
		t.Fatalf("Save(fork) error = %v", err)
	}
	reloadedSrc, err := store.Load(src.ID)
	if err != nil {
		t.Fatalf("Load(src) error = %v", err)
	}
	if len(reloadedSrc.Messages) != 2 {
		t.Fatalf("source transcript changed: %#v", reloadedSrc.Messages)
	}
	reloadedFork, err := store.Load(fork.ID)
	if err != nil {
		t.Fatalf("Load(fork) error = %v", err)
	}
	if len(reloadedFork.Messages) != 3 {
		t.Fatalf("fork transcript = %d messages", len(reloadedFork.Messages))
	}
	if reloadedFork.Todos != src.Todos {
		t.Fatalf("fork todos = %q, want %q", reloadedFork.Todos, src.Todos)
	}

	metas, err := store.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	found := false
	for _, meta := range metas {
		if meta.ID == fork.ID {
			found = true
			if meta.ParentSessionID != src.ID {
				t.Fatalf("listed parent = %q", meta.ParentSessionID)
			}
		}
	}
	if !found {
		t.Fatal("fork missing from the session list")
	}
}

func TestLoadTodosAndSaveTodos(t *testing.T) {
	store := NewStore(t.TempDir())
	sess := store.Create("todos")
	if err := store.Save(sess); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if got := store.LoadTodos(sess.ID); got != "[]" {
		t.Fatalf("initial todos = %q, want []", got)
	}
	if err := store.SaveTodos(sess.ID, `[{"content":"a","status":"pending"}]`); err != nil {
		t.Fatalf("SaveTodos() error = %v", err)
	}
	if got := store.LoadTodos(sess.ID); got != `[{"content":"a","status":"pending"}]` {
		t.Fatalf("todos = %q", got)
	}
	if got := store.LoadTodos("missing"); got != "[]" {
		t.Fatalf("unknown session todos = %q", got)
	}
}

func TestSnapshotFileAndLatestSnapshot(t *testing.T) {
	store := NewStore(t.TempDir())
	sess := store.Create("snapshots")
	if err := store.Save(sess); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if store.HasReadFile(sess.ID, "a.go") {
		t.Fatal("HasReadFile() = true before any read")
	}
	if err := store.RecordReadFile(sess.ID, "a.go"); err != nil {
		t.Fatalf("RecordReadFile() error = %v", err)
	}
	if !store.HasReadFile(sess.ID, "a.go") {
		t.Fatal("HasReadFile() = false after RecordReadFile")
	}

	if err := store.SnapshotFile(sess.ID, "a.go", []byte("v1")); err != nil {
		t.Fatalf("SnapshotFile() error = %v", err)
	}
	if err := store.SnapshotFile(sess.ID, "a.go", []byte("v2")); err != nil {
		t.Fatalf("SnapshotFile() error = %v", err)
	}
	snap, ok, err := store.LatestSnapshot(sess.ID)
	if err != nil || !ok {
		t.Fatalf("LatestSnapshot() = %+v, ok=%v, err=%v", snap, ok, err)
	}
	if snap.Path != "a.go" || string(snap.Content) != "v2" || snap.Version != 2 {
		t.Fatalf("snapshot = %+v", snap)
	}

	if _, ok, err := store.LatestSnapshot("missing"); err != nil || ok {
		t.Fatalf("LatestSnapshot(missing) ok=%v err=%v", ok, err)
	}
}

// A database written by an older build still carries the message_count
// triggers; opening it must drop them so saving stays linear.
func TestStoreDropsRetiredMessageCountTriggers(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	if err := store.Save(store.Create("legacy")); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	// Reinstate the retired triggers by hand, then reopen the store.
	store.mu.Lock()
	for _, name := range []string{"messages_after_insert_count", "messages_after_delete_count", "messages_after_update_session_count"} {
		if _, err := store.db.Exec(`CREATE TRIGGER IF NOT EXISTS ` + name + `
			AFTER INSERT ON messages
			BEGIN
				UPDATE sessions SET message_count = (SELECT COUNT(*) FROM messages WHERE session_id = NEW.session_id) WHERE id = NEW.session_id;
			END`); err != nil {
			store.mu.Unlock()
			t.Fatalf("create trigger %s: %v", name, err)
		}
	}
	store.mu.Unlock()

	reopened := NewStore(dir)
	if err := reopened.Save(reopened.Create("after migration")); err != nil {
		t.Fatalf("Save() on reopened store error = %v", err)
	}
	reopened.mu.Lock()
	defer reopened.mu.Unlock()
	for _, name := range []string{"messages_after_insert_count", "messages_after_delete_count", "messages_after_update_session_count"} {
		var n int
		if err := reopened.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'trigger' AND name = ?`, name).Scan(&n); err != nil {
			t.Fatalf("query trigger %s: %v", name, err)
		}
		if n != 0 {
			t.Fatalf("retired trigger %s survived the migration", name)
		}
	}
}

// Saving an unchanged prefix must not rewrite it: that is the whole point of
// the incremental writer.
func TestStoreIncrementalMessageWriteKeepsUntouchedRows(t *testing.T) {
	store := NewStore(t.TempDir())
	sess := store.Create("incremental")
	sess.Messages = []agent.Message{
		{Role: agent.RoleUser, Content: "first"},
		{Role: agent.RoleAssistant, Content: "second"},
	}
	if err := store.Save(sess); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	before := map[string]string{}
	store.mu.Lock()
	rows, err := store.db.Query(`SELECT id, updated_at FROM messages WHERE session_id = ?`, sess.ID)
	if err != nil {
		store.mu.Unlock()
		t.Fatalf("query messages: %v", err)
	}
	for rows.Next() {
		var id, updated string
		if err := rows.Scan(&id, &updated); err != nil {
			rows.Close()
			store.mu.Unlock()
			t.Fatalf("scan: %v", err)
		}
		before[id] = updated
	}
	rows.Close()
	store.mu.Unlock()

	// Append a turn and save again.
	sess.Messages = append(sess.Messages, agent.Message{Role: agent.RoleUser, Content: "third"})
	if err := store.Save(sess); err != nil {
		t.Fatalf("Save() after append error = %v", err)
	}

	store.mu.Lock()
	defer store.mu.Unlock()
	for id, updated := range before {
		var after string
		if err := store.db.QueryRow(`SELECT updated_at FROM messages WHERE id = ?`, id).Scan(&after); err != nil {
			t.Fatalf("row %s disappeared: %v", id, err)
		}
		if after != updated {
			t.Fatalf("untouched row %s was rewritten (%s -> %s)", id, updated, after)
		}
	}
	var count, messageCount int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE session_id = ?`, sess.ID).Scan(&count); err != nil {
		t.Fatalf("count messages: %v", err)
	}
	if err := store.db.QueryRow(`SELECT message_count FROM sessions WHERE id = ?`, sess.ID).Scan(&messageCount); err != nil {
		t.Fatalf("count column: %v", err)
	}
	if count != 3 || messageCount != 3 {
		t.Fatalf("messages = %d, message_count = %d, want 3 and 3", count, messageCount)
	}
}

// Shrinking the transcript (a compact) must prune the rows that no longer
// exist, and keep message_count in step.
func TestStorePrunesMessagesAfterCompaction(t *testing.T) {
	store := NewStore(t.TempDir())
	sess := store.Create("compact")
	for i := 0; i < 5; i++ {
		sess.Messages = append(sess.Messages, agent.Message{Role: agent.RoleUser, Content: "message"})
	}
	if err := store.Save(sess); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	sess.Messages = []agent.Message{{Role: agent.RoleUser, Content: "<conversation_summary>\nsummary"}}
	if err := store.Save(sess); err != nil {
		t.Fatalf("Save() after compact error = %v", err)
	}

	loaded, err := store.Load(sess.ID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(loaded.Messages) != 1 || !strings.Contains(loaded.Messages[0].Content, "conversation_summary") {
		t.Fatalf("loaded messages = %#v", loaded.Messages)
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	var count, messageCount int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE session_id = ?`, sess.ID).Scan(&count); err != nil {
		t.Fatalf("count messages: %v", err)
	}
	if err := store.db.QueryRow(`SELECT message_count FROM sessions WHERE id = ?`, sess.ID).Scan(&messageCount); err != nil {
		t.Fatalf("count column: %v", err)
	}
	if count != 1 || messageCount != 1 {
		t.Fatalf("messages = %d, message_count = %d, want 1 and 1", count, messageCount)
	}
}

// BenchmarkStoreSaveAppendingTurn measures the incremental writer as the
// transcript grows. Saving used to delete and re-insert every message on each
// turn, and three triggers recomputed COUNT(*) over the session per row, which
// made long sessions quadratic in row visits. The writer now rewrites only the
// rows that changed, so the remaining cost is the linear per-save pass that
// encodes each message for comparison (~1ms at 800 messages).
func BenchmarkStoreSaveAppendingTurn(b *testing.B) {
	for _, messages := range []int{50, 200, 800} {
		b.Run(fmt.Sprintf("%d-messages", messages), func(b *testing.B) {
			store := NewStore(b.TempDir())
			sess := store.Create("bench")
			for i := 0; i < messages; i++ {
				sess.Messages = append(sess.Messages, agent.Message{Role: agent.RoleUser, Content: "message body"})
			}
			if err := store.Save(sess); err != nil {
				b.Fatalf("initial Save() error = %v", err)
			}

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				sess.Messages = append(sess.Messages, agent.Message{Role: agent.RoleUser, Content: "another turn"})
				sess.UpdatedAt = time.Now()
				if err := store.Save(sess); err != nil {
					b.Fatalf("Save() error = %v", err)
				}
			}
		})
	}
}
