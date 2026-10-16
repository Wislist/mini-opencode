package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"time"
)

// PermissionMode controls approval, not an OS sandbox. Safety and plan hooks
// run before this policy and remain authoritative in every mode.
type PermissionMode string

const (
	PermissionModeAsk        PermissionMode = "ask"
	PermissionModeAutoReview PermissionMode = "auto-review"
	PermissionModeFullAccess PermissionMode = "full-access"
)

func ParsePermissionMode(value string) (PermissionMode, error) {
	switch mode := PermissionMode(value); mode {
	case "", PermissionModeAsk:
		return PermissionModeAsk, nil
	case PermissionModeAutoReview, PermissionModeFullAccess:
		return mode, nil
	default:
		return "", fmt.Errorf("unknown permission mode %q (use ask, auto-review, full-access)", value)
	}
}

func (m PermissionMode) Label() string {
	switch m {
	case PermissionModeAutoReview:
		return "Auto review"
	case PermissionModeFullAccess:
		return "Full access"
	default:
		return "Ask for approval"
	}
}

// ModePermissionPolicy owns the shared mode used by approval and tool-level
// path validation. The base policy is immutable after construction.
type ModePermissionPolicy struct {
	mu   sync.RWMutex
	mode PermissionMode
	base DefaultPermissionPolicy
	// allowCommands are extra auto-review prefix rules on top of the built-in
	// set; reviewer is the optional model second opinion. Both are only
	// consulted in auto-review mode.
	allowCommands [][]string
	reviewer      CommandReviewer
	reviewTimeout time.Duration
}

func NewModePermissionPolicy(workDir string, roots []string, mode PermissionMode) (*ModePermissionPolicy, error) {
	p := &ModePermissionPolicy{
		base:          NewDefaultPermissionPolicyWithRoots(workDir, append([]string(nil), roots...)),
		reviewTimeout: defaultCommandReviewTimeout,
	}
	if err := p.SetMode(mode); err != nil {
		return nil, err
	}
	return p, nil
}

// SetCommandAllowlist replaces the extra auto-review prefix rules. It is
// additive to the built-in set, so it can widen but never narrow what
// auto-review approves. Malformed rules are dropped.
func (p *ModePermissionPolicy) SetCommandAllowlist(rules [][]string) {
	normalized := NormalizeCommandAllowlist(rules)
	p.mu.Lock()
	p.allowCommands = normalized
	p.mu.Unlock()
}

// SetCommandReviewer installs the model-backed reviewer that auto-review
// consults for shell commands the local rules cannot classify. Passing nil
// disables the second opinion, leaving auto-review purely rule-based.
func (p *ModePermissionPolicy) SetCommandReviewer(reviewer CommandReviewer) {
	p.mu.Lock()
	p.reviewer = reviewer
	p.mu.Unlock()
}

func (p *ModePermissionPolicy) commandAllowlist() [][]string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.allowCommands
}

func (p *ModePermissionPolicy) commandReviewer() (CommandReviewer, time.Duration) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	timeout := p.reviewTimeout
	if timeout <= 0 {
		timeout = defaultCommandReviewTimeout
	}
	return p.reviewer, timeout
}

func (p *ModePermissionPolicy) SetMode(mode PermissionMode) error {
	normalized, err := ParsePermissionMode(string(mode))
	if err != nil {
		return err
	}
	p.mu.Lock()
	p.mode = normalized
	p.mu.Unlock()
	return nil
}

func (p *ModePermissionPolicy) Mode() PermissionMode {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.mode == "" {
		return PermissionModeAsk
	}
	return p.mode
}

func (p *ModePermissionPolicy) FullAccess() bool { return p.Mode() == PermissionModeFullAccess }

