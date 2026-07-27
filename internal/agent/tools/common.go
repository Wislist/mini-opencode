package tools

import "time"

const (
	BashToolName      = "bash"
	ReadToolName      = "read"
	WriteToolName     = "write"
	EditToolName      = "edit"
	LSToolName        = "ls"
	GlobToolName      = "glob"
	GrepToolName      = "grep"
	JobOutputToolName = "job_output"
	JobKillToolName   = "job_kill"

	DefaultMaxOutputLength = 20_000
)

const InstallSkillToolName = "install_skill"

const DefaultAutoBackgroundAfter = time.Minute

var DefaultBannedCommands = []string{
	"rm -rf /",
	"sudo rm",
	"git reset --hard",
	"git push",
	"chmod -R 777",
	"chown -R",
}
