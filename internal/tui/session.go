package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/wislist/mini-opencode/internal/agent"
	"github.com/wislist/mini-opencode/internal/session"
)

// SessionManager owns the session store and provides create/switch/save
// operations. The TUI calls these to implement /newsession and /session.

const sessionFlushDebounce = 33 * time.Millisecond

type SessionManager interface {
	CreateSession(title string) *session.Session
	SwitchSession(id string) (*session.Session, error)
	SaveCurrentSession(messages []agent.Message)
	ListSessions() ([]session.Meta, error)
	CurrentSession() *session.Session
}

// sessionsLoadedMsg carries the result of an async session list query.
type sessionsLoadedMsg struct {
	metas []session.Meta
	err   error
}

// sessionSwitchedMsg carries the result of an async session load.
type sessionSwitchedMsg struct {
	sess *session.Session
	err  error
}

// handleSessionListKey processes key events in the session-picker overlay.
func (m *Model) handleSessionListKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyUp, tea.KeyShiftTab:
		if m.sessionCursor > 0 {
			m.sessionCursor--
		}
		m.refreshViewport()
	case tea.KeyDown, tea.KeyTab:
		if m.sessionCursor < len(m.sessionList)-1 {
			m.sessionCursor++
		}
		m.refreshViewport()
	case tea.KeyEnter:
		if len(m.sessionList) == 0 {
			m.state = stateIdle
			m.refreshViewport()
			return m, textinput.Blink
		}
		id := m.sessionList[m.sessionCursor].ID
		return m, m.switchSessionCmd(id)
	case tea.KeyEsc, tea.KeyCtrlC:
		m.state = stateIdle
		m.refreshViewport()
		return m, textinput.Blink
	}
	return m, nil
}

// handleNewSession saves the current session (if any), creates a fresh one,
// resets the runtime, and clears the transcript.
func (m *Model) handleNewSession() (tea.Model, tea.Cmd) {
	if m.sessions == nil {
		m.addBlock(errorStyle.Render("sessions not configured"))
		m.refreshViewport()
		return m, nil
	}
	m.saveCurrentSession()
	m.cancelPendingSessionFlush()
	m.currentSession = m.sessions.Create("new session")
	if m.runtime != nil {
		m.runtime.SetMessages(nil)
	}
	m.blocks = nil
	m.streamingIdx = -1
	m.streamingText = ""
	m.todos = ""
	m.addBlock(dimStyle.Render("new session started"))
	m.gitStatus = collectGitStatus(m.workingDir)
	m.refreshViewport()
	// Clearing the model content is not always enough after a long transcript:
	// terminal-side wrapping may have scrolled the alternate screen, leaving
	// old rows above the newly rendered header. Force the renderer back to the
	// top-left and clear those rows before drawing the fresh session.
	return m, tea.ClearScreen
}

// handleListSessions loads the session list asynchronously and enters the
// session-picker state.
func (m *Model) handleListSessions() (tea.Model, tea.Cmd) {
	if m.sessions == nil {
		m.addBlock(errorStyle.Render("sessions not configured"))
		m.refreshViewport()
		return m, nil
	}
	store := m.sessions
	go func() {
		metas, err := store.List()
		m.program.Send(sessionsLoadedMsg{metas: metas, err: err})
	}()
	m.addBlock(dimStyle.Render("loading sessions..."))
	m.refreshViewport()
	return m, nil
}

// switchSessionCmd returns a command that loads the selected session and
// restores it into the runtime.
func (m *Model) switchSessionCmd(id string) tea.Cmd {
	store := m.sessions
	return func() tea.Msg {
		sess, err := store.Load(id)
		return sessionSwitchedMsg{sess: sess, err: err}
	}
}