func (p *ModePermissionPolicy) Check(ctx context.Context, call ToolCall, def ToolDefinition) PermissionDecision {
	if err := ctx.Err(); err != nil {
		return PermissionDecision{Action: PermissionDeny, Reason: err.Error()}
	}
	var args map[string]json.RawMessage
	if err := json.Unmarshal(call.Arguments, &args); err != nil || args == nil {
		return PermissionDecision{Action: PermissionDeny, Reason: "tool arguments must be a JSON object"}
	}
	mode := p.Mode()
	if mode == PermissionModeFullAccess {
		return PermissionDecision{Action: PermissionAllow, Reason: "full-access mode"}
	}
	decision := p.base.Check(ctx, call, def)
	if mode != PermissionModeAutoReview || decision.Action != PermissionConfirm {
		return decision
	}
	// The local rules are the first opinion: deterministic, offline, and
	// conservative. Only what they cannot classify reaches the model reviewer,
	// and only a reviewer that explicitly approves turns the confirmation into
	// an approval — every other outcome (refusal, malformed reply, transport
	// error, timeout, no reviewer) leaves the decision with the user.
	if locallyReviewable(call, args, p.commandAllowlist()) {
		return PermissionDecision{Action: PermissionAllow, Reason: "auto-review: bounded local operation"}
	}
	reason, reviewed, approved := p.modelReview(ctx, call, args)
	if approved {
		if reason == "" {
			reason = "approved"
		}
		return PermissionDecision{Action: PermissionAllow, Reason: "auto-review (model): " + reason}
	}
	if reviewed && reason != "" {
		decision.Reason = "auto-review: model review declined — " + reason
		return decision
	}
	decision.Reason = "auto-review could not establish low risk; user approval required"
	return decision
}

// modelReview asks the reviewer about a shell command. It reports whether a
// review happened at all, so an unavailable reviewer and a reviewer that said
// no can be told apart in the message shown to the user.
func (p *ModePermissionPolicy) modelReview(ctx context.Context, call ToolCall, args map[string]json.RawMessage) (reason string, reviewed bool, approved bool) {
	reviewer, timeout := p.commandReviewer()
	if reviewer == nil || call.Name != "bash" {
		return "", false, false
	}
	command, background, ok := shellCommand(args)
	if !ok || background || strings.TrimSpace(command) == "" {
		return "", false, false
	}
	reviewCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	allowed, reason := reviewer.ReviewCommand(reviewCtx, command)
	return strings.TrimSpace(reason), true, allowed
}

// shellCommand extracts a bash command and its background flag.
func shellCommand(args map[string]json.RawMessage) (command string, background bool, ok bool) {
	if raw, present := args["run_in_background"]; present {
		if json.Unmarshal(raw, &background) != nil {
			return "", false, false
		}
	}
	if json.Unmarshal(args["command"], &command) != nil {
		return "", false, false
	}
	return command, background, true
}

func locallyReviewable(call ToolCall, args map[string]json.RawMessage, extraRules [][]string) bool {
	switch call.Name {
	case "write", "edit":
		var path string
		return json.Unmarshal(args["path"], &path) == nil && strings.TrimSpace(path) != ""
	case "bash":
		command, background, ok := shellCommand(args)
		if !ok || background {
			return false
		}
		return locallyReviewableShellCommandForOS(command, runtime.GOOS, extraRules)
	}
	return false
}

// builtinCommandPrefixRules are the project's own build and test entry points:
// they run the repository's code by design, which is what the user wants from a
// coding agent. Everything else that compiles or runs code still asks, or goes
// to the model reviewer. Deliberately absent: `go generate` (runs directives
// found in the source), a bare `npm` (can publish), and bare interpreters.
var builtinCommandPrefixRules = [][]string{
	{"go", "build"},
	{"go", "test"},
	{"go", "run"},
	{"go", "vet"},
	{"go", "fmt"},
	{"make"},
	{"npm", "test"},
	{"npm", "run"},
	{"npm", "ci"},
	{"pytest"},
	{"python", "-m", "pytest"},
	{"python3", "-m", "pytest"},
}

