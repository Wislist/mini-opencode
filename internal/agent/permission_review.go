package agent

import (
	"context"
	"encoding/json"
	"strings"
	"time"
)

// CommandReviewer asks a model whether one shell command may run without the
// user's approval. It is the second opinion auto-review falls back to when the
// local rules cannot classify a command; the local rules always win.
//
// Implementations must be safe for concurrent use: the runtime can review
// commands from a parallel batch of read-only tool calls. Returning false for
// any doubt is the expected behaviour — the caller keeps asking the user.
type CommandReviewer interface {
	ReviewCommand(ctx context.Context, command string) (allowed bool, reason string)
}

// defaultCommandReviewTimeout bounds one review. A review that hangs must never
// hold a tool call open indefinitely; on timeout the decision stays with the
// user.
const defaultCommandReviewTimeout = 20 * time.Second

// ParseCommandReview interprets a reviewer's reply. The reply must contain a
// JSON object with a boolean "allow" field; anything else — prose only, a
// missing field, a stringly-typed boolean — is reported as not understood so
// the caller can fall back to asking the user.
func ParseCommandReview(content string) (allowed bool, reason string, ok bool) {
	body := stripCodeFence(strings.TrimSpace(content))
	start := strings.Index(body, "{")
	if start < 0 {
		return false, "", false
	}
	var payload struct {
		Allow  *bool  `json:"allow"`
		Reason string `json:"reason"`
	}
	// Decode only the first JSON value so trailing commentary is tolerated.
	if err := json.NewDecoder(strings.NewReader(body[start:])).Decode(&payload); err != nil {
		return false, "", false
	}
	if payload.Allow == nil {
		return false, "", false
	}
	return *payload.Allow, strings.TrimSpace(payload.Reason), true
}

// stripCodeFence removes a surrounding Markdown fence so a fenced JSON reply
// parses like a bare one.
func stripCodeFence(content string) string {
	if !strings.HasPrefix(content, "```") {
		return content
	}
	if newline := strings.IndexByte(content, '\n'); newline >= 0 {
		content = content[newline+1:]
	} else {
		return content
	}
	return strings.TrimSuffix(strings.TrimSpace(content), "```")
}

// NormalizeCommandAllowlist cleans configured command prefixes. Empty rules and
// rules with a blank token are dropped: an empty prefix would otherwise match
// every command.
func NormalizeCommandAllowlist(rules [][]string) [][]string {
	if len(rules) == 0 {
		return nil
	}
	var out [][]string
	for _, rule := range rules {
		tokens := make([]string, 0, len(rule))
		valid := true
		for _, token := range rule {
			token = strings.TrimSpace(token)
			if token == "" {
				valid = false
				break
			}
			tokens = append(tokens, token)
		}
		if !valid || len(tokens) == 0 {
			continue
		}
		out = append(out, tokens)
	}
	return out
}
