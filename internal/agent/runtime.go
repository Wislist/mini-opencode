package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

type Runtime struct {
	// mu guards every mutable field below. It is never held across a provider
	// call or a tool call: those release the lock first and re-acquire it to
	// record the outcome, so a UI goroutine can always read state (Messages,
	// Usage, ContextTokens) while a run is in flight.
	mu           sync.Mutex
	systemPrompt string
	provider     Provider
	tools        *ToolRegistry
	toolService  ToolService
	confirmer    PermissionConfirmer
	permissions  *ModePermissionPolicy
	messages     []Message
	maxTurns     int
	hooks        HookChain
	// usage accumulates provider-reported token accounting across every
	// completion this runtime performed since it was created.
	usage Usage
	// contextWindow is the provider's max prompt size in tokens. Zero disables
	// automatic compaction.
	contextWindow int
	// compactionPrompt is the system prompt used to summarize the conversation
	// when automatic compaction fires.
	compactionPrompt string
	// compactThreshold is the fraction of contextWindow that triggers
	// automatic compaction.
	compactThreshold float64
	// lastPromptTokens is the prompt size the provider reported for the most
	// recent completion, which is the most accurate context size available.
	lastPromptTokens int
	// autoCompactions counts automatic compactions performed in this runtime.
	autoCompactions int
	// todoReader supplies the task list driving the todo chain. Nil keeps the
	// run single-shot: the agent finishes its turn and the run ends.
	todoReader TodoReader
	// runRetries is how many times a failed turn is retried before the run
	// fails, on top of the provider's own HTTP-level retries.
	runRetries int
	// blockedReason and blockedNeeds record a blocker reported through
	// todo_blocked, which ends the todo chain for this run.
	blockedReason string
	blockedNeeds  string
	// retrySleep is the wait between run-level retries. Indirected so tests do
	// not have to sleep through the backoff.
	retrySleep func(ctx context.Context, d time.Duration) error
	// postCompactTokens is the context size right after the last compaction.
	// A further automatic compaction requires meaningful growth beyond it, so a
	// summary that is itself larger than the threshold cannot make the runtime
	// compact on every single turn.
	postCompactTokens int
	// summaryIndex is the position in messages of the newest compaction
	// summary, or -1 when the conversation has never been compacted. Messages
	// before it are retained but no longer sent to the provider, which is what
	// makes compaction non-destructive.
	summaryIndex int
}

// compactionGrowthFraction is how much new context must accumulate after a
// compaction before another automatic compaction is allowed.
const compactionGrowthFraction = 0.1

// Compaction headroom constants. A window larger than
// largeContextWindowThreshold keeps a flat token buffer; a smaller one keeps a
// proportional share of the window. Both describe how much free space must
// remain, not how much may be used.
const (
	largeContextWindowThreshold = 200_000
	largeContextWindowBuffer    = 20_000
	smallContextWindowRatio     = 0.2
)

type PermissionConfirmer func(ctx context.Context, call ToolCall, result ToolResult) bool

type RuntimeOption func(*Runtime)

func NewRuntime(provider Provider, opts ...RuntimeOption) *Runtime {
	tools := NewToolRegistry()
	r := &Runtime{
		systemPrompt: defaultSystemPrompt,
		provider:     provider,
		tools:        tools,
		toolService:  NewRegistryToolService(tools),
		maxTurns:     100,
		runRetries:   DefaultRunRetries,
		retrySleep:   sleepContext,
		summaryIndex: -1,
	}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

// SystemPrompt returns the active system prompt.
func (r *Runtime) SystemPrompt() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.systemPrompt
}

// SetSystemPrompt replaces the system prompt for subsequent runs. The /init
// command uses it to run one analysis turn with a different prompt and then
// restore the normal one.
func (r *Runtime) SetSystemPrompt(prompt string) {
	if prompt == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.systemPrompt = prompt
}

func WithSystemPrompt(prompt string) RuntimeOption {
	return func(r *Runtime) {
		if prompt != "" {
			r.systemPrompt = prompt
		}
	}
}

// WithContextWindow sets the provider's prompt budget so the runtime can
// compact the conversation before it overflows.
func WithContextWindow(tokens int) RuntimeOption {
	return func(r *Runtime) {
		if tokens > 0 {
			r.contextWindow = tokens
		}
	}
}

// WithCompactionPrompt supplies the summarization prompt and enables automatic
// compaction. Without it, compaction stays manual (/compact).
func WithCompactionPrompt(prompt string) RuntimeOption {
	return func(r *Runtime) {
		if strings.TrimSpace(prompt) != "" {
			r.compactionPrompt = prompt
		}
	}
}

// DefaultCompactThreshold is the context occupancy that triggers automatic
// compaction.
const DefaultCompactThreshold = 0.85

// WithCompactionThreshold overrides when automatic compaction fires, as a
// fraction of the context window.
func WithCompactionThreshold(fraction float64) RuntimeOption {
	return func(r *Runtime) {
		if fraction > 0 && fraction < 1 {
			r.compactThreshold = fraction
		}
	}
}

// AutoCompactions reports how many times the runtime compacted automatically.
func (r *Runtime) AutoCompactions() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.autoCompactions
}