// locallyReviewableShellCommandForOS approves shell commands whose risk the
// local rules can establish without a model. The command is tokenized first:
// any shell metacharacter (chains, pipes, redirects, command substitution,
// globs, subshells, escapes, newlines) or unbalanced quote makes it
// non-reviewable, so a command can never grow a second statement or borrow an
// allowlisted prefix. The program must be a bare name (or the trusted
// /bin/pwd, /bin/ls): a path could point an allowlisted name at someone else's
// binary.
//
// Two sets of commands qualify: the read-only set (pwd, ls, read-only git, go
// metadata subcommands), and prefix rules — the built-in build/test entry
// points plus whatever the user configured in allow_commands.
func locallyReviewableShellCommandForOS(command string, goos string, extraRules [][]string) bool {
	tokens, ok := splitSafeShellWords(command)
	if !ok || len(tokens) == 0 {
		return false
	}
	var program string
	switch tokens[0] {
	case "/bin/pwd", "/bin/ls":
		program = strings.TrimPrefix(tokens[0], "/bin/")
	default:
		if strings.ContainsAny(tokens[0], `/\`) {
			return false
		}
		program = strings.ToLower(tokens[0])
	}
	switch program {
	case "git":
		if safeGitCommand(tokens[1:]) {
			return true
		}
	case "go":
		if safeGoCommand(tokens[1:]) {
			return true
		}
	case "pwd", "ls":
		return true
	}
	// PowerShell listing cmdlets are argument-restricted: their parameter
	// surface is large, so only the bare form is reviewed.
	if goos == "windows" && len(tokens) == 1 {
		switch program {
		case "get-location", "get-childitem", "dir":
			return true
		}
	}
	if matchesAnyCommandPrefix(tokens, builtinCommandPrefixRules) {
		return true
	}
	return matchesAnyCommandPrefix(tokens, extraRules)
}

// matchesAnyCommandPrefix reports whether tokens start with any rule.
func matchesAnyCommandPrefix(tokens []string, rules [][]string) bool {
	for _, rule := range rules {
		if matchesCommandPrefix(tokens, rule) {
			return true
		}
	}
	return false
}

// matchesCommandPrefix matches a token prefix case-insensitively on the
// program and exactly on the arguments, then vets every remaining argument.
func matchesCommandPrefix(tokens []string, rule []string) bool {
	if len(rule) == 0 || len(rule) > len(tokens) {
		return false
	}
	if !strings.EqualFold(tokens[0], rule[0]) {
		return false
	}
	for i := 1; i < len(rule); i++ {
		if tokens[i] != rule[i] {
			return false
		}
	}
	for _, arg := range tokens[len(rule):] {
		if !prefixRuleArgumentAllowed(arg) {
			return false
		}
	}
	return true
}

// prefixRuleArgumentAllowed rejects the arguments that would let an otherwise
// bounded build or test command reach outside the workspace, or hand execution
// to another program: absolute paths (and Windows drive paths), parent
// traversal, and the go tool's -exec/-toolexec hooks. Everything else — flags,
// relative paths, test filters — is fine, because running the project's own
// code is the point of these rules.
func prefixRuleArgumentAllowed(arg string) bool {
	lower := strings.ToLower(arg)
	switch {
	case lower == "-exec", strings.HasPrefix(lower, "-exec="),
		lower == "-toolexec", strings.HasPrefix(lower, "-toolexec="):
		return false
	}
	if strings.HasPrefix(arg, "/") || strings.HasPrefix(arg, `\`) {
		return false
	}
	if len(arg) >= 2 && arg[1] == ':' && isASCIILetter(arg[0]) {
		return false
	}
	if arg == ".." || strings.HasPrefix(arg, "../") || strings.HasPrefix(arg, `..\`) {
		return false
	}
	return !strings.Contains(arg, "/../") && !strings.Contains(arg, `\..\`)
}

func isASCIILetter(r byte) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
}

// splitSafeShellWords splits a command on unquoted whitespace and rejects any
// rune outside a conservative safe set. Double and single quotes group words;
// quote characters are never emitted. An unterminated quote fails.
func splitSafeShellWords(command string) ([]string, bool) {
	var (
		tokens  []string
		current strings.Builder
		quote   rune
		hasWord bool
	)
	flush := func() {
		if hasWord {
			tokens = append(tokens, current.String())
			current.Reset()
			hasWord = false
		}
	}
	for _, r := range command {
		switch {
		case r == '\'' || r == '"':
			if quote == r {
				quote = 0
			} else if quote == 0 {
				quote = r
			} else {
				current.WriteRune(r)
			}
			// An empty quoted string is still a word.
			hasWord = true
		case r == ' ' || r == '\t':
			if quote != 0 {
				current.WriteRune(r)
			} else {
				flush()
			}
		default:
			if !safeShellRune(r) {
				return nil, false
			}
			current.WriteRune(r)
			hasWord = true
		}
	}
	if quote != 0 {
		return nil, false
	}
	flush()
	return tokens, true
}

// safeShellRune is the allowed character set for reviewable commands. It
// excludes every bash/powershell metacharacter, so no token can chain,
// redirect, substitute, glob or escape.
func safeShellRune(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		return true
	}
	switch r {
	case '-', '_', '.', '/', '~', ':', '=', ',', '+', '@':
		return true
	}
	return false
}

// safeGitCommand allows only read-only git subcommands. Flags that can spawn a
// process (`-c`, `--config`, `--exec-path`, pager/transport hooks) and any
// `--output` redirect are refused everywhere.
func safeGitCommand(args []string) bool {
	if len(args) == 0 {
		return false
	}
	for _, arg := range args {
		switch arg {
		case "-c", "--config", "--exec-path", "--paginate", "--upload-pack", "--receive-pack", "--exec":
			return false
		}
		if strings.HasPrefix(arg, "--output") {
			return false
		}
	}
	switch args[0] {
	case "status", "log", "diff", "show", "blame", "rev-parse", "ls-files", "describe", "shortlog":
		return true
	case "branch":
		for _, arg := range args[1:] {
			switch arg {
			case "-a", "-r", "-v", "-vv", "--all", "--remotes", "--verbose", "--list", "--show-current":
			default:
				return false
			}
		}
		return true
	case "remote":
		for _, arg := range args[1:] {
			if arg != "-v" && arg != "--verbose" {
				return false
			}
		}
		return true
	}
	return false
}

// safeGoCommand allows only go subcommands that inspect the toolchain or
// package metadata without compiling or running anything. `go env -w/-u`
// writes to the user's go env file, so it is refused.
func safeGoCommand(args []string) bool {
	if len(args) == 0 {
		return false
	}
	switch args[0] {
	case "version", "doc", "list":
		return true
	case "env":
		for _, arg := range args[1:] {
			if arg == "-w" || arg == "-u" {
				return false
			}
		}
		return true
	}
	return false
}

// PermissionCommand is shared by CLI and TUI. Merely viewing the choices or
// typing full-access without its explicit confirmation never changes mode.
func PermissionCommand(current PermissionMode, input string) (next PermissionMode, changed bool, message string) {
	if current == "" {
		current = PermissionModeAsk
	}
	fields := strings.Fields(input)
	if len(fields) == 1 {
		return current, false, fmt.Sprintf("permissions: %s · %s\n  /permissions full-access  skip per-call approval (requires confirm)\n  /permissions auto-review  approve build/test and other low-risk commands; ask when uncertain\n  /permissions ask          ask before dangerous operations (default)\nApplies to this process only; this is not an OS sandbox.", current, current.Label())
	}
	if len(fields) < 2 || len(fields) > 3 || (len(fields) == 3 && (fields[1] != "full-access" || fields[2] != "confirm")) {
		return current, false, "usage: /permissions [ask|auto-review|full-access [confirm]]"
	}
	mode, err := ParsePermissionMode(fields[1])
	if err != nil {
		return current, false, err.Error()
	}
	if mode == PermissionModeFullAccess && current != mode && len(fields) != 3 {
		return current, false, "Warning: full access skips per-call approval and removes tool-level workspace limits. Tools may access files and networks available to the current user. Safety hooks, plan mode, and read-before-write still apply.\nConfirm with /permissions full-access confirm"
	}
	return mode, mode != current, fmt.Sprintf("permissions: %s · %s (this process only; safety hooks still apply)", mode, mode.Label())
}
