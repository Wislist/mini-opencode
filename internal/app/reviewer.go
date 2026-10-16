package app

import (
	"context"
	"strings"
	"sync"

	"github.com/wislist/mini-opencode/internal/agent"
	"github.com/wislist/mini-opencode/internal/agent/prompt"
	"github.com/wislist/mini-opencode/internal/config"
)

// reviewProvider is the slice of agent.Provider the reviewer needs. It is
// deliberately narrow so tests can supply a stub.
type reviewProvider interface {
	Complete(ctx context.Context, req agent.Request) (agent.AssistantResponse, error)
}

// commandReviewer implements agent.CommandReviewer on top of the active
// provider. It is the second opinion auto-review consults for shell commands
// the local rules cannot classify.
//
// Every failure is reported as "not approved": a provider error, a timeout, or
// a reply that does not parse must never turn into an approval, because that
// would hand the user's decision to a broken call.
type commandReviewer struct {
	provider     reviewProvider
	systemPrompt func() (string, error)

	once   sync.Once
	prompt string
	err    error
}

// newCommandReviewer builds a reviewer backed by the given provider. Passing a
// provider that resolves the live configuration (see liveProvider) keeps the
// reviewer on whichever provider the user selected.
func newCommandReviewer(provider reviewProvider, workingDir string) agent.CommandReviewer {
	return &commandReviewer{
		provider: provider,
		systemPrompt: func() (string, error) {
			return prompt.CommandReviewSystemPrompt(workingDir)
		},
	}
}

// configureCommandReview attaches the configured auto-review command prefixes
// and the model reviewer to a permission policy. It runs on every runtime
// build, so a /provider switch hands the reviewer the new configuration.
// A config that disables the reviewer removes any previously installed one, so
// the policy always matches the file.
func configureCommandReview(policy *agent.ModePermissionPolicy, cfg config.Config, workingDir string) {
	policy.SetCommandAllowlist(cfg.Permissions.AllowCommands)
	if !cfg.Permissions.AutoReviewModelEnabled() {
		policy.SetCommandReviewer(nil)
		return
	}
	policy.SetCommandReviewer(newCommandReviewer(liveProvider{cfg: &cfg, workingDir: workingDir}, workingDir))
}

func (r *commandReviewer) ReviewCommand(ctx context.Context, command string) (bool, string) {
	systemPrompt, err := r.system()
	if err != nil {
		return false, "review unavailable: " + err.Error()
	}
	response, err := r.provider.Complete(ctx, agent.Request{
		SystemPrompt: systemPrompt,
		Messages:     []agent.Message{{Role: agent.RoleUser, Content: commandReviewMessage(command)}},
	})
	if err != nil {
		return false, "review unavailable: " + err.Error()
	}
	allowed, reason, ok := agent.ParseCommandReview(response.Content)
	if !ok {
		return false, "review response was not understood"
	}
	return allowed, reason
}

// system renders the reviewer prompt once; it is derived from a template and
// never changes while the process runs.
func (r *commandReviewer) system() (string, error) {
	r.once.Do(func() {
		r.prompt, r.err = r.systemPrompt()
	})
	return r.prompt, r.err
}

// commandReviewMessage wraps the command in a fence so the reviewer reads it as
// data. The fence is safe: the tokenizer that let the command reach the
// reviewer rejects backticks.
func commandReviewMessage(command string) string {
	return "Command to review:\n```sh\n" + strings.TrimSpace(command) + "\n```"
}