// ContextTokens returns the best available estimate of the prompt size the
// provider will see: the prompt token count the provider reported for the last
// completion when available, otherwise a character-based estimate. The
// provider number is authoritative because it counts the real tokenization.
func (r *Runtime) ContextTokens() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.contextTokensLocked()
}

// ContextBreakdown separates the two parts of the prompt: the fixed system
// prompt plus tool schemas, and the conversation itself.
//
// The split matters for display. The system prompt is large by nature — the
// coder template, tool instructions, AGENTS.md and skills together reach tens
// of kilobytes — and it is present in every request, so it forms a floor that
// never drops. Showing only the total makes a cleared conversation look like
// nothing happened: after /newsession the total barely moves because the floor
// dominates it.
type ContextBreakdown struct {
	// SystemTokens is the system prompt estimate. It is always a character
	// estimate, because the provider reports only the whole prompt.
	SystemTokens int
	// ConversationTokens is the transcript size that would be sent: the
	// messages after the compaction marker.
	ConversationTokens int
	// Reported is the prompt size the provider reported for the last
	// completion, or 0 when it has not reported one yet.
	Reported int
}

// Total is the prompt size the provider would see, preferring the reported
// figure once it exceeds the estimate.
func (b ContextBreakdown) Total() int {
	estimate := b.SystemTokens + b.ConversationTokens
	if b.Reported > estimate {
		return b.Reported
	}
	return estimate
}

// ContextDetails returns the system/conversation split behind ContextTokens.
func (r *Runtime) ContextDetails() ContextBreakdown {
	r.mu.Lock()
	defer r.mu.Unlock()
	return ContextBreakdown{
		SystemTokens:       estimateTokens(r.systemPrompt),
		ConversationTokens: r.conversationTokensLocked(),
		Reported:           r.lastPromptTokens,
	}
}

// conversationTokensLocked estimates only the transcript that is actually sent,
// excluding the system prompt. Callers must hold mu.
func (r *Runtime) conversationTokensLocked() int {
	total := 0
	for _, msg := range r.messages[min(r.summaryOrZero(), len(r.messages)):] {
		total += estimateTokens(msg.Content)
		total += estimateTokens(msg.ToolCallID)
		for _, call := range msg.ToolCalls {
			total += estimateTokens(call.ID) + estimateTokens(call.Name) + estimateTokens(string(call.Arguments))
		}
	}
	return total
}

// contextTokensLocked is contextTokens without locking; callers must hold mu.
func (r *Runtime) contextTokensLocked() int {
	estimate := r.contextEstimateLocked()
	if r.lastPromptTokens > estimate {
		return r.lastPromptTokens
	}
	return estimate
}

// SetContextTokens records a provider-reported prompt size. A stored session
// carries the number its previous runtime ended on, and a caller that only
// knows the transcript has no number to pass, so zero clears the field and lets
// the runtime fall back to its own estimate.
func (r *Runtime) SetContextTokens(tokens int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if tokens < 0 {
		tokens = 0
	}
	r.lastPromptTokens = tokens
}

// needsCompaction reports whether the context has grown past the threshold.
func (r *Runtime) needsCompaction() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.needsCompactionLocked()
}

// needsCompactionLocked is needsCompaction without locking.
//
// The trigger is expressed as remaining headroom rather than as a fraction
// already consumed, because the two behave differently as a window grows. On a
// large window a fixed fraction leaves a huge, effectively wasted margin; on a
// small window a fixed token buffer would be a large share of the window. So a
// window above largeContextWindowThreshold keeps a flat buffer, and a smaller
// one keeps a proportional share. This mirrors how Crush sizes the same
// decision.
func (r *Runtime) needsCompactionLocked() bool {
	if r.contextWindow <= 0 || r.compactionPrompt == "" || len(r.messages) < 2 {
		return false
	}
	tokens := r.contextTokensLocked()
	remaining := r.contextWindow - tokens
	if remaining > r.compactionHeadroom() {
		return false
	}
	if r.postCompactTokens > 0 {
		minGrowth := int(float64(r.contextWindow) * compactionGrowthFraction)
		if minGrowth < 1 {
			minGrowth = 1
		}
		if tokens < r.postCompactTokens+minGrowth {
			return false
		}
	}
	return true
}

// compactionHeadroom is how much of the window must stay free before a request
// is considered too close to the limit. An explicit compact_threshold keeps the
// old "fraction consumed" meaning, so existing configs are unaffected; when it
// is unset the remaining-buffer rule applies.
func (r *Runtime) compactionHeadroom() int {
	if r.compactThreshold > 0 {
		return r.contextWindow - int(float64(r.contextWindow)*r.compactThreshold)
	}
	if r.contextWindow > largeContextWindowThreshold {
		return largeContextWindowBuffer
	}
	return int(float64(r.contextWindow) * smallContextWindowRatio)
}

// WithTodoReader enables the todo chain: while the session task list still has
// outstanding items, the run pushes itself forward to the next one instead of
// stopping and waiting for the user.
func WithTodoReader(reader TodoReader) RuntimeOption {
	return func(r *Runtime) {
		if reader != nil {
			r.todoReader = reader
		}
	}
}

