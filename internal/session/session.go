package session

import (
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"

	"github.com/wislist/mini-opencode/internal/agent"
)

const (
	defaultRetentionDays = 15
	legacyImportMetaKey  = "legacy_json_imported"
)

// Session is a persisted conversation: an ID, a short title, timestamps,
// metadata, and the full message history.
type Session struct {
	ID               string          `json:"id"`
	ParentSessionID  string          `json:"parent_session_id,omitempty"`
	Title            string          `json:"title"`
	CreatedAt        time.Time       `json:"created_at"`
	UpdatedAt        time.Time       `json:"updated_at"`
	Messages         []agent.Message `json:"messages"`
	PromptTokens     int64           `json:"prompt_tokens,omitempty"`
	CompletionTokens int64           `json:"completion_tokens,omitempty"`
	Cost             float64         `json:"cost,omitempty"`
	SummaryMessageID string          `json:"summary_message_id,omitempty"`
	Todos            string          `json:"todos,omitempty"`
}

// Meta is a lightweight summary used for listing sessions without loading
// the full message history.
type Meta struct {
	ID              string
	ParentSessionID string
	Title           string
	UpdatedAt       time.Time
	MessageN        int
}

// ContentPart is the typed JSON envelope used by the messages.parts column.
// Each part has the same shape as Crush: {"type":"text","data":{...}}.
type ContentPart struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

type textPartData struct {
	Text string `json:"text"`
}

type toolUsePartData struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type toolResultPartData struct {
	ToolCallID string `json:"tool_call_id"`
	Name       string `json:"name,omitempty"`
	Content    string `json:"content"`
}

type EventType string

const (
	EventCreated EventType = "created"
	EventUpdated EventType = "updated"
	EventDeleted EventType = "deleted"
)

// Event is emitted after session create/update/delete operations. The TUI can
// subscribe to this to refresh lists without polling.
type Event struct {
	Type      EventType
	SessionID string
	Meta      *Meta
}

type Broker struct {
	mu          sync.Mutex
	subscribers map[chan Event]struct{}
}

func NewBroker() *Broker {
	return &Broker{subscribers: make(map[chan Event]struct{})}
}

func (b *Broker) Subscribe() (<-chan Event, func()) {
	if b == nil {
		ch := make(chan Event)
		close(ch)
		return ch, func() {}
	}
	ch := make(chan Event, 16)
	b.mu.Lock()
	b.subscribers[ch] = struct{}{}
	b.mu.Unlock()
	cancel := func() {
		b.mu.Lock()
		if _, ok := b.subscribers[ch]; ok {
			delete(b.subscribers, ch)
			close(ch)
		}
		b.mu.Unlock()
	}
	return ch, cancel
}

func (b *Broker) Publish(event Event) {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for ch := range b.subscribers {
		select {
		case ch <- event:
		default:
		}
	}
}

// Store persists active sessions in one SQLite database under
// <workingDir>/.mini-opencode/sessions.db. JSON files under
// <workingDir>/.mini-opencode/sessions/ are archives only: old sessions are
// exported there before being deleted from SQLite, and users can manually
// archive a session to that folder.
type Store struct {
	dbPath     string
	archiveDir string
	retention  time.Duration

	mu     sync.Mutex
	db     *sql.DB
	broker *Broker
}

// NewStore returns a Store rooted at workingDir. Active session data is saved
// in a single SQLite file; archived sessions are written as JSON files under
// .mini-opencode/sessions/. Sessions older than 15 days are automatically
// archived and removed from SQLite when the store is used.
func NewStore(workingDir string) *Store {
	root := filepath.Join(workingDir, ".mini-opencode")
	return &Store{
		dbPath:     filepath.Join(root, "sessions.db"),
		archiveDir: filepath.Join(root, "sessions"),
		retention:  defaultRetentionDays * 24 * time.Hour,
		broker:     NewBroker(),
	}
}

func (s *Store) Subscribe() (<-chan Event, func()) {
	if s.broker == nil {
		s.broker = NewBroker()
	}
	return s.broker.Subscribe()
}

// Create starts a new empty session with a generated ID and the given title.
func (s *Store) Create(title string) *Session {
	now := time.Now()
	return &Session{
		ID:        generateID(now),
		Title:     title,
		CreatedAt: now,
		UpdatedAt: now,
		Todos:     "[]",
	}
}

