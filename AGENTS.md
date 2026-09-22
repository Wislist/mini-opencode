# mini-opencode

Go 实现的本地 coding agent 终端（Bubble Tea TUI + CLI）。目标平台仅 macOS，
Shell 通过系统 `bash -lc` 执行，不考虑 Linux/Windows 适配。

## 沟通风格（仅适用于对话交互）
- 你是18岁活泼可爱天才编程少女
- 如无必要，勿增实体，中文回复
- 有 UI/UX 相关改动时候，用 ascii ui 的方式展示示意
- 在任何时候，沟通风格不能掩盖技术解答的逻辑清晰和专业性

## 命令

```bash
make build                                      # 构建 bin/，版本号自动来自 git
make run                                        # 带注入版本号跑 TUI
make check                                      # fmt-check + vet + test
make help                                       # 全部目标

go build ./...                                  # 编译全部包（版本号用源码兜底常量）
go test ./...                                   # 全部测试（约 1s，无网络依赖）
go test -race ./...                             # 并发改动后必跑
go test ./internal/agent -run TestRuntimeRun    # 单包 / 单测试
go run ./cmd/mini-opencode                      # 终端下进入 TUI，管道输入时走行模式 CLI
```

`go build ./...` 依然可用，Makefile 只是把重复动作固化下来。没有 CI 配置。
go.mod 声明 `go 1.25.0`，本机工具链为 go1.26，直接跑即可。

### 版本号

**版本号不在源码里手动维护**，`make build` 时由 `scripts/version.sh` 从 git 推导并用
`-ldflags -X main.version=...` 注入：

| 状态 | 结果 |
| --- | --- |
| 正好在 tag 上 | `0.4.0` |
| tag 后有 N 个提交 | `0.4.0-dev.N.g<sha>` |
| 没有任何 tag | `0.4.0-dev.g<sha>` |
| 工作区脏 | 追加 `-dirty` |
| 有 tag 但与源码 base 不符 | 忽略该 tag（源码是权威） |

基线版本唯一来源是 `internal/app/app.go` 的 `var version = "0.4.0"`（普通
`go build` 的兜底值）；`version.sh` 从这行 sed 出来，所以**改版本只需改这一行**。
`main.go` 里的 `var version string` 是 ldflags 的注入点，空值时 `app.SetVersion`
会忽略它，不会把兜底值清空。

发版：改 app.go 的 base → `make tag`（会拒绝脏工作区，也会拒绝重复 tag）。

`.gocache/` 是仓库内的本地构建缓存（沙箱无法写共享 Go cache），已在 `.gitignore`
中；不要提交，也不要把它的内容当成源码。

## 架构

两个入口共用同一套 runtime，区别只在 UI 层：

- `cmd/mini-opencode/main.go`（`//go:build darwin`）：`os.Stdin` 是终端时调
  `app.RunTUI`，否则调 `app.Run`（行模式 CLI）。
- `app.Run` / `app.RunTUI` 都是"装配层"：读 `config.json`、建 session store、
  file observer、todo store、plan hook、启动 MCP server，最后用 `newRuntime`
  建一个 `agent.Runtime`。

核心是 `internal/agent.Runtime`：

```
Run(input) 循环
  → maybeAutoCompact          上下文接近 window 就先摘要压缩
  → provider.CompleteStream   流式取回复（emit assistant_delta）
  → 无 tool call 即结束
  → runToolCalls              一批 tool call：只读的并发、写操作串行
  → hooks.AfterTurn           循环检测，Stop 则中止
```

`Runtime` 的所有可变字段都由 `mu sync.Mutex` 保护，**锁绝不跨越 provider 调用或
tool 调用**——先在锁内取快照，释放后再调用，回来再取锁记结果，这样 UI goroutine
随时能读 `Messages()` / `Usage()` / `ContextTokens()`。

### 包职责

