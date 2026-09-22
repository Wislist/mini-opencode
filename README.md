# mini-opencode

一个从零开始实现的 Go 版 agent 终端项目。

本项目不依赖仓库里已有的 `02-llmg`、`03-mini-opencode` 或其它历史实现。

> 目标平台为 macOS，暂不考虑 Linux/Windows 适配。Shell 通过系统 `bash -lc` 执行。

## 目标

实现一个接近 `crush` 一半能力的本地 agent 终端：

1. Bubble Tea TUI
2. Agent runtime event stream
3. 权限与工具执行体验
4. 上下文工程（超过阈值自动压缩 + 真实 token 计数）
5. 多 provider 配置
6. MCP lifecycle
7. Git diff 与测试反馈

## 当前阶段

- 独立 `go.mod` 与入口 `cmd/mini-opencode`
- 标准库 CLI 壳（非终端 stdin）与 Bubble Tea TUI（终端）
- `internal/agent` runtime：turn loop、event stream、hook 链、usage 累计
- provider 抽象：`echo` 与 OpenAI 兼容（SSE 流式、usage、retry/backoff、可配置 timeout）
- 工具集：bash/read/write/edit/ls/glob/grep/job_output/job_kill/todo_write/todo_blocked/exit_plan_mode/task/web_fetch/web_search/install_skill/memory
- 文件观察：read-before-write + 改动前快照 + `/undo`
- 并发：只读工具同一批次并发执行，写操作串行；runtime 状态加锁，`go test -race ./...` 通过
- 权限：deny/confirm 策略、会话级 always-allow、写操作 diff 预览
- Plan 模式闭环：只读约束 + `exit_plan_mode` 提交 + 审批后继续实现
- todo 执行链：列表未完成的项由 runtime 自动续跑，阻塞由 agent 通过 `todo_blocked` 自行判定
- run 级重试：整轮 provider 失败自动重试，transcript 不丢、工具不重复执行
- 上下文压缩：按剩余余量触发（crush 式阈值）；**非破坏性**——摘要追加并标记，原文保留在
  内存与 SQLite，只在发给 provider 时截断；压缩时把 todo 列表并入总结指令
- 会话检索：FTS5 全文索引 `messages`，触发器随写同步，可搜索历史会话- 跨会话记忆：`.mini-opencode/memory/*.md`（人类可读可编辑），`memory` 工具主动写入 +
  run 结束后台自动提炼；召回按相关性只注入前 3 条，`/memory` 查看
- MCP：启动 stdio server、握手、工具以 `<server>__<tool>` 注册、`/mcp` 查看状态
- 会话：SQLite 持久化、message parts、归档、todos、token 统计、`parent_session_id` 分支（`/fork`）
- Prompt：coder/summary/initialize/title/task/agentic_fetch 模板全部接入
- Skills：curated/local/GitHub 安装，注入 `<available_skills>`

## 目录

```text
cmd/mini-opencode     CLI 入口
internal/app          CLI 壳、TUI 启动、运行时装配
internal/agent        agent 核心工作流（runtime/provider/hook/permission）
internal/agent/prompt Prompt 组装与模板
internal/agent/tools  工具实现与指令模板
internal/mcp          MCP stdio client、manager 与工具适配
internal/session      SQLite 会话存储（messages/files/read_files/todos + FTS5 全文检索）
internal/memory       跨会话记忆（Markdown 笔记、自动提炼、按需召回）
internal/skills       Skill 存取与安装（curated/local/GitHub）
internal/tui          Bubble Tea 界面
internal/diffutil     权限提示用的行级 diff
internal/config       config.json / secrets.json
docs/commands.md      斜杠命令与权限交互
docs/mcp.md           MCP 接入说明
docs/prompt.md        Prompt 组装说明
docs/tools.md         Tools 说明
docs/providers.md     Provider 配置说明
docs/skills.md        Skills 说明
docs/hooks.md         Hooks（危险行为拦截 / 循环检测 / plan 模式）说明
docs/memory.md        跨会话记忆（文件格式、自动提炼、召回策略）
```

## 运行

