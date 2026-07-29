package tui

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/wislist/mini-opencode/internal/agent"
	"github.com/wislist/mini-opencode/internal/config"
	"github.com/wislist/mini-opencode/internal/session"
)

type appState int

const (
	stateIdle appState = iota
	stateRunning
	statePermission
	stateKeyPrompt
	stateQuitting
	stateCompacting
	stateSessionList
)

// InteractionMode toggles between plan (read-only analysis) and code
// (full edit access). Switched with the Tab key.
type InteractionMode int

const (
	// ModeCode allows full tool access including file edits and commands.
	ModeCode InteractionMode = iota
	// ModePlan blocks all non-read-only tools; the agent can only inspect
	// the codebase and propose plans.
	ModePlan
)

func (mode InteractionMode) String() string {
	switch mode {
	case ModePlan:
		return "PLAN"
	default:
		return "CODE"
	}
}

// Messages bridging the synchronous runtime goroutine into Bubble Tea.
type runtimeEventMsg struct{ event agent.Event }
type runtimeDoneMsg struct{ err error }
type permissionRequestMsg struct {
	call   agent.ToolCall
	result *agent.ToolResult
	resp   chan bool
}
type compactDoneMsg struct {
	before      int
	userSummary string
	err         error
}

// Callbacks the TUI needs from app.go.
type KeySaver func(key string) (config.Config, error)

// NameSaver persists custom user/assistant display names and returns the
// updated config.
type NameSaver func(user, assistant string) (config.Config, error)
type RuntimeFactory func(cfg config.Config) (*agent.Runtime, error)

type Model struct {
	viewport viewport.Model
	input    textinput.Model
	spinner  spinner.Model
	keyInput textinput.Model

	state      appState
	runtime    *agent.Runtime
	program    *tea.Program
	cfg        *config.Config
	workingDir string
	version    string

	blocks []string
	width  int
	height int
	// streamingIdx is the index in blocks of the currently streaming
	// assistant message; -1 when not streaming.
	streamingIdx  int
	streamingText string

	gitStatus GitStatus

	sessions       *session.Store
	currentSession *session.Session
	sessionList    []session.Meta
	sessionCursor  int

	commandFiltered []CommandItem
	commandCursor   int

	mode     InteractionMode
	planHook *agent.PlanModeHook

	pendingPerm    *permissionRequestMsg
	keySaver       KeySaver
	nameSaver      NameSaver
	runtimeFactory RuntimeFactory
	compactor      Compactor

	ctx    context.Context
	cancel context.CancelFunc

	nativeCursorMu  sync.RWMutex
	nativeCursorCol int
	nativeCursorRow int
	nativeCursorOK  bool

	sessionFlushMu       sync.Mutex
	sessionFlushTimer    *time.Timer
	sessionFlushSnapshot *session.Session
}

// Compactor summarizes the current conversation context. It returns the
// generated compact result, including a short user-facing summary.
type Compactor func(ctx context.Context) (agent.CompactResult, error)

func New(cfg *config.Config, workingDir, ver string) *Model {
	vp := viewport.New(80, 20)

	ti := textinput.New()
	ti.Placeholder = "ask anything...  (/help for commands)"
	ti.Prompt = ""
	ti.CharLimit = 0
	ti.Focus()

	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = spinnerStyle

	ki := textinput.New()
	ki.Prompt = ""
	ki.EchoMode = textinput.EchoPassword
	ki.CharLimit = 0

	return &Model{
		viewport:     vp,
		input:        ti,
		spinner:      sp,
		keyInput:     ki,
		state:        stateIdle,
		streamingIdx: -1,
		cfg:          cfg,
		workingDir:   workingDir,
		version:      ver,
		mode:         ModeCode,
	}
}

func (m *Model) SetRuntime(rt *agent.Runtime)        { m.runtime = rt }
func (m *Model) Runtime() *agent.Runtime             { return m.runtime }
func (m *Model) SetProgram(p *tea.Program)           { m.program = p }
func (m *Model) SetKeySaver(ks KeySaver)             { m.keySaver = ks }
func (m *Model) SetNameSaver(ns NameSaver)           { m.nameSaver = ns }
func (m *Model) SetRuntimeFactory(rf RuntimeFactory) { m.runtimeFactory = rf }
func (m *Model) SetCompactor(c Compactor)            { m.compactor = c }
func (m *Model) SetSessionStore(s *session.Store)    { m.sessions = s }

// SetPlanHook attaches the agent-side plan mode enforcer. The TUI toggles
// hook.Active when switching modes.
func (m *Model) SetPlanHook(h *agent.PlanModeHook) { m.planHook = h }

// PlanHook returns the agent-side plan mode enforcer, or nil if unset.
func (m *Model) PlanHook() *agent.PlanModeHook { return m.planHook }

