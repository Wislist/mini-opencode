package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// Completer is the subset of a provider the distiller needs. Declaring it here
// keeps this package independent of the agent package's provider type.
type Completer interface {
	Complete(ctx context.Context, systemPrompt string, messages []DistillMessage) (string, error)
}

// DistillMessage is one turn handed to the distiller.
type DistillMessage struct {
	Role    string
	Content string
}

// distillPrompt instructs the model to extract only durable knowledge. The
// emphasis on "would this help a future session" and the explicit exclusion of
// task progress is what keeps memory from filling with noise: a summary of what
// was just done is already covered by the transcript, and writing it to disk
// would only crowd out recall later.
const distillPrompt = `You maintain long-term memory for a coding agent working in a repository.

Below is the tail of a conversation that just finished. Extract knowledge that would still be
useful in a LATER, unrelated session — things that are true about this project or this
environment, not about the conversation.

Record things like:
- architecture decisions and the reasoning behind them
- project conventions and invariants a newcomer would get wrong
- environment quirks, required commands, versions, and gotchas
- corrections to earlier assumptions

Do NOT record:
- what was just accomplished, or a summary of the conversation
- the current task's progress, next steps, or open questions
- anything already obvious from reading the repository
- secrets, API keys, tokens, or credentials of any kind

Return ONLY a JSON array. Each element is an object with:
  "name"  - short kebab-case stable identifier, used to overwrite an existing note on the same subject
  "title" - a short human-readable title
  "tags"  - array of short lowercase labels
  "body"  - the note itself, concise Markdown; prefer file paths, symbol names, exact commands

Return [] when the conversation contains nothing durable. An empty array is the correct and
common answer. Never invent facts; only record what the conversation actually established.`

// compactSummaryTag marks a runtime compaction summary inside a transcript.
const compactSummaryTag = "<conversation_summary>"

// IsCompactionSummary reports whether content is a compaction summary. Those
// restate earlier turns, so distilling one would re-record knowledge under a
// fresh timestamp instead of recognizing it as already known.
func IsCompactionSummary(content string) bool {
	return strings.Contains(content, compactSummaryTag)
}

// DistillResult is what the distiller produced.
type DistillResult struct {
	// Notes are the extracted memories, already normalized.
	Notes []Note
	// Raw is the model's unparsed response, kept for diagnostics when parsing
	// finds nothing usable.
	Raw string
	// Skipped counts proposed notes rejected as invalid.
	Skipped int
}

// Distiller turns a finished conversation into durable notes. It is the
// automatic half of memory: the agent writes notes deliberately through the
// memory tool, and the distiller catches what it did not think to save.
type Distiller struct {
	completer Completer
	store     *Store
}

// NewDistiller builds a distiller. It returns nil when either dependency is
// missing, so callers can register it unconditionally.
func NewDistiller(completer Completer, store *Store) *Distiller {
	if completer == nil || store == nil {
		return nil
	}
	return &Distiller{completer: completer, store: store}
}

// maxDistillMessages bounds how much of the conversation is sent. Only the
// tail matters for extracting durable facts, and sending the whole transcript
// would make this as expensive as the compaction it is meant to complement.
const maxDistillMessages = 40

// Extract asks the model what is worth remembering and stores the result.
// A conversation that yields nothing durable stores nothing and reports no
// error: that is the expected common case, not a failure.
func (d *Distiller) Extract(ctx context.Context, messages []DistillMessage) (DistillResult, error) {
	if d == nil {
		return DistillResult{}, nil
	}
	if len(messages) == 0 {
		return DistillResult{}, nil
	}
	if len(messages) > maxDistillMessages {
		messages = messages[len(messages)-maxDistillMessages:]
	}

	raw, err := d.completer.Complete(ctx, distillPrompt, messages)
	if err != nil {
		return DistillResult{}, fmt.Errorf("memory: distill: %w", err)
	}
	proposed, ok := parseDistillResponse(raw)
	if !ok {
		return DistillResult{Raw: raw}, nil
	}

	result := DistillResult{Raw: raw}
	for _, note := range proposed {
		if strings.TrimSpace(note.Body) == "" {
			result.Skipped++
			continue
		}
		if _, err := d.store.Write(note); err != nil {
			result.Skipped++
			continue
		}
		result.Notes = append(result.Notes, note)
	}
	return result, nil
}

// parseDistillResponse reads the model's JSON array, tolerating a response
// wrapped in prose or a Markdown code fence — both are common even when the
// prompt asks for bare JSON.
func parseDistillResponse(raw string) ([]Note, bool) {
	text := strings.TrimSpace(raw)
	if text == "" {
		return nil, false
	}
	// Strip a fenced block if the model wrapped its answer.
	if idx := strings.Index(text, "```"); idx != -1 {
		rest := text[idx+3:]
		rest = strings.TrimPrefix(rest, "json")
		if end := strings.Index(rest, "```"); end != -1 {
			text = strings.TrimSpace(rest[:end])
		}
	}
	// Fall back to the outermost array, which also handles a leading sentence.
	if !strings.HasPrefix(text, "[") {
		start := strings.Index(text, "[")
		end := strings.LastIndex(text, "]")
		if start == -1 || end <= start {
			return nil, false
		}
		text = text[start : end+1]
	}

	var payload []struct {
		Name  string   `json:"name"`
		Title string   `json:"title"`
		Tags  []string `json:"tags"`
		Body  string   `json:"body"`
	}
	if err := json.Unmarshal([]byte(text), &payload); err != nil {
		return nil, false
	}
	notes := make([]Note, 0, len(payload))
	for _, item := range payload {
		name := Slugify(item.Name)
		if name == "" {
			name = Slugify(item.Title)
		}
		if name == "" {
			continue
		}
		notes = append(notes, Note{
			Name:  name,
			Title: strings.TrimSpace(item.Title),
			Tags:  item.Tags,
			Body:  strings.TrimSpace(item.Body),
		})
	}
	return notes, true
}

// ConversationToDistill keeps only the user and assistant turns from a
// transcript. Tool traffic is dropped: tool output is mostly file contents and
// command logs, which are noise for extracting durable knowledge and would
// dominate the request. The user and assistant turns carry the reasoning worth
// keeping.
func ConversationToDistill(roles, contents []string) []DistillMessage {
	var out []DistillMessage
	for i := range roles {
		switch roles[i] {
		case "user", "assistant":
		default:
			continue
		}
		var content string
		if i < len(contents) {
			content = strings.TrimSpace(contents[i])
		}
		if content == "" {
			continue
		}
		out = append(out, DistillMessage{Role: roles[i], Content: content})
	}
	return out
}