// DefaultRunRetries is the number of whole-turn retries after a provider
// failure, on top of the provider's internal HTTP retries.
const DefaultRunRetries = 2

// WithRunRetries sets how many times a failed turn is retried. Zero disables
// run-level retries.
func WithRunRetries(retries int) RuntimeOption {
	return func(r *Runtime) {
		if retries >= 0 {
			r.runRetries = retries
		}
	}
}

func WithMaxTurns(maxTurns int) RuntimeOption {
	return func(r *Runtime) {
		if maxTurns > 0 {
			r.maxTurns = maxTurns
		}
	}
}

func WithTool(tool Tool) RuntimeOption {
	return func(r *Runtime) {
		_ = r.tools.Register(tool)
	}
}

func WithToolService(service ToolService) RuntimeOption {
	return func(r *Runtime) {
		if service != nil {
			r.toolService = service
		}
	}
}

func WithPermissionPolicy(policy PermissionPolicy) RuntimeOption {
	return func(r *Runtime) {
		r.tools.SetPermissionPolicy(policy)
		r.permissions, _ = policy.(*ModePermissionPolicy)
	}
}

// SetPermissionMode is called at idle UI/CLI boundaries. It does not revoke
// already-running background jobs or replace the policy used by the tools.
func (r *Runtime) SetPermissionMode(mode PermissionMode) error {
	r.mu.Lock()
	policy := r.permissions
	r.mu.Unlock()
	if policy == nil {
		return fmt.Errorf("runtime does not support permission mode switching")
	}
	return policy.SetMode(mode)
}

func WithPermissionConfirmer(confirmer PermissionConfirmer) RuntimeOption {
	return func(r *Runtime) {
		r.confirmer = confirmer
	}
}

// SetMessages replaces the conversation history. It is used to restore a
// saved session into the runtime.
//
// SetMessages swaps the transcript for a copy of messages.
//
// The compaction accounting is reset as well. The token counts and the
// post-compaction baseline describe the previous conversation, so carrying
// them into a different one makes needsCompaction report true on the very
// first turn and compact the freshly loaded context again and again instead
// of answering.
func (r *Runtime) SetMessages(messages []Message) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.replaceMessagesLocked(messages)
	r.resetCompactionStateLocked()
}

// AdoptStateFrom moves the conversation state of src onto r.
//
// Switching provider or model builds a runtime from the new configuration, and
// a fresh runtime knows nothing about the conversation in flight. Copying the
// transcript, the token accounting and the compaction measurement across is
// what makes such a switch mid-session non-destructive; the alternative,
// starting over, reads to the user as losing the session.
func (r *Runtime) AdoptStateFrom(src *Runtime) {
	if r == nil || src == nil || r == src {
		return
	}
	// Snapshot the source under its own lock, then write under r's: the two are
	// different runtimes, and holding both would invite a lock-order problem
	// with a UI goroutine reading either one.
	src.mu.Lock()
	messages := make([]Message, len(src.messages))
	copy(messages, src.messages)
	usage := src.usage
	lastPromptTokens := src.lastPromptTokens
	postCompactTokens := src.postCompactTokens
	autoCompactions := src.autoCompactions
	systemPrompt := src.systemPrompt
	src.mu.Unlock()

	r.mu.Lock()
	defer r.mu.Unlock()
	// replaceMessagesLocked re-derives the compaction marker from the content,
	// so an already-compacted transcript stays compacted.
	r.replaceMessagesLocked(messages)
	r.usage = usage
	r.lastPromptTokens = lastPromptTokens
	r.postCompactTokens = postCompactTokens
	r.autoCompactions = autoCompactions
	if systemPrompt != "" {
		r.systemPrompt = systemPrompt
	}
}

// resetCompactionStateLocked drops the measurements that describe the previous
// conversation. Callers must hold mu.
func (r *Runtime) resetCompactionStateLocked() {
	r.lastPromptTokens = 0
	r.postCompactTokens = 0
	r.autoCompactions = 0
}

// replaceMessagesLocked swaps the transcript for a copy of messages. The new
// transcript may already contain a compaction summary (a session reloaded after
// a compaction), so the marker is re-derived from the content rather than
// assumed absent.
func (r *Runtime) replaceMessagesLocked(messages []Message) {
	r.messages = make([]Message, len(messages))
	copy(r.messages, messages)
	r.summaryIndex = -1
	for i, msg := range r.messages {
		if isCompactSummaryContent(msg.Content) {
			r.summaryIndex = i
		}
	}
}

// isCompactSummaryContent reports whether content is a compaction summary. The
// runtime owns this format, so it can detect its own markers without asking the
// session package.
func isCompactSummaryContent(content string) bool {
	return strings.Contains(content, "<conversation_summary>")
}

// appendMessage records one message in the transcript.
func (r *Runtime) appendMessage(msg Message) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.messages = append(r.messages, msg)
}

