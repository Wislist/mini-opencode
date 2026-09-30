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
- 权限：完全信任 / 替我审核 / 请求批准三种模式、会话级 always-allow、写操作 diff 预览
- 多 provider：`providers` 目录（自定义名称/端点/模型列表/密钥，第三方中转开箱可用）、
  `/provider` 与 `/model` 浮层切换（仅本次进程生效）、切换时通过 `AdoptStateFrom` 保住对话；
  旧的单 `provider` 块继续可用
- 新增供应商流程（TUI）：`/provider add` 三框表单（名称/地址/密钥，密钥遮罩）→ 向该地址请求
  `/models` **主动拉取全部模型**（含厂商上报的上下文窗口，自动写进 `context_window`）
  → 多选勾选（默认全选）→ 写入 `config.json` 并立即切换；
  校验或拉取失败退回表单且保留已填内容，任何一步 `esc` 都不会落盘。`/model refresh`
  对当前 provider 做同样的拉取（列表为已配置 ∪ 拉回的并集）
- Plan 模式闭环：只读约束 + `exit_plan_mode` 提交 + 审批后继续实现
- todo 执行链：列表未完成的项由 runtime 自动续跑，阻塞由 agent 通过 `todo_blocked` 自行判定
- todo 面板：只在有未完成项时显示（完成后自动消失），`ctrl+t` 手动显隐，窄/矮终端下自动收缩不溢出
- run 级重试：整轮 provider 失败自动重试，transcript 不丢、工具不重复执行
- 上下文窗口：三级来源（显式 `context_window` → 内置模型表 → 占位猜测 8192），**猜测值在
  header / `/status` / `/provider` 里标成「估值」**，并提供两条修复路径（手填或 `/model refresh`
  从 `/models` 自动读）
- 上下文压缩：按剩余余量触发（crush 式阈值）；**非破坏性**——摘要追加并标记，原文保留在
  内存与 SQLite，只在发给 provider 时截断；压缩时把 todo 列表并入总结指令
- 会话检索：FTS5 全文索引 `messages`，触发器随写同步，可搜索历史会话- 跨会话记忆：`.mini-opencode/memory/*.md`（人类可读可编辑），`memory` 工具主动写入 +
  run 结束后台自动提炼；召回按相关性只注入前 3 条，`/memory` 查看
- MCP：`stdio` + `http`（streamable HTTP，含 `Mcp-Session-Id`）+ `sse`（legacy HTTP+SSE）
  三种传输，`headers`/`token`/`token_env` 鉴权，工具以 `<server>__<tool>` 注册、`/mcp` 查看状态
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
| 正好在 tag 上 | `0.5.0` |
| tag 后有 N 个提交 | `0.5.0-dev.N.g<sha>` |
| 没有任何 tag | `0.5.0-dev.g<sha>` |
| 工作区有未提交改动 | 追加 `-dirty` |

基线版本只在 `internal/app/app.go` 定义一处，`scripts/version.sh` 从那里读取，所以
**改版本号只需要改那一行**。发版：改 base → `make tag`。


## Workspace 访问范围

### 权限模式

CLI 与 TUI 都支持 `/permissions` 查看选项，TUI 顶栏（宽度允许时）和 `/status` 显示当前模式：

| 命令 | 模式 | 行为 |
| --- | --- | --- |
| `/permissions ask` | 请求批准（默认） | 保留现有 deny/confirm 策略；危险工具弹出人工审批。 |
| `/permissions auto-review` | 替我审核 | 保守本地规则自动批准范围内的 `write`/`edit`，及精确命令 `pwd`、`/bin/pwd`、`/bin/ls`；其他需审批工具仍交给用户。不是模型审核器。 |
| `/permissions full-access confirm` | 完全信任 | 解除主 agent 文件工具的工作区限制、跳过逐次审批及旧命令黑名单；仍受安全 Hook、plan 模式、先读后写与系统账户权限约束。 |

只输入 `/permissions full-access` 会显示风险提示，不会启用。模式切换仅对本次进程有效，
清除已有工具 always-allow 授权，不清空对话；不允许在前台 run 中切换，也不会终止已启动的后台进程。
保存设置（如 `/provider add`）不会意外持久化临时权限模式。启动默认值可显式写入配置：

```json
{ "permissions": { "mode": "ask" } }
```

未知模式会报错，未配置时使用 `ask`。显式配置 `full-access` 表示已同意在启动时启用，不再二次提示。

**这些是应用层审批机制，不是操作系统沙箱。** `bash -lc` 包括 shell 启动文件会以当前账户权限运行；
命令字符串里的路径和网络行为没有系统级隔离。Web 工具沿用只读免审批行为，MCP 依据其工具行为声明审批；
只读 `task` 子 agent 仍限制在原工作区与 allowed roots 内。不要将模式名称理解为与 Codex 安全能力完全等价。

### 配置文件工具的允许目录

默认情况下内置文件工具只能读写启动时所在的工作目录（完全访问模式除外）。如果从子目录启动又需要访问整个项目，可以在 `config.json` 里配置 `workspace.allowed_roots`：

```json
{
  "workspace": {
    "allowed_roots": ["/Users/wislist/Desktop/worksplace/LLM-Learn"]
  }
}
```

`allowed_roots` 中的路径（绝对路径或相对当前工作目录）会被规范化为绝对路径并去重，agent 在权限校验和文件工具中都会把这些目录视为可访问范围（该列表只存在于 `config.json` 里）。

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