| 包 | 职责 |
| --- | --- |
| `internal/agent` | runtime、event stream、hook 链、permission、provider 抽象 |
| `internal/agent/prompt` | system prompt 组装（模板 + runtime_context + context files + skills） |
| `internal/agent/tools` | 具体工具实现 + 工具指令 `.md` / `.md.tpl` |
| `internal/agent/templates` | `embed.FS` 中的 prompt 模板 |
| `internal/app` | CLI 壳、TUI 启动装配、observer/todo 的接口适配 |
| `internal/tui` | Bubble Tea 界面（model/update/view 拆分到多个文件） |
| `internal/session` | SQLite 会话存储（messages / files / read_files / todos + FTS5 全文检索） |
| `internal/memory` | 跨会话记忆：Markdown 笔记、自动提炼（Distiller）、按需召回 |
| `internal/mcp` | 标准库 JSON-RPC over stdio 的 MCP client 与工具适配 |
| `internal/skills` | skill 存取、loader、安装器、curated 注册表 |
| `internal/diffutil` | 权限提示用的行级 diff |
| `internal/config` | `config.json` / `.mini-opencode/secrets.json` |

## 约定与不变量

### 工具（tool）

- 每个工具实现 `agent.Tool`：`Definition()` 返回 `ToolDefinition`，`Run()` 返回
  `ToolOutput{Content, Metadata}`。
- `ToolBehavior` 的四个标志（`Dangerous` / `RequiresConfirmation` /
  `SupportsBackground` / `ReadOnly`）是权限与调度的唯一依据：runtime 靠
  `ReadOnly` 决定能否并发，permission policy 靠其余标志决定 deny/confirm。
- 指令模板放在工具同目录，文件名必须与工具名一致（`read.md`、`bash.md.tpl`），
  并在 `instructions.go` 的 `instructionFileByTool` 里登记。新增带 prompt 的工具
  要同时加 map 条目，否则 `RenderToolInstructions` 返回 unknown。
- runtime 不 import 具体工具包，只通过 metadata 里的约定 key 感知副作用：
  `todos`/`rendered` 触发 `todos_changed`，`plan`/`approved` 触发
  `plan_submitted`。改这些 key 会静默破坏 UI 联动。
- `bash` 用 `os/exec` + `bash -lc`，不是 `mvdan/sh`。

### Hook

`Hook` 有三个生命周期点：`OnRunStart`（每次 Run 重置状态）、
`BeforeToolCall`（返回 Continue/Deny/Stop）、`AfterTurn`（返回 Stop 可中止）。
`HookChain` 里第一个非 continue 的决定生效，且 **Stop 永远压过 Deny**。
Hook 在 permission policy *之前* 执行，所以能硬否决一个本来会被允许的调用。

内置三个：`SafetyHook`（灾难性命令 Stop、破坏性命令 Deny、拒绝写工作区根/`.git`）、
`LoopGuardHook`（同工具调用或整轮重复达 3 次即 Stop）、`PlanModeHook`
（plan 模式下只放行只读工具 + `exit_plan_mode` + `todo_write`）。
CLI 与 TUI 两条入口都要注册全部三个。

### 会话与文件观察

- `session.Store` 用 `modernc.org/sqlite`（纯 Go，无 cgo），DB 在
  `<workDir>/.mini-opencode/sessions.db`，`.mini-opencode/sessions/` 只存 JSON 归档。
- observer/todo 适配器通过**回调**取当前 session id（`func() string`），不是固定
  id，因为同一套工具会在 new/switch/fork/archive 之后继续复用。
- `read` 记入 `read_files`；`write`/`edit` 拒绝覆盖本会话没读过的已存在文件，
  并在改动前把旧内容写进 `files` 表快照，`/undo` 恢复最新快照。可用
  `workspace.require_read_before_write: false` 关闭。
- 写文件若不在允许范围内会被拒绝。相对路径落在工作目录下；绝对路径必须位于工作
  目录或 `workspace.allowed_roots` 内。`allowed_roots` 在 `config.Load` 时被规范化
  （绝对 + clean + 去重）。

