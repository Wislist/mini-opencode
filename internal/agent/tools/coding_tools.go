package tools

import "github.com/wislist/mini-opencode/internal/agent"

type CodingToolOptions struct {
	WorkDir         string
	AllowedRoots    []string
	Jobs            *JobManager
	InstructionData InstructionData
}

func CodingTools(options CodingToolOptions) []agent.Tool {
	if options.Jobs == nil {
		options.Jobs = NewJobManager()
	}
	fileOptions := FileOptions{
		WorkDir:         options.WorkDir,
		AllowedRoots:    options.AllowedRoots,
		InstructionData: options.InstructionData,
	}
	return []agent.Tool{
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
	}
}