// promptState returns the prompt pieces the provider request needs. Reading
// them together keeps a turn self-consistent even if a UI goroutine loads
// another session halfway through.
//
// When the conversation has been compacted, the transcript is truncated at the
// newest summary: everything before it is retained in memory (and on disk) but
// no longer sent, which is what keeps compaction non-destructive. The summary
// is rewritten to the user role because a request must not start with an
// assistant turn.
func (r *Runtime) promptState() (string, []Message) {
	r.mu.Lock()
	defer r.mu.Unlock()
	start := 0
	if r.summaryIndex >= 0 && r.summaryIndex < len(r.messages) {
		start = r.summaryIndex
	}
	out := make([]Message, len(r.messages)-start)
	copy(out, r.messages[start:])
	if start > 0 && len(out) > 0 {
		out[0].Role = RoleUser
	}
	return r.systemPrompt, out
}

// WithHook appends a runtime lifecycle hook. Hooks fire before each tool
// call and after each turn; see the Hook interface for semantics.
func WithHook(hook Hook) RuntimeOption {
	return func(r *Runtime) {
		if hook != nil {
			r.hooks = append(r.hooks, hook)
		}
	}
}

func (r *Runtime) Messages() []Message {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Message, len(r.messages))
	copy(out, r.messages)
	return out
}

// Usage returns the provider-reported token totals accumulated by this
// runtime. It is the zero value when the provider reports no usage.
func (r *Runtime) Usage() Usage {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.usage
}

// SetUsage restores accumulated token totals, used when a saved session is
// loaded back into the runtime.
func (r *Runtime) SetUsage(usage Usage) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.usage = usage
}

// addUsage accumulates one response's usage and records the prompt size, which
// is the most accurate context measurement available.
func (r *Runtime) addUsage(usage Usage) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.usage = r.usage.Add(usage)
	if usage.PromptTokens > 0 {
		r.lastPromptTokens = usage.PromptTokens
	}
}

func (r *Runtime) Tools() []ToolView {
	return r.toolService.ListTools()
}

// ContextEstimate returns a rough token count for the full context the
// provider would see (system prompt + all messages). It uses ~4 ASCII
// characters per token plus one token per non-ASCII rune, which keeps the
// estimate usable for CJK conversations without a tokenizer dependency. The
// provider-reported prompt size, when available, is preferred; see
// ContextTokens.
func (r *Runtime) ContextEstimate() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.contextEstimateLocked()
}

// contextEstimateLocked is ContextEstimate without locking. Only the messages
// actually sent to the provider are counted: after a compaction the pre-summary
// history is retained but not transmitted, so counting it would report a context
// far larger than the request and re-trigger compaction on every turn.
func (r *Runtime) contextEstimateLocked() int {
	return estimateTokens(r.systemPrompt) + r.conversationTokensLocked()
}

// summaryOrZero is the active truncation start, or 0 when never compacted.
func (r *Runtime) summaryOrZero() int {
	if r.summaryIndex < 0 {
		return 0
	}
	return r.summaryIndex
}

// estimateTokens approximates the token count of a string.
func estimateTokens(text string) int {
	if text == "" {
		return 0
	}
	ascii, other := 0, 0
	for _, r := range text {
		if r < 128 {
			ascii++
			continue
		}
		other++
	}
	return ascii/4 + other
}

func (r *Runtime) toolDefinition(name string) ToolDefinition {
	def, _ := r.tools.Definition(name)
	return def
}

// CompactResult contains both the full internal summary saved into context and
// a short user-facing summary safe to show in the transcript.
type CompactResult struct {
	Summary     string
	UserSummary string
}

// compactPromptSuffix closes the summarization instruction with the task list
// so the summary always carries the outstanding work forward. Keeping this in
// the instruction (rather than only in the transcript) makes the model record
// statuses even when the list is long.
const compactPromptSuffix = "\n\nInclude these tasks and their statuses in your summary. " +
	"Instruct the resuming assistant to use the `todo_write` tool to continue tracking progress on these tasks."

// Compact asks the provider to summarize the current conversation. The
// summaryPrompt is the instruction prompt (e.g. from the summary template)
// describing how to summarize. It returns the generated summary text.
func (r *Runtime) Compact(ctx context.Context, summaryPrompt string) (string, error) {
	result, err := r.CompactDetailed(ctx, summaryPrompt)
	return result.Summary, err
}

// CompactDetailed summarizes the conversation and installs the result as the
// new head of the transcript. Compaction is non-destructive: the summary is
// appended as an assistant message and SummaryMessageID marks it, so the
// messages before it stay in the transcript (and in the store) and a later
// promptState simply stops at the marker. That keeps /undo, session reload and
// a re-summarization of the original conversation possible after a compaction.
//
// It also returns the prompt-requested user-facing summary section so the UI
// can acknowledge what was preserved without dumping the full compacted context.
func (r *Runtime) CompactDetailed(ctx context.Context, summaryPrompt string) (CompactResult, error) {
	history := r.Messages()
	if len(history) == 0 {
		return CompactResult{}, nil
	}

	instruction := summaryPrompt + r.compactTodoSuffix()
	// The provider call happens without the lock so a UI goroutine can still
	// read state while a long summary is generated.
	resp, err := r.provider.Complete(ctx, Request{
		SystemPrompt: instruction,
		Messages:     history,
	})
	if err != nil {
		return CompactResult{}, err
	}
	summary := strings.TrimSpace(resp.Content)
	if summary == "" {
		return CompactResult{}, fmt.Errorf("provider returned empty summary")
	}
	if !resp.Usage.IsZero() {
		r.addUsage(resp.Usage)
	}

	r.mu.Lock()
	// A compaction chain: the summary is appended after the previous one, so
	// the marker moves forward and every earlier summary is dropped from the
	// prompt by the same truncation rule. The assistant role matches how the
	// provider produced it; promptState rewrites it to user when sending.
	r.messages = append(r.messages, Message{
		Role: RoleAssistant,
		Content: "<conversation_summary>\n" + summary +
			"\n</conversation_summary>\n\nThe above summarizes our previous conversation. Continue from this context.",
	})
	r.summaryIndex = len(r.messages) - 1
	r.postCompactTokens = r.contextTokensLocked()
	r.mu.Unlock()
	return CompactResult{Summary: summary, UserSummary: extractCompactUserSummary(summary)}, nil
}

