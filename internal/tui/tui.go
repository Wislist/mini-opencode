package tui

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/wislist/mini-opencode/internal/agent"
	"github.com/wislist/mini-opencode/internal/agent/tools"
	"github.com/wislist/mini-opencode/internal/config"
	"github.com/wislist/mini-opencode/internal/memory"
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
	statePlanApproval
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

// permissionDecision is the user's verdict on a tool permission request.
// always additionally allowlists the tool for the rest of the session.
type permissionDecision struct {
	allow  bool
	always bool
}

type permissionRequestMsg struct {
	call   agent.ToolCall
	result *agent.ToolResult
	resp   chan permissionDecision
}
type compactDoneMsg struct {
	before      int
	userSummary string
	err         error
}

// sessionTitleMsg carries an asynchronously generated session title.
type sessionTitleMsg struct {
	title string
	err   error
}

// planDecision is the user's verdict on a submitted plan.
type planDecision struct {
	approved bool
	feedback string
}

type planApprovalMsg struct {
	plan string
	resp chan planDecision
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

	// terminal records how the host terminal delivers wheel input so scroll
	// steps can be tuned per terminal.
	terminal terminalProfile

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
	// todos is the rendered task list shown above the input bar.
	todos string

	// mcpStatus reports the MCP server states for the /mcp command.
	mcpStatus func() []string
	// snapshotRestorer restores the newest file snapshot of the session.
	snapshotRestorer func() (string, error)
	// titleGenerator asks the provider for a short session title.
	titleGenerator titleGenerator
	// initPromptProvider renders the /init system prompt.
	initPromptProvider func() (string, error)
	// systemPromptRestore holds the prompt to restore once the /init run ends.
	systemPromptRestore string
	// memoryDistiller extracts durable memories after a run completes. Nil
	// disables automatic extraction.
	memoryDistiller *memory.Distiller
	// memoryStore backs the /memory command.
	memoryStore *memory.Store
	// streamRenderPending is set while a throttled repaint of the streaming
	// assistant block is queued; see stream.go for why rendering is throttled.
	streamRenderPending bool
	// transcriptDirty marks the cached viewport content as stale. It defaults
	// to true so the first refresh builds the content, and is set by addBlock
	// and markBlocksChanged. Refreshing when it is false is a cheap no-op,
	// which matters because View() runs on every spinner tick.
	transcriptDirty bool
	// footerDirty marks the memoized footer sections stale; see renderFooter
	// in render.go. It defaults to true so the first render builds them.
	cachedFooter []string
	footerDirty  bool

	pendingPerm *permissionRequestMsg
	// sessionAllowed holds tool names the user approved for the whole session
	// ("always allow"), so repeated prompts for the same tool stop appearing.
	sessionAllowed map[string]bool
	pendingPlan    *planApprovalMsg
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
		viewport: vp,
		input:    ti,
		spinner:  sp,
		keyInput: ki,
		state:    stateIdle,
		// The initial viewport content has never been built, so the first
		// refresh must not be skipped.
		transcriptDirty: true,
		// The footer has never been built, so the first render must not take
		// the cached (nil) path.
		footerDirty:  true,
		streamingIdx: -1,
		cfg:          cfg,
		workingDir:   workingDir,
		version:      ver,
		mode:         ModeCode,
		terminal:     detectTerminalProfile(),
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

// CurrentSessionID returns the active session id, or "" when none exists. It
// lets collaborators (such as the file observer) resolve the live session
// without holding a stale pointer.
func (m *Model) CurrentSessionID() string {
	if m.currentSession == nil {
		return ""
	}
	return m.currentSession.ID
}

// MemoryQuery returns the text that cross-session recall is scored against:
// the first real user message of the active session. It deliberately skips
// compaction summaries and injected todo continuations, which are runtime
// bookkeeping rather than the work being done, so recall keys off what the
// user actually asked for.
func (m *Model) MemoryQuery() string {
	if m.currentSession == nil {
		return ""
	}
	for _, msg := range m.currentSession.Messages {
		if msg.Role != agent.RoleUser {
			continue
		}
		if isCompactSummaryMessage(msg) || agent.IsTodoContinuationText(msg.Content) {
			continue
		}
		if text := strings.TrimSpace(msg.Content); text != "" {
			return text
		}
	}
	// A brand-new session has no transcript yet; fall back to whatever the
	// user has typed so far so recall still has something to work with.
	return strings.TrimSpace(m.input.Value())
}

// SetPlanHook attaches the agent-side plan mode enforcer. The TUI toggles
// hook when switching modes.
func (m *Model) SetPlanHook(h *agent.PlanModeHook) { m.planHook = h }

// onPlanSubmitted records the plan decision in the transcript and refreshes
// the mode badge.
func (m *Model) onPlanSubmitted(event agent.Event) {
	verdict := "rejected"
	if m.mode == ModeCode {
		verdict = "approved"
	}
	m.addBlock(toolArrow.Render("⟳ plan " + verdict + " — mode is now " + m.mode.String()))
	m.refreshViewport()
}

// todosForSession loads and renders the stored task list of a session.
func (m *Model) todosForSession(sessionID string) string {
	if m.sessions == nil || sessionID == "" {
		return ""
	}
	return todoTextFromJSON(m.sessions.LoadTodos(sessionID))
}

// PlanHook returns the agent-side plan mode enforcer, or nil if unset.
func (m *Model) PlanHook() *agent.PlanModeHook { return m.planHook }

// SetMCPStatusProvider attaches a callback that renders MCP server status
// lines for the /mcp command.
func (m *Model) SetMCPStatusProvider(provider func() []string) { m.mcpStatus = provider }

// SetSnapshotRestorer attaches the callback backing /undo.
func (m *Model) SetSnapshotRestorer(restore func() (string, error)) { m.snapshotRestorer = restore }

// SetTitleGenerator attaches the provider-backed session title generator.
func (m *Model) SetTitleGenerator(g titleGenerator) { m.titleGenerator = g }

// SetInitPromptProvider attaches the renderer for the /init system prompt.
func (m *Model) SetInitPromptProvider(provider func() (string, error)) {
	m.initPromptProvider = provider
}

// AddSystemNotice appends a dim informational line to the transcript. It is
// used for startup diagnostics such as a failed MCP server.
func (m *Model) AddSystemNotice(text string) {
	m.addBlock(dimStyle.Render("! " + text))
}

// toggleMode switches between plan and code mode, updating the hook state.
func (m *Model) toggleMode() {
	if m.mode == ModeCode {
		m.mode = ModePlan
	} else {
		m.mode = ModeCode
	}
	if m.planHook != nil {
		m.planHook.SetActive(m.mode == ModePlan)
	}
	m.refreshViewport()
}

// MakePlanApprover returns the approver handed to the exit_plan_mode tool. It
// shows the plan in the TUI, waits for the user's verdict, and turns plan mode
// off when the plan is approved so the run can continue into implementation.
func (m *Model) MakePlanApprover() tools.PlanApprover {
	return planApproverFunc(func(ctx context.Context, plan string) (bool, string) {
		resp := make(chan planDecision, 1)
		m.program.Send(planApprovalMsg{plan: plan, resp: resp})
		select {
		case <-ctx.Done():
			return false, ""
		case decision := <-resp:
			if decision.approved {
				m.mode = ModeCode
				if m.planHook != nil {
					m.planHook.SetActive(false)
				}
			}
			return decision.approved, decision.feedback
		}
	})
}

// planApproverFunc adapts a function to the tools.PlanApprover interface.
type planApproverFunc func(ctx context.Context, plan string) (bool, string)

func (f planApproverFunc) ApprovePlan(ctx context.Context, plan string) (bool, string) {
	return f(ctx, plan)
}

func (m *Model) MakeConfirmer() agent.PermissionConfirmer {
	return func(ctx context.Context, call agent.ToolCall, result agent.ToolResult) bool {
		if m.isSessionAllowed(call.Name) {
			return true
		}
		resp := make(chan permissionDecision, 1)
		m.program.Send(permissionRequestMsg{call: call, result: &result, resp: resp})
		select {
		case <-ctx.Done():
			return false
		case decision := <-resp:
			// The UI handler records session-wide approvals when it answers.
			return decision.allow
		}
	}
}

// isSessionAllowed reports whether the user allowlisted this tool.
func (m *Model) isSessionAllowed(name string) bool {
	return m.sessionAllowed[name]
}

// allowToolForSession records a session-wide approval for one tool.
func (m *Model) allowToolForSession(name string) {
	if m.sessionAllowed == nil {
		m.sessionAllowed = map[string]bool{}
	}
	m.sessionAllowed[name] = true
}

// SessionAllowedTools lists the allowlisted tool names, sorted.
func (m *Model) SessionAllowedTools() []string {
	if len(m.sessionAllowed) == 0 {
		return nil
	}
	names := make([]string, 0, len(m.sessionAllowed))
	for name := range m.sessionAllowed {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
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

	// Any incoming message may change what the footer shows (state, todos,
	// input, mode, session list), so the memoized footer is invalidated up
	// front rather than at every mutation site — a missed invalidation would
	// show stale UI, which is far worse than rebuilding when unnecessary.
	// The paths that return early below invalidate for themselves.
	switch msg.(type) {
	case spinner.TickMsg, streamRenderMsg:
		// These change only the spinner glyph and the transcript, neither of
		// which the footer renders, so the footer cache stays valid.
	default:
		m.invalidateFooter()
	}

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

	case streamRenderMsg:
		m.flushStreamRender()

	case runtimeEventMsg:
		if msg.event.Type == agent.EventTodosChanged {
			m.todos = msg.event.Todos
			m.refreshViewport()
			break
		}
		if msg.event.Type == agent.EventPlanSubmitted {
			m.onPlanSubmitted(msg.event)
			break
		}
		if cmd := m.handleRuntimeEvent(msg.event); cmd != nil {
			cmds = append(cmds, cmd)
		}

	case runtimeDoneMsg:
		m.state = stateIdle
		if msg.err != nil {
			m.addBlock(errorStyle.Render("✗ " + msg.err.Error()))
		}
		if m.systemPromptRestore != "" && m.runtime != nil {
			// /init runs with a different system prompt; put the normal one
			// back as soon as that run is over.
			m.runtime.SetSystemPrompt(m.systemPromptRestore)
			m.systemPromptRestore = ""
		}
		m.saveCurrentSession()
		if cmd := m.maybeGenerateTitle(); cmd != nil {
			cmds = append(cmds, cmd)
		}
		// Extract durable memories in the background once the run is over.
		// This is intentionally after the UI is already idle: extraction must
		// never delay the answer the user just waited for.
		if msg.err == nil {
			if cmd := m.startDistill(); cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
		m.gitStatus = collectGitStatus(m.workingDir)
		m.refreshViewport()
		m.input.Focus()
		cmds = append(cmds, textinput.Blink)

	case memoryDistilledMsg:
		// Extraction is a background enhancement, so a failure is reported
		// quietly rather than as an error the user must act on: the run they
		// cared about already succeeded.
		if msg.err != nil {
			break
		}
		if msg.notes > 0 {
			m.addBlock(toolArrow.Render(fmt.Sprintf("⟳ remembered %d note(s) for future sessions", msg.notes)))
			m.refreshViewport()
		}

	case sessionTitleMsg:
		if msg.err == nil && msg.title != "" && m.currentSession != nil {
			m.currentSession.Title = msg.title
			m.saveCurrentSession()
			m.refreshViewport()
		}

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
				m.runtime.SetContextTokens(0)
				m.runtime.SetUsage(agent.Usage{
					PromptTokens:     int(msg.sess.PromptTokens),
					CompletionTokens: int(msg.sess.CompletionTokens),
					TotalTokens:      int(msg.sess.PromptTokens + msg.sess.CompletionTokens),
				})
			}
			m.blocks = nil
			m.markBlocksChanged()
			m.streamingIdx = -1
			m.streamingText = ""
			m.todos = m.todosForSession(msg.sess.ID)
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

	case planApprovalMsg:
		m.pendingPlan = &msg
		m.state = statePlanApproval
		m.refreshViewport()

	case tea.KeyMsg:
		// handleKey returns early, so it invalidates the footer itself.
		m.invalidateFooter()
		return m.handleKey(msg)

	case tea.MouseMsg:
		m.invalidateFooter()
		return m.handleMouse(msg)
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
	m.transcriptDirty = true
}

// markBlocksChanged records that a block was edited in place, so the cached
// transcript is rebuilt on the next refresh. Callers that assign into m.blocks
// directly must call this.
func (m *Model) markBlocksChanged() {
	m.transcriptDirty = true
}

// viewportTailBlocks bounds how many trailing blocks are handed to the
// viewport. The transcript is append-only and the viewport can only ever show
// the last screenful, so joining the entire history on every frame grew
// linearly with the conversation and showed up as stutter late in a long task.
// Keeping a generous tail preserves scrollback in practice while making the
// per-frame cost independent of how long the session has run.
const viewportTailBlocks = 400

// refreshViewport rebuilds the viewport content and pins it to the bottom.
//
// The join is skipped when nothing changed, because View() runs on every
// message — including every spinner tick — and the transcript usually has not
// moved between frames.
func (m *Model) refreshViewport() {
	if !m.transcriptDirty {
		m.viewport.GotoBottom()
		return
	}
	m.transcriptDirty = false
	m.viewport.SetContent(m.transcriptContent())
	m.viewport.GotoBottom()
}

// transcriptContent joins the rendered blocks, capped to the trailing window.
func (m *Model) transcriptContent() string {
	blocks := m.blocks
	if len(blocks) > viewportTailBlocks {
		blocks = blocks[len(blocks)-viewportTailBlocks:]
	}
	if len(blocks) == 1 {
		return blocks[0]
	}
	// Pre-size the builder: the joined length is what the allocation would be
	// anyway, and this avoids repeated growth on a large transcript.
	total := 0
	for _, b := range blocks {
		total += len(b) + 2
	}
	var sb strings.Builder
	sb.Grow(total)
	for i, b := range blocks {
		if i > 0 {
			sb.WriteString("\n\n")
		}
		sb.WriteString(b)
	}
	return sb.String()
}
