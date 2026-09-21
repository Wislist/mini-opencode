# MCP

`mini-opencode` 的 MCP 层使用标准库实现 JSON-RPC over stdio，当前支持：

- `initialize`
- `tools/list`
- `tools/call`
- MCP tool 到 `agent.Tool` 的适配

## 启动与注册

`config.json` 里 `mcpServers` 中 `enabled: true` 的 server 会在启动时被拉起
（`internal/mcp.Manager`），完成 `initialize` + `tools/list` 握手后，每个工具以
`<server>__<tool>` 的形式注册到 runtime，因此 MCP 工具不会静默覆盖同名内置工具。
启用的名字前缀也会写进工具描述（`[mcp:<server>] ...`）。

单个 server 启动失败（命令不存在、握手超时，默认 15s）不会中断启动：错误会打印成
`warning: mcp server "x" failed: ...`，并用 `/mcp` 随时查看每个 server 的状态。

所有 MCP 工具默认需要确认（`requires_confirmation`），与内置的写操作一致。

## Go 官方能力

本项目预留了 Go 官方能力的 MCP 接入位：

```json
{
  "mcpServers": {
    "go": {
      "enabled": false,
      "command": "gopls",
      "args": ["mcp"]
    }
  }
}
```

当前本机没有安装 `gopls`，因此没有实际启动验证。等本机存在可用的 Go 官方 MCP server 后，把 `enabled` 改成 `true`，再把命令和参数调整为实际 server 的启动方式。

如果最终 Go 官方只提供 LSP 而不是 MCP，后续应新增 `internal/golang` 或 `internal/lsp`，通过 `gopls` LSP 接入 diagnostics、definition、references、symbols 等能力。