// compactTodoSuffix renders the outstanding task list into the summarization
// instruction. It returns "" when no task list is attached or every entry is
// already complete, so plain chats are not padded with an empty section.
func (r *Runtime) compactTodoSuffix() string {
	if r.todoReader == nil {
		return ""
	}
	items := ParseTodos(r.todoReader.Load())
	if len(items) == 0 {
		return ""
	}
	if progress := SummarizeTodos(items); !progress.Outstanding() {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\n## Current Todo List\n\n")
	for _, item := range items {
		fmt.Fprintf(&b, "- [%s] %s\n", NormalizeTodoStatus(item.Status), item.Content)
	}
	return b.String() + compactPromptSuffix
}

func extractCompactUserSummary(summary string) string {
	lines := strings.Split(summary, "\n")
	start := -1
	for i, line := range lines {
		if isUserSummaryHeading(line) {
			start = i + 1
			break
		}
	}
	if start == -1 {
		return "Context compacted. Detailed summary saved internally."
	}

	var out []string
	inCodeFence := false
	for _, line := range lines[start:] {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			inCodeFence = !inCodeFence
			continue
		}
		if !inCodeFence && isMarkdownHeading(trimmed) {
			break
		}
		if inCodeFence {
			continue
		}
		out = append(out, strings.TrimRight(line, " \t"))
	}
	userSummary := strings.TrimSpace(strings.Join(out, "\n"))
	if userSummary == "" {
		return "Context compacted. Detailed summary saved internally."
	}
	return userSummary
}

func isMarkdownHeading(line string) bool {
	line = strings.TrimSpace(line)
	return strings.HasPrefix(line, "# ") || strings.HasPrefix(line, "## ") || strings.HasPrefix(line, "### ") || strings.HasPrefix(line, "#### ") || strings.HasPrefix(line, "##### ") || strings.HasPrefix(line, "###### ")
}

func isUserSummaryHeading(line string) bool {
	heading := strings.ToLower(strings.TrimSpace(line))
	heading = strings.TrimLeft(heading, "# ")
	heading = strings.TrimSpace(strings.Trim(heading, ":"))
	heading = strings.ReplaceAll(heading, "-", " ")
	return heading == "user summary" || heading == "user facing summary" || heading == "user visible summary"
}

func (r *Runtime) Run(ctx context.Context, input string, emit func(Event)) error {
	if emit == nil {
		emit = func(Event) {}
	}

	r.appendMessage(Message{Role: RoleUser, Content: input})
	emit(Event{Type: EventRunStarted})
	r.hooks.OnRunStart(ctx)

	chain := &todoChain{reader: r.todoReader, gated: r.todoChainGated()}

	for turn := 1; turn <= r.maxTurns; turn++ {
		emit(Event{Type: EventTurnStarted, Turn: turn})

		// Compact before the next request once the context approaches the
		// window, so the provider never receives an oversized prompt.
		r.maybeAutoCompact(ctx, turn, emit)

		resp, err := r.completeTurn(ctx, turn, emit)
		if err != nil {
			emit(Event{Type: EventRunFailed, Turn: turn, Error: err})
			return err
		}

		for _, warning := range resp.Warnings {
			emit(Event{Type: EventProviderWarning, Turn: turn, Error: errors.New(warning)})
		}

		msg := Message{
			Role:      RoleAssistant,
			Content:   resp.Content,
			ToolCalls: resp.ToolCalls,
		}
		r.appendMessage(msg)
		emit(Event{Type: EventAssistantResponse, Turn: turn, Message: &msg})
		if !resp.Usage.IsZero() {
			r.addUsage(resp.Usage)
			usage := resp.Usage
			emit(Event{Type: EventUsage, Turn: turn, Usage: &usage})
		}

		if len(resp.ToolCalls) == 0 {
			// The model is done talking. If the task list still has work, the
			// run continues with the next item instead of ending here.
			if blocked, reason, needs := r.todoBlocked(); blocked {
				chain.blocked = true
				emit(Event{Type: EventTodoBlocked, Turn: turn, BlockedReason: reason, BlockedNeeds: needs})
			}
			chain.gated = r.todoChainGated()
			if step := chain.next(); step.Continue {
				emit(Event{
					Type:  EventTodoContinuation,
					Turn:  turn,
					Todos: RenderTodos(step.Progress.Items),
					Plan:  step.Instruction,
				})
				r.appendMessage(Message{Role: RoleUser, Content: step.Instruction})
				continue
			} else if step.Reason != "" {
				emit(Event{Type: EventTodoChainStopped, Turn: turn, Error: errors.New(step.Reason),
					Todos: RenderTodos(step.Progress.Items)})
			}
			emit(Event{Type: EventRunFinished, Turn: turn})
			return nil
		}

		if err := r.runToolCalls(ctx, turn, resp.ToolCalls, emit); err != nil {
			emit(Event{Type: EventRunFailed, Turn: turn, Error: err})
			return err
		}

		if decision := r.hooks.AfterTurn(ctx, turn, r.Messages()); decision.Action == HookStop {
			err := fmt.Errorf("run stopped by hook: %s", decision.Reason)
			emit(Event{Type: EventHookStopped, Turn: turn, Error: err})
			emit(Event{Type: EventRunFailed, Turn: turn, Error: err})
			return err
		}
	}

	err := fmt.Errorf(
		"turn budget exhausted after %d turns. Send a follow-up message to continue from here, "+
			"raise agent.max_turns in config.json, or run /compact to shrink the context", r.maxTurns)
	emit(Event{Type: EventBudgetExhausted, Turn: r.maxTurns, Error: err})
	emit(Event{Type: EventRunFailed, Turn: r.maxTurns, Error: err})
	return err
}

