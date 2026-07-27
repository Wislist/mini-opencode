package tui

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/wislist/mini-opencode/internal/agent"
)

func (m *Model) handleInput(input string) (tea.Model, tea.Cmd) {
	switch {
	case input == "/quit" || input == "/exit" || input == "quit" || input == "exit":
		m.state = stateQuitting
		return m, tea.Quit
	case input == "/help":
		m.addBlock(m.renderHelp())
		m.refreshViewport()
		return m, nil
	case input == "/version":
		m.addBlock(fmt.Sprintf("mini-opencode %s", m.version))
		m.refreshViewport()
		return m, nil
	case input == "/tools":
		m.addBlock(m.renderTools())
		m.refreshViewport()
		return m, nil
	case input == "/workspace":
		m.addBlock(m.renderWorkspace())
		m.refreshViewport()
		return m, nil
	case input == "/status":
		m.gitStatus = collectGitStatus(m.workingDir)
		m.addBlock(m.renderStatus())
		m.refreshViewport()
		return m, nil
	case input == "/key":
		m.state = stateKeyPrompt
		m.keyInput.Reset()
		m.keyInput.Focus()
		return m, textinput.Blink
	case strings.HasPrefix(input, "/key "):
		return m.saveKey(strings.TrimSpace(strings.TrimPrefix(input, "/key ")))
	case input == "/name":
		m.addBlock(m.renderNames())
		m.refreshViewport()
		return m, nil
	case strings.HasPrefix(input, "/name "):
		return m.handleName(input)
	case input == "/compact":
		return m.startCompact()
	case input == "/newsession":
		return m.handleNewSession()
	case input == "/session", input == "/sessions":
		return m.handleListSessions()
	case strings.HasPrefix(input, "/"):
		m.addBlock(errorStyle.Render("unknown command: " + input))
		m.refreshViewport()
		return m, nil
	}

	if m.runtime == nil {
		m.addBlock(errorStyle.Render("no runtime available. use /key to configure."))
		m.refreshViewport()
		return m, nil
	}

	m.addBlock(m.renderUserMessage(input))
	m.state = stateRunning
	m.input.Blur()

	sendText := input
	if m.mode == ModePlan {
		sendText = "You are in plan mode. Do not modify any files or execute commands. " +
			"Analyze the request, inspect the codebase with read-only tools, and provide " +
			"a detailed plan with suggestions only.\n\n" + input
	}

	ctx, cancel := context.WithCancel(context.Background())
	m.ctx = ctx
	m.cancel = cancel

	go func() {
		err := m.runtime.Run(ctx, sendText, func(event agent.Event) {
			m.program.Send(runtimeEventMsg{event: event})
		})
		m.program.Send(runtimeDoneMsg{err: err})
	}()

	return m, spinner.Tick
}

func (m *Model) saveKey(key string) (tea.Model, tea.Cmd) {
	if m.keySaver != nil {
		newCfg, err := m.keySaver(key)
		if err != nil {
			m.addBlock(errorStyle.Render("✗ " + err.Error()))
		} else {
			*m.cfg = newCfg
			m.addBlock(toolArrow.Render("[deepseek key saved]"))
			if m.runtimeFactory != nil {
				rt, rerr := m.runtimeFactory(newCfg)
				if rerr != nil {
					m.addBlock(errorStyle.Render("✗ " + rerr.Error()))
				} else {
					m.SetRuntime(rt)
				}
			}
		}
	}
	m.state = stateIdle
	m.refreshViewport()
	return m, textinput.Blink
}

