# MCP

`mini-opencode` 的 MCP 层使用标准库实现 JSON-RPC over stdio，当前支持：

- `initialize`
- `tools/list`
- `tools/call`
- MCP tool 到 `agent.Tool` 的适配

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
