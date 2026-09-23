# mini-opencode

Go 实现的本地 coding agent 终端（Bubble Tea TUI + CLI）。目标平台仅 macOS，
Shell 通过系统 `bash -lc` 执行，不考虑 Linux/Windows 适配。

## 沟通风格（仅适用于对话交互）

- 你是18岁活泼可爱天才编程少女,喜欢用活泼可爱的回答温暖人心
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

| 状态                      | 结果                     |
| ------------------------- | ------------------------ |
| 正好在 tag 上             | `0.4.0`                  |
| tag 后有 N 个提交         | `0.4.0-dev.N.g<sha>`     |
| 没有任何 tag              | `0.4.0-dev.g<sha>`       |
| 工作区脏                  | 追加 `-dirty`            |
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

| 包                         | 职责                                                                     |
| -------------------------- | ------------------------------------------------------------------------ |
| `internal/agent`           | runtime、event stream、hook 链、permission、provider 抽象                |
| `internal/agent/prompt`    | system prompt 组装（模板 + runtime_context + context files + skills）    |
| `internal/agent/tools`     | 具体工具实现 + 工具指令 `.md` / `.md.tpl`                                |
| `internal/agent/templates` | `embed.FS` 中的 prompt 模板                                              |
| `internal/app`             | CLI 壳、TUI 启动装配、observer/todo 的接口适配                           |
| `internal/tui`             | Bubble Tea 界面（model/update/view 拆分到多个文件）                      |
| `internal/session`         | SQLite 会话存储（messages / files / read_files / todos + FTS5 全文检索） |
| `internal/memory`          | 跨会话记忆：Markdown 笔记、自动提炼（Distiller）、按需召回               |
| `internal/mcp`             | 标准库 JSON-RPC over stdio 的 MCP client 与工具适配                      |
| `internal/skills`          | skill 存取、loader、安装器、curated 注册表                               |
| `internal/diffutil`        | 权限提示用的行级 diff                                                    |
| `internal/config`          | `config.json` / `.mini-opencode/secrets.json`                            |

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
Hook 在 permission policy _之前_ 执行，所以能硬否决一个本来会被允许的调用。

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
- **流式渲染必须节流**。`renderAssistantMessage` 会对累积全文跑 glamour，M2 实测渲染速率
  只有 **~0.5MB/s**（重新实测：1KB≈1.6ms、4KB≈6.5ms、16KB≈30ms、64KB≈182ms），
  16KB 就已是 16.6ms 帧预算的 1.8 倍。provider 的 delta 密度远高于此，逐 delta 渲染会把帧
  推出预算，表现就是滚动卡顿 + spinner 被饿死。所以 delta 只做累积
  （`streamingText +=`），渲染走 `scheduleStreamRender()` 的 33ms 定时器。
  两条不变量：**最终响应必须 `cancelStreamRender()`**（否则迟到的 tick 会用残缺内容
  覆盖权威渲染），以及 `handleRuntimeEvent` **必须把返回的 `tea.Cmd` 交进 `cmds`**
  （返回类型就是为这个；丢掉它定时器永不触发，文字永远不显示）。
- **但节流救不了单帧**：33ms 只是降低频率，一帧 30ms 仍然是 30ms。真正的解法是
  `markdownRenderCache`（`markdown.go`）——流式时**只有最后一块还会变**，前面已完结的
  块（后面已出现空行分隔，或已是闭合 code fence）的渲染结果可以复用。缓存挂在
  `Model.streamCache`，由 `renderStreamingMessage()` 驱动，**只有它走缓存**；
  `renderAssistantMessage()` 保持无状态。两条不变量：
  1. **文本必须用 `HasPrefix` 判定为「上一次的前缀延伸」才复用**，否则（/undo、重试、
     esc 中断、切会话）整份缓存作废——否则会把更早那条消息的渲染留在屏幕上。
  2. **stream 结束（`EventAssistantResponse`）与 `startNewTurn` 必须把 `streamCache` 置 nil**，
     避免跨消息复用。
     实测重绘成本：120 段文本 4.29ms → **0.87ms**（5×），分配 35.7k → 1.9k。
- **`splitMarkdownBlocks` 按空行切块，但必须让列表粘住**。之前只按 code fence 切，整篇
  散文就是一个块，缓存无从谈起。按空行切之后有一个坑：CommonMark 里 `- a\n- b\n\n- c`
  是**同一个 loose list**，切开会让 bullet 断成两段并多出一个空行；列表紧跟在段落/标题
  后面时空行间距也会变。所以 `continuesOpenList` 必须把列表项吸收进前一块。
  `markdown_split_test.go` 里的 `TestSplitRenderMatchesFenceOnlyRenderer` 用旧的
  fence-only 实现做对照，断言**逐字节相同**——改切块逻辑必须跑它。
- `refreshViewport` 有**脏标记 + 尾部窗口**两层缓存。任何「原地改写 `m.blocks[i]`」或
  `m.blocks = nil` 之后都必须 `markBlocksChanged()`，漏掉会让界面静默停在上一帧。