// handleName parses a "/name" command. Supported forms:
//
//	/name                    show current names
//	/name user <name>        set the user display name
//	/name assistant <name>   set the assistant display name
//	/name <name>             set both names to <name>
func (m *Model) handleName(input string) (tea.Model, tea.Cmd) {
	args := strings.TrimSpace(strings.TrimPrefix(input, "/name"))
	fields := strings.Fields(args)
	if len(fields) == 0 {
		m.addBlock(m.renderNames())
		m.refreshViewport()
		return m, nil
	}

	var user, assistant string
	switch fields[0] {
	case "user", "u":
		if len(fields) < 2 {
			m.addBlock(errorStyle.Render("usage: /name user <name>"))
			m.refreshViewport()
			return m, nil
		}
		user = strings.Join(fields[1:], " ")
	case "assistant", "a":
		if len(fields) < 2 {
			m.addBlock(errorStyle.Render("usage: /name assistant <name>"))
			m.refreshViewport()
			return m, nil
		}
		assistant = strings.Join(fields[1:], " ")
	default:
		// "/name <value>" sets both labels at once.
		user = strings.Join(fields, " ")
		assistant = user
	}

	if m.nameSaver == nil {
		m.addBlock(errorStyle.Render("name saver not configured"))
		m.refreshViewport()
		return m, nil
	}
	newCfg, err := m.nameSaver(user, assistant)
	if err != nil {
		m.addBlock(errorStyle.Render("✗ " + err.Error()))
	} else {
		*m.cfg = newCfg
		m.addBlock(toolArrow.Render("[names updated] " +
			m.userLabelStyle().Render(m.cfg.User) + " / " + assistantLabel.Render(m.cfg.Assistant)))
	}
	m.state = stateIdle
	m.refreshViewport()
	return m, textinput.Blink
}

func (m *Model) startCompact() (tea.Model, tea.Cmd) {
	if m.runtime == nil {
		m.addBlock(errorStyle.Render("no runtime available. use /key to configure."))
		m.refreshViewport()
		return m, nil
	}
	if len(m.runtime.Messages()) == 0 {
		m.addBlock(dimStyle.Render("nothing to compact yet"))
		m.refreshViewport()
		return m, nil
	}
	if m.compactor == nil {
		m.addBlock(errorStyle.Render("compactor not configured"))
		m.refreshViewport()
		return m, nil
	}
	m.state = stateCompacting
	m.input.Blur()
	m.addBlock(dimStyle.Render("compacting context..."))
	m.refreshViewport()

	ctx, cancel := context.WithCancel(context.Background())
	m.ctx = ctx
	m.cancel = cancel

	go func() {
		summary, err := m.compactor(ctx)
		m.program.Send(compactDoneMsg{summary: summary, err: err})
	}()

	return m, spinner.Tick
}

func (m *Model) handleRuntimeEvent(event agent.Event) {
	switch event.Type {
	case agent.EventAssistantDelta:
		if event.Delta == "" {
			return
		}
		m.streamingText += event.Delta
		rendered := m.renderAssistantMessage(m.streamingText)
		if m.streamingIdx < 0 {
			m.addBlock(rendered)
			m.streamingIdx = len(m.blocks) - 1
		} else {
			m.blocks[m.streamingIdx] = rendered
		}
		m.refreshViewport()
		return
	case agent.EventAssistantResponse:
		wasStreaming := m.streamingIdx >= 0
		if event.Message != nil && event.Message.Content != "" {
			// If we were streaming, replace the in-progress block with the
			// authoritative final content. Otherwise (tool-only response with
			// no content deltas) add a fresh block.
			if wasStreaming {
				m.blocks[m.streamingIdx] = m.renderAssistantMessage(event.Message.Content)
			} else {
				m.addBlock(m.renderAssistantMessage(event.Message.Content))
			}
		}
		m.streamingIdx = -1
		m.streamingText = ""
	case agent.EventToolCallStarted:
		if event.ToolCall != nil {
			m.addBlock(m.renderToolCall(event.ToolCall))
		}
	case agent.EventToolCallFinished:
		if event.ToolResult != nil {
			resultWidth := max(1, m.width-2)
			if event.ToolResult.Error != "" {
				m.addBlock(toolError.Width(resultWidth).Render("✗ " + event.ToolResult.Error))
			} else {
				content := event.ToolResult.Content
				if len(content) > 200 {
					content = content[:200] + "..."
				}
				m.addBlock(toolArrow.Width(resultWidth).Render("→ " + content))
			}
		}
	case agent.EventToolCallFailed, agent.EventToolPermissionDenied:
		errWidth := max(1, m.width-2)
		if event.ToolResult != nil && event.ToolResult.Error != "" {
			m.addBlock(toolError.Width(errWidth).Render("✗ " + event.ToolResult.Error))
		}
		if event.Error != nil && event.Error.Error() != "" {
			m.addBlock(toolError.Width(errWidth).Render("✗ " + event.Error.Error()))
		}
	}
	m.refreshViewport()
}
