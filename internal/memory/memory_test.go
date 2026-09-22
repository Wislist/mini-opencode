package memory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWriteThenReadRoundTrips(t *testing.T) {
	store := NewStore(t.TempDir())
	written, err := store.Write(Note{
		Name:  "Compaction Design",
		Title: "Compaction is non-destructive",
		Tags:  []string{"Architecture", "context"},
		Body:  "The summary is appended and the marker truncates the prompt.",
	})
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if written.Name != "compaction-design" {
		t.Fatalf("name = %q, want slugified", written.Name)
	}
	if written.CreatedAt.IsZero() || written.UpdatedAt.IsZero() {
		t.Fatal("timestamps not set")
	}

	got, err := store.Read("compaction-design")
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if got.Title != "Compaction is non-destructive" {
		t.Fatalf("title = %q", got.Title)
	}
	if got.Body != "The summary is appended and the marker truncates the prompt." {
		t.Fatalf("body = %q", got.Body)
	}
	if len(got.Tags) != 2 || got.Tags[0] != "architecture" {
		t.Fatalf("tags = %#v, want lowercased", got.Tags)
	}

	// The file must stay human-editable Markdown with front matter.
	raw, err := os.ReadFile(filepath.Join(store.Dir(), "compaction-design.md"))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	text := string(raw)
	if !strings.HasPrefix(text, "---\n") {
		t.Fatalf("missing front matter:\n%s", text)
	}
	if !strings.Contains(text, "tags: architecture, context") {
		t.Fatalf("tags not rendered:\n%s", text)
	}
}

func TestWritePreservesCreatedAtAndDerivesName(t *testing.T) {
	store := NewStore(t.TempDir())
	first, err := store.Write(Note{Title: "Release checklist", Body: "one"})
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if first.Name != "release-checklist" {
		t.Fatalf("name derived from title = %q", first.Name)
	}

	second, err := store.Write(Note{Name: "release-checklist", Title: "Release checklist", Body: "two"})
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if !second.CreatedAt.Equal(first.CreatedAt) {
		t.Fatalf("CreatedAt moved on update: %v -> %v", first.CreatedAt, second.CreatedAt)
	}
	if second.Body != "two" {
		t.Fatalf("body = %q, want the updated content", second.Body)
	}
	notes, err := store.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(notes) != 1 {
		t.Fatalf("notes = %d, want 1 (update must not create a second file)", len(notes))
	}
}

func TestWriteRejectsInvalidInput(t *testing.T) {
	store := NewStore(t.TempDir())
	if _, err := store.Write(Note{Title: "ok", Body: "   "}); err == nil {
		t.Fatal("empty body was accepted")
	}
	if _, err := store.Write(Note{Title: "!!!", Body: "x"}); err == nil {
		t.Fatal("unslugifiable name was accepted")
	}
}

func TestListIsEmptyWhenNothingStored(t *testing.T) {
	store := NewStore(t.TempDir())
	notes, err := store.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(notes) != 0 {
		t.Fatalf("notes = %#v, want none", notes)
	}
}

func TestDeleteRemovesNoteAndToleratesMissing(t *testing.T) {
	store := NewStore(t.TempDir())
	if _, err := store.Write(Note{Name: "temporary", Body: "x"}); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if err := store.Delete("temporary"); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if notes, _ := store.List(); len(notes) != 0 {
		t.Fatalf("notes after delete = %#v", notes)
	}
	if err := store.Delete("never-existed"); err != nil {
		t.Fatalf("Delete() of absent note = %v, want nil", err)
	}
}

// TestReadsHandWrittenFile verifies that a plain Markdown file dropped into the
// memory directory is usable, since the feature exists to be user-editable.
func TestReadsHandWrittenFile(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	if err := os.MkdirAll(store.Dir(), 0700); err != nil {
		t.Fatal(err)
	}
	plain := "# Notes\n\nDeploys go through the staging branch first.\n"
	if err := os.WriteFile(filepath.Join(store.Dir(), "deploys.md"), []byte(plain), 0600); err != nil {
		t.Fatal(err)
	}

	notes, err := store.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(notes) != 1 {
		t.Fatalf("notes = %d, want 1", len(notes))
	}
	if notes[0].Name != "deploys" {
		t.Fatalf("name = %q, want the filename stem", notes[0].Name)
	}
	if !strings.Contains(notes[0].Body, "staging branch") {
		t.Fatalf("body = %q", notes[0].Body)
	}
}