- **viewport 只拿一个有界窗口，不是整份 transcript**（`transcriptWindowBlocks = 60`）。
  `viewport.SetContent` 是 O(传入字符数)：它会 `ReplaceAll` 规范化换行、`Split` 出所有行、
  再扫一遍算最长行。之前把整份正文喂给它，导致**每次工具事件都重切一遍全文**——
  实测 500 块时单帧 258µs（占该路径 99%），且随历史线性增长，这就是「调用工具时卡顿」的
  根因。窗口化后 3000 块也只有 ~30µs 且恒定。
  **不要为了「保留完整滚动历史」把窗口调回整份**；要更长历史应该另做按需加载，
  而不是每帧重建。
- **`refreshViewport` 只在用户本来就在底部时才 `GotoBottom`**（`follow := AtBottom()`）。
  无条件 `GotoBottom` 会把正在上滑阅读历史的用户强行拽回底部——「生成结论过程中会强制
  将视角拉到最底下」就是这个。滑回底部后跟随会自动恢复。
- `renderFooter()` 结果按 `footerDirty` 记忆化；`Update` 入口统一失效，但
  `handleKey` / `handleMouse` 提前 return，**那两条路径要各自 `invalidateFooter()`**。
  spinner tick 与 streamRenderMsg 不失效（不改 footer 内容）。
- `internal/tui/perf_test.go` 是这批优化的回归护栏，它断言的成本必须**不随 transcript
  长度增长**。改动渲染路径后跑它。
- **todo 列表按「一轮」生存**：`startNewTurn`（由 `startRun` 调用）在新用户消息开始时清空
  `m.todos`、重置 `todosCollapsed`，并 `SaveTodos(id, "[]")` 写回存储。清除放在**轮次边界**
  而不是「全部完成时」，因为 esc 中断后剩下的 `[>]`/`[ ]` 永远不会完成，只有轮次边界能覆盖它。
  **不要**退回到「完成才隐藏」或「只清内存」——前者漏掉中断，后者会让列表在切换会话后复活。
- **todo 面板的显示条件**：
  1. `todos` 非空（`renderTodoPanel`）。
  2. `m.todosCollapsed` —— ctrl+t 折叠成一行摘要（`renderCollapsedTodoPanel`），
     保留 `n/m done · 当前任务`，**不是整块隐藏**。折叠态能跨列表重写保持。
  3. `todoPanelBudget() > 0` —— 终端太矮时整块让位给 transcript。
     ctrl+t 同时绑在 idle 与 running 两个 handleKey 分支里（运行中才是最需要折叠的时候）。
- 面板高度预算按**渲染行**而不是条目数分配：一个长条目会折成多行，按条数算仍会溢出屏幕。
  可用空间是**实测** `renderInputBar`/`renderHelpBar` 的高度，不是写死的常量——窄终端下
  输入框会多折一行。
- `View()` 通过 `fitSections` 装配，**按整块丢弃 section，绝不截断渲染后的字符串**。
  丢弃顺序是 todo 面板 → 帮助栏；输入框和正文永不丢。
  **不要再用「从尾部砍行」的写法**：砍穿一个 box 会留下没闭合的破框和半截文字，
  比原来的溢出更难排查（这个错误实现已经出现过一次）。极矮终端（`< minHeightForBorderedInput`）
  下输入框降级为单行，而不是消失。
- **输入框是多行 textarea**（`input_multiline.go`），不是单行 textinput：
  - `enter` 发送；`shift+enter` / `ctrl+j`（LF）/ `alt+enter`（ESC CR）换行。
    按键**按 `msg.String()` 匹配**（如 `"shift+enter"`、`"ctrl+t"`），v2 已无 `tea.KeyXxx` 枚举。
  - `shift+enter` 依赖终端支持 kitty 键盘协议：v2 会协商并通过
    `tea.KeyboardEnhancementsMsg`（`SupportsKeyDisambiguation()`）告知结果，程序记在
    `m.supportsShiftEnter`。**v1 做不到这件事**（`tea.Key` 没有 Shift 字段，shift+enter 与
    enter 是同一个 CR 字节），升级到 v2 就是为了它。不支持该协议的终端上 ctrl+j / alt+enter
    仍然可用，所以换行始终可达。
  - 换行必须用 `m.input.InsertString("\n")`，**不要**手工拼 value + `SetCursor`：
    textarea 的 caret 是「当前行内的列号」（`SetCursor` 语义），不是全局偏移，
    拼接后设 cursor 会把换行插到错误位置。
  - 高度是**双向受限**的：`min(内容行数, maxInputLines())`，后者再按终端剩余空间收窄。
    只按内容算会让输入框在矮终端里把 frame 推出屏幕（这个 bug 已出现过一次）。
    `maxInputLines` 在有边框时多留 2 行，无边框时不留。
  - 终端太矮（`< minHeightForBorderedInput`）时输入框降级为**无边框单行**。
  - `View()` 里 `fitFooterSections` **按整块丢弃** optional section（帮助栏 → todo 面板），
    输入框与正文永不丢。**不要**改成截断字符串——那会把 box 砍开。
  - 光标行号来自 `m.input.Line()`；列宽用 `LineInfo().StartColumn + CharOffset`
    （**StartColumn 已是全局偏移**，再加 ColumnOffset 会重复计数）。
