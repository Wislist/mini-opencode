package session

import (
	"fmt"
	"strings"
	"time"

	"github.com/wislist/mini-opencode/internal/agent"
)

// MessageSearchResult is one hit from a full-text search over stored sessions.
type MessageSearchResult struct {
	SessionID    string
	SessionTitle string
	Position     int
	Role         agent.Role
	// Snippet is a short extract around the match with the query terms intact.
	Snippet string
	// CreatedAt is when the message was written.
	CreatedAt time.Time
	// Score is the FTS5 bm25 rank; lower is a better match.
	Score float64
}

// searchSchemaStatements builds the full-text index used by SearchMessages.
//
// The index is a plain (non-external-content) FTS5 table kept in sync by
// triggers on messages rather than by application code, so writes made through
// any path stay searchable. It is created separately from schemaStatements
// because FTS5 is a compile-time option of the SQLite build: if it is missing
// the store still works, only search reports itself unavailable.
func searchSchemaStatements() []string {
	return []string{
		`CREATE VIRTUAL TABLE IF NOT EXISTS messages_fts USING fts5(
			content,
			session_id UNINDEXED,
			position UNINDEXED,
			role UNINDEXED
		)`,
		`CREATE TRIGGER IF NOT EXISTS messages_after_insert_fts
			AFTER INSERT ON messages
			BEGIN
				INSERT INTO messages_fts(content, session_id, position, role)
				VALUES (NEW.parts, NEW.session_id, NEW.position, NEW.role);
			END`,
		`CREATE TRIGGER IF NOT EXISTS messages_after_delete_fts
			AFTER DELETE ON messages
			BEGIN
				DELETE FROM messages_fts
				WHERE session_id = OLD.session_id AND position = OLD.position;
			END`,
		`CREATE TRIGGER IF NOT EXISTS messages_after_update_fts
			AFTER UPDATE ON messages
			BEGIN
				DELETE FROM messages_fts
				WHERE session_id = OLD.session_id AND position = OLD.position;
				INSERT INTO messages_fts(content, session_id, position, role)
				VALUES (NEW.parts, NEW.session_id, NEW.position, NEW.role);
			END`,
	}
}

// initSearchLocked creates the FTS index and backfills it from any messages
// that predate it, so search works on an existing database. Callers must hold
// the store lock and have a usable db.
func (s *Store) initSearchLocked() error {
	for _, stmt := range searchSchemaStatements() {
		if _, err := s.db.Exec(stmt); err != nil {
			// FTS5 unavailable in this build: disable search rather than
			// failing session storage entirely.
			s.searchErr = fmt.Errorf("full-text search unavailable: %w", err)
			return nil
		}
	}
	s.searchErr = nil

	// Backfill once. The index is empty exactly when it has never been built,
	// which is also when the backfill is needed; counting is cheap and makes
	// this idempotent across restarts.
	var indexed, stored int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM messages_fts`).Scan(&indexed); err != nil {
		return err
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM messages`).Scan(&stored); err != nil {
		return err
	}
	if indexed >= stored {
		return nil
	}
	_, err := s.db.Exec(`INSERT INTO messages_fts(content, session_id, position, role)
		SELECT parts, session_id, position, role FROM messages`)
	return err
}

// SearchAvailable reports whether full-text search is usable in this build.
func (s *Store) SearchAvailable() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureLocked(); err != nil {
		return false
	}
	return s.searchErr == nil
}

// SearchMessages finds stored messages matching a full-text query, newest
// first. The query uses FTS5 syntax, so `foo bar` matches either term and
// `"foo bar"` matches the phrase. An empty query returns no results.
func (s *Store) SearchMessages(query string, limit int) ([]MessageSearchResult, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 20
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureLocked(); err != nil {
		return nil, err
	}
	if s.searchErr != nil {
		return nil, s.searchErr
	}

	rows, err := s.db.Query(`
		SELECT f.session_id, COALESCE(s.title, ''), f.position, f.role,
		       COALESCE(m.parts, ''),
		       COALESCE(m.created_at, ''),
		       bm25(messages_fts)
		FROM messages_fts f
		LEFT JOIN messages m
			ON m.session_id = f.session_id AND m.position = f.position
		LEFT JOIN sessions s ON s.id = f.session_id
		WHERE messages_fts MATCH ?
		ORDER BY bm25(messages_fts)
		LIMIT ?`, query, limit)
	if err != nil {
		return nil, fmt.Errorf("search messages: %w", err)
	}
	defer rows.Close()

	var out []MessageSearchResult
	for rows.Next() {
		var (
			hit       MessageSearchResult
			roleRaw   string
			rawParts  string
			createdAt string
		)
		if err := rows.Scan(&hit.SessionID, &hit.SessionTitle, &hit.Position,
			&roleRaw, &rawParts, &createdAt, &hit.Score); err != nil {
			return nil, err
		}
		hit.Role = agent.Role(roleRaw)
		hit.Snippet = searchSnippet(roleRaw, rawParts)
		if ts, err := parseTime(createdAt); err == nil {
			hit.CreatedAt = ts
		}
		out = append(out, hit)
	}
	return out, rows.Err()
}

// searchSnippet renders the stored parts JSON as readable text. The FTS index
// stores the encoded parts column, so a raw snippet would otherwise surface
// JSON envelope syntax instead of the message the user wrote.
func searchSnippet(role, rawParts string) string {
	msg, err := decodeMessageParts(agent.Role(role), rawParts)
	if err != nil {
		return truncateSnippet(rawParts)
	}
	return truncateSnippet(msg.Content)
}

// truncateSnippet bounds a snippet so a search hit stays a preview.
func truncateSnippet(text string) string {
	text = strings.TrimSpace(text)
	const maxRunes = 400
	runes := []rune(text)
	if len(runes) <= maxRunes {
		return text
	}
	return string(runes[:maxRunes]) + "..."
}