// noteBlocker records a blocker declared through todo_blocked. Keying off the
// tool result metadata keeps the runtime decoupled from the concrete tool, like
// the todo and plan signals.
func (r *Runtime) noteBlocker(result *ToolResult) {
	if result == nil || result.Metadata == nil {
		return
	}
	blocked, _ := result.Metadata["blocked"].(bool)
	if !blocked {
		return
	}
	reason, _ := result.Metadata["reason"].(string)
	needs, _ := result.Metadata["needs"].(string)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.blockedReason = strings.TrimSpace(reason)
	r.blockedNeeds = strings.TrimSpace(needs)
}

// todoBlocked reports the blocker the agent declared in this run, if any.
func (r *Runtime) todoBlocked() (bool, string, string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.blockedReason == "" {
		return false, "", ""
	}
	return true, r.blockedReason, r.blockedNeeds
}

// todoChainGated reports whether a hook suppresses automatic continuation. The
// gate is re-checked every turn, because plan mode can be toggled mid-run.
func (r *Runtime) todoChainGated() bool {
	for _, hook := range r.hooks {
		if gate, ok := hook.(TodoChainGate); ok && gate.BlocksTodoContinuation() {
			return true
		}
	}
	return false
}

// completeTurn performs one provider request, retrying the turn when the
// provider fails. A failed request records nothing, so a retry replays the same
// prompt with the same transcript: no tool is executed twice.
func (r *Runtime) completeTurn(ctx context.Context, turn int, emit func(Event)) (AssistantResponse, error) {
	var lastErr error
	for attempt := 0; attempt <= r.runRetries; attempt++ {
		if attempt > 0 {
			if err := r.retrySleep(ctx, runRetryBackoff(attempt)); err != nil {
				return AssistantResponse{}, lastErr
			}
			emit(Event{Type: EventRunRetry, Turn: turn, Attempt: attempt, Error: lastErr})
		}
		systemPrompt, history := r.promptState()
		// A completed response from a previous attempt is discarded, so the
		// deltas already streamed to the UI are superseded by the retry.
		resp, err := r.provider.CompleteStream(ctx, Request{
			SystemPrompt: systemPrompt,
			Messages:     history,
			Tools:        r.tools.Specs(),
		}, func(delta string) {
			emit(Event{Type: EventAssistantDelta, Turn: turn, Delta: delta})
		})
		if err == nil {
			return resp, nil
		}
		lastErr = err
		if ctx.Err() != nil {
			return AssistantResponse{}, err
		}
	}
	return AssistantResponse{}, lastErr
}

// runRetryBackoff returns the wait before retry attempt n (1-based).
func runRetryBackoff(attempt int) time.Duration {
	backoff := time.Second
	for i := 1; i < attempt; i++ {
		backoff *= 2
		if backoff >= 8*time.Second {
			return 8 * time.Second
		}
	}
	return backoff
}

// sleepContext waits for d or until ctx is cancelled.
func sleepContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// maybeAutoCompact summarizes the conversation when the context approaches the
// window. Failures are reported but never abort the run: an oversized request
// is worth attempting, and the user can still run /compact by hand.
func (r *Runtime) maybeAutoCompact(ctx context.Context, turn int, emit func(Event)) {
	if !r.needsCompaction() {
		return
	}
	before := r.ContextTokens()
	result, err := r.CompactDetailed(ctx, r.compactionPrompt)
	if err != nil {
		emit(Event{Type: EventContextCompacted, Turn: turn, Error: err, ContextTokens: before})
		return
	}
	r.mu.Lock()
	r.lastPromptTokens = 0
	r.autoCompactions++
	r.mu.Unlock()
	emit(Event{
		Type:          EventContextCompacted,
		Turn:          turn,
		ContextTokens: before,
		Plan:          result.UserSummary,
	})
}

