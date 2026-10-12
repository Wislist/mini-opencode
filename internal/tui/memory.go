package tui

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/wislist/mini-opencode/internal/agent"
	"github.com/wislist/mini-opencode/internal/memory"
)

// distillTimeout bounds the background memory-extraction call. It is shorter
// than a normal provider request because this runs after the user already has
// their answer: a slow extraction should give up rather than linger.
const distillTimeout = 60 * time.Second

// memoryDistilledMsg reports the outcome of a background memory extraction.
type memoryDistilledMsg struct {
	notes int
	err   error
}

type memorySearchMsg struct {
	query string
	notes []memory.Note
	err   error
}

// SetMemoryDistiller attaches the background memory extractor. A nil distiller
// disables automatic extraction; the memory tool still works.
func (m *Model) SetMemoryDistiller(d *memory.Distiller) { m.memoryDistiller = d }

// SetMemoryStore attaches the note store used by the /memory command. It is a
// separate setter from the distiller because a user may want to inspect memory
// even when automatic extraction is unavailable.
func (m *Model) SetMemoryStore(s *memory.Store) { m.memoryStore = s }

// SetMemoryRetriever attaches the query path used by /memory. Search runs in a
// tea.Cmd because hybrid retrieval may make a network request.
func (m *Model) SetMemoryRetriever(r memory.Retriever) { m.memoryRetriever = r }

// handleMemory implements /memory: with no argument it lists what is stored,
// with one it searches. Output goes into the transcript so it scrolls and
// copies like any other message.
func (m *Model) handleMemory(input string) (tea.Model, tea.Cmd) {
	if m.memoryStore == nil {
		m.addBlock(errorStyle.Render("memory is not configured"))
		m.refreshViewport()
		return m, nil
	}
	query := strings.TrimSpace(strings.TrimPrefix(input, "/memory"))

	retriever := m.memoryRetriever
	if retriever == nil {
		retriever = memory.LexicalRetriever{Store: m.memoryStore}
	}
	return m, func() tea.Msg {
		notes, err := retriever.Search(context.Background(), query, memoryListLimit)
		return memorySearchMsg{query: query, notes: notes, err: err}
	}
}

func (m *Model) renderMemorySearch(msg memorySearchMsg) {
	query, notes, err := msg.query, msg.notes, msg.err
	if err != nil {
		m.addBlock(errorStyle.Render("✗ memory: " + err.Error()))
		m.refreshViewport()
		return
	}
	if len(notes) == 0 {
		if query == "" {
			m.addBlock(dimStyle.Render("no memories stored yet — the agent saves notes as it works"))
		} else {
			m.addBlock(dimStyle.Render("no memories matched " + strconv.Quote(query)))
		}
		m.refreshViewport()
		return
	}

	var b strings.Builder
	fmt.Fprintf(&b, "memory (%d note(s), %s)\n", len(notes), m.memoryStore.Dir())
	for _, note := range notes {
		fmt.Fprintf(&b, "\n  %s  %s\n", toolArrow.Render("◆"), note.Title)
		if len(note.Tags) > 0 {
			fmt.Fprintf(&b, "    tags: %s\n", strings.Join(note.Tags, ", "))
		}
		b.WriteString(indentLines(note.Body, "    "))
		b.WriteString("\n")
	}
	m.addBlock(dimStyle.Render(strings.TrimRight(b.String(), "\n")))
	m.refreshViewport()
}

// memoryListLimit bounds how many notes /memory prints at once.
const memoryListLimit = 10

// distillMessagesFor converts runtime messages into distiller input. Tool
// traffic and runtime bookkeeping are dropped: tool output is largely file
// contents and command logs, which are noise for extracting durable knowledge,
// and a compaction summary restates earlier turns that were already seen.
func distillMessagesFor(messages []agent.Message) []memory.DistillMessage {
	var out []memory.DistillMessage
	for _, msg := range messages {
		if msg.Role != agent.RoleUser && msg.Role != agent.RoleAssistant {
			continue
		}
		if agent.IsTodoContinuationText(msg.Content) {
			continue
		}
		if memory.IsCompactionSummary(msg.Content) {
			continue
		}
		if msg.Content == "" {
			continue
		}
		out = append(out, memory.DistillMessage{Role: string(msg.Role), Content: msg.Content})
	}
	return out
}
