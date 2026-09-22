package tools

import (
	"strings"

	"github.com/wislist/mini-opencode/internal/agent"
)

type CodingToolOptions struct {
	WorkDir         string
	AllowedRoots    []string
	Jobs            *JobManager
	InstructionData InstructionData
	// Observer records reads and snapshots for the active session.
	Observer FileObserver
	// RequireReadBeforeWrite rejects write/edit on an unread existing file.
	RequireReadBeforeWrite bool
	// Todos persists the agent's task list for the active session.
	Todos TodoStore
	// PlanApprover reviews plans submitted through exit_plan_mode.
	PlanApprover PlanApprover
	// Web configures web_fetch, and web_search when a search endpoint is set.
	Web WebOptions
	// TaskRunner delegates read-only investigations to a subagent.
	TaskRunner TaskRunner
}

func CodingTools(options CodingToolOptions) []agent.Tool {
	if options.Jobs == nil {
		options.Jobs = NewJobManager()
	}
	fileOptions := FileOptions{
		WorkDir:                options.WorkDir,
		AllowedRoots:           options.AllowedRoots,
		InstructionData:        options.InstructionData,
		Observer:               options.Observer,
		RequireReadBeforeWrite: options.RequireReadBeforeWrite,
	}
	tools := []agent.Tool{
		NewReadTool(fileOptions),
		NewWriteTool(fileOptions),
		NewEditTool(fileOptions),
		NewLSTool(fileOptions),
		NewGlobTool(fileOptions),
		NewGrepTool(fileOptions),
		NewBashTool(BashOptions{
			WorkDir:         options.WorkDir,
			InstructionData: options.InstructionData,
			Jobs:            options.Jobs,
		}),
		NewJobOutputTool(JobOutputOptions{
			Jobs:            options.Jobs,
			InstructionData: options.InstructionData,
		}),
		NewJobKillTool(JobKillOptions{
			Jobs:            options.Jobs,
			InstructionData: options.InstructionData,
		}),
		NewInstallSkillTool(fileOptions),
		NewTodoWriteTool(TodoWriteOptions{Store: options.Todos}),
		NewTodoBlockedTool(),
		NewExitPlanModeTool(ExitPlanModeOptions{Approver: options.PlanApprover}),
		NewWebFetchTool(options.Web),
	}
	if strings.TrimSpace(options.Web.SearchURL) != "" {
		tools = append(tools, NewWebSearchTool(options.Web))
	}
	if options.TaskRunner != nil {
		tools = append(tools, NewTaskTool(TaskOptions{Runner: options.TaskRunner}))
	}
	return tools
}

// ReadOnlyTools returns the tool subset a subagent may use: it can inspect the
// workspace but cannot modify it or execute commands.
func ReadOnlyTools(options FileOptions) []agent.Tool {
	options = normalizeFileOptions(options)
	return []agent.Tool{
		NewReadTool(options),
		NewLSTool(options),
		NewGlobTool(options),
		NewGrepTool(options),
	}
}