// handleArchiveSession exports the current session to JSON, removes it from
// active SQLite storage, and starts a fresh conversation.
func (m *Model) handleArchiveSession() (tea.Model, tea.Cmd) {
	if m.sessions == nil {
		m.addBlock(errorStyle.Render("sessions not configured"))
		m.refreshViewport()
		return m, nil
	}
	if m.currentSession == nil {
		m.addBlock(errorStyle.Render("no current session"))
		m.refreshViewport()
		return m, nil
	}
	if err := m.saveCurrentSessionErr(); err != nil {
		m.addBlock(errorStyle.Render("✗ archive: " + err.Error()))
		m.refreshViewport()
		return m, nil
	}
	path, err := m.sessions.Archive(m.currentSession.ID)
	if err != nil {
		m.addBlock(errorStyle.Render("✗ archive: " + err.Error()))
		m.refreshViewport()
		return m, nil
	}

	archivedTitle := m.currentSession.Title
	m.cancelPendingSessionFlush()
	m.currentSession = m.sessions.Create("new session")
	if m.runtime != nil {
		m.runtime.SetMessages(nil)
	}
	m.blocks = nil
	m.streamingIdx = -1
	m.streamingText = ""
	m.todos = ""
	m.addBlock(toolArrow.Render("archived session: " + archivedTitle))
	m.addBlock(dimStyle.Render(path))
	m.addBlock(dimStyle.Render("new session started"))
	m.gitStatus = collectGitStatus(m.workingDir)
	m.refreshViewport()
	return m, tea.ClearScreen
}

// handleForkSession branches the active session into a new one and switches to
// it, so the conversation continues on a copy while the original is preserved.
func (m *Model) handleForkSession() (tea.Model, tea.Cmd) {
	if m.sessions == nil || m.currentSession == nil {
		m.addBlock(errorStyle.Render("sessions not configured"))
		m.refreshViewport()
		return m, nil
	}
	m.saveCurrentSession()
	fork, err := m.sessions.Fork(m.currentSession.ID, "")
	if err != nil {
		m.addBlock(errorStyle.Render("✗ fork: " + err.Error()))
		m.refreshViewport()
		return m, nil
	}
	m.currentSession = fork
	if m.runtime != nil {
		m.runtime.SetMessages(fork.Messages)
		m.runtime.SetUsage(agent.Usage{
			PromptTokens:     int(fork.PromptTokens),
			CompletionTokens: int(fork.CompletionTokens),
			TotalTokens:      int(fork.PromptTokens + fork.CompletionTokens),
		})
	}
	m.todos = m.todosForSession(fork.ID)
	m.blocks = nil
	m.streamingIdx = -1
	m.streamingText = ""
	m.addBlock(toolArrow.Render("branched into " + fork.Title))
	m.renderHistoryIntoBlocks(fork.Messages)
	m.gitStatus = collectGitStatus(m.workingDir)
	m.refreshViewport()
	return m, tea.ClearScreen
}

// saveCurrentSession persists the current runtime messages to the active
// session. It auto-titles untitled sessions from the first user message.
func (m *Model) saveCurrentSession() {
	_ = m.saveCurrentSessionErr()
}

func (m *Model) saveCurrentSessionErr() error {
	m.cancelPendingSessionFlush()
	if m.sessions == nil || m.currentSession == nil || m.runtime == nil {
		return nil
	}
	msgs := m.runtime.Messages()
	m.currentSession.Messages = msgs
	usage := m.runtime.Usage()
	m.currentSession.PromptTokens = int64(usage.PromptTokens)
	m.currentSession.CompletionTokens = int64(usage.CompletionTokens)
	if m.currentSession.Title == "new session" {
		for _, msg := range msgs {
			if msg.Role == agent.RoleUser && !isCompactSummaryMessage(msg) {
				m.currentSession.Title = session.TitleFromMessage(msg.Content)
				break
			}
		}
	}
	return m.sessions.Save(m.currentSession)
}

// queueSessionFlush persists streaming state with a Crush-style 33ms debounce.
// Final events call saveCurrentSessionErr(), which cancels any pending debounced
// write and flushes the authoritative runtime history synchronously.
func (m *Model) queueSessionFlush(extra []agent.Message) {
	snap := m.sessionSnapshot(extra)
	if snap == nil {
		return
	}
	m.sessionFlushMu.Lock()
	m.sessionFlushSnapshot = snap
	if m.sessionFlushTimer == nil {
		m.sessionFlushTimer = time.AfterFunc(sessionFlushDebounce, m.flushPendingSessionSnapshot)
	} else {
		m.sessionFlushTimer.Reset(sessionFlushDebounce)
	}
	m.sessionFlushMu.Unlock()
}

func (m *Model) flushPendingSessionSnapshot() {
	m.sessionFlushMu.Lock()
	snap := m.sessionFlushSnapshot
	m.sessionFlushSnapshot = nil
	m.sessionFlushTimer = nil
	m.sessionFlushMu.Unlock()
	if snap != nil && m.sessions != nil {
		_ = m.sessions.Save(snap)
	}
}

