# mini-opencode

一个从零开始实现的 Go 版 agent 终端项目。

本项目不依赖仓库里已有的 `02-llmg`、`03-mini-opencode` 或其它历史实现。

> 目标平台为 macOS，暂不考虑 Linux/Windows 适配。Shell 通过系统 `bash -lc` 执行。

## 目标

实现一个接近 `crush` 一半能力的本地 agent 终端：

1. Bubble Tea TUI
2. Agent runtime event stream
3. 权限与工具执行体验
4. 上下文工程
5. 多 provider 配置
6. MCP lifecycle
7. Git diff 与测试反馈

## 当前阶段

当前已经搭好第一层 agent 工作流：

- 独立 `go.mod`
- 独立入口 `cmd/mini-opencode`
- 标准库 CLI 循环
- `internal/agent` runtime
- provider 抽象
- tool 抽象与注册表
- `internal/agent/tools` 常用编码工具模板
- event stream
- 基础 turn loop：用户输入 -> provider -> assistant message -> tool call -> tool result -> 继续推理
- MCP stdio client 与 tool adapter
- 后续再逐步加入 provider、tools、TUI 和 MCP

## 目录

```text
cmd/mini-opencode     CLI 入口
internal/app          当前标准库 CLI 壳
internal/agent        agent 核心工作流
internal/agent/prompt Prompt 组装与模板
internal/agent/tools  常用编码工具模板
internal/mcp          MCP stdio client 与工具适配
docs/mcp.md           MCP 接入说明
docs/prompt.md        Prompt 组装说明
docs/tools.md         Tools 说明
docs/providers.md     Provider 配置说明
internal/skills       Skill 存取与安装（curated/local/GitHub）
docs/skills.md        Skills 说明
docs/hooks.md         Hooks（危险行为拦截 / 循环检测）说明
```

## 运行

```bash
go run ./cmd/mini-opencode
```

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

## Skills

Agent 可通过 `install_skill` 工具自行安装 skill（`SKILL.md`），来源包括内置 curated 列表、本地路径、GitHub 仓库。安装后 skill 会出现在系统 prompt 的 `<available_skills>` 中；CLI 中用 `/skills` 查看已安装与可安装的 skill。

## Hooks

Runtime 内置两个 hook 拦截危险行为与 token 浪费：

- `SafetyHook`：拦截删库、`drop database`、`git push --force`、`git clean -fdx`、`rm -rf .git` 等破坏性命令，并禁止写入工作区根目录或 `.git`；遇到 `rm -rf /`、`mkfs` 等灾难性命令直接中止运行。
- `LoopGuardHook`：检测 agent 重复调用同一工具或整轮回复重复，达到阈值（默认 3 次）即中止运行，避免重复思考造成的 token 浪费。

详见 [docs/hooks.md](docs/hooks.md)。
