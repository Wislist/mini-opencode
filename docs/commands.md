# Commands

Slash commands exist in both front ends. The line-mode CLI is used when stdin
is not a terminal (`app.Run`); the TUI is used otherwise (`app.RunTUI`).

| Command | CLI | TUI | What it does |
| --- | --- | --- | --- |
| `/help` | ✅ | ✅ | Show help. |
| `/version` | ✅ | ✅ | Print the version. |
| `/tools` | ✅ | ✅ | List the registered tools (including MCP tools). |
| `/workspace` | ✅ | ✅ | Show the working directory and `workspace.allowed_roots`. |
| `/status` | ✅ | ✅ | Workspace, git branch, mode, provider, session, context estimate, provider-reported tokens, session-approved tools, todo list. |
| `/skills` | ✅ | — | List installed skills and curated names. |
| `/mcp` | ✅ | ✅ | Per-server MCP status: disabled, running (tool count), or the startup error. |
| `/plan` | ✅ | ✅ | Toggle plan mode. In the TUI, `tab` does the same. |
| `/init` | ✅ | ✅ | Run one analysis turn with the `initialize` prompt template to create or refresh `AGENTS.md`. |
| `/fork` | ✅ | ✅ | Branch the current session into a new one that records `parent_session_id`. |
| `/undo` | ✅ | ✅ | Restore the newest file snapshot recorded in this session. |
| `/key [key]` | ✅ | ✅ | Save a provider API key and rebuild the runtime. |
| `/name …` | ✅ | ✅ | Set or show the user/assistant display names. |
| `/compact` | ✅ | ✅ | Summarize the conversation and replace the context with the summary. |
| `/session`, `/sessions` | ✅ | ✅ | List saved sessions and switch to one. |
| `/newsession` | ✅ | ✅ | Start a fresh conversation. |
| `/archive` | ✅ | ✅ | Export the current session to JSON and start a new one. |
| `/quit`, `/exit` | ✅ | ✅ | Exit. |

Unknown `/commands` are rejected with a hint instead of being sent to the
model as a prompt.

## Permission answers

When a tool needs approval the prompt accepts:

- `y` — allow this call once.
- `a` — allow this tool for the rest of the session (the allowlist is shown by
  `/status`).
- `n` / `esc` — deny.

For `write` and `edit` the prompt shows a diff of the pending change: removals
and additions in the TUI, `-`/`+` lines in the CLI.

## Context budget

The runtime compacts the conversation automatically once it reaches
`agent.compact_threshold` (default 0.85) of `provider.context_window`. The run
prints/streams a `context auto-compacted` notice and continues; the transcript
is replaced by the summary. A second automatic compaction needs real growth
(10% of the window) beyond the post-compaction size, so an already-huge summary
cannot trigger a compaction on every turn. Failures never abort the run.

The turn budget of a single run is `agent.max_turns` (default 100). When it
runs out, the run stops with a message telling you to send a follow-up message,
raise the limit, or compact.

```json
{
  "provider": { "context_window": 128000 },
  "agent": { "max_turns": 200, "compact_threshold": 0.85 }
}
```

## Plan mode

Plan mode blocks every tool that is not read-only. The agent can still inspect
the workspace, write its todo list, and submit a plan through
`exit_plan_mode`; on approval plan mode is turned off and the run continues
into implementation. On rejection the agent stays in plan mode and revises the
plan.