// pendingToolCall is one tool call from an assistant message together with the
// scheduling decisions made while running the batch.
type pendingToolCall struct {
	call       ToolCall
	readOnly   bool
	prehandled bool
}

// runToolCalls executes every tool call of one assistant message. Hooks run in
// order for the whole batch first, so a stop decision still aborts before any
// side effect. Read-only calls are grouped into consecutive runs and executed
// concurrently, which cuts wall-clock time for exploration-heavy turns; calls
// that may mutate state stay serial and always run after the read-only group
// that precedes them.
func (r *Runtime) runToolCalls(ctx context.Context, turn int, calls []ToolCall, emit func(Event)) error {
	items := make([]pendingToolCall, 0, len(calls))
	for _, call := range calls {
		call := call
		def, ok := r.tools.Definition(call.Name)
		if decision := r.hooks.BeforeToolCall(ctx, call, def); decision.Action != HookContinue {
			if decision.Action == HookStop {
				err := fmt.Errorf("run stopped by hook: %s", decision.Reason)
				emit(Event{Type: EventHookStopped, Turn: turn, ToolCall: &call, Error: err})
				return err
			}
			result := ToolResult{
				ToolCallID: call.ID,
				Name:       call.Name,
				Error:      "hook denied: " + decision.Reason,
				Metadata:   map[string]any{"hook": string(HookDeny)},
			}
			r.recordToolResult(&result)
			emit(Event{Type: EventHookDenied, Turn: turn, ToolCall: &call, ToolResult: &result, Error: fmt.Errorf("%s", decision.Reason)})
			items = append(items, pendingToolCall{call: call, prehandled: true})
			continue
		}
		items = append(items, pendingToolCall{call: call, readOnly: isParallelizable(def, ok)})
	}

	for i := 0; i < len(items); {
		if items[i].prehandled {
			i++
			continue
		}
		if items[i].readOnly {
			j := i
			for j < len(items) && items[j].readOnly && !items[j].prehandled {
				j++
			}
			if err := r.runReadOnlyBatch(ctx, turn, items[i:j], emit); err != nil {
				return err
			}
			i = j
			continue
		}
		if err := r.runTool(ctx, turn, items[i].call, emit); err != nil {
			return err
		}
		i++
	}
	return nil
}

// isParallelizable reports whether a tool call can safely run alongside other
// calls in the same batch. Only side-effect-free tools qualify: anything that
// may require user confirmation has to run through the serial path so the UI
// prompt is still raised once and awaited.
func isParallelizable(def ToolDefinition, known bool) bool {
	if !known {
		return false
	}
	b := def.Behavior
	return b.ReadOnly && !b.Dangerous && !b.RequiresConfirmation
}

// runReadOnlyBatch executes read-only tool calls concurrently. Results are
// recorded in the original call order so the message history stays stable and
// provider-friendly, and a single failure never cancels its siblings.
func (r *Runtime) runReadOnlyBatch(ctx context.Context, turn int, items []pendingToolCall, emit func(Event)) error {
	if len(items) == 1 {
		return r.runTool(ctx, turn, items[0].call, emit)
	}

	results := make([]*ToolResult, len(items))
	var wg sync.WaitGroup
	for i := range items {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = r.runToolSilently(ctx, items[i].call)
		}()
	}
	wg.Wait()

	for _, result := range results {
		if result == nil {
			continue
		}
		call := ToolCall{ID: result.ToolCallID, Name: result.Name}
		r.recordToolResult(result)
		if result.Error != "" {
			emit(Event{Type: EventToolCallFailed, Turn: turn, ToolCall: &call, ToolResult: result, Error: fmt.Errorf("%s", result.Error)})
			continue
		}
		emit(Event{Type: EventToolCallFinished, Turn: turn, ToolCall: &call, ToolResult: result})
		r.emitTodosChanged(turn, result, emit)
		r.emitPlanSubmitted(turn, result, emit)
	}
	return nil
}

// runToolSilently runs a tool to completion and returns its final result. It is
// the concurrency-safe core used by the read-only batch, which cannot rely on
// the per-tool event stream because several tools run at once.
func (r *Runtime) runToolSilently(ctx context.Context, call ToolCall) *ToolResult {
	events, err := r.toolService.RunTool(ctx, ToolRunRequest{Call: call})
	if err != nil {
		return &ToolResult{ToolCallID: call.ID, Name: call.Name, Error: err.Error()}
	}
	var last *ToolResult
	for toolEvent := range events {
		switch toolEvent.Type {
		case ToolEventFinished, ToolEventFailed, ToolEventPermissionDenied, ToolEventPermissionRequired:
			if toolEvent.Result != nil {
				last = toolEvent.Result
			}
		}
	}
	if last == nil {
		return &ToolResult{ToolCallID: call.ID, Name: call.Name, Error: "tool produced no result"}
	}
	return last
}

