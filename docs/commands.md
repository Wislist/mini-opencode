# Commands

Slash commands exist in both front ends. The line-mode CLI is used when stdin
is not a terminal (`app.Run`); the TUI is used otherwise (`app.RunTUI`).

| Command | CLI | TUI | What it does |
| --- | --- | --- | --- |
| `/permissions [ask\|auto-review\|full-access [confirm]]` | ✅ | ✅ | 查看或切换权限模式（完全信任 / 替我审核 / 请求批准）；完全信任必须显式确认。 |
| `/provider [name]` | ✅ | ✅ | `providers` 目录：列出（标出激活项与密钥来源）或切换到某个提供商。仅本次进程生效。 |
| `/provider add <name> <base_url> [model]` | ✅ | ✅ | 带参数：新增或就地更新提供商并写入全局 `~/.mini-opencode/config.json`。不带参数（TUI）：打开三框表单。 |
| `/model [id]` | ✅ | ✅ | 列出当前提供商的模型或切换模型；未列出的 id 也接受。仅本次进程生效。 |
| `/model context [tokens\|1M\|128K\|auto]` | ✅ | ✅ | 查看或保存当前提供商的上下文窗口，立即生效并保留对话；TUI 模型菜单按 `c` 填写。 |
| `/model refresh` | ✅ | ✅ | 向当前提供商请求 `/models`：拉回全部模型（含厂商上报的上下文窗口），TUI 里多选勾选，CLI 取并集写入全局 `~/.mini-opencode/config.json`。 |
| `/status` | ✅ | ✅ | Workspace, git branch, mode, provider（含 `window`）, session, context estimate (split into system prompt vs conversation), provider-reported tokens, session-approved tools, todo list。窗口是估值时会额外说明怎么修。 |
| `/skills` | ✅ | — | List installed skills and curated names. |
| `/mcp` | ✅ | ✅ | Per-server MCP status: disabled, running (tool count), or the startup error. |
| `/init` | ✅ | ✅ | Run one analysis turn with the `initialize` prompt template to create or refresh `AGENTS.md`. |
| `/fork` | ✅ | ✅ | Branch the current session into a new one that records `parent_session_id`. |
| `/undo` | ✅ | ✅ | Restore the newest file snapshot recorded in this session. |
| `/compact` | ✅ | ✅ | Summarize the conversation and make the summary the active head of the transcript (the original messages are retained). |
| `/memory [query]` | ✅ | ✅ | List stored cross-session memories, or search them when a query is given. |
| `/session`, `/sessions` | ✅ | ✅ | List saved sessions and switch to one. |
| `/newsession` | ✅ | ✅ | Start a fresh conversation. Clears the transcript, so the conversation part of the context drops to 0. |
| `/archive` | ✅ | ✅ | Export the current session to JSON and start a new one. |
| `/quit`, `/exit` | ✅ | ✅ | Exit. |

Unknown `/commands` are rejected with a hint instead of being sent to the
model as a prompt. `tab` is the only plan-mode switch (the TUI has no `/plan`).

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
| `↑` `↓` `enter` `esc` | Drive the `/permissions` picker. |
| `shift+enter` | Insert a newline in the prompt box. |
| `ctrl+j` / `alt+enter` | Insert a newline too; fallbacks for terminals without the kitty keyboard protocol. |
| `enter` | Send the message; while a run is in progress, keep the draft for after it ends. |
| `↑` `↓` / wheel | Scroll the transcript. |
| drag (left button) | Select text; releasing copies it to the system clipboard. |
| click (no drag) | Clear the selection. |
| `esc` | Interrupt a run; otherwise deny a prompt, reject a plan, or close the picker. |
| `ctrl+c` | Quit when idle, interrupt when running. |

### Permission modes

`/permissions` with no argument opens a picker as a bottom overlay rather than
printing the choices into the transcript — a menu belongs on the overlay, not in
the conversation, and leaving it in the scrollback made it read as part of the
history:

```
╭──────────────────────────────────────────────────────────────╮
│ permissions:  ↑↓ select · enter apply · esc cancel           │
│ ▶ * 请求批准   每次危险操作都询问                            │
│     替我审核   保守本地规则，不确定时询问                    │
│     完全信任   跳过逐次审批（需确认）                        │
│ 仅本次进程生效；不是操作系统沙箱。                           │
╰──────────────────────────────────────────────────────────────╯
```