func TestSearchRanksRelevantNotesFirst(t *testing.T) {
	store := NewStore(t.TempDir())
	write := func(name, title, body string, tags []string) {
		t.Helper()
		if _, err := store.Write(Note{Name: name, Title: title, Body: body, Tags: tags}); err != nil {
			t.Fatalf("Write(%s) error = %v", name, err)
		}
	}
	write("sqlite-tuning", "SQLite tuning", "WAL mode and busy_timeout keep writes fast.", []string{"database"})
	write("tui-colors", "TUI colors", "The palette lives in styles.go.", []string{"ui"})
	write("deploys", "Deploy process", "Staging first, then production.", []string{"ops"})

	hits, err := store.Search("sqlite tuning", 5)
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(hits) == 0 {
		t.Fatal("Search() returned nothing")
	}
	if hits[0].Name != "sqlite-tuning" {
		t.Fatalf("top hit = %q, want sqlite-tuning", hits[0].Name)
	}
}

func TestSearchUsesTagsAndLimits(t *testing.T) {
	store := NewStore(t.TempDir())
	for _, name := range []string{"a", "b", "c", "d"} {
		if _, err := store.Write(Note{
			Name:  name,
			Title: "note " + name,
			Tags:  []string{"shared"},
			Body:  "shared body content",
		}); err != nil {
			t.Fatalf("Write() error = %v", err)
		}
	}
	hits, err := store.Search("shared", 2)
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(hits) != 2 {
		t.Fatalf("hits = %d, want the limit of 2", len(hits))
	}
}

// TestSearchEmptyQueryReturnsRecent documents the "what do we know" default.
// The two writes are forced onto distinct timestamps so the assertion tests
// recency ordering rather than the resolution of the clock.
func TestSearchEmptyQueryReturnsRecent(t *testing.T) {
	store := NewStore(t.TempDir())
	older := Note{Name: "first", Body: "one"}
	if _, err := store.Write(older); err != nil {
		t.Fatal(err)
	}
	// Backdate the first note so ordering cannot depend on how fast the two
	// writes happen to complete.
	backdate(t, store, "first")

	if _, err := store.Write(Note{Name: "second", Body: "two"}); err != nil {
		t.Fatal(err)
	}
	hits, err := store.Search("", 1)
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("hits = %d, want 1", len(hits))
	}
	if hits[0].Name != "second" {
		t.Fatalf("most recent hit = %q, want second", hits[0].Name)
	}
}

// backdate rewrites a stored note's timestamps to an hour ago, so a test can
// assert recency ordering without sleeping.
func backdate(t *testing.T, store *Store, name string) {
	t.Helper()
	note, err := store.Read(name)
	if err != nil {
		t.Fatalf("Read(%s) error = %v", name, err)
	}
	note.CreatedAt = note.CreatedAt.Add(-time.Hour)
	note.UpdatedAt = note.UpdatedAt.Add(-time.Hour)
	path := filepath.Join(store.Dir(), name+FileExt)
	if err := os.WriteFile(path, []byte(render(note)), 0600); err != nil {
		t.Fatalf("backdate %s: %v", name, err)
	}
}

// TestSearchMatchesCJK covers the tokenizer path for non-space-delimited text,
// which is the common case for this project's users.
func TestSearchMatchesCJK(t *testing.T) {
	store := NewStore(t.TempDir())
	if _, err := store.Write(Note{
		Name:  "compaction",
		Title: "上下文压缩设计",
		Body:  "压缩后保留原文，只在提示中截断。",
	}); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if _, err := store.Write(Note{Name: "unrelated", Title: "Deploys", Body: "staging first"}); err != nil {
		t.Fatal(err)
	}

	hits, err := store.Search("压缩", 5)
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(hits) == 0 || hits[0].Name != "compaction" {
		t.Fatalf("hits = %#v, want the CJK note first", hits)
	}
}

func TestSlugify(t *testing.T) {
	cases := map[string]string{
		"Compaction Design": "compaction-design",
		"  spaced   out  ":  "spaced-out",
		"under_scores ok":   "under-scores-ok",
		"":                  "",
		"!!!":               "",
		"mixed 中文 words":    "mixed-words",
	}
	for in, want := range cases {
		if got := Slugify(in); got != want {
			t.Errorf("Slugify(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMalformedNoteDoesNotHideOthers(t *testing.T) {
	store := NewStore(t.TempDir())
	if _, err := store.Write(Note{Name: "good", Body: "healthy content"}); err != nil {
		t.Fatal(err)
	}
	// A note whose front matter parses to an empty body is skipped.
	bad := "---\ntitle: broken\n---\n\n"
	if err := os.WriteFile(filepath.Join(store.Dir(), "broken.md"), []byte(bad), 0600); err != nil {
		t.Fatal(err)
	}
	notes, err := store.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(notes) != 1 || notes[0].Name != "good" {
		t.Fatalf("notes = %#v, want only the healthy note", notes)
	}
}