// toggleMode switches between plan and code mode, updating the hook state.
func (m *Model) toggleMode() {
	if m.mode == ModeCode {
		m.mode = ModePlan
	} else {
		m.mode = ModeCode
	}
	if m.planHook != nil {
		m.planHook.Active = m.mode == ModePlan
	}
	m.refreshViewport()
}
func (m *Model) MakeConfirmer() agent.PermissionConfirmer {
	return func(ctx context.Context, call agent.ToolCall, result agent.ToolResult) bool {
		resp := make(chan bool, 1)
		m.program.Send(permissionRequestMsg{call: call, result: &result, resp: resp})
		select {
		case <-ctx.Done():
			return false
		case ok := <-resp:
			return ok
		}
	}
}

func (m *Model) Init() tea.Cmd {
	m.addBlock(dimStyle.Render("welcome to mini-opencode") + "\n" +
		dimStyle.Render("type /help for commands, or just start typing."))
	m.gitStatus = collectGitStatus(m.workingDir)
	if m.sessions != nil && m.currentSession == nil {
		m.currentSession = m.sessions.Create("new session")
	}
	return textinput.Blink
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		// Never render into the terminal's final cell. Many terminals wrap as
		// soon as that cell is written; one physical wrap can scroll the whole
		// alternate screen and expose rows from the previous conversation.
		m.width = max(1, msg.Width-1)
		m.height = msg.Height
		m.viewport.Width = m.width
		m.viewport.Height = max(1, msg.Height-5)
		// Reserve the common header + input box + help bar layout. View()
		// recalculates this from the actual footer for menus and prompts.
		// Constrain the textinput so typed text stays within the bordered
		// input bar (border 2 + padding 2 + prompt glyph 2 = 6).
		m.input.Width = max(1, m.width-6)
		m.keyInput.Width = max(1, m.width-6)
		m.refreshViewport()
		return m, nil

	case spinner.TickMsg:
		if m.state == stateRunning || m.state == stateCompacting {
			var cmd tea.Cmd
			m.spinner, cmd = m.spinner.Update(msg)
			cmds = append(cmds, cmd)
		}

	case runtimeEventMsg:
		m.handleRuntimeEvent(msg.event)

	case runtimeDoneMsg:
		m.state = stateIdle
		if msg.err != nil {
			m.addBlock(errorStyle.Render("✗ " + msg.err.Error()))
		}
		m.saveCurrentSession()
		m.gitStatus = collectGitStatus(m.workingDir)
		m.refreshViewport()
		m.input.Focus()
		cmds = append(cmds, textinput.Blink)

	case compactDoneMsg:
		m.state = stateIdle
		if msg.err != nil {
			m.addBlock(errorStyle.Render("✗ compact: " + msg.err.Error()))
		} else {
			m.addBlock(toolArrow.Render(fmt.Sprintf("⟳ context compacted: %d messages -> 1", msg.before)))
			if strings.TrimSpace(msg.userSummary) != "" {
				m.addBlock(m.renderAssistantMessage(msg.userSummary))
			} else {
				m.addBlock(dimStyle.Render("summary saved internally"))
			}
			m.saveCurrentSession()
		}
		m.gitStatus = collectGitStatus(m.workingDir)
		m.refreshViewport()
		m.input.Focus()
		cmds = append(cmds, textinput.Blink)

	case sessionsLoadedMsg:
		if msg.err != nil {
			m.state = stateIdle
			m.addBlock(errorStyle.Render("✗ sessions: " + msg.err.Error()))
			m.refreshViewport()
			m.input.Focus()
			cmds = append(cmds, textinput.Blink)
			break
		}
		m.sessionList = msg.metas
		m.sessionCursor = 0
		m.state = stateSessionList
		m.refreshViewport()

	case sessionSwitchedMsg:
		m.state = stateIdle
		if msg.err != nil {
			m.addBlock(errorStyle.Render("✗ switch session: " + msg.err.Error()))
		} else if msg.sess != nil {
			m.currentSession = msg.sess
			if m.runtime != nil {
				m.runtime.SetMessages(msg.sess.Messages)
			}
			m.blocks = nil
			m.streamingIdx = -1
			m.streamingText = ""
			m.addBlock(dimStyle.Render("session: " + msg.sess.Title))
			m.renderHistoryIntoBlocks(msg.sess.Messages)
			m.gitStatus = collectGitStatus(m.workingDir)
		}
		m.refreshViewport()
		m.input.Focus()
		cmds = append(cmds, textinput.Blink)

	case permissionRequestMsg:
		m.pendingPerm = &msg
		m.state = statePermission
		m.refreshViewport()

	case tea.KeyMsg:
		return m.handleKey(msg)
	}

	var cmd tea.Cmd
	m.viewport, cmd = m.viewport.Update(msg)
	cmds = append(cmds, cmd)

	if m.state == stateIdle || m.state == stateRunning {
		m.input, cmd = m.input.Update(msg)
		cmds = append(cmds, cmd)
	}
	if m.state == stateKeyPrompt {
		m.keyInput, cmd = m.keyInput.Update(msg)
		cmds = append(cmds, cmd)
	}

	return m, tea.Batch(cmds...)
}

// ── helpers ───────────────────────────────────────────

func (m *Model) addBlock(block string) {
	m.blocks = append(m.blocks, block)
}

func (m *Model) refreshViewport() {
	m.viewport.SetContent(strings.Join(m.blocks, "\n\n"))
	m.viewport.GotoBottom()
}