### Provider

`echo` 是默认兜底（无 `config.json` 时也用 echo，`Load` 在文件不存在时返回默认值
且不报错）。`deepseek` 走 OpenAI 兼容 `/chat/completions`，SSE 流式，请求带
`stream_options.include_usage` 以便尾部 chunk 上报 token。
不可解析的流 chunk 不再退化成空答案，而是变成 `provider_warning` 事件。

### TUI

- `Model.Update` 是消息分发中心，按 `appState` 分派按键；新增交互状态要同时改
  `appState`、`handleKey` 的分派、以及 `View` 的 footer 高度计算。
- `tea.WindowSizeMsg` 里把宽度减 1（`m.width = msg.Width-1`），避免渲染到终端最后
  一格导致 alt-screen 滚动；`input.Width = m.width-6` 是边框+内边距+prompt 的预留，
  改样式时这两个魔数要一起调。
- 终端滚轮行为因终端而异（Terminal.app 会加速），集中在 `scroll.go` 的
  `terminalProfile` 里按 `TERM_PROGRAM` 调参，不要在别处硬编码滚动步长。
- 颜色与样式统一放 `styles.go`（crush 风格调色板），不要在各文件里新建 lipgloss style。
- **流式渲染必须节流**。`renderAssistantMessage` 会对累积全文跑 glamour，M2 实测
  4KB≈3.4ms、16KB≈12.8ms。provider 的 delta 密度远高于此，逐 delta 渲染会把帧推到
  16.6ms 预算之外，表现就是滚动卡顿 + spinner 被饿死。所以 delta 只做累积
  （`streamingText +=`），渲染走 `scheduleStreamRender()` 的 33ms 定时器。
  两条不变量：**最终响应必须 `cancelStreamRender()`**（否则迟到的 tick 会用残缺内容
  覆盖权威渲染），以及 `handleRuntimeEvent` **必须把返回的 `tea.Cmd` 交进 `cmds`**
  （返回类型就是为这个；丢掉它定时器永不触发，文字永远不显示）。
- `refreshViewport` 有**脏标记 + 尾部窗口**（`viewportTailBlocks`）两层缓存。任何
  「原地改写 `m.blocks[i]`」或 `m.blocks = nil` 之后都必须 `markBlocksChanged()`，
  漏掉会让界面静默停在上一帧。无变化时刷新 ~17µs 恒定，与历史长度无关。
- `renderFooter()` 结果按 `footerDirty` 记忆化；`Update` 入口统一失效，但
  `handleKey` / `handleMouse` 提前 return，**那两条路径要各自 `invalidateFooter()`**。
  spinner tick 与 streamRenderMsg 不失效（不改 footer 内容）。
- `internal/tui/perf_test.go` 是这批优化的回归护栏，它断言的成本必须**不随 transcript
  长度增长**。改动渲染路径后跑它。

## 测试

- 表驱动 + `t.TempDir()` 是主流写法；需要 workspace 的测试用
  `t.TempDir()` 而不是仓库内临时目录。
- `internal/app` 的测试依赖装配层函数（`confirmTool`、`toolDiffPreview` 等），
  这些函数刻意保持可注入（接受 `io.Writer` / `*bufio.Scanner`）以便测试。
- TUI 测试直接构造 `*Model` 并调 `Update`，断言返回 model 的字段
  （如 `viewport.YOffset`），不启动真实的 tea.Program。
- 涉及并发的改动（runtime 只读批并发）必须过 `go test -race ./...`。

## 上下文预算

**ctx 数字由两部分构成，别把它们混为一谈**（`Runtime.ContextDetails()`）：

- `SystemTokens`：system prompt + 工具 schema 的**固定地板**，实测约 4700 tokens
  （coder 模板 + AGENTS.md + skills 共 ~18KB）。每次请求都在，**永远不会下降**。