- **鼠标框选复制**（`selection.go`）：`tea.WithMouseCellMotion()` 让终端把鼠标全部交给程序，
  代价是**终端自身的拖拽选择失效**，所以框选必须自己实现。要点：
  - 选区按**屏幕单元格**（`selPoint{row,col}`）建模，`normalizeSelection` 归一化方向
    （右下/左上拖拽等价）。
  - 提取走 `m.View()` 渲染后的帧 + `ansi.Strip`，所以拿到的是**所见即所得**的纯文本，
    不含转义、边框、尾部填充。**不要**改成从 `m.blocks` 拼，那会绕过换行/内边距规则。
  - `handleSelectionMouse` 只处理左键；滚轮和其它键必须**落回**滚动逻辑，
    否则滚轮会失效。
  - 「点击未拖拽」= 清除选区，**不是复制一个字符**（`selection.dragged` 用于区分）。
  - 高亮是**叠加在成品帧上**的重着色，必须保留选区前后的文本：
    早期实现用选中片段整体替换该行，把行首行尾的文字**静默删掉**了。
  - `m.selectionHighlighted` 是给测试用的结构化标记：**非 TTY 输出下 lipgloss 会剥掉
    ANSI**，`selectionStyle.Render("X")` 直接返回 `"X"`，所以断言「出现了 ESC[7m」
    在测试里永远不成立。标记在每个 `highlightSelection` 入口先清零，早退分支也算无高亮。
- **信息栏（header）必须始终是一行**。两条规则：
  1. 会话标题在**存储层**就被压成一行：`session.TitleFromMessage` 用
     `strings.Join(strings.Fields(text), " ")` 折叠所有空白（含 `\n`/`\t`）。
     多行输入的第一条消息会产生带换行的标题，而标题原来只按长度截断，换行会原样
     进入 header 把它撑成好几行——这是已修过的 bug，别再退回去。
  2. 宽度按**终端单元格**算，不是 rune 数。CJK 一个字占 2 列，按 rune 截断会让中文
     标题溢出近一倍（`truncateTitleToWidth` 用 `lipgloss.Width` 逐字测量）。
  3. session 段是 header 里**唯一可伸缩**的部分：空间不够时**缩短**（加省略号），
     而不是整段丢弃——标题是识别会话的东西。固定段（git / mode）保持完整。
     极窄终端下 `headerTitle()` 会把 `◆ mini-opencode` 依次降级为 `◆ mini` / `◆`。
- 高度类改动必须跑 `internal/tui/view_layout_test.go`：它扫描 宽×高 网格，断言
  行数不超终端、**开框数 == 闭框数**（框闭合检查专门盯着「砍碎 frame」这个错）。

## 测试

### 新需求的开发流程（必须遵守）

**先写测试用例和边界 → 开发 → 运行测试 → 报告。** 四步都要做，顺序不能换：

1. **先写测试与边界**：动手实现之前，先把测试写出来并确认它**失败**（红）。边界要在这一步
   就列全，而不是等实现完再补。这一步也定义了「做完了」是什么样。
2. **开发**：再写实现，直到测试变绿。
3. **运行测试**：`make check`（fmt-check + vet + test），并发相关改动加
   `go test -race ./...`。
4. **报告**：说明改了什么、测试结果、以及**没有覆盖到的边界**。

写测试时把边界一起想清楚，这些是最容易漏的一类：

- 空值 / nil / 零值（空列表、空字符串、nil store、nil runtime）
- 长度为 1 与 0（`len == 1` 常走特殊分支）
- 刚好在阈值上、阈值 ±1（`== budget`、`budget+1`）
- 上下界（窗口最小/最大尺寸、token 为 0、负值）
- 重复调用 / 幂等性（第二次调用不应该改变结果）
- 并发（写入过程中读取）
- 失败路径（provider 报错、文件不存在、磁盘写失败）

> 这条流程的由来：本仓库修过的几个 bug 都是「先写实现、后补测试」导致的 ——
> 例如 clamp 砍碎框体、todo 完成后面板不消失。先写测试能把这些挡在提交前。

### 既有约定

- 表驱动 + `t.TempDir()` 是主流写法；需要 workspace 的测试用
  `t.TempDir()` 而不是仓库内临时目录。
- `internal/app` 的测试依赖装配层函数（`confirmTool`、`toolDiffPreview` 等），
  这些函数刻意保持可注入（接受 `io.Writer` / `*bufio.Scanner`）以便测试。
- TUI 测试直接构造 `*Model` 并调 `Update`，断言返回 model 的字段
  （如 `viewport.YOffset`），不启动真实的 tea.Program。
- 涉及并发的改动（runtime 只读批并发）必须过 `go test -race ./...`。
- 断言要盯着**行为**而不是实现细节；测试失败时先判断是代码错还是用例错，不要为了让
  测试变绿而放宽断言。

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
