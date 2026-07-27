package session

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/wislist/mini-opencode/internal/agent"
)

// Session is a persisted conversation: an ID, a short title, timestamps,
// and the full message history.
type Session struct {
	ID        string          `json:"id"`
	Title     string          `json:"title"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
	Messages  []agent.Message `json:"messages"`
}

// Meta is a lightweight summary used for listing sessions without loading
// the full message history.
type Meta struct {
	ID        string
	Title     string
	UpdatedAt time.Time
	MessageN  int
}

// Store persists sessions as individual JSON files under
// <workingDir>/.mini-opencode/sessions/.
type Store struct {
	dir string
}

// NewStore returns a Store rooted at workingDir. The sessions directory is
// created lazily on first write.
func NewStore(workingDir string) *Store {
	return &Store{dir: filepath.Join(workingDir, ".mini-opencode", "sessions")}
}

// Create starts a new empty session with a generated ID and the given title.
func (s *Store) Create(title string) *Session {
	now := time.Now()
	return &Session{
		ID:        generateID(now),
		Title:     title,
		CreatedAt: now,
		UpdatedAt: now,
	}
}

// Save writes (or overwrites) a session to disk.
func (s *Store) Save(sess *Session) error {
	if sess == nil {
		return fmt.Errorf("session: nil session")
	}
	if sess.ID == "" {
		return fmt.Errorf("session: empty id")
	}
	sess.UpdatedAt = time.Now()
	if err := os.MkdirAll(s.dir, 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(sess, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(s.path(sess.ID), data, 0600)
}

// Load reads a single session by ID.
func (s *Store) Load(id string) (*Session, error) {
	data, err := os.ReadFile(s.path(id))
	if err != nil {
		return nil, err
	}
	var sess Session
	if err := json.Unmarshal(data, &sess); err != nil {
		return nil, err
	}
	return &sess, nil
}

// Delete removes a session file. Missing files are not an error.
func (s *Store) Delete(id string) error {
	err := os.Remove(s.path(id))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// List returns metadata for all stored sessions, newest first.
func (s *Store) List() ([]Meta, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var metas []Meta
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".json")
		sess, err := s.Load(id)
		if err != nil {
			continue
		}
		metas = append(metas, Meta{
			ID:        sess.ID,
			Title:     sess.Title,
			UpdatedAt: sess.UpdatedAt,
			MessageN:  len(sess.Messages),
		})
	}
	sort.Slice(metas, func(i, j int) bool {
		return metas[i].UpdatedAt.After(metas[j].UpdatedAt)
	})
	return metas, nil
}

func (s *Store) path(id string) string {
	return filepath.Join(s.dir, id+".json")
}

// generateID produces a sortable timestamp-based ID with a short random
// suffix to avoid collisions within the same second.
func generateID(now time.Time) string {
	return now.Format("20060102-150405") + "-" + randomSuffix(4)
}

// TitleFromMessage derives a short session title from the first user message.
func TitleFromMessage(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return "new session"
	}
	if len([]rune(text)) > 48 {
		return string([]rune(text)[:48]) + "..."
	}
	return text
}

func randomSuffix(n int) string {
	const hex = "0123456789abcdef"
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		// Fallback: time-based pseudo-random.
		seed := time.Now().UnixNano()
		for i := range b {
			b[i] = hex[seed%int64(len(hex))]
			seed = seed/int64(len(hex)) + 1
		}
		return string(b)
	}
	// Map raw bytes to hex chars.
	for i := range b {
		b[i] = hex[b[i]%byte(len(hex))]
	}
	return string(b)
}