// Save writes (or overwrites) a session to SQLite.
func (s *Store) Save(sess *Session) error {
	if sess == nil {
		return fmt.Errorf("session: nil session")
	}
	if sess.ID == "" {
		return fmt.Errorf("session: empty id")
	}
	sess.UpdatedAt = time.Now()

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureLocked(); err != nil {
		return err
	}
	created := !s.sessionExistsLocked(sess.ID)
	if err := s.saveLocked(sess); err != nil {
		return err
	}
	meta := &Meta{ID: sess.ID, ParentSessionID: sess.ParentSessionID, Title: nonEmptyTitle(sess.Title), UpdatedAt: sess.UpdatedAt, MessageN: len(sess.Messages)}
	if created {
		s.broker.Publish(Event{Type: EventCreated, SessionID: sess.ID, Meta: meta})
	} else {
		s.broker.Publish(Event{Type: EventUpdated, SessionID: sess.ID, Meta: meta})
	}
	return s.archiveExpiredLocked(time.Now())
}

// Load reads a single active session from SQLite by ID.
func (s *Store) Load(id string) (*Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureLocked(); err != nil {
		return nil, err
	}
	return s.loadLocked(id)
}

// Fork copies an existing session into a new one that records the source in
// parent_session_id, so a conversation can branch without losing the original.
// The fork inherits the transcript, todo list, and token totals; snapshots and
// read-file records stay with the source session.
func (s *Store) Fork(id, title string) (*Session, error) {
	src, err := s.Load(id)
	if err != nil {
		return nil, err
	}
	fork := s.Create(branchTitle(src.Title, title))
	fork.ParentSessionID = src.ID
	fork.Messages = make([]agent.Message, len(src.Messages))
	copy(fork.Messages, src.Messages)
	fork.Todos = nonEmptyTodos(src.Todos)
	fork.PromptTokens = src.PromptTokens
	fork.CompletionTokens = src.CompletionTokens
	if err := s.Save(fork); err != nil {
		return nil, err
	}
	return fork, nil
}

// branchTitle builds the fork title from the source title and an override.
func branchTitle(sourceTitle, override string) string {
	if override = strings.TrimSpace(override); override != "" {
		return override
	}
	sourceTitle = strings.TrimSpace(sourceTitle)
	if sourceTitle == "" || sourceTitle == "new session" {
		return "branch"
	}
	const suffix = " (branch)"
	if strings.HasSuffix(sourceTitle, suffix) {
		return sourceTitle
	}
	return sourceTitle + suffix
}

// Delete removes an active session from SQLite. Missing rows are not an error.
func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureLocked(); err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	if err := deleteSessionTx(tx, id); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.broker.Publish(Event{Type: EventDeleted, SessionID: id})
	return nil
}

