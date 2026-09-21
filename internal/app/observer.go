package app

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/wislist/mini-opencode/internal/agent"
	"github.com/wislist/mini-opencode/internal/diffutil"
	"github.com/wislist/mini-opencode/internal/session"
)

// sessionFileObserver implements tools.FileObserver using the session store:
// reads are recorded in read_files and pre-modification content is kept in the
// files table so a session can restore what an edit replaced.
//
// The session id is resolved through a callback because the active session can
// change (new session, switch, fork, archive) while the same tool set stays
// registered on the runtime.
type sessionFileObserver struct {
	store     *session.Store
	sessionID func() string
}

func newSessionFileObserver(store *session.Store, sessionID func() string) *sessionFileObserver {
	if store == nil || sessionID == nil {
		return nil
	}
	return &sessionFileObserver{store: store, sessionID: sessionID}
}

func (o *sessionFileObserver) RecordRead(path string) error {
	if o == nil {
		return nil
	}
	id := o.sessionID()
	if id == "" {
		return nil
	}
	return o.store.RecordReadFile(id, path)
}

func (o *sessionFileObserver) HasRead(path string) bool {
	if o == nil {
		return true // no tracking configured: never block a write
	}
	id := o.sessionID()
	if id == "" {
		return true
	}
	return o.store.HasReadFile(id, path)
}

func (o *sessionFileObserver) Snapshot(path string, content []byte) error {
	if o == nil {
		return nil
	}
	id := o.sessionID()
	if id == "" {
		return nil
	}
	return o.store.SnapshotFile(id, path, content)
}

// restoreLatestSnapshot writes the newest stored snapshot of the session back
// to disk and reports the restored path.
func restoreLatestSnapshot(out io.Writer, store *session.Store, sessionID string) error {
	if store == nil || sessionID == "" {
		return fmt.Errorf("no active session to restore")
	}
	snap, ok, err := store.LatestSnapshot(sessionID)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("no snapshot recorded in this session")
	}
	if err := os.WriteFile(snap.Path, snap.Content, 0644); err != nil {
		return err
	}
	fmt.Fprintf(out, "[restored %s (version %d)]\n", snap.Path, snap.Version)
	return nil
}

// sessionTodoStore implements tools.TodoStore for the active session.
type sessionTodoStore struct {
	store     *session.Store
	sessionID func() string
}

func newSessionTodoStore(store *session.Store, sessionID func() string) *sessionTodoStore {
	if store == nil || sessionID == nil {
		return nil
	}
	return &sessionTodoStore{store: store, sessionID: sessionID}
}

func (s *sessionTodoStore) Load() string {
	if s == nil {
		return "[]"
	}
	id := s.sessionID()
	if id == "" {
		return "[]"
	}
	return s.store.LoadTodos(id)
}

func (s *sessionTodoStore) Save(todosJSON string) error {
	if s == nil {
		return nil
	}
	id := s.sessionID()
	if id == "" {
		return nil
	}
	return s.store.SaveTodos(id, todosJSON)
}

// toolDiffPreview renders the change a mutating tool call would make, as a
// plain-text diff for the terminal permission prompt. It returns "" for calls
// that do not modify files.
func toolDiffPreview(call agent.ToolCall, workDir string) string {
	switch call.Name {
	case "edit":
		var args struct {
			Path      string `json:"path"`
			OldString string `json:"old_string"`
			NewString string `json:"new_string"`
		}
		if err := json.Unmarshal(call.Arguments, &args); err != nil || args.OldString == "" {
			return ""
		}
		lines := make([]string, 0, 16)
		for _, line := range strings.Split(strings.TrimRight(args.OldString, "\n"), "\n") {
			lines = append(lines, "- "+line)
		}
		for _, line := range strings.Split(strings.TrimRight(args.NewString, "\n"), "\n") {
			lines = append(lines, "+ "+line)
		}
		return renderDiffPreview(lines, args.Path)
	case "write":
		var args struct {
			Path    string `json:"path"`
			Content string `json:"content"`
		}
		if err := json.Unmarshal(call.Arguments, &args); err != nil || args.Path == "" {
			return ""
		}
		target := args.Path
		if !filepath.IsAbs(target) {
			target = filepath.Join(workDir, target)
		}
		existing, err := os.ReadFile(target)
		if err != nil {
			lines := make([]string, 0, 16)
			for _, line := range strings.Split(strings.TrimRight(args.Content, "\n"), "\n") {
				lines = append(lines, "+ "+line)
			}
			return renderDiffPreview(lines, args.Path+" (new file)")
		}
		return renderDiffPreview(diffutil.LineDiff(string(existing), args.Content), args.Path)
	default:
		return ""
	}
}

// renderDiffPreview caps a diff at 24 lines for a terminal prompt.
func renderDiffPreview(lines []string, label string) string {
	if len(lines) == 0 {
		return ""
	}
	const maxLines = 24
	shown := lines
	if len(shown) > maxLines {
		shown = shown[:maxLines]
	}
	var b strings.Builder
	fmt.Fprintf(&b, "--- %s\n", label)
	for _, line := range shown {
		b.WriteString(line)
		b.WriteByte('\n')
	}
	if len(lines) > len(shown) {
		fmt.Fprintf(&b, "... %d more changed lines\n", len(lines)-len(shown))
	}
	return strings.TrimRight(b.String(), "\n")
}