`*` marks the mode in effect. Choosing full access opens a second confirmation
step, because it removes the per-call approval gate; `esc` there returns to the
picker rather than dismissing everything. Switching modes also clears the
session's "always allow" grants, since the trust level is being re-decided.

`/permissions ask|auto-review|full-access [confirm]` still works directly for
scripts and muscle memory — the picker is an addition, not a replacement.

### Switching provider and model

裸 `/provider`、`/model` 打开浮层，带参数走直接路径（与 `/permissions` 同一套交互）：

```
╭──────────────────────────────────────────────────────────────╮
│ provider:  ↑↓ select · enter switch · esc cancel             │
│ ▶ * relay  我的中转  https://relay.example.com/v1  …  key=config
│     deepseek  https://api.deepseek.com  …  key=env:DEEPSEEK_API_KEY │
│     echo  本地回显，无需密钥                                  │
│ 切换仅本次进程生效；新增用 /provider add <name> <base_url> [model]
╰──────────────────────────────────────────────────────────────╯
```

列表最后一行是 `＋ 新增提供商`：进入三框表单（名称 / 地址 / 密钥），提交后向该地址请求
`/models`，再让你勾选要启用的模型。步骤与键位见 [docs/providers.md](providers.md)。

要点：

- 光标默认停在**当前生效**的提供商上（`*` 标出它），enter 生效、esc 取消。
- 列表比屏幕长时**跟着光标滚动**，表头显示 `显示 3-8/21`；勾选层同理。
- 选中当前项是空操作：不会白白重建 runtime。
- 切换会重建 runtime，但**对话、token 统计、压缩标记、system prompt 都保留**
  （`Runtime.AdoptStateFrom`）。构建失败（例如新提供商还没密钥）时旧 runtime 继续服务，
  配置停在新的提供商上，所以紧接着补上的密钥（`/provider add` 同名条目）正好写到正确的名字下。
- `add`、`/model refresh`、`/model context` 写全局 `~/.mini-opencode/config.json`；`switch`
  与 `/model <id>` 不写（仅本次进程生效）。
- 每条 provider 行都带 `key=config|env:NAME|missing`，这行就是「为什么切换失败」的答案。

### Multiline input

The prompt box grows with its content, up to six lines, and wraps long lines
automatically. `enter` sends; `ctrl+j` and `alt+enter` insert a newline.

The composer stays focused and editable while an answer is still streaming, so
the next message can be drafted during a run. `enter` does not start another
turn then: the draft is kept and sent once the run finishes. The real terminal
cursor follows the composer during the run as well, which keeps IME pre-edit
text (for example Chinese input) inside the input box instead of on the help
bar; without it, committed characters could appear to be swallowed.

`shift+enter` inserts a newline, which requires the terminal to disambiguate
modified keys. Bubble Tea v2 negotiates the kitty keyboard protocol for this and
reports the result as `KeyboardEnhancementsMsg`; when the terminal supports it,
`shift+enter` arrives as its own key. On terminals that do not, the protocol is
unavailable and `ctrl+j` (LF) and `alt+enter` (ESC CR) remain — they are distinct
bytes that work regardless, so a newline is always reachable.

The upgrade to Bubble Tea v2 was required for this: v1's key type had no shift
field at all, so `shift+enter` and `enter` were literally the same value.

### Scrolling

The transcript follows new output only while the view is already at the bottom,
so scrolling up to read history is not interrupted by a streaming answer. Send a
message and the view returns to the newest content: the question just asked and
the answer that follows are always on screen.

### Selecting and copying text

Enabling mouse reporting (so the wheel arrives as exact events instead of being
translated by the terminal) takes drag-to-select away from the terminal, so
selection is implemented in the program: drag with the left button over the
transcript or the input box, and the selected text is copied to the system
clipboard on release through the host clipboard API (macOS pasteboard or the
Windows clipboard).