func (r *Runtime) runTool(ctx context.Context, turn int, call ToolCall, emit func(Event)) error {
	events, err := r.toolService.RunTool(ctx, ToolRunRequest{Call: call})
	if err != nil {
		return err
	}
	for toolEvent := range events {
		switch toolEvent.Type {
		case ToolEventStarted:
			emit(Event{Type: EventToolCallStarted, Turn: turn, ToolCall: &call})
		case ToolEventFinished:
			r.recordToolResult(toolEvent.Result)
			emit(Event{Type: EventToolCallFinished, Turn: turn, ToolCall: &call, ToolResult: toolEvent.Result})
			r.emitTodosChanged(turn, toolEvent.Result, emit)
			r.emitPlanSubmitted(turn, toolEvent.Result, emit)
		case ToolEventFailed:
			r.recordToolResult(toolEvent.Result)
			emit(Event{Type: EventToolCallFailed, Turn: turn, ToolCall: &call, ToolResult: toolEvent.Result, Error: toolEvent.Error})
		case ToolEventPermissionRequired:
			emit(Event{Type: EventToolPermissionRequired, Turn: turn, ToolCall: &call, ToolResult: toolEvent.Result, Error: toolEvent.Error})
			if toolEvent.Result == nil || r.confirmer == nil || !r.confirmer(ctx, call, *toolEvent.Result) {
				r.recordToolResult(toolEvent.Result)
				return nil
			}
			return r.runApprovedTool(ctx, turn, call, emit)
		case ToolEventPermissionDenied:
			r.recordToolResult(toolEvent.Result)
			emit(Event{Type: EventToolPermissionDenied, Turn: turn, ToolCall: &call, ToolResult: toolEvent.Result, Error: toolEvent.Error})
		}
	}
	return nil
}

func (r *Runtime) runApprovedTool(ctx context.Context, turn int, call ToolCall, emit func(Event)) error {
	events, err := r.toolService.RunTool(ctx, ToolRunRequest{Call: call, Approved: true})
	if err != nil {
		return err
	}
	for toolEvent := range events {
		switch toolEvent.Type {
		case ToolEventStarted:
			// The initial permission-check run already emitted started. Emitting
			// it again after approval renders the same tool prompt twice.
			continue
		case ToolEventFinished:
			r.recordToolResult(toolEvent.Result)
			emit(Event{Type: EventToolCallFinished, Turn: turn, ToolCall: &call, ToolResult: toolEvent.Result})
			r.emitTodosChanged(turn, toolEvent.Result, emit)
			r.emitPlanSubmitted(turn, toolEvent.Result, emit)
		case ToolEventFailed:
			r.recordToolResult(toolEvent.Result)
			emit(Event{Type: EventToolCallFailed, Turn: turn, ToolCall: &call, ToolResult: toolEvent.Result, Error: toolEvent.Error})
		case ToolEventPermissionDenied:
			r.recordToolResult(toolEvent.Result)
			emit(Event{Type: EventToolPermissionDenied, Turn: turn, ToolCall: &call, ToolResult: toolEvent.Result, Error: toolEvent.Error})
		case ToolEventPermissionRequired:
			r.recordToolResult(toolEvent.Result)
			emit(Event{Type: EventToolPermissionRequired, Turn: turn, ToolCall: &call, ToolResult: toolEvent.Result, Error: toolEvent.Error})
		}
	}
	return nil
}

func (r *Runtime) recordToolResult(result *ToolResult) {
	if result == nil {
		return
	}
	r.noteBlocker(result)
	r.appendMessage(Message{
		Role:       RoleTool,
		ToolCallID: result.ToolCallID,
		Content:    resultMessageContent(*result),
	})
}

// emitTodosChanged raises EventTodosChanged when a tool result carries a
// rendered todo list in its metadata. Keying off metadata keeps the runtime
// decoupled from the concrete todo tool.
func (r *Runtime) emitTodosChanged(turn int, result *ToolResult, emit func(Event)) {
	if result == nil || result.Metadata == nil {
		return
	}
	if _, ok := result.Metadata["todos"]; !ok {
		return
	}
	rendered, _ := result.Metadata["rendered"].(string)
	if rendered == "" {
		return
	}
	emit(Event{Type: EventTodosChanged, Turn: turn, Todos: rendered})
}

// emitPlanSubmitted raises EventPlanSubmitted when a tool result carries a
// submitted plan, so the UI can acknowledge the approval decision.
func (r *Runtime) emitPlanSubmitted(turn int, result *ToolResult, emit func(Event)) {
	if result == nil || result.Metadata == nil {
		return
	}
	plan, _ := result.Metadata["plan"].(string)
	if plan == "" {
		return
	}
	if _, ok := result.Metadata["approved"]; !ok {
		return
	}
	emit(Event{Type: EventPlanSubmitted, Turn: turn, Plan: plan})
}

func resultMessageContent(result ToolResult) string {
	if result.Error != "" {
		return `{"error": "` + result.Error + `"}`
	}
	if strings.TrimSpace(result.Content) == "" {
		return "(no output)"
	}
	return result.Content
}

const defaultSystemPrompt = `You are mini-opencode, a local coding agent terminal.
Work step by step. Use tools when needed. Keep final answers concise.`
