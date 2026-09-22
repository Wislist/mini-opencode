// Package memory implements cross-session memory: durable notes the agent
// writes about a workspace and reads back in later conversations.
//
// Memory lives in Markdown files under <workDir>/.mini-opencode/memory/. Files
// rather than a database, because the point of memory is that the user can
// read, edit, diff and version it alongside the code it describes. Each file
// carries a small YAML front matter block for the metadata (title, tags,
// timestamps) and a Markdown body for the note itself.
//
// Recall is deliberately explicit rather than automatic: notes are parsed and
// scored against a query, and only the best few are injected into a prompt.
// Dumping every note into every system prompt would spend the context budget
// that memory exists to save.
package memory

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// DirName is the directory (under .mini-opencode) holding memory files.
const DirName = "memory"

// FileExt is the extension of a memory note.
const FileExt = ".md"

// Note is one stored memory.
type Note struct {
	// Name is the slug identifying the note; it is also its filename stem.
	Name string
	// Title is the human-readable heading.
	Title string
	// Tags are free-form labels used for recall scoring.
	Tags []string
	// Body is the note content in Markdown.
	Body string
	// CreatedAt and UpdatedAt are file timestamps.
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Store reads and writes memory notes for one workspace.
//
// A Store is safe for concurrent use: the TUI reads notes to render recall
// while a run goroutine writes them through the memory tool, and the session
// store's own locking happens on a different layer entirely.
type Store struct {
	dir string
	mu  sync.RWMutex
}

// NewStore returns a Store rooted at <workDir>/.mini-opencode/memory.
func NewStore(workDir string) *Store {
	return &Store{dir: filepath.Join(workDir, ".mini-opencode", DirName)}
}

// Dir returns the directory holding the notes.
func (s *Store) Dir() string {
	if s == nil {
		return ""
	}
	return s.dir
}

// Write creates or replaces a note. An empty name is derived from the title so
// a caller that only supplies a title still lands in a stable file.
func (s *Store) Write(note Note) (Note, error) {
	if s == nil {
		return Note{}, errors.New("memory: nil store")
	}
	name := Slugify(note.Name)
	if name == "" {
		name = Slugify(note.Title)
	}
	if name == "" {
		return Note{}, errors.New("memory: a name or title is required")
	}
	body := strings.TrimSpace(note.Body)
	if body == "" {
		return Note{}, errors.New("memory: body is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := os.MkdirAll(s.dir, 0700); err != nil {
		return Note{}, fmt.Errorf("memory: create dir: %w", err)
	}
	path := s.pathFor(name)
	now := time.Now().UTC()
	createdAt := now
	// Preserve the original creation time when updating an existing note, so
	// "when did we learn this" survives a rewrite.
	if existing, err := readNote(path); err == nil && !existing.CreatedAt.IsZero() {
		createdAt = existing.CreatedAt
	}

	stored := Note{
		Name:      name,
		Title:     strings.TrimSpace(note.Title),
		Tags:      normalizeTags(note.Tags),
		Body:      body,
		CreatedAt: createdAt,
		UpdatedAt: now,
	}
	if stored.Title == "" {
		stored.Title = name
	}
	if err := os.WriteFile(path, []byte(render(stored)), 0600); err != nil {
		return Note{}, fmt.Errorf("memory: write %s: %w", path, err)
	}
	return stored, nil
}

// List returns every stored note, most recently updated first. A missing
// memory directory is not an error: it just means nothing has been remembered.
func (s *Store) List() ([]Note, error) {
	if s == nil {
		return nil, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.listLocked()
}

func (s *Store) listLocked() ([]Note, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("memory: read dir: %w", err)
	}
	var notes []Note
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), FileExt) {
			continue
		}
		note, err := readNote(filepath.Join(s.dir, entry.Name()))
		if err != nil {
			// A malformed note must not hide the rest of the memory.
			continue
		}
		notes = append(notes, note)
	}
	sort.SliceStable(notes, func(i, j int) bool {
		if notes[i].UpdatedAt.Equal(notes[j].UpdatedAt) {
			// Same timestamp (a coarse clock, or several writes within one
			// tick): fall back to creation time, then to the name, purely so
			// the order is deterministic rather than filesystem-dependent.
			if !notes[i].CreatedAt.Equal(notes[j].CreatedAt) {
				return notes[i].CreatedAt.After(notes[j].CreatedAt)
			}
			return notes[i].Name < notes[j].Name
		}
		return notes[i].UpdatedAt.After(notes[j].UpdatedAt)
	})
	return notes, nil
}

// Read returns one note by name.
func (s *Store) Read(name string) (Note, error) {
	if s == nil {
		return Note{}, errors.New("memory: nil store")
	}
	slug := Slugify(name)
	if slug == "" {
		return Note{}, errors.New("memory: name is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	note, err := readNote(s.pathFor(slug))
	if err != nil {
		return Note{}, err
	}
	return note, nil
}

// Delete removes a note. Deleting an absent note is not an error.
func (s *Store) Delete(name string) error {
	if s == nil {
		return errors.New("memory: nil store")
	}
	slug := Slugify(name)
	if slug == "" {
		return errors.New("memory: name is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.Remove(s.pathFor(slug)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("memory: delete %s: %w", slug, err)
	}
	return nil
}

// Search returns the notes most relevant to query, best first, limited to
// limit results. An empty query returns the most recently updated notes, which
// is the useful default when a caller wants "what do we know" rather than a
// specific fact.
func (s *Store) Search(query string, limit int) ([]Note, error) {
	notes, err := s.List()
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 5
	}
	if strings.TrimSpace(query) == "" {
		if len(notes) > limit {
			notes = notes[:limit]
		}
		return notes, nil
	}
	return Score(notes, query, limit), nil
}

// pathFor maps a slug to its file path. The slug is already restricted to
// [a-z0-9-] by Slugify, so it cannot escape the memory directory.
func (s *Store) pathFor(slug string) string {
	return filepath.Join(s.dir, slug+FileExt)
}
