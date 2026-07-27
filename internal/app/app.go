package app

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/wislist/mini-opencode/internal/agent"
	"github.com/wislist/mini-opencode/internal/agent/prompt"
	"github.com/wislist/mini-opencode/internal/agent/tools"
	"github.com/wislist/mini-opencode/internal/config"
	"github.com/wislist/mini-opencode/internal/session"
	"github.com/wislist/mini-opencode/internal/skills"
)

const version = "0.2.0"

func Run(ctx context.Context, in io.Reader, out io.Writer) error {
	scanner := bufio.NewScanner(in)
	workingDir, err := os.Getwd()
	if err != nil {
		return err
	}
	cfg, err := config.Load("config.json")
	if err != nil {
		return err
	}
	if err := ensureProviderKey(scanner, out, workingDir, &cfg); err != nil {
		return err
	}
	runtime, err := newRuntime(workingDir, cfg, scanner, out)
	if err != nil {
		return err
	}

	sessions := session.NewStore(workingDir)
	currentSession := sessions.Create("new session")

	fmt.Fprintf(out, "mini-opencode %s\n", version)
	fmt.Fprintln(out, "commands: /help /version /tools /workspace /skills /key /compact /session /newsession /quit")

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		fmt.Fprint(out, "\n> ")
		if !scanner.Scan() {
			return scanner.Err()
		}

		input := strings.TrimSpace(scanner.Text())
		if input == "" {
			continue
		}

		switch input {
		case "/help":
			printHelp(out)
		case "/version":
			fmt.Fprintf(out, "mini-opencode %s\n", version)
		case "/tools":
			for _, tool := range runtime.Tools() {
				fmt.Fprintf(out, "%s\t%s\n", tool.Name, tool.Description)
			}
		case "/workspace":
			printWorkspace(out, workingDir, cfg.Workspace.AllowedRoots)
		case "/skills":
			printSkills(out, workingDir)
		case "/compact":
			if err := runCompact(ctx, out, workingDir, runtime); err != nil {
				fmt.Fprintf(out, "error: %v\n", err)
			}
		case "/newsession":
			saveSession(sessions, currentSession, runtime)
			currentSession = sessions.Create("new session")
			runtime.SetMessages(nil)
			fmt.Fprintln(out, "[new session started]")
		case "/session", "/sessions":
			metas, err := sessions.List()
			if err != nil {
				fmt.Fprintf(out, "error: %v\n", err)
				continue
			}
			if len(metas) == 0 {
				fmt.Fprintln(out, "[no saved sessions]")
				continue
			}
			for i, meta := range metas {
				fmt.Fprintf(out, "  %d. %s  (%d msgs, %s)\n", i+1, meta.Title, meta.MessageN, meta.UpdatedAt.Format("2006-01-02 15:04"))
			}
			fmt.Fprint(out, "select session number (0 to cancel): ")
			if !scanner.Scan() {
				return scanner.Err()
			}
			choice := strings.TrimSpace(scanner.Text())
			idx, err := strconv.Atoi(choice)
			if err != nil || idx < 1 || idx > len(metas) {
				if choice != "0" && choice != "" {
					fmt.Fprintln(out, "[cancelled]")
				}
				continue
			}
			sess, err := sessions.Load(metas[idx-1].ID)
			if err != nil {
				fmt.Fprintf(out, "error: %v\n", err)
				continue
			}
			saveSession(sessions, currentSession, runtime)
			currentSession = sess
			runtime.SetMessages(sess.Messages)
			fmt.Fprintf(out, "[switched to: %s]\n", sess.Title)
		case "/key":
			if err := configureDeepSeekKey(scanner, out, workingDir, &cfg, ""); err != nil {
				fmt.Fprintf(out, "error: %v\n", err)
				continue
			}
			runtime, err = newRuntime(workingDir, cfg, scanner, out)
			if err != nil {
				fmt.Fprintf(out, "error: %v\n", err)
				continue
			}
			fmt.Fprintln(out, "[deepseek key saved]")
		case "/name":
			fmt.Fprintf(out, "user: %s  assistant: %s\n", cfg.User, cfg.Assistant)
			fmt.Fprintln(out, "usage: /name user <name> | /name assistant <name> | /name <name>")
		case "/quit", "quit", "exit":
			return nil
		default:
			if strings.HasPrefix(input, "/name ") {
				fields := strings.Fields(strings.TrimPrefix(input, "/name "))
				var user, assistant string
				switch fields[0] {
				case "user", "u":
					user = strings.Join(fields[1:], " ")
				case "assistant", "a":
					assistant = strings.Join(fields[1:], " ")
				default:
					user = strings.Join(fields, " ")
					assistant = user
				}
				if user != "" {
					cfg.User = user
				}
				if assistant != "" {
					cfg.Assistant = assistant
				}
				if err := config.Save(filepath.Join(workingDir, "config.json"), cfg); err != nil {
					fmt.Fprintf(out, "error: %v\n", err)
					continue
				}
				fmt.Fprintf(out, "[names updated] %s / %s\n", cfg.User, cfg.Assistant)
				continue
			}
			if strings.HasPrefix(input, "/key ") {
				key := strings.TrimSpace(strings.TrimPrefix(input, "/key "))
				if err := configureDeepSeekKey(scanner, out, workingDir, &cfg, key); err != nil {
					fmt.Fprintf(out, "error: %v\n", err)
					continue
				}
				runtime, err = newRuntime(workingDir, cfg, scanner, out)
				if err != nil {
					fmt.Fprintf(out, "error: %v\n", err)
					continue
				}
				fmt.Fprintln(out, "[deepseek key saved]")
				continue
			}
			if err := runtime.Run(ctx, input, renderEvent(out)); err != nil {
				fmt.Fprintf(out, "error: %v\n", err)
			}
			saveSession(sessions, currentSession, runtime)
		}
	}
}