```bash
make run          # 带 git 版本号跑 TUI
make build        # 构建到 bin/mini-opencode
make check        # fmt-check + vet + test
make help         # 全部目标

go run ./cmd/mini-opencode   # 也可以直接跑，版本号用源码兜底值
```

## 版本号

`make build` 用 git 信息自动注入版本号，不需要手动维护：

| 状态 | 报告的版本 |
| --- | --- |
| 正好在 tag 上 | `0.4.0` |
| tag 后有 N 个提交 | `0.4.0-dev.N.g<sha>` |
| 没有任何 tag | `0.4.0-dev.g<sha>` |
| 工作区有未提交改动 | 追加 `-dirty` |

基线版本只在 `internal/app/app.go` 定义一处，`scripts/version.sh` 从那里读取，所以
**改版本号只需要改那一行**。发版：改 base → `make tag`。


## Workspace 访问范围

默认情况下 agent 只能读写它启动时所在的工作目录。如果从子目录启动又需要访问整个项目，可以在 `config.json` 里配置 `workspace.allowed_roots`：

```json
{
  "workspace": {
    "allowed_roots": ["/Users/wislist/Desktop/worksplace/LLM-Learn"]
  }
}
```

`allowed_roots` 中的路径（绝对路径或相对当前工作目录）会被规范化为绝对路径并去重，agent 在权限校验和文件工具中都会把这些目录视为可访问范围。用 `/workspace` 命令查看当前工作目录与已允许的根目录。

同一节点还控制改文件前的观察策略：

```json
{
  "workspace": { "require_read_before_write": true }
}
```

默认 `true`：`write`/`edit` 不能覆盖本会话未 `read` 过的已存在文件，且改动前会把旧内容存进会话快照，`/undo` 可回滚最后一次改动。

## 上下文预算

对话接近 `provider.context_window` 时 runtime 会自动压缩（默认阈值 0.85），打印 `context auto-compacted` 后继续跑；单次 run 的 turn 预算由 `agent.max_turns` 控制（默认 100），打满时给出可操作的提示而不是静默失败。

```json
{
  "provider": { "context_window": 128000 },
  "agent": { "max_turns": 200, "compact_threshold": 0.85 }
}
```

## MCP

```json
{
  "mcpServers": {
    "go": { "enabled": true, "command": "gopls", "args": ["mcp"] }
  }
}
```

`enabled: true` 的 server 会在启动时拉起并注册其工具；单个 server 失败只打印告警，不阻塞启动。用 `/mcp` 查看每个 server 的状态与工具数。

## Web

`web_fetch` 开箱可用；`web_search` 需要配置搜索端点（例如本地 SearXNG）：

```json
{
  "web": { "search_url": "http://localhost:8888/search?format=json&q={query}" }
}
```

未配置端点时 `web_search` 不会注册，避免无谓的重试。抓取到的网页内容会被标记为不可信数据。

## Skills

Agent 可通过 `install_skill` 工具自行安装 skill（`SKILL.md`），来源包括内置 curated 列表、本地路径、GitHub 仓库。安装后 skill 会出现在系统 prompt 的 `<available_skills>` 中（CLI 与 TUI 都会注入）；CLI 中用 `/skills` 查看已安装与可安装的 skill。

## Hooks

Runtime 内置三个 hook：

- `SafetyHook`：拦截删库、`drop database`、`git push --force`、`git clean -fdx`、`rm -rf .git` 等破坏性命令，并禁止写入工作区根目录或 `.git`；遇到 `rm -rf /`、`mkfs` 等灾难性命令直接中止运行。
- `LoopGuardHook`：检测 agent 重复调用同一工具或整轮回复重复，达到阈值（默认 3 次）即中止运行，避免重复思考造成的 token 浪费。
- `PlanModeHook`：plan 模式下只放行只读工具（以及 `exit_plan_mode`、`todo_write`）。

两条入口（CLI/TUI）都会注册全部三个 hook。

详见 [docs/hooks.md](docs/hooks.md)，命令与权限交互见 [docs/commands.md](docs/commands.md)。