Transcript selections are anchored to rendered conversation rows: scrolling moves
highlights with the text, and copying includes selected rows outside the visible
viewport. While holding the left mouse button, drag to the top or bottom edge of
the transcript to scroll continuously. Moving back inside, releasing, or reaching
the end stops scrolling. The input and footer are excluded from transcript drags.

Copying uses the viewport renderer, preserving wrapping and terminal-cell columns
(including Chinese text), without ANSI escapes. A click without a drag clears the
selection. Resizing clears it because text wrapping can change; `esc` and sending
a message also clear it.

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
- `auto-review`（替我审核）：在默认策略之上，用本地规则批准工作区 / allowed roots 内的
  `write`、`edit`，以及一组 shell 命令。shell 命令先做分词：出现任何 shell 元字符
  （`;` `|` `&` `>` `<` `$( )` 反引号、通配符、subshell、换行、转义）或引号不闭合即需审批；
  程序必须是 PATH 上的裸名字（或受信的 `/bin/pwd`、`/bin/ls`）。本地规则放行两类命令：
  1. **只读命令**：`pwd`、`ls`、`git` 只读子命令（`status`/`log`/`diff`/`show`/`blame`/
     `rev-parse`/`ls-files`/`describe`/`shortlog`，以及只读形式的 `branch`/`remote`）、
     `go` 的 `version`/`env`/`list`/`doc`（`go env -w` 除外）；Windows 另放行
     `Get-Location`/`Get-ChildItem`/`dir`。
  2. **构建 / 测试入口的前缀规则**（内置 + `permissions.allow_commands`）：`go build`/
     `test`/`run`/`vet`/`fmt`、`make`、`npm test`/`run`/`ci`、`pytest`、`python -m pytest`。
     前缀之后的参数仍受限：绝对路径、`..` 穿越、Windows 盘符路径，以及 `go` 的
     `-exec`/`-toolexec` 都不匹配，所以借道写不到工作区外。刻意不含 `go generate`
     （执行源码里的指令）、裸 `npm`（可 publish）、裸解释器与 `npx`。
  3. **模型审核**（`permissions.auto_review_model`，默认开启）：本地规则判不了时，把这条
     命令交给当前 provider 做一次独立判定（20s 超时、不带工具）。只有模型显式返回
     `{"allow": true}` 才转为放行；拒绝、回复不合法、调用失败或超时都退回人工审批
     （fail-closed）。设 `auto_review_model: false` 回到纯本地规则、零网络调用。
  `git -c`/`--config`/`--exec-path`/`--output`、`git branch -D`、`git remote add` 等仍需审批。
  显式后台运行仍需审批。**模型审核不是沙箱**，它只是一次建议；SafetyHook 仍然在先。
- `full-access`（完全信任）：主 agent 的文件工具可访问当前账户有权限的任意路径，
  跳过逐次审批和旧命令黑名单。SafetyHook / PlanModeHook / LoopGuardHook、先读后写保持有效。
  普通 `git push` 不再受旧黑名单阻止，但强推、硬重置等仍由 SafetyHook 阻止。

`permissions.allow_commands` 是追加前缀，例如 `[["cargo","test"],["terraform","plan"]]`。
它只能**加宽** auto-review，且只在全局 `~/.mini-opencode/config.json` 生效 —— 项目
config 里的 `allow_commands` / `auto_review_model` 会被忽略，否则 clone 下来的仓库就能
给自己的构建脚本开白名单。项目 config 仍可收窄 `permissions.mode`。

`/permissions` 显示三种选项。`/permissions full-access` 只显示警告；必须再输入
`/permissions full-access confirm` 才启用。其余模式直接切换。重复选择当前模式是无副作用操作，
未知值、错误参数不会改变模式。切换仅限前台空闲时，成功切换会清空旧的 always-allow 工具授权。

模式在本进程内跨会话、配置密钥后的 runtime 重建保持，不写回配置，退出后恢复
项目配置 `<project>/.mini-opencode/config.json` 中的 `permissions.mode`（缺省 `ask`）。显式配置 `full-access` 表示用户已同意在启动时启用。
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
`providers[].context_window` gets small. The run prints/streams a
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
  "providers": [{ "name": "deepseek", "model": "deepseek-chat", "api_key_env": "DEEPSEEK_API_KEY", "context_window": 128000 }],
  "active_provider": "deepseek",
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