func printHelp(out io.Writer) {
	fmt.Fprintln(out, "mini-opencode is a fresh Go agent terminal project.")
	fmt.Fprintln(out, "commands: /key <deepseek-api-key> saves a local key and switches provider to DeepSeek.")
}

// printWorkspace reports the working directory and any additional allowed
// roots the agent may read and write outside the working directory.
func printWorkspace(out io.Writer, workingDir string, allowedRoots []string) {
	fmt.Fprintf(out, "workspace: %s\n", workingDir)
	if len(allowedRoots) == 0 {
		fmt.Fprintln(out, "allowed roots: (none)")
		return
	}
	fmt.Fprintln(out, "allowed roots:")
	for _, root := range allowedRoots {
		fmt.Fprintf(out, "  - %s\n", root)
	}
}

// saveSession persists the current runtime messages to the active session.
// It auto-titles untitled sessions from the first user message.
func saveSession(store *session.Store, sess *session.Session, rt *agent.Runtime) {
	if store == nil || sess == nil || rt == nil {
		return
	}
	msgs := rt.Messages()
	sess.Messages = msgs
	if sess.Title == "new session" {
		for _, msg := range msgs {
			if msg.Role == agent.RoleUser {
				sess.Title = session.TitleFromMessage(msg.Content)
				break
			}
		}
	}
	_ = store.Save(sess)
}

func runCompact(ctx context.Context, out io.Writer, workingDir string, runtime *agent.Runtime) error {
	if len(runtime.Messages()) == 0 {
		fmt.Fprintln(out, "[nothing to compact yet]")
		return nil
	}
	before := len(runtime.Messages())
	summaryPrompt, err := prompt.SummarySystemPrompt(workingDir)
	if err != nil {
		return err
	}
	fmt.Fprintln(out, "[compacting context...]")
	summary, err := runtime.Compact(ctx, summaryPrompt)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "[context compacted: %d messages -> 1]\n", before)
	fmt.Fprintln(out, summary)
	return nil
}

func toPromptSkills(installed []skills.Skill) []prompt.Skill {
	out := make([]prompt.Skill, 0, len(installed))
	for _, s := range installed {
		out = append(out, prompt.Skill{Name: s.Name, Description: s.Description, Location: s.Location})
	}
	return out
}

func printSkills(out io.Writer, workingDir string) {
	installed, err := skills.LoadSkills(workingDir)
	if err != nil {
		fmt.Fprintf(out, "error loading skills: %v\n", err)
		return
	}
	if len(installed) == 0 {
		fmt.Fprintln(out, "no skills installed")
	} else {
		fmt.Fprintln(out, "installed skills:")
		for _, s := range installed {
			fmt.Fprintf(out, "  %s\t%s\n", s.Name, s.Description)
		}
	}
	fmt.Fprintf(out, "curated available: %s\n", strings.Join(skills.CuratedNames(), ", "))
}

func newRuntime(workingDir string, cfg config.Config, scanner *bufio.Scanner, out io.Writer) (*agent.Runtime, error) {
	promptContext := prompt.DefaultPromptContext(workingDir)
	contextFiles, err := prompt.DiscoverContextFiles(workingDir, nil)
	if err != nil {
		return nil, err
	}
	promptContext.ContextFiles = contextFiles

	installed, err := skills.LoadSkills(workingDir)
	if err != nil {
		return nil, err
	}
	promptContext.Skills = toPromptSkills(installed)

	systemPrompt, err := prompt.BuildSystemPrompt(prompt.PromptCoder, promptContext)
	if err != nil {
		return nil, err
	}

	provider, err := newProvider(cfg.Provider, workingDir)
	if err != nil {
		return nil, err
	}

	options := []agent.RuntimeOption{
		agent.WithSystemPrompt(systemPrompt),
		agent.WithPermissionPolicy(agent.NewDefaultPermissionPolicyWithRoots(workingDir, cfg.Workspace.AllowedRoots)),
		agent.WithPermissionConfirmer(confirmTool(scanner, out)),
		agent.WithHook(agent.NewSafetyHook(workingDir)),
		agent.WithHook(agent.NewLoopGuardHook()),
	}
	for _, tool := range tools.CodingTools(tools.CodingToolOptions{WorkDir: workingDir, AllowedRoots: cfg.Workspace.AllowedRoots}) {
		options = append(options, agent.WithTool(tool))
	}

	return agent.NewRuntime(
		provider,
		options...,
	), nil
}

