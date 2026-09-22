# Tools

`internal/agent/tools` contains the coding tool set. The CLI and TUI register
these plus any tools advertised by configured MCP servers; `/tools` lists the
result.

## Built-in tools

| Tool | Purpose |
| --- | --- |
| `bash` | Shell commands via `bash -lc`, with validation, output truncation, and background jobs. |
| `read` | Read a workspace file with optional line range limits. |
| `write` | Create or overwrite a file. |
| `edit` | Exact string replacement in an existing file. |
| `ls` | List directories under a workspace path. |
| `glob` | Find files by glob pattern. |
| `grep` | Search text inside workspace files. |
| `job_output` | Read output and status of a background job. |
| `job_kill` | Terminate a background job. |
| `todo_write` | Record or update the session task list. |
| `todo_blocked` | Report a real blocker and stop the task-list chain. |
| `exit_plan_mode` | Submit a plan for approval and leave plan mode. |
| `task` | Delegate a read-only investigation to a subagent. |
| `web_fetch` | Fetch an http(s) URL and return readable text. |
| `web_search` | Search the web through a configured endpoint. |
| `install_skill` | Install a `SKILL.md` skill from a curated name, local path, or GitHub repo. |
| `memory` | Read and write durable notes that outlive the conversation (see `docs/memory.md`). |

`bash` uses `os/exec` with `bash -lc`, not `mvdan/sh`. It supports command
validation, banned-command checks, working directory validation, output
truncation, foreground execution, explicit background execution, and
auto-backgrounding after a configured duration.

`read`, `write`, `edit`, and `ls` share workspace path validation. Relative
paths resolve under the work directory; absolute paths are allowed only when
they stay inside the work directory or an allowed root.

`read` returns at most `limit` lines (default 200, hard cap 2000) and stops at
the byte budget as well, so a minified or generated file cannot flood the
context through one call. When it truncates, the content ends with the line
range it returned, the total line count, and the `offset` to continue from, and
the metadata carries `truncated` / `truncated_by` (`lines` or `bytes`).

`bash` rejects a command with a trailing background operator (`&`, whitespace
and newlines ignored, `&&` excluded) and points at `run_in_background` instead.

`glob` and `grep` are implemented in Go. They ignore `.git`, `.gocache`,
`node_modules`, `vendor`, `dist`, `build`, and `target`. `glob` uses Go
`filepath.Match` semantics rather than full shell globstar behavior.

## File observation: read before write, and snapshots

Mutating file tools are wired to a session-scoped observer:

- `read` records the file in the `read_files` table.
- `write` and `edit` refuse to touch an existing file the session has not read,
  with an error telling the model to read it first. New files are always
  allowed.
- Before an overwrite or edit, the previous content is stored in the `files`
  table, versioned per session and path.

`/undo` restores the newest snapshot of the session. The rule can be disabled
with `"workspace": {"require_read_before_write": false}`.

## Task list

`todo_write` records the plan and `todo_blocked` reports a blocker; both are
read-only from the permission point of view. The run loop consumes the stored
list (see the task-list execution chain in [commands.md](commands.md)): it keeps
the run going while items remain and stops when the agent reports a blocker or
the list is complete. The runtime reads the list through the `TodoReader`
interface and reacts to the `todos`/`rendered` and `blocked`/`reason` metadata
keys, so it never imports this package.

`TodoReader.Load` is called from the run goroutine. An implementation must be
safe for that call; the shipped session-backed reader serializes on the session
store's own lock.

## Memory

`memory` is a single tool with an `action` switch (`write`, `search`, `read`,
`list`, `delete`) rather than several tools, which keeps the schema count down.
It is declared read-only because notes live under `.mini-opencode/` and never
touch the workspace, so it stays usable in plan mode and never prompts.

The tool is constructed as `nil` when no store is configured, so callers
register it unconditionally. Full behavior, file format, and recall scoring are
in [memory.md](memory.md).

## Task delegation

`task` runs a nested runtime with only `read`, `ls`, `glob`, and `grep`, using
the `task` prompt template. It cannot modify the workspace, it does not see the
parent conversation, and only its final answer returns to the parent — which
keeps exploratory output out of the main context.

## Web tools

`web_fetch` performs an http(s) GET, converts HTML to text (dropping scripts,
styles, and markup), caps the result at roughly 20k characters, and prefixes
the content with a notice that it is untrusted data.

`web_search` needs an endpoint, because search is a provider choice:

```json
{
  "web": {
    "search_url": "http://localhost:8888/search?format=json&q={query}",
    "search_api_key_env": "SEARCH_API_KEY",
    "timeout_seconds": 30
  }
}
```

`{query}` is replaced with the URL-encoded query; without the placeholder the
query is appended as `q=`. Results are parsed from the common JSON shapes
(`results` / `items` / `data` / a top-level array, with `title`/`name`,
`url`/`link`, `snippet`/`content`), falling back to scraping anchors from HTML.
When no endpoint is configured the tool is not registered at all, so it cannot
waste a turn.

## Tool interface

Agent tools expose a `ToolDefinition` and run with structured input/output:

- `ToolDefinition`: name, description, JSON schema, prompt text, behavior flags
- `ToolBehavior`: dangerous, requires confirmation, supports background, read-only
- `ToolInput`: call id, tool name, raw JSON arguments
- `ToolOutput`: textual content plus metadata for UI/runtime consumers

Metadata carries values such as `cwd`, `exit_code`, `job_id`, truncation flags,
`todos`/`rendered` for the todo tool, and `plan`/`approved` for plan
submission. The runtime watches those keys to raise `todos_changed` and
`plan_submitted` events without importing the concrete tools.

## Permissions

`ToolRegistry` can be configured with a `PermissionPolicy` before running tools.

The default policy checks banned shell command fragments such as `sudo`, `git
push`, and `git reset --hard`, plus `path` and `working_dir` arguments escaping
the configured workspace, and tool behavior flags.

Denied calls return a `ToolResult` with `permission=deny` metadata. Calls that
require confirmation return `permission=confirm`; the front end then asks
`allow? [y] once · [a] always this session · [n] deny`. An `a` answer adds the
tool to the session allowlist, which `/status` reports.

## Tool service

`RegistryToolService` wraps `ToolRegistry` for UI usage and exposes `ListTools`
and `RunTool` (an event stream). Tool execution emits `tool_started`,
`tool_finished`, `tool_failed`, `tool_permission_required`, and
`tool_permission_denied`; the runtime maps these into its own events.

## Instruction rendering

Tool instructions live beside the tool package as `.md` or `.md.tpl` files.
`RenderToolInstructions` loads the matching file by tool name: static `.md`
files are returned as trimmed text, `.md.tpl` files are rendered with
`InstructionData` (banned commands, output length, result count, `rg`
availability). Concrete tools put the rendered instructions into
`ToolDefinition.Prompt`, which the provider adapter appends to the description.
