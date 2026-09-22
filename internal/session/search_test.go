package session

import (
	"strings"
	"testing"

	"github.com/wislist/mini-opencode/internal/agent"
)

func TestSearchMessagesFindsStoredTranscripts(t *testing.T) {
	store := NewStore(t.TempDir())
	if !store.SearchAvailable() {
		t.Skip("full-text search unavailable in this SQLite build")
	}

	parser := store.Create("parser work")
	parser.Messages = []agent.Message{
		{Role: agent.RoleUser, Content: "how do I fix the tokenizer bug"},
		{Role: agent.RoleAssistant, Content: "the tokenizer splits CJK badly"},
	}
	if err := store.Save(parser); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	other := store.Create("unrelated")
	other.Messages = []agent.Message{{Role: agent.RoleUser, Content: "hello world"}}
	if err := store.Save(other); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	hits, err := store.SearchMessages("tokenizer", 10)
	if err != nil {
		t.Fatalf("SearchMessages() error = %v", err)
	}
	if len(hits) != 2 {
		t.Fatalf("hits = %d, want 2", len(hits))
	}
	for _, hit := range hits {
		if hit.SessionID != parser.ID {
			t.Fatalf("hit from unexpected session: %q", hit.SessionID)
		}
		if hit.SessionTitle != "parser work" {
			t.Fatalf("hit title = %q", hit.SessionTitle)
		}
		// Snippets must be readable text, never the stored parts JSON.
		if strings.Contains(hit.Snippet, `"type"`) {
			t.Fatalf("snippet leaked JSON envelope: %q", hit.Snippet)
		}
		if !strings.Contains(hit.Snippet, "tokenizer") {
			t.Fatalf("snippet = %q, want it to contain the query", hit.Snippet)
		}
		if hit.Role != agent.RoleUser && hit.Role != agent.RoleAssistant {
			t.Fatalf("hit role = %q", hit.Role)
		}
		if hit.CreatedAt.IsZero() {
			t.Fatal("hit CreatedAt is zero")
		}
	}
}

func TestSearchMessagesSupportsPhraseAndEmptyQueries(t *testing.T) {
	store := NewStore(t.TempDir())
	if !store.SearchAvailable() {
		t.Skip("full-text search unavailable in this SQLite build")
	}
	sess := store.Create("s")
	sess.Messages = []agent.Message{
		{Role: agent.RoleUser, Content: "hello world"},
		{Role: agent.RoleUser, Content: "hello there"},
	}
	if err := store.Save(sess); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	// A bare query matches either term.
	loose, err := store.SearchMessages("world", 10)
	if err != nil {
		t.Fatalf("SearchMessages() error = %v", err)
	}
	if len(loose) != 1 {
		t.Fatalf("loose hits = %d, want 1", len(loose))
	}

	// A quoted query is a phrase.
	phrase, err := store.SearchMessages(`"hello world"`, 10)
	if err != nil {
		t.Fatalf("SearchMessages() phrase error = %v", err)
	}
	if len(phrase) != 1 {
		t.Fatalf("phrase hits = %d, want 1", len(phrase))
	}

	// No match yields no results and no error.
	none, err := store.SearchMessages("zzzznope", 10)
	if err != nil {
		t.Fatalf("SearchMessages() empty error = %v", err)
	}
	if len(none) != 0 {
		t.Fatalf("unexpected hits: %#v", none)
	}

	// An empty query is a no-op rather than an FTS syntax error.
	blank, err := store.SearchMessages("   ", 10)
	if err != nil {
		t.Fatalf("SearchMessages() blank error = %v", err)
	}
	if len(blank) != 0 {
		t.Fatalf("blank query returned hits: %#v", blank)
	}
}

// TestSearchMessagesTracksEditsAndDeletes verifies the triggers keep the index
// in step with the messages table, which is what makes an edited or compacted
// transcript searchable by its current content.
func TestSearchMessagesTracksEditsAndDeletes(t *testing.T) {
	store := NewStore(t.TempDir())
	if !store.SearchAvailable() {
		t.Skip("full-text search unavailable in this SQLite build")
	}
	sess := store.Create("s")
	sess.Messages = []agent.Message{{Role: agent.RoleUser, Content: "original phrase"}}
	if err := store.Save(sess); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	sess.Messages = []agent.Message{{Role: agent.RoleUser, Content: "replacement phrase"}}
	if err := store.Save(sess); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	if hits, err := store.SearchMessages("original", 10); err != nil || len(hits) != 0 {
		t.Fatalf("stale index: hits=%d err=%v", len(hits), err)
	}
	if hits, err := store.SearchMessages("replacement", 10); err != nil || len(hits) != 1 {
		t.Fatalf("new content not indexed: hits=%d err=%v", len(hits), err)
	}

	if err := store.Delete(sess.ID); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if hits, err := store.SearchMessages("replacement", 10); err != nil || len(hits) != 0 {
		t.Fatalf("deleted messages still indexed: hits=%d err=%v", len(hits), err)
	}
}
