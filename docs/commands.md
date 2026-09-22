# Commands

Slash commands exist in both front ends. The line-mode CLI is used when stdin
is not a terminal (`app.Run`); the TUI is used otherwise (`app.RunTUI`).

| Command | CLI | TUI | What it does |
| --- | --- | --- | --- |
| `/help` | ✅ | ✅ | Show help. |
| `/version` | ✅ | ✅ | Print the version. |
| `/tools` | ✅ | ✅ | List the registered tools (including MCP tools). |
| `/workspace` | ✅ | ✅ | Show the working directory and `workspace.allowed_roots`. |
| `/status` | ✅ | ✅ | Workspace, git branch, mode, provider, session, context estimate (split into system prompt vs conversation), provider-reported tokens, session-approved tools, todo list. |
| `/skills` | ✅ | — | List installed skills and curated names. |
| `/mcp` | ✅ | ✅ | Per-server MCP status: disabled, running (tool count), or the startup error. |
| `/plan` | ✅ | ✅ | Toggle plan mode. In the TUI, `tab` does the same. |
| `/init` | ✅ | ✅ | Run one analysis turn with the `initialize` prompt template to create or refresh `AGENTS.md`. |
| `/fork` | ✅ | ✅ | Branch the current session into a new one that records `parent_session_id`. |
| `/undo` | ✅ | ✅ | Restore the newest file snapshot recorded in this session. |
| `/key [key]` | ✅ | ✅ | Save a provider API key and rebuild the runtime. |
| `/name …` | ✅ | ✅ | Set or show the user/assistant display names. |
| `/compact` | ✅ | ✅ | Summarize the conversation and make the summary the active head of the transcript (the original messages are retained). |
| `/memory [query]` | ✅ | ✅ | List stored cross-session memories, or search them when a query is given. |
| `/session`, `/sessions` | ✅ | ✅ | List saved sessions and switch to one. |
| `/newsession` | ✅ | ✅ | Start a fresh conversation. Clears the transcript, so the conversation part of the context drops to 0. |
| `/archive` | ✅ | ✅ | Export the current session to JSON and start a new one. |
| `/quit`, `/exit` | ✅ | ✅ | Exit. |

Unknown `/commands` are rejected with a hint instead of being sent to the
model as a prompt.

## Interrupting a run (TUI)

While the agent is thinking or compacting, `esc` cancels the current run and
returns to the prompt; `ctrl+c` is still accepted so muscle memory does not quit
the program by accident. While idle, `ctrl+c` quits and `esc` only closes the
command menu (or is passed through to the input field). `esc` keeps its other
meanings: deny a permission request, reject a submitted plan, and close the
session picker.

## Permission answers

When a tool needs approval the prompt accepts:

- `y` — allow this call once.
- `a` — allow this tool for the rest of the session (the allowlist is shown by
  `/status`).
- `n` / `esc` — deny.

For `write` and `edit` the prompt shows a diff of the pending change: removals
and additions in the TUI, `-`/`+` lines in the CLI.

## Task list execution chain

`todo_write` is not just a display: while the session task list still has
outstanding items, the run continues on its own.

- When the model ends a turn with text only, the runtime checks the list. If
  items remain, it injects a continuation message naming the next item, keeps
  the transcript, and runs again. No user message is needed between items.
- The judgement belongs to the agent: it keeps going while work remains, and it
  stops the chain deliberately by calling `todo_blocked` with a reason and what
  it needs. The run then ends cleanly (not as a failure) and the TUI shows the
  pause with the blocker.
- There is no per-item confirmation and no todo-specific round budget. The only
  guard is a stall detector: if the list does not change across
  `maxStalledContinuations` (3) consecutive continuation rounds and the agent
  reported no blocker, the chain stops and says so. This is a dead-loop guard,
  not a work budget.
- `agent.todo_chain: false` disables the chain and restores single-shot runs.

```json
{
  "agent": { "todo_chain": true, "run_retries": 2 }
}
```

## Retrying a failed run

A turn whose provider request fails is retried with the transcript intact:
the failed attempt records nothing, so nothing is replayed and no tool runs
twice. Retries are visible as `run_retry` events ("⟳ retrying the turn"), and
`agent.run_retries` (default 2, `0` disables) bounds them. Retryable HTTP
failures (429/5xx/network) are already retried inside the provider first.

## Context budget

The runtime compacts the conversation automatically when the remaining room in
`provider.context_window` gets small. The run prints/streams a
`context auto-compacted` notice and continues.

The trigger is expressed as remaining headroom rather than a fraction already
used: a window above 200k keeps a flat 20k buffer, a smaller one keeps 20% of
the window free. Setting `agent.compact_threshold` explicitly keeps the older
"fraction consumed" meaning, so existing configs behave exactly as before.

The displayed `ctx` figure is the whole prompt, and it has two parts
(`Runtime.ContextDetails()`):

- **system prompt + tools** — a fixed floor, roughly 4.7k tokens (~18KB of coder
  template, tool instructions, `AGENTS.md` and skills). It is present in every
  request and never decreases.
- **conversation** — the transcript that would actually be sent, counted from
  the compaction marker.

The ctx indicator also shows the conversation part (`ctx 4% · 50 chat`) and
`/status` prints both numbers on separate lines. This matters because clearing
the session with `/newsession` only takes the conversation to zero: the total
barely moves, since the floor dominates it, which previously looked like a
failed reset. The floor is genuinely part of the prompt, so it stays in the
total and in the compaction calculation.

Compaction is non-destructive: the summary is appended and marked, and the
prompt simply starts at that marker. The original messages stay in memory and in
SQLite, so `/undo`, session reload and a later re-summarization still have the
full conversation. A second automatic compaction needs real growth (10% of the
window) beyond the post-compaction size, so an already-huge summary cannot
trigger a compaction on every turn. Failures never abort the run.

The turn budget of a single run is `agent.max_turns` (default 100). When it
runs out, the run stops with a message telling you to send a follow-up message,
raise the limit, or compact.

```json
{
  "provider": { "context_window": 128000 },
  "agent": { "max_turns": 200, "compact_threshold": 0.85 }
}
```

## Cross-session memory

`/compact` handles a single conversation growing too large; memory handles
forgetting *between* conversations. Notes live as Markdown under
`.mini-opencode/memory/`, are written both by the `memory` tool and by a
background extraction pass after each run, and are recalled by relevance into the
next session's system prompt. See [memory.md](memory.md).

## Plan mode

Plan mode blocks every tool that is not read-only. The agent can still inspect
the workspace, write its todo list, and submit a plan through
`exit_plan_mode`; on approval plan mode is turned off and the run continues
into implementation. On rejection the agent stays in plan mode and revises the
plan.
