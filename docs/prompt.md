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
