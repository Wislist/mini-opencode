# Commands

Slash commands exist in both front ends. The line-mode CLI is used when stdin
is not a terminal (`app.Run`); the TUI is used otherwise (`app.RunTUI`).

| Command | CLI | TUI | What it does |
| --- | --- | --- | --- |
| `/help` | ✅ | ✅ | Show help. |
| `/version` | ✅ | ✅ | Print the version. |
| `/tools` | ✅ | ✅ | List the registered tools (including MCP tools). |
| `/workspace` | ✅ | ✅ | Show the working directory and `workspace.allowed_roots`. |
| `/permissions [ask\|auto-review\|full-access [confirm]]` | ✅ | ✅ | 查看或切换权限模式；完全访问必须显式确认。 |
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

## Keyboard shortcuts (TUI)

| Key | Effect |
| --- | --- |
| `tab` | Toggle plan mode (or accept the highlighted slash-command completion). |
| `ctrl+t` | Fold or unfold the pinned task panel. Works while a run is in progress. |
| `shift+enter` | Insert a newline in the prompt box. |
| `ctrl+j` / `alt+enter` | Insert a newline too; fallbacks for terminals without the kitty keyboard protocol. |
| `enter` | Send the message. |
| `↑` `↓` / wheel | Scroll the transcript. |
| drag (left button) | Select text; releasing copies it to the system clipboard. |
| click (no drag) | Clear the selection. |
| `esc` | Interrupt a run; otherwise deny a prompt, reject a plan, or close the picker. |
| `ctrl+c` | Quit when idle, interrupt when running. |

### Multiline input

The prompt box grows with its content, up to six lines, and wraps long lines
automatically. `enter` sends; `ctrl+j` and `alt+enter` insert a newline.

`shift+enter` inserts a newline, which requires the terminal to disambiguate
modified keys. Bubble Tea v2 negotiates the kitty keyboard protocol for this and
reports the result as `KeyboardEnhancementsMsg`; when the terminal supports it,
`shift+enter` arrives as its own key. On terminals that do not, the protocol is
unavailable and `ctrl+j` (LF) and `alt+enter` (ESC CR) remain — they are distinct
bytes that work regardless, so a newline is always reachable.

The upgrade to Bubble Tea v2 was required for this: v1's key type had no shift
field at all, so `shift+enter` and `enter` were literally the same value.

### Selecting and copying text

Enabling mouse reporting (so the wheel arrives as exact events instead of being
translated by the terminal) takes drag-to-select away from the terminal, so
selection is implemented in the program: drag with the left button over the
transcript or the input box, and the selected text is copied to the system
clipboard on release via `pbcopy`.

The extracted text is taken from the rendered frame, so it is what you actually
see — no ANSI escapes, no box borders, no trailing padding. A click without a
drag clears the selection instead of copying one character, and the selection is
also cleared on `esc` and when you send a message.

### The task panel

The panel is **scoped to one turn**. It is cleared when you send the next
message, so a list that finished, was abandoned, or was interrupted with `esc`
does not stay pinned above the input for the rest of the conversation. The store
is cleared at the same time, so switching conversations cannot resurrect it.

`ctrl+t` folds it down to a single summary line:

```
expanded                              folded
╭──────────────────────────╮          ╭──────────────────────────╮
│ todos:                   │          │ todos: 2/5 done · task 3 │
│ [x] a                    │   ctrl+t │              (ctrl+t)    │
│ [>] task 3               │  ──────▶ ╰──────────────────────────╯
│ [ ] d                    │
│ (2/5 done)               │  ◀──────
╰──────────────────────────╯   ctrl+t
```

Folding keeps the progress counters visible rather than hiding the panel
entirely: the point is to stop it consuming screen space, not to hide that work
is outstanding. The fold state survives the agent rewriting the list. The toggle
writes nothing into the transcript — the panel is pinned chrome, and the fold
state is already legible from the panel itself. `ctrl+t` appears in the help bar
(and in the running footer) whenever a list exists, so the key stays
discoverable.

## Permission answers

### Modes

- `ask`（请求批准，默认）：沿用现有策略；超出允许根目录、命中旧命令黑名单直接拒绝，
  `Dangerous` / `RequiresConfirmation` 工具请求人工审批；只读工具照常免审批。
- `auto-review`（帮我批准）：在默认策略之上，用保守本地规则批准工作区 / allowed roots 内的
  `write`、`edit`，以及精确的 `pwd`、`/bin/pwd`、`/bin/ls`。不接受额外参数、命令拼接、重定向、
  命令替换、项目脚本或 Git 配置执行；显式后台运行仍需审批。无法判定的需审批操作仍提示用户。不是独立模型审核器。
- `full-access`（完全访问）：主 agent 的文件工具可访问当前账户有权限的任意路径，
  跳过逐次审批和旧命令黑名单。SafetyHook / PlanModeHook / LoopGuardHook、先读后写保持有效。
  普通 `git push` 不再受旧黑名单阻止，但强推、硬重置等仍由 SafetyHook 阻止。

`/permissions` 显示三种选项。`/permissions full-access` 只显示警告；必须再输入
`/permissions full-access confirm` 才启用。其余模式直接切换。重复选择当前模式是无副作用操作，
未知值、错误参数不会改变模式。切换仅限前台空闲时，成功切换会清空旧的 always-allow 工具授权。

模式在本进程内跨会话、配置密钥后的 runtime 重建保持，不写回配置，退出后恢复
`config.json` 中的 `permissions.mode`（缺省 `ask`）。显式配置 `full-access` 表示用户已同意在启动时启用。
`/status` 报告模式；TUI 顶栏空间不足时省略徽标，完整信息仍可用命令查看。

**无操作系统级沙箱**：shell 启动文件、shell 内的路径访问和网络连接不受系统级隔离。
Web 工具沿用只读免审批；MCP 按声明的行为标志审批；`task` 子 agent 仍是原工作区内的只读工具集。
降级权限只影响后续调用，不杀死已经运行的后台任务（需要时先用 `job_kill` 停止）。

### Manual answers

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
