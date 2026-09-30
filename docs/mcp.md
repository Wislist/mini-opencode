# MCP

`mini-opencode` 的 MCP 层用标准库实现 JSON-RPC，支持三种传输：

| `type` | 传输 | 需要的字段 |
| --- | --- | --- |
| `stdio`（默认） | 拉起子进程，JSON-RPC over stdin/stdout | `command`、`args` |
| `http` | streamable HTTP：每次 `POST` 到 `url`，响应可为 JSON 或 SSE 帧 | `url` |
| `sse` | legacy HTTP+SSE：`GET` 建立事件流，先收 `endpoint` 事件，再 `POST` 到该地址 | `url` |

三种传输共用同一套方法：`initialize`、`tools/list`、`tools/call`，以及 MCP tool 到
`agent.Tool` 的适配。

## 配置

```json
{
  "mcpServers": {
    "go": { "enabled": true, "command": "gopls", "args": ["mcp"] },
    "github": {
      "enabled": true,
      "type": "http",
      "url": "https://api.github.com/mcp/",
      "token_env": "GH_PAT",
      "headers": { "X-Tenant": "acme" },
      "timeout_seconds": 30
    },
    "stream": {
      "enabled": true,
      "type": "sse",
      "url": "https://example.com/sse",
      "token": "static-token"
    }
  }
}
```

| 字段 | 说明 |
| --- | --- |
| `enabled` | 只有 `true` 的 server 会在启动时连接 |
| `type` | `stdio`（留空即默认）、`http`、`sse`；未知值在启动时记为失败状态 |
| `command` / `args` | stdio server 的启动命令 |
| `url` | http / sse 的端点；`type` 是这两种之一时**必填** |
| `headers` | 额外请求头，原样带上（例如租户 id、私有鉴权方案） |
| `token` / `token_env` | 静态令牌，作为 `Authorization: Bearer <token>` 发送；优先用 `token_env` 让密钥留在环境里 |
| `timeout_seconds` | 单次请求上限，默认 60s |

**鉴权优先级**：`headers` 里已经显式给了 `Authorization`（任意大小写）时不会被覆盖 ——
Basic / 私有 scheme 因此不需要改代码。否则依次取 `token`、`token_env`，都没有就不带
`Authorization` 头。

**启动与注册**：启用的 server 在启动时连接，完成 `initialize` + `tools/list` 握手后，
每个工具以 `<server>__<tool>` 形式注册到 runtime，因此 MCP 工具不会静默覆盖同名内置
工具；启用的名字前缀也会写进工具描述（`[mcp:<server>] ...`）。

单个 server 失败（命令不存在、URL 缺失、握手超时）不会中断启动：错误打印成
`warning: mcp server "x" failed: ...` 并记录状态，`/mcp` 随时可查。启动握手上限 15s，
与单次请求超时相互独立。

所有 MCP 工具默认需要确认（`requires_confirmation`），与内置写操作一致。

## 远程传输的取舍

- **会话**：http 传输会保存响应头里的 `Mcp-Session-Id`，后续请求带上它（并发安全）。
  `Close()` 会尽力而为地 `DELETE` 该会话，失败不影响退出，且可以重复调用。
- **sse 事件流**：读流 goroutine 挂在传输自身的 context 上，而不是某一次调用的 context
  —— 否则第一个被取消的调用会杀掉其他在途调用还在用的流。流结束时**所有**等待者会被
  唤醒并立刻报错，而不是各自傻等到超时。
- **断流不重连**：sse 流一旦结束，该 server 的后续调用会快速失败（重启进程恢复）。
- **不上报 `notifications/initialized`**：与 stdio 传输保持一致；严格按较新规范要求这个
  通知的 server 可能拒绝握手。
- **错误文本**：非 2xx 只报状态码与截断后的响应体（≤512B），不回显请求头 —— 错误字符串
  正是会进日志的那种文本，而请求头里有 token。

## Go 官方能力

预留了 Go 官方 MCP server 的接入位：

```json
{
  "mcpServers": {
    "go": { "enabled": false, "command": "gopls", "args": ["mcp"] }
  }
}
```

本机没有安装 `gopls`，所以没有实际启动验证。等本机有可用的 Go 官方 MCP server 后把
`enabled` 改成 `true` 即可。

如果 Go 官方最终只提供 LSP 而不是 MCP，后续应新增 `internal/golang` 或 `internal/lsp`，
通过 `gopls` LSP 接入 diagnostics、definition、references、symbols 等能力。
