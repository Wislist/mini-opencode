# Tools

`internal/agent/tools` contains the first-pass coding tool set for the agent.

The CLI registers the default coding tool set on startup. Use `/tools` in the CLI to list the registered tools.

Current scope:

- `bash`: shell command execution tool and instruction file
- `read`: read workspace files
- `write`: create or overwrite files
- `edit`: exact string replacement
- `ls`: list workspace directories
- `glob`: find files by glob
- `grep`: search text in files
- `job_output`: read background job output and status
- `job_kill`: stop background jobs

This package currently defines names, defaults, instruction files, concrete shell/job tools, concrete file tools, and concrete search tools.
The runtime/TUI execution interface will be added in later tasks.

- `install_skill`: install a `SKILL.md` skill from a curated name, local path, or GitHub repo path. See [skills.md](skills.md).

`bash` is now implemented as the first concrete tool. It currently uses
`os/exec` with `bash -lc`, not `mvdan/sh`. It supports:

- command validation
- banned command checks
- working directory validation
- output truncation
- foreground execution
- explicit background execution
- auto-backgrounding after a configured duration
- job id/status metadata for future TUI rendering

`job_output` and `job_kill` share the same `JobManager` used by `bash`, allowing callers to inspect or terminate background commands by `job_id`.

`read`, `write`, `edit`, and `ls` share workspace path validation. Relative paths resolve under `WorkDir`; absolute paths are allowed only when they remain inside `WorkDir`.

`glob` and `grep` are implemented in Go for now. They ignore `.git`, `.gocache`, `node_modules`, `vendor`, `dist`, `build`, and `target`. `glob` currently uses Go `filepath.Match` semantics rather than full shell globstar behavior.

## Tool interface

Agent tools expose a `ToolDefinition` and run with structured input/output:

- `ToolDefinition`: name, description, JSON schema, prompt text, behavior flags
- `ToolBehavior`: dangerous, requires confirmation, supports background, read-only
- `ToolInput`: call id, tool name, raw JSON arguments
- `ToolOutput`: textual content plus metadata for UI/runtime consumers

The metadata channel is reserved for values such as `cwd`, `exit_code`, `job_id`, truncation flags, and other tool-specific fields.

## Permissions

`ToolRegistry` can be configured with a `PermissionPolicy` before running tools.

The default policy currently checks:

- banned shell command fragments such as `sudo`, `git push`, and `git reset --hard`
- `path` and `working_dir` arguments escaping the configured workspace
- tool behavior flags such as `dangerous` and `requires_confirmation`

Denied calls return a `ToolResult` with `permission=deny` metadata. Calls that require confirmation return `permission=confirm` metadata. The current CLI asks `allow tool <name>? [y/N]:`; approving reruns the same tool call in approved mode. The future TUI can replace this prompt with a modal using the same event flow.

## Tool service

`RegistryToolService` wraps `ToolRegistry` for future TUI usage.

It exposes:

- `ListTools`: stable tool metadata for menus/help views
- `RunTool`: an event stream for a single tool call

Tool execution emits:

- `tool_started`
- `tool_finished`
- `tool_failed`
- `tool_permission_required`
- `tool_permission_denied`

The TUI should subscribe to these events rather than calling concrete tools directly.

Runtime tool execution now goes through `ToolService`, so tool events are mapped into runtime events such as `tool_call_started`, `tool_call_finished`, `tool_call_failed`, `tool_permission_required`, and `tool_permission_denied`.

## Instruction rendering

Tool instructions live beside the tool package as `.md` or `.md.tpl` files.

`RenderToolInstructions` loads the matching file by tool name:

- static `.md` files are returned as trimmed text
- `.md.tpl` files are rendered with `InstructionData`

`DefaultInstructionData` currently provides:

- banned command list
- max output length
- max result count
- whether `rg` is available

Concrete tools should put the rendered instructions into `ToolDefinition.Prompt` when they are implemented.