func (m *Model) cancelPendingSessionFlush() {
	m.sessionFlushMu.Lock()
	if m.sessionFlushTimer != nil {
		m.sessionFlushTimer.Stop()
		m.sessionFlushTimer = nil
	}
	m.sessionFlushSnapshot = nil
	m.sessionFlushMu.Unlock()
}

func (m *Model) sessionSnapshot(extra []agent.Message) *session.Session {
	if m.sessions == nil || m.currentSession == nil || m.runtime == nil {
		return nil
	}
	snap := *m.currentSession
	msgs := m.runtime.Messages()
	if len(extra) > 0 {
		msgs = append(msgs, extra...)
	}
	snap.Messages = msgs
	if snap.Title == "new session" {
		for _, msg := range msgs {
			if msg.Role == agent.RoleUser && !isCompactSummaryMessage(msg) {
				snap.Title = session.TitleFromMessage(msg.Content)
				break
			}
		}
	}
	return &snap
}

// renderHistoryIntoBlocks rebuilds the transcript blocks from saved messages
// so a restored session shows its prior conversation.
func (m *Model) renderHistoryIntoBlocks(messages []agent.Message) {
	for _, msg := range messages {
		switch msg.Role {
		case agent.RoleUser:
			if isCompactSummaryMessage(msg) {
				m.addBlock(toolArrow.Render("⟳ previous context compacted"))
				continue
			}
			m.addBlock(m.renderUserMessage(msg.Content))
		case agent.RoleAssistant:
			if msg.Content != "" {
				m.addBlock(m.renderAssistantMessage(msg.Content))
			}
			for _, call := range msg.ToolCalls {
				cc := call
				m.addBlock(m.renderToolCall(&cc))
			}
		case agent.RoleTool:
			content := msg.Content
			if len(content) > 200 {
				content = content[:200] + "..."
			}
			arrowW := 2                                            // "→ " visual width
			wrapped := wordWrap(content, max(1, m.width-arrowW-2)) // -2 for toolArrow padding
			m.addBlock(toolArrow.Render("→ " + wrapped))
		}
	}
}

// renderSessionSegment renders the current session title for the header.
func (m *Model) renderSessionSegment() string {
	if m.currentSession == nil {
		return ""
	}
	return sessionStyle.Render(" " + truncateTitle(m.currentSession.Title, 30))
}

// renderSessionList renders the session picker overlay.
func (m *Model) renderSessionList() string {
	if len(m.sessionList) == 0 {
		w := boxWidth(m)
		inner := max(1, w-4)
		content := wordWrap(dimStyle.Render("no saved sessions"), inner) + "\n" + wordWrap(dimStyle.Render("esc to go back"), inner)
		return permBox.Width(w).Render(content)
	}
	var lines []string
	lines = append(lines, keyLabel.Render("sessions:")+"  "+dimStyle.Render("↑↓ select · enter to open · esc to cancel"))
	for i, meta := range m.sessionList {
		marker := "  "
		title := truncateTitle(meta.Title, max(10, m.width-30))
		if i == m.sessionCursor {
			marker = "▶ "
			title = toolName.Render(title)
		}
		prefix := ""
		if meta.ParentSessionID != "" {
			// A branched session keeps its parent visible in the list.
			prefix = dimStyle.Render("↳ ")
		}
		info := dimStyle.Render(fmt.Sprintf("  %d msgs · %s", meta.MessageN, meta.UpdatedAt.Format("2006-01-02 15:04")))
		title = prefix + title
		lines = append(lines, marker+title+info)
	}
	w := boxWidth(m)
	inner := max(1, w-4)
	for i, line := range lines {
		lines[i] = wordWrap(line, inner)
	}
	return sessionBox.Width(w).Render(strings.Join(lines, "\n"))
}

// truncateTitle shortens a session title for the header display.
func truncateTitle(title string, maxRunes int) string {
	r := []rune(title)
	if len(r) <= maxRunes {
		return title
	}
	return string(r[:maxRunes-1]) + "…"
}

func isCompactSummaryMessage(msg agent.Message) bool {
	return msg.Role == agent.RoleUser && strings.Contains(msg.Content, "<conversation_summary>")
}