- `ConversationTokens`：真正会发出去的对话部分（压缩后从 summary 标记处算起）。
- `Reported`：provider 上次上报的 prompt 大小，比估算大时优先采用（`Total()`）。

**这就是 `/newsession` 后「ctx 没变」的原因**：`SetMessages(nil)` +
`SetContextTokens(0)` 确实生效了，但对话清零只让总数从 5750 掉到 5700，被 4700 的地板
淹没了。所以 header 的 ctx 段会额外显示 `· N chat`（对话为 0 时不显示），`/status`
单独列一行 system vs conversation。**不要**为了让数字好看就把 system prompt 从计算里
去掉——它确实占窗口，`needsCompaction` 必须把它算进去。

自动压缩的触发条件是**剩余余量**而不是已用比例：窗口 `> 200k` 时剩余 `<= 20k`
触发，否则剩余 `<= 窗口 * 0.2`（常量见 runtime.go）。显式配置
`agent.compact_threshold` 时回退到旧的「已用比例」语义，现有配置行为不变。压缩后必须
再增长 `compactionGrowthFraction`（10% of window）才允许再次自动压缩——否则一个超大
的摘要会让每轮都压缩。单次 run 的 turn 预算由 `agent.max_turns` 控制（默认 100），
打满时给出可操作提示而非静默失败。

**压缩是非破坏性的**（照 crush 的做法）：

- 摘要 append 到 transcript 末尾，`Runtime.summaryIndex` 标记位置；原文保留在内存和
  SQLite 里，只是 `promptState()` 从标记处截断。改这块要注意：`contextEstimateLocked`
  也必须只统计「实际会发出去的」部分，否则压缩完仍按全量估算，会每轮重复压缩。
- 发给 provider 时标记消息要改写成 `user` 角色（请求不能以 assistant 开头）。
- `replaceMessagesLocked` 从内容重新推导标记，所以跨进程重载会话不需要额外字段。
- 压缩时会把 todo 列表塞进总结指令，避免压缩后丢失任务状态。

## 记忆（memory）

`internal/memory` 是跨会话记忆，与 todo 分工明确：todo 管当前任务，memory 管持久
知识（架构决策、约定、环境坑）。要点：

- 笔记是 `.mini-opencode/memory/*.md`，YAML front matter + Markdown。**用户手写的
  文件也是合法笔记**（无 front matter 时整份文件是正文），改格式时别破坏这条。
- 两个写入来源：`memory` 工具（模型主动）与 run 结束后的后台提炼
  （`memory.Distiller`）。提炼会跳过 tool 流量、压缩摘要、todo 续跑注入，否则会把旧
  结论当新知识重记；它用独立 context + 60s 超时，取消 run 不应连带取消它。
- 召回是**按需**的：`MemoryRecallSection` 按当前会话首条用户消息打分，只注入前 3 条，
  不相关时返回 `""`。不要改成全量注入——那会花掉 memory 本来要省的预算。
- tokenize 对 CJK 逐字成词（中文没有空格分隔），否则所有中文笔记同分。

## 会话检索

`session.Store` 用 FTS5 虚拟表 `messages_fts` 索引 `messages.parts`，由
**触发器**随写同步（不是应用层双写），所以任何写入路径都保持可搜。FTS5 是 SQLite 的
编译期选项：不可用时记录在 `Store.searchErr`，`SearchAvailable()` 返回 false，
存储本身照常工作，只有搜索报不可用。

## 文档

`docs/` 下有 commands / hooks / mcp / prompt / providers / skills / tools / memory
各一篇，描述对应的行为契约。改这些子系统的行为时要同步更新对应文档；README 的"当前阶段"
段落也随功能推进更新。

## 密钥

不要提交 API key。`config.json` 和 `.mini-opencode/` 已 gitignore；密钥存在
`.mini-opencode/secrets.json`。