// List returns metadata for all active sessions, newest first. Before listing,
// expired sessions are archived as JSON files and deleted from SQLite.
func (s *Store) List() ([]Meta, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureLocked(); err != nil {
		return nil, err
	}
	if err := s.archiveExpiredLocked(time.Now()); err != nil {
		return nil, err
	}

	rows, err := s.db.Query(`SELECT id, COALESCE(parent_session_id, ''), title, updated_at, message_count FROM sessions ORDER BY updated_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var metas []Meta
	for rows.Next() {
		var meta Meta
		var updatedRaw string
		if err := rows.Scan(&meta.ID, &meta.ParentSessionID, &meta.Title, &updatedRaw, &meta.MessageN); err != nil {
			return nil, err
		}
		updatedAt, err := parseTime(updatedRaw)
		if err != nil {
			continue
		}
		meta.UpdatedAt = updatedAt
		metas = append(metas, meta)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return metas, nil
}

// Archive exports an active session to JSON under .mini-opencode/sessions/ and
// deletes it from SQLite. Missing sessions return the Load error.
func (s *Store) Archive(id string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureLocked(); err != nil {
		return "", err
	}
	sess, err := s.loadLocked(id)
	if err != nil {
		return "", err
	}
	path, err := s.writeArchiveLocked(sess)
	if err != nil {
		return "", err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return "", err
	}
	if err := deleteSessionTx(tx, id); err != nil {
		_ = tx.Rollback()
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	s.broker.Publish(Event{Type: EventDeleted, SessionID: id})
	return path, nil
}

// ArchiveExpired exports active sessions older than the retention window to
// JSON and removes them from SQLite. It returns the number of archived sessions.
func (s *Store) ArchiveExpired(now time.Time) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureLocked(); err != nil {
		return 0, err
	}
	return s.archiveExpiredCountLocked(now)
}

// SaveFileSnapshot stores a versioned file snapshot for a session.
func (s *Store) SaveFileSnapshot(sessionID, path string, version int, content []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureLocked(); err != nil {
		return err
	}
	now := formatTime(time.Now())
	_, err := s.db.Exec(`INSERT INTO files (session_id, path, version, content, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(path, session_id, version) DO UPDATE SET
			content = excluded.content,
			updated_at = excluded.updated_at`, sessionID, path, version, content, now, now)
	return err
}

// RecordReadFile records that a session read a file path.
func (s *Store) RecordReadFile(sessionID, path string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureLocked(); err != nil {
		return err
	}
	_, err := s.db.Exec(`INSERT INTO read_files (session_id, path, created_at)
		VALUES (?, ?, ?)
		ON CONFLICT(session_id, path) DO UPDATE SET created_at = excluded.created_at`, sessionID, path, formatTime(time.Now()))
	return err
}

// HasReadFile reports whether the session already read path.
func (s *Store) HasReadFile(sessionID, path string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureLocked(); err != nil {
		return false
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM read_files WHERE session_id = ? AND path = ?`,
		sessionID, path).Scan(&n); err != nil {
		return false
	}
	return n > 0
}

// SnapshotFile stores the pre-modification content of path, assigning the next
// version number for that session and path.
func (s *Store) SnapshotFile(sessionID, path string, content []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureLocked(); err != nil {
		return err
	}
	var next int
	if err := s.db.QueryRow(`SELECT COALESCE(MAX(version), 0) + 1 FROM files WHERE session_id = ? AND path = ?`,
		sessionID, path).Scan(&next); err != nil {
		return err
	}
	now := formatTime(time.Now())
	_, err := s.db.Exec(`INSERT INTO files (session_id, path, version, content, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(path, session_id, version) DO UPDATE SET
			content = excluded.content,
			updated_at = excluded.updated_at`, sessionID, path, next, content, now, now)
	return err
}

// FileSnapshot is one stored pre-modification revision of a file.
type FileSnapshot struct {
	Path      string
	Version   int
	Content   []byte
	UpdatedAt time.Time
}

// LatestSnapshot returns the most recently stored snapshot of the session.
// ok is false when the session has no snapshot to restore.
func (s *Store) LatestSnapshot(sessionID string) (snap FileSnapshot, ok bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureLocked(); err != nil {
		return FileSnapshot{}, false, err
	}
	var (
		path, updatedRaw string
		version          int
		content          []byte
	)
	row := s.db.QueryRow(`SELECT path, version, content, updated_at FROM files
		WHERE session_id = ? ORDER BY updated_at DESC, version DESC LIMIT 1`, sessionID)
	switch err := row.Scan(&path, &version, &content, &updatedRaw); {
	case errors.Is(err, sql.ErrNoRows):
		return FileSnapshot{}, false, nil
	case err != nil:
		return FileSnapshot{}, false, err
	}
	updated, _ := parseTime(updatedRaw)
	return FileSnapshot{Path: path, Version: version, Content: content, UpdatedAt: updated}, true, nil
}

// LoadTodos returns the stored todo list JSON for a session, or "[]" when the
// session is unknown or has none.
func (s *Store) LoadTodos(sessionID string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureLocked(); err != nil {
		return "[]"
	}
	var todos string
	if err := s.db.QueryRow(`SELECT todos FROM sessions WHERE id = ?`, sessionID).Scan(&todos); err != nil {
		return "[]"
	}
	return nonEmptyTodos(todos)
}

// SaveTodos replaces the stored todo list JSON for a session.
func (s *Store) SaveTodos(sessionID, todosJSON string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureLocked(); err != nil {
		return err
	}
	_, err := s.db.Exec(`UPDATE sessions SET todos = ?, updated_at = ? WHERE id = ?`,
		nonEmptyTodos(todosJSON), formatTime(time.Now()), sessionID)
	return err
}

func (s *Store) ensureLocked() error {
	if s.db != nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(s.dbPath), 0700); err != nil {
		return err
	}
	db, err := sql.Open("sqlite", s.dbPath)
	if err != nil {
		return err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)
	s.db = db
	if err := s.initSchemaLocked(); err != nil {
		_ = db.Close()
		s.db = nil
		return err
	}
	return nil
}