func confirmTool(scanner *bufio.Scanner, out io.Writer) agent.PermissionConfirmer {
	return func(ctx context.Context, call agent.ToolCall, result agent.ToolResult) bool {
		fmt.Fprintf(out, "allow tool %s? [y/N]: ", call.Name)
		if !scanner.Scan() {
			return false
		}
		answer := strings.ToLower(strings.TrimSpace(scanner.Text()))
		return answer == "y" || answer == "yes" || answer == "允许"
	}
}

func newProvider(cfg config.ProviderConfig, workingDir string) (agent.Provider, error) {
	switch strings.ToLower(cfg.Name) {
	case "", "echo":
		return agent.EchoProvider{}, nil
	case "deepseek", "openai-compatible", "openai_compatible":
		apiKey := cfg.ResolvedAPIKeyFrom(workingDir)
		return agent.NewOpenAICompatibleProvider(agent.OpenAICompatibleConfig{
			BaseURL: cfg.BaseURL,
			APIKey:  apiKey,
			Model:   cfg.Model,
		})
	default:
		return nil, fmt.Errorf("unknown provider: %s", cfg.Name)
	}
}

func ensureProviderKey(scanner *bufio.Scanner, out io.Writer, workingDir string, cfg *config.Config) error {
	if strings.ToLower(cfg.Provider.Name) != "deepseek" {
		return nil
	}
	if cfg.Provider.ResolvedAPIKeyFrom(workingDir) != "" {
		return nil
	}
	return configureDeepSeekKey(scanner, out, workingDir, cfg, "")
}

func configureDeepSeekKey(scanner *bufio.Scanner, out io.Writer, workingDir string, cfg *config.Config, key string) error {
	if key == "" {
		fmt.Fprint(out, "DeepSeek API key: ")
		if !scanner.Scan() {
			return scanner.Err()
		}
		key = strings.TrimSpace(scanner.Text())
	}
	if key == "" {
		return fmt.Errorf("deepseek api key is required")
	}
	provider := cfg.Provider
	if strings.ToLower(provider.Name) != "deepseek" {
		provider = config.DefaultDeepSeekProvider()
	}
	if provider.BaseURL == "" {
		provider.BaseURL = "https://api.deepseek.com"
	}
	if provider.Model == "" {
		provider.Model = "deepseek-chat"
	}
	if provider.APIKeyEnv == "" {
		provider.APIKeyEnv = "DEEPSEEK_API_KEY"
	}
	provider.APIKey = ""
	cfg.Provider = provider
	if err := config.SaveProviderKey(workingDir, provider.Name, key); err != nil {
		return err
	}
	return config.Save(filepath.Join(workingDir, "config.json"), *cfg)
}

func renderEvent(out io.Writer) func(agent.Event) {
	streamed := false
	return func(event agent.Event) {
		switch event.Type {
		case agent.EventTurnStarted:
			fmt.Fprintf(out, "[turn %d]\n", event.Turn)
		case agent.EventAssistantDelta:
			if event.Delta != "" {
				fmt.Fprint(out, event.Delta)
				streamed = true
			}
		case agent.EventAssistantResponse:
			if streamed {
				// Content already printed token-by-token; just end the line.
				fmt.Fprintln(out)
				streamed = false
				return
			}
			if event.Message != nil && event.Message.Content != "" {
				fmt.Fprintln(out, event.Message.Content)
			}
		case agent.EventToolCallStarted:
			if event.ToolCall != nil {
				fmt.Fprintf(out, "tool: %s\n", event.ToolCall.Name)
			}
		case agent.EventToolCallFinished:
			if event.ToolResult == nil {
				return
			}
			if event.ToolResult.Error != "" {
				fmt.Fprintf(out, "tool error: %s\n", event.ToolResult.Error)
				return
			}
			fmt.Fprintf(out, "tool result: %s\n", event.ToolResult.Content)
		case agent.EventToolCallFailed, agent.EventToolPermissionRequired, agent.EventToolPermissionDenied:
			if event.ToolResult != nil && event.ToolResult.Error != "" {
				fmt.Fprintf(out, "tool error: %s\n", event.ToolResult.Error)
			}
		case agent.EventHookDenied:
			if event.ToolResult != nil && event.ToolResult.Error != "" {
				fmt.Fprintf(out, "hook blocked: %s\n", event.ToolResult.Error)
			}
		case agent.EventHookStopped:
			if event.Error != nil {
				fmt.Fprintf(out, "hook stopped run: %v\n", event.Error)
			}
		}
	}
}
