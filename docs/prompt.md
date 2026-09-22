# Prompt System

`mini-opencode` 的 prompt 分三层：

1. 静态模板：`internal/agent/prompt/templates/*.md` 和 `*.md.tpl`
2. 运行时上下文：由 `internal/agent/prompt/prompt.go` 注入
3. 软覆盖：`ContextFiles` 和 `Skills`

## 静态模板

`coder.md.tpl` 是默认 system prompt 模板，保留 XML 风格分块规则：

- `<critical_rules>`
- `<communication_style>`
- `<workflow>`
- `<decision_making>`
- `<editing_files>`
- `<task_completion>`

模板本身尽量保持纯文本。动态内容不要写死在模板里。

## 运行时上下文

`BuildSystemPrompt` 会在模板后追加：

```xml
<runtime_context>
Working directory: ...
Is git repo: yes/no
Platform: ...
Today's date: ...
</runtime_context>
```

## ContextFiles

`DiscoverContextFiles` 默认读取：

- `AGENTS.md`
- `agents.md`
- `CLAUDE.md`
- `claude.md`
- `.cursorrules`
- `.cursor/rules/*.md`
- `.github/copilot-instructions.md`

这些文件会注入到：

```xml
<context_files>
<context_file path="...">
...
</context_file>
</context_files>
```

因此项目级指令只需要改 ContextFiles，不需要改 Go 源码。

## 记忆召回

除 ContextFiles 外，system prompt 之后还会追加一段由 memory 打分召回的
`<memories>`（见 `docs/memory.md`）。它是**按需**注入的：查询不相关时不追加任何
内容，空工作区不付出任何成本。

## 压缩（compaction）

压缩是**非破坏性**的：

- 摘要作为一条 assistant 消息 **append** 到 transcript 末尾，`Runtime.summaryIndex`
  标记它的位置；
- 原文全部保留在内存与 SQLite 中，只是 `promptState()` 从标记处开始截断，不再发给
  provider；
- 发给 provider 时标记消息会被改写成 `user` 角色 —— 一次请求不能以 assistant 开头。

好处：`/undo`、审计、以及「对原始对话重新总结」在压缩之后依然可行；跨进程重载会话
时 `replaceMessagesLocked` 会从内容重新推导标记，不需要额外持久化字段。

触发阈值按**剩余余量**而不是已用比例：

| 窗口 | 触发条件 |
| --- | --- |
| `> 200_000` | 剩余 `<= 20_000` |
| `<= 200_000` | 剩余 `<= 窗口 * 0.2` |

固定比例在大窗口下会浪费巨大余量，固定 token 缓冲在小窗口下又占比过高，所以按窗口
大小二选一。显式配置了 `agent.compact_threshold` 时仍按旧的「已用比例」语义，现有
配置行为不变。

压缩时会把当前 todo 列表写进总结指令，并要求总结保留任务状态，避免压缩后丢失
「还剩什么没做」。

> 阈值常量（`largeContextWindowThreshold` / `largeContextWindowBuffer` /
> `smallContextWindowRatio`）与「按剩余量」的思路参考了 crush 的实现。

## 模板的实际用途

`coder.md.tpl` 是默认 system prompt。其余模板都已有对应能力：

| 模板 | 用途 |
| --- | --- |
| `summary.md` | `/compact` 生成会话摘要 |
| `initialize.md.tpl` | `/init` 分析仓库并生成 `AGENTS.md` |
| `title.md` | 会话首轮后生成短标题（替换 `new session` 占位） |
| `task.md.tpl` | `task` 工具的只读子 agent system prompt |
| `agentic_fetch_prompt.md.tpl` | 供 `web_fetch` / `web_search` 抓取内容后的分析流程 |

## Skills

`PromptContext.Skills` 会注入到：

```xml
<available_skills>
<skill>
  <name>...</name>
  <description>...</description>
  <location>...</location>
</skill>
</available_skills>
```

后续接 skill loader 时，只需要填充 `PromptContext.Skills`。