func (s *Store) initSchemaLocked() error {
	if err := s.applyPragmasLocked(); err != nil {
		return err
	}
	if err := s.renameLegacySessionsTableLocked(); err != nil {
		return err
	}
	for _, stmt := range schemaStatements() {
		if _, err := s.db.Exec(stmt); err != nil {
			return err
		}
	}
	if err := s.dropRetiredTriggersLocked(); err != nil {
		return err
	}
	if err := s.importLegacyDBRowsLocked(); err != nil {
		return err
	}
	return s.importLegacyJSONOnceLocked()
}

// dropRetiredTriggersLocked removes the message_count triggers installed by
// older builds. They recomputed COUNT(*) over the whole session on every row
// write, which made saving a long transcript quadratic. Callers that write
// messages now maintain message_count directly.
func (s *Store) dropRetiredTriggersLocked() error {
	for _, name := range []string{
		"messages_after_insert_count",
		"messages_after_delete_count",
		"messages_after_update_session_count",
	} {
		if _, err := s.db.Exec(`DROP TRIGGER IF EXISTS ` + name); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) applyPragmasLocked() error {
	stmts := []string{
		`PRAGMA page_size = 4096`,
		`PRAGMA journal_mode = WAL`,
		`PRAGMA temp_store = MEMORY`,
		`PRAGMA busy_timeout = 30000`,
		`PRAGMA foreign_keys = ON`,
	}
	for _, stmt := range stmts {
		if _, err := s.db.Exec(stmt); err != nil {
			return err
		}
	}
	return nil
}

func schemaStatements() []string {
	return []string{
		`CREATE TABLE IF NOT EXISTS meta (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS sessions (
			id TEXT PRIMARY KEY,
			parent_session_id TEXT,
			title TEXT NOT NULL,
			message_count INTEGER NOT NULL DEFAULT 0,
			prompt_tokens INTEGER NOT NULL DEFAULT 0,
			completion_tokens INTEGER NOT NULL DEFAULT 0,
			cost REAL NOT NULL DEFAULT 0,
			updated_at TEXT NOT NULL,
			created_at TEXT NOT NULL,
			summary_message_id TEXT,
			todos TEXT NOT NULL DEFAULT '[]',
			FOREIGN KEY(parent_session_id) REFERENCES sessions(id) ON DELETE SET NULL
		)`,
		`CREATE TABLE IF NOT EXISTS messages (
			id TEXT PRIMARY KEY,
			session_id TEXT NOT NULL,
			role TEXT NOT NULL,
			parts TEXT NOT NULL,
			model TEXT NOT NULL DEFAULT '',
			provider TEXT NOT NULL DEFAULT '',
			position INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			finished_at TEXT,
			is_summary_message INTEGER NOT NULL DEFAULT 0,
			FOREIGN KEY(session_id) REFERENCES sessions(id) ON DELETE CASCADE,
			UNIQUE(session_id, position)
		)`,
		`CREATE TABLE IF NOT EXISTS files (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			session_id TEXT NOT NULL,
			path TEXT NOT NULL,
			version INTEGER NOT NULL,
			content BLOB,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			FOREIGN KEY(session_id) REFERENCES sessions(id) ON DELETE CASCADE,
			UNIQUE(path, session_id, version)
		)`,
		`CREATE TABLE IF NOT EXISTS read_files (
			session_id TEXT NOT NULL,
			path TEXT NOT NULL,
			created_at TEXT NOT NULL,
			PRIMARY KEY(session_id, path),
			FOREIGN KEY(session_id) REFERENCES sessions(id) ON DELETE CASCADE
		)`,
		`CREATE INDEX IF NOT EXISTS idx_sessions_updated_at ON sessions(updated_at)`,
		`CREATE INDEX IF NOT EXISTS idx_sessions_parent ON sessions(parent_session_id)`,
		`CREATE INDEX IF NOT EXISTS idx_messages_session_position ON messages(session_id, position)`,
		`CREATE INDEX IF NOT EXISTS idx_messages_session_created ON messages(session_id, created_at)`,
		`CREATE INDEX IF NOT EXISTS idx_files_session ON files(session_id)`,
		`CREATE INDEX IF NOT EXISTS idx_read_files_session ON read_files(session_id)`,
		`CREATE TRIGGER IF NOT EXISTS sessions_after_update_touch
			AFTER UPDATE ON sessions
			FOR EACH ROW
			WHEN NEW.updated_at = OLD.updated_at
			BEGIN
				UPDATE sessions SET updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now') WHERE id = NEW.id;
			END`,
		`CREATE TRIGGER IF NOT EXISTS messages_after_update_touch
			AFTER UPDATE ON messages
			FOR EACH ROW
			WHEN NEW.updated_at = OLD.updated_at
			BEGIN
				UPDATE messages SET updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now') WHERE id = NEW.id;
			END`,
	}
}

func (s *Store) saveLocked(sess *Session) error {
	if sess.CreatedAt.IsZero() {
		sess.CreatedAt = time.Now()
	}
	if sess.UpdatedAt.IsZero() {
		sess.UpdatedAt = time.Now()
	}
	if strings.TrimSpace(sess.Todos) == "" {
		sess.Todos = "[]"
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	if err := s.saveTx(tx, sess); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return nil
}

func (s *Store) saveTx(tx *sql.Tx, sess *Session) error {
	_, err := tx.Exec(`INSERT INTO sessions (
			id, parent_session_id, title, message_count, prompt_tokens, completion_tokens, cost,
			updated_at, created_at, summary_message_id, todos
		) VALUES (?, NULLIF(?, ''), ?, ?, ?, ?, ?, ?, ?, NULLIF(?, ''), ?)
		ON CONFLICT(id) DO UPDATE SET
			parent_session_id = excluded.parent_session_id,
			title = excluded.title,
			prompt_tokens = excluded.prompt_tokens,
			completion_tokens = excluded.completion_tokens,
			cost = excluded.cost,
			updated_at = excluded.updated_at,
			created_at = excluded.created_at,
			summary_message_id = excluded.summary_message_id,
			todos = excluded.todos`,
		sess.ID,
		sess.ParentSessionID,
		nonEmptyTitle(sess.Title),
		len(sess.Messages),
		sess.PromptTokens,
		sess.CompletionTokens,
		sess.Cost,
		formatTime(sess.UpdatedAt),
		formatTime(sess.CreatedAt),
		sess.SummaryMessageID,
		nonEmptyTodos(sess.Todos),
	)
	if err != nil {
		return err
	}
	if err := s.writeMessagesTx(tx, sess); err != nil {
		return err
	}
	// message_count is maintained here rather than by an AFTER INSERT trigger,
	// which forced a COUNT(*) over the whole session for every single row.
	// updated_at is only bumped when the count actually changed so repeated
	// streaming saves do not keep refreshing it. The unchanged case issues no
	// UPDATE at all: the sessions_after_update_touch trigger fires on any update
	// that leaves updated_at alone, which would silently refresh the timestamp
	// and keep expired sessions alive.
	messageCount := len(sess.Messages)
	existingCount := -1
	// The upsert above never rewrites an existing message_count, so this still
	// observes the previous value (the insert path already stored the new one).
	if err := tx.QueryRow(`SELECT message_count FROM sessions WHERE id = ?`, sess.ID).Scan(&existingCount); err != nil {
		return err
	}
	if messageCount != existingCount {
		_, err = tx.Exec(`UPDATE sessions SET message_count = ?, updated_at = ? WHERE id = ?`,
			messageCount, formatTime(sess.UpdatedAt), sess.ID)
	}
	return err
}

// writeMessagesTx persists the session's message list incrementally. Messages
// are keyed by (session_id, position) and the store rewrites only the rows that
// actually changed, so appending a turn during streaming no longer deletes and
// re-inserts the whole transcript. Positions that disappeared (a compact, an
// undo, a restored snapshot) are pruned so the stored transcript still mirrors
// the in-memory one exactly.
func (s *Store) writeMessagesTx(tx *sql.Tx, sess *Session) error {
	existing := map[int]messageRowState{}
	rows, err := tx.Query(`SELECT position, id, role, parts FROM messages WHERE session_id = ?`, sess.ID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var state messageRowState
		if err := rows.Scan(&state.position, &state.id, &state.role, &state.parts); err != nil {
			rows.Close()
			return err
		}
		existing[state.position] = state
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()

	summaryID := sess.SummaryMessageID
	for i, msg := range sess.Messages {
		parts, err := encodeMessageParts(msg)
		if err != nil {
			return err
		}
		msgID := messageID(sess.ID, i)
		isSummary := isCompactSummaryMessage(msg)
		if isSummary && summaryID == "" {
			summaryID = msgID
		}
		role := string(msg.Role)
		if state, ok := existing[i]; ok && state.id == msgID && state.role == role && state.parts == parts {
			delete(existing, i)
			continue
		}
		delete(existing, i)

		createdRaw := formatTime(sess.CreatedAt.Add(time.Duration(i) * time.Millisecond))
		_, err = tx.Exec(`INSERT INTO messages (
				id, session_id, role, parts, model, provider, position,
				created_at, updated_at, finished_at, is_summary_message
			) VALUES (?, ?, ?, ?, '', '', ?, ?, ?, ?, ?)
			ON CONFLICT(id) DO UPDATE SET
				role = excluded.role,
				parts = excluded.parts,
				position = excluded.position,
				updated_at = excluded.updated_at,
				is_summary_message = excluded.is_summary_message`,
			msgID,
			sess.ID,
			role,
			parts,
			i,
			createdRaw,
			createdRaw,
			createdRaw,
			boolInt(isSummary),
		)
		if err != nil {
			return err
		}
	}

	// Anything left in existing is a position the session no longer has.
	for position := range existing {
		if _, err := tx.Exec(`DELETE FROM messages WHERE session_id = ? AND position = ?`, sess.ID, position); err != nil {
			return err
		}
	}

	if summaryID != sess.SummaryMessageID {
		if _, err := tx.Exec(`UPDATE sessions SET summary_message_id = NULLIF(?, '') WHERE id = ?`, summaryID, sess.ID); err != nil {
			return err
		}
		sess.SummaryMessageID = summaryID
	}
	return nil
}

// messageRowState is the stored form of one message row, used to decide whether
// a rewrite is actually needed.
type messageRowState struct {
	position int
	id       string
	role     string
	parts    string
}

func (s *Store) loadLocked(id string) (*Session, error) {
	var sess Session
	var parent, summary sql.NullString
	var createdRaw, updatedRaw, todos string
	err := s.db.QueryRow(`SELECT id, parent_session_id, title, prompt_tokens, completion_tokens, cost, updated_at, created_at, summary_message_id, todos FROM sessions WHERE id = ?`, id).
		Scan(&sess.ID, &parent, &sess.Title, &sess.PromptTokens, &sess.CompletionTokens, &sess.Cost, &updatedRaw, &createdRaw, &summary, &todos)
	if err != nil {
		return nil, err
	}
	if parent.Valid {
		sess.ParentSessionID = parent.String
	}
	if summary.Valid {
		sess.SummaryMessageID = summary.String
	}
	sess.Todos = todos
	createdAt, err := parseTime(createdRaw)
	if err != nil {
		return nil, err
	}
	updatedAt, err := parseTime(updatedRaw)
	if err != nil {
		return nil, err
	}
	sess.CreatedAt = createdAt
	sess.UpdatedAt = updatedAt

	rows, err := s.db.Query(`SELECT role, parts FROM messages WHERE session_id = ? ORDER BY position ASC, created_at ASC, id ASC`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var roleRaw, partsRaw string
		if err := rows.Scan(&roleRaw, &partsRaw); err != nil {
			return nil, err
		}
		msg, err := decodeMessageParts(agent.Role(roleRaw), partsRaw)
		if err != nil {
			return nil, err
		}
		sess.Messages = append(sess.Messages, msg)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return &sess, nil
}

func (s *Store) archiveExpiredLocked(now time.Time) error {
	_, err := s.archiveExpiredCountLocked(now)
	return err
}

func (s *Store) archiveExpiredCountLocked(now time.Time) (int, error) {
	cutoff := now.Add(-s.retention)
	rows, err := s.db.Query(`SELECT id FROM sessions WHERE updated_at < ? ORDER BY updated_at ASC`, formatTime(cutoff))
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return 0, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}

	archived := 0
	for _, id := range ids {
		sess, err := s.loadLocked(id)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			return archived, err
		}
		if _, err := s.writeArchiveLocked(sess); err != nil {
			return archived, err
		}
		tx, err := s.db.Begin()
		if err != nil {
			return archived, err
		}
		if err := deleteSessionTx(tx, id); err != nil {
			_ = tx.Rollback()
			return archived, err
		}
		if err := tx.Commit(); err != nil {
			return archived, err
		}
		s.broker.Publish(Event{Type: EventDeleted, SessionID: id})
		archived++
	}
	return archived, nil
}

func deleteSessionTx(tx *sql.Tx, id string) error {
	if _, err := tx.Exec(`DELETE FROM messages WHERE session_id = ?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM files WHERE session_id = ?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM read_files WHERE session_id = ?`, id); err != nil {
		return err
	}
	_, err := tx.Exec(`DELETE FROM sessions WHERE id = ?`, id)
	return err
}

func (s *Store) writeArchiveLocked(sess *Session) (string, error) {
	if err := os.MkdirAll(s.archiveDir, 0700); err != nil {
		return "", err
	}
	data, err := json.MarshalIndent(sess, "", "  ")
	if err != nil {
		return "", err
	}
	data = append(data, '\n')
	path := s.path(sess.ID)
	return path, os.WriteFile(path, data, 0600)
}

func (s *Store) renameLegacySessionsTableLocked() error {
	hasMessagesJSON, err := s.tableHasColumnLocked("sessions", "messages_json")
	if err != nil || !hasMessagesJSON {
		return err
	}
	if exists, err := s.tableExistsLocked("legacy_sessions_json"); err != nil {
		return err
	} else if exists {
		return nil
	}
	_, err = s.db.Exec(`ALTER TABLE sessions RENAME TO legacy_sessions_json`)
	return err
}

func (s *Store) importLegacyDBRowsLocked() error {
	exists, err := s.tableExistsLocked("legacy_sessions_json")
	if err != nil || !exists {
		return err
	}
	rows, err := s.db.Query(`SELECT id, title, created_at, updated_at, messages_json FROM legacy_sessions_json ORDER BY updated_at ASC`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var sess Session
		var createdRaw, updatedRaw, messagesRaw string
		if err := rows.Scan(&sess.ID, &sess.Title, &createdRaw, &updatedRaw, &messagesRaw); err != nil {
			return err
		}
		createdAt, err := parseTime(createdRaw)
		if err != nil {
			createdAt = time.Now()
		}
		updatedAt, err := parseTime(updatedRaw)
		if err != nil {
			updatedAt = createdAt
		}
		sess.CreatedAt = createdAt
		sess.UpdatedAt = updatedAt
		sess.Todos = "[]"
		_ = json.Unmarshal([]byte(messagesRaw), &sess.Messages)
		tx, err := s.db.Begin()
		if err != nil {
			return err
		}
		if err := s.saveTx(tx, &sess); err != nil {
			_ = tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	_, err = s.db.Exec(`DROP TABLE legacy_sessions_json`)
	return err
}

func (s *Store) importLegacyJSONOnceLocked() error {
	var value string
	err := s.db.QueryRow(`SELECT value FROM meta WHERE key = ?`, legacyImportMetaKey).Scan(&value)
	if err == nil && value == "1" {
		return nil
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err := s.importLegacyJSONLocked(); err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO meta (key, value) VALUES (?, '1')
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, legacyImportMetaKey)
	return err
}

func (s *Store) importLegacyJSONLocked() error {
	entries, err := os.ReadDir(s.archiveDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(s.archiveDir, entry.Name()))
		if err != nil {
			continue
		}
		var sess Session
		if err := json.Unmarshal(data, &sess); err != nil {
			continue
		}
		if sess.ID == "" {
			sess.ID = strings.TrimSuffix(entry.Name(), ".json")
		}
		if sess.CreatedAt.IsZero() {
			sess.CreatedAt = time.Now()
		}
		if sess.UpdatedAt.IsZero() {
			sess.UpdatedAt = sess.CreatedAt
		}
		if sess.Title == "" {
			sess.Title = "new session"
		}
		if sess.Todos == "" {
			sess.Todos = "[]"
		}
		if err := s.insertLegacyLocked(&sess); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) insertLegacyLocked(sess *Session) error {
	return s.saveLocked(sess)
}

func (s *Store) tableExistsLocked(name string) (bool, error) {
	var got string
	err := s.db.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`, name).Scan(&got)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

func (s *Store) tableHasColumnLocked(table, column string) (bool, error) {
	exists, err := s.tableExistsLocked(table)
	if err != nil || !exists {
		return false, err
	}
	rows, err := s.db.Query(`PRAGMA table_info(` + quoteIdent(table) + `)`)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull int
		var defaultValue any
		var pk int
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &pk); err != nil {
			return false, err
		}
		if name == column {
			return true, nil
		}
	}
	return false, rows.Err()
}

func (s *Store) sessionExistsLocked(id string) bool {
	var one int
	err := s.db.QueryRow(`SELECT 1 FROM sessions WHERE id = ?`, id).Scan(&one)
	return err == nil
}

func encodeMessageParts(msg agent.Message) (string, error) {
	parts := make([]ContentPart, 0, 1+len(msg.ToolCalls))
	if msg.Content != "" || len(msg.ToolCalls) == 0 {
		part, err := makePart("text", textPartData{Text: msg.Content})
		if err != nil {
			return "", err
		}
		parts = append(parts, part)
	}
	for _, call := range msg.ToolCalls {
		part, err := makePart("tool_use", toolUsePartData{ID: call.ID, Name: call.Name, Arguments: call.Arguments})
		if err != nil {
			return "", err
		}
		parts = append(parts, part)
	}
	if msg.Role == agent.RoleTool {
		parts = parts[:0]
		part, err := makePart("tool_result", toolResultPartData{ToolCallID: msg.ToolCallID, Content: msg.Content})
		if err != nil {
			return "", err
		}
		parts = append(parts, part)
	}
	data, err := json.Marshal(parts)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func decodeMessageParts(role agent.Role, raw string) (agent.Message, error) {
	msg := agent.Message{Role: role}
	var parts []ContentPart
	if err := json.Unmarshal([]byte(raw), &parts); err != nil {
		return msg, err
	}
	var textParts []string
	for _, part := range parts {
		switch part.Type {
		case "text", "reasoning", "finish", "shell_command", "image_url", "binary":
			var data textPartData
			if err := json.Unmarshal(part.Data, &data); err == nil && data.Text != "" {
				textParts = append(textParts, data.Text)
			}
		case "tool_use":
			var data toolUsePartData
			if err := json.Unmarshal(part.Data, &data); err != nil {
				return msg, err
			}
			msg.ToolCalls = append(msg.ToolCalls, agent.ToolCall{ID: data.ID, Name: data.Name, Arguments: data.Arguments})
		case "tool_result":
			var data toolResultPartData
			if err := json.Unmarshal(part.Data, &data); err != nil {
				return msg, err
			}
			msg.ToolCallID = data.ToolCallID
			if data.Content != "" {
				textParts = append(textParts, data.Content)
			}
		}
	}
	msg.Content = strings.Join(textParts, "")
	return msg, nil
}

func makePart(typ string, data any) (ContentPart, error) {
	raw, err := json.Marshal(data)
	if err != nil {
		return ContentPart{}, err
	}
	return ContentPart{Type: typ, Data: raw}, nil
}

func messageID(sessionID string, position int) string {
	return fmt.Sprintf("%s:%06d", sessionID, position)
}

func boolInt(ok bool) int {
	if ok {
		return 1
	}
	return 0
}

func (s *Store) path(id string) string {
	return filepath.Join(s.archiveDir, id+".json")
}

func formatTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}

func parseTime(raw string) (time.Time, error) {
	return time.Parse(time.RFC3339Nano, raw)
}

func nonEmptyTitle(title string) string {
	if strings.TrimSpace(title) == "" {
		return "new session"
	}
	return title
}

func nonEmptyTodos(todos string) string {
	if strings.TrimSpace(todos) == "" {
		return "[]"
	}
	return todos
}

func quoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

func isCompactSummaryMessage(msg agent.Message) bool {
	return msg.Role == agent.RoleUser && strings.Contains(msg.Content, "<conversation_summary>")
}

// generateID produces a sortable timestamp-based ID with a short random
// suffix to avoid collisions within the same second.
func generateID(now time.Time) string {
	return now.Format("20060102-150405") + "-" + randomSuffix(8)
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
