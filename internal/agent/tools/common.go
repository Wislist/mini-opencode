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

	// DefaultReadLimit is the line budget of a read call that omits limit.
	DefaultReadLimit = 200
	// MaxReadLimit caps the line budget a caller may ask for.
	MaxReadLimit = 2000
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
