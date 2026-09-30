# Providers

`mini-opencode` 支持一次配置**多个提供商**，并在会话中切换。每个提供商有自己的名字、
端点、模型列表和密钥，因此第三方中转商 / 自建网关不需要改代码就能接入。

## 目录与激活项

配置分两块，都在 `config.json`：

- `providers`：提供商数组（目录）。每项一个条目。
- `active_provider`：启动时使用哪一项（按名字匹配，大小写不敏感）。

```json
{
  "providers": [
    {
      "name": "relay",
      "label": "我的中转",
      "base_url": "https://relay.example.com/v1",
      "api_key_env": "RELAY_API_KEY",
      "models": ["gpt-4o", "deepseek-chat"],
      "model": "gpt-4o",
      "context_window": 200000,
      "timeout_seconds": 180
    },
    { "name": "deepseek", "base_url": "https://api.deepseek.com", "model": "deepseek-chat", "api_key_env": "DEEPSEEK_API_KEY" },
    { "name": "echo", "type": "echo" }
  ],
  "active_provider": "relay"
}
```

解析顺序（`config.Load`）：

1. `providers` 非空时，**它就是整个目录**，旧的单 `provider` 块被忽略。
2. `providers` 为空时，旧的 `provider` 块（如果有）充当整个目录 —— 老配置原样可用。
3. 都不存在时是内置的 `echo`。
4. 激活项：`active_provider` 指定的那一项；没写就是目录里的**第一项**。
   `active_provider` 写了但名字不存在会**报错**（列出已配置的名字），不会静默回落到第一项。

`Load` 之后 `config.Provider` 始终是「已解析的激活项」，所以任何只关心单个提供商的
代码（上下文窗口、子 agent、标题生成、`/status`）都不需要知道目录的存在。

### 条目字段

| 字段 | 说明 |
| --- | --- |
| `name` | 提供商名字，`/provider`、`secrets.json` 都用它做主键；不能含空白 |
| `label` | 展示名（`/provider`、`/status` 显示它，缺省用 `name`） |
| `type` | `echo` 或 `openai-compatible`（别名 `openai`、`openai_compatible`）；留空按名字推断 |
| `base_url` | API 根地址，例如 `https://relay.example.com/v1` |
| `model` | 当前使用的模型 |
| `models` | `/model` 可选的模型列表；当前 `model` 一定会出现在列表里 |
| `api_key` | 直接写在配置里的密钥（不推荐，见下） |
| `api_key_env` | 从环境变量读密钥 |
| `context_window` | 该模型的上下文窗口；留空按模型名猜（未知模型 8k） |
| `timeout_seconds` | 单次请求超时，默认 120s |
| `max_retries` | 可重试失败的重试次数，默认 2；显式 `0` 关闭 |

**`type` 留空怎么推断**：`name` 是 `echo`（或既没名字也没端点）→ 本地回显；其他任何
名字 → OpenAI 兼容端点。这就是自定义名字能直接用起来的原因。

非 OpenAI 兼容的提供商目前没有实现：写 `"type": "anthropic"` 之类的值会在 `Load`
阶段直接报错，而不是等到第一次请求才失败。

## 切换与新增

```bash
/provider                                         # 列出目录，标出激活项和密钥来源
/provider relay                                   # 切到 relay（仅本次进程生效）
/provider add relay https://relay.example.com/v1 gpt-4o   # 新增/更新并写入 config.json
/model                                            # 列出当前提供商的模型
/model deepseek-chat                              # 切换模型（仅本次进程生效）
```

### 新增供应商（三框表单 + 拉取模型）

TUI 里 `/provider` 打开列表，最后一行 `＋ 新增提供商`（或直接 `/provider add` 不带参数）
进入表单：

```
╭───────────────────────────────────────────────────────────────╮
│ 新增提供商:  tab/↑↓ 切换 · enter 提交 · esc 取消              │
│   名称  relay2                                                │
│   地址  https://relay2.example.com/v1                         │
│ ▶ 密钥  ***************                                       │
│ 提交后会向该地址请求 /models，再让你勾选可用模型。            │
╰───────────────────────────────────────────────────────────────╯
```

- `tab`/`↑↓` 跳转三个框，`enter` 提交，`esc` 取消。密钥用密码框渲染（截图/共享屏幕不会漏）。
- 提交后**校验就地报错**（名称必填、不能含空白，地址必须是绝对 http(s) 地址），
  不合法就停在同一页，已填内容保留。
- 校验通过后请求 `{base_url}/models`，把该供应商提供的模型全列出来：

```
╭───────────────────────────────────────────────────────────────╮
│ 选择模型:  space 勾选 · a 全选/全不选 · enter 确认（3/4）· esc 取消 │
│   [x] gpt-4o                                                  │
│ ▶ [x] deepseek-chat                                           │
│   [ ] deepseek-reasoner                                       │
│   [x] qwen3:30b                                               │
│ 勾选的模型会写进 config.json 的 models，第一个勾选的成为当前 model。 │
╰───────────────────────────────────────────────────────────────╯
```

- **默认全选**（拉回来的一个都不丢），`space` 取消个别，`a` 全选/全不选，`enter` 确认。
- 一个都不勾会被拒绝：没有模型的 provider 发不出任何请求。
- **确认之前不落盘**：表单、拉取、勾选任何一步 `esc` 取消，`config.json` 与
  `secrets.json` 都不会变。
- 拉取失败（401 / 超时 / 不是 OpenAI 兼容端点）会退回表单并保留已填内容，可以改了重试。
- 密钥留空则**跳过拉取**直接保存（配合 `api_key_env` 的场景），并提示之后补上密钥：
  再用一次 `/provider add`（同名条目会就地更新，第三个框写密钥）或改 `config.json`。
- 该地址返回空列表时也保存，并说明要用 `/model <id>` 指定。

`/model refresh` 对当前 provider 做同一件事：重新拉取并多选，列表是
**已配置 ∪ 拉回来的并集**（刷新不会丢掉手写的模型），已勾选状态默认全选。

CLI 行模式没有浮层：`/provider add <name> <base_url> [model]` 直接保存，
`/model refresh` 拉取后取**并集**写入 `config.json`（脚本友好，不可交互勾选）。

要点：

- **切换只影响本次进程**：`/provider relay`、`/model x` 都不写 `config.json`。文件是配置，
  一次菜单选择不是。要固定下来就改 `active_provider` / `model`。
- **`add` 会写 `config.json`** —— 「新增提供商」本身就是配置变更。同名条目是**就地更新**，
  只覆盖你给出的字段，其余（label、timeout、models…）保留。
- **`/model <id>` 不校验** id 是否在 `models` 里：中转商可能提供你没枚举的模型，
  拒绝它们恰好把这命令在最需要的地方废掉。切换后该 id 会出现在 `/model` 列表里。
- **切换会重建 runtime，但不会丢对话**：新 runtime 通过 `Runtime.AdoptStateFrom` 继承
  transcript、token 统计、压缩标记和 system prompt。构建失败（例如还没配密钥）时旧
  runtime 继续服务，配置停在新的提供商上，于是紧接着补上的密钥正好写到正确的名字下。
- TUI 里裸 `/provider`、`/model` 打开浮层（↑↓ 选择、enter 生效、esc 取消），
  带参数走直接路径；CLI 行模式只有文本输出，没有浮层。

## 上下文窗口（ctx 的分母）

窗口有三个来源，优先级从高到低：

| 来源 | 含义 | 界面上 |
| --- | --- | --- |
| `context_window`（配置里显式写的） | 用户说了算 | 正常显示 |
| 内置模型表 | 认识 `deepseek-chat` / `gpt-4o` / `o3` 等常见模型 | 正常显示 |
| 占位猜测（**8192**） | 模型名不在表里，谁也不知道 | 标成 **估值** |

**猜测值必须看得见**：第三方中转商的模型名（`qwen3:30b`、`local-model-x`…）都会落到
占位值，而 system prompt 地板本身就有 ~4.7k，于是 `/newsession` 之后 ctx 会显示 57%，
看起来像 bug 而不是像缺一个配置。所以：

- header 显示 `ctx 57% (估) · 1.2k chat`；
- `/status` 额外打一行说明，并给出两条修复路径；
- `/provider` 列表里该行显示 `窗口 8192（估）`。

**自动获取**：新增供应商的表单与 `/model refresh` 都会请求 `{base_url}/models`，并把厂商
上报的上下文长度写进 `context_window`。字段名各家不同，逐一识别：

`context_length`（OpenRouter）· `context_window` · `max_context_length` ·
`max_context_tokens` · `max_model_len`（vLLM）· `max_input_tokens` ·
`input_token_limit` · `context_size` · `n_ctx`（llama.cpp），以及 OpenRouter 的嵌套
`top_provider.context_length`。第一个正数生效；字符串数字也接受；超过 1 亿视为噪音忽略。

规则：

- **只有上报了正数才写**。厂商没报窗口时保留用户手填的 `context_window`，绝不因为一次
  列表里没有这个字段就把配置清空。
- 会话里切换模型时，窗口跟着模型走（同一次拉取记住的窗口会被复用），否则指示器会一直除
  以上一个模型的窗口。
- 占位值故意取得**小**而不是大方：早压缩浪费点 token，超窗口是硬报错。

## 密钥

**不要提交 API key。** `config.json` 和 `.mini-opencode/` 都在 `.gitignore` 里。

密钥按优先级解析：`api_key` → `api_key_env` → `.mini-opencode/secrets.json`（按提供商
名字存）。

```text
.mini-opencode/secrets.json
{
  "provider_keys": {
    "relay": "sk-...",
    "deepseek": "sk-..."
  }
}
```

写密钥有三条路：

1. **TUI 的新增表单**（推荐）：`/provider add` 的第三个框就是密钥，确认时写进
   `secrets.json`（0600 权限），并把它作为该提供商的主密钥。
2. **改 `config.json`**：`api_key`（明文，最省事但会随文件流传）或 `api_key_env`
   （从环境变量读，推荐）。
3. **CLI 启动提示**：行模式下激活的提供商缺密钥时，进入对话前会先问一次。

密钥一律按**提供商名字**存进 `.mini-opencode/secrets.json`；`/provider` 列表每行都会显示
`key=config|env:NAME|secrets|missing`，这行就是「为什么这个提供商发不出请求」的答案。

给已有提供商换密钥：再跑一次 `/provider add`，**名字填一样的**，地址与密钥正常填——
同名是就地更新，不会新增条目。

启动时如果激活的提供商缺密钥：TUI 会以 `echo` 起界面，并在转录里提示用
`/provider add` 补上或换一个提供商，而不是直接退出。

## 旧的单 provider 配置

老配置不需要改：

```json
{
  "provider": {
    "name": "deepseek",
    "base_url": "https://api.deepseek.com",
    "model": "deepseek-chat",
    "api_key_env": "DEEPSEEK_API_KEY"
  }
}
```

它会被当成只有一个条目的目录；一旦你新增了 `providers` 数组，`provider` 块就不再参与
解析（`/provider add` 重写文件时也会把它写成目录形式，`active_provider` 记录当前激活项）。

## Retries and timeouts

网络错误、408/409/429 或任何 5xx 会用指数退避重试（500ms 起，翻倍，上限 8s）。
不可重试的状态码立刻失败。两者都按条目配置：

```json
{
  "providers": [
    { "name": "relay", "base_url": "https://relay.example.com/v1", "model": "gpt-4o", "timeout_seconds": 180, "max_retries": 4 }
  ]
}
```

`timeout_seconds` 默认 120，`max_retries` 默认 2；显式 `"max_retries": 0` 关闭重试。

## Stream visibility

无法解析的流式 chunk 不会退化成空答案：前三个 chunk（带内容引用）会变成
`provider_warning` 事件，TUI 内联显示、CLI 打印。既没有内容、也没有 tool call、
又只有无法解析的 chunk（或缺少 `[DONE]` 终止符）的流会**显式失败**。

## Token accounting

provider 上报的 `usage` 累计在 runtime 上、持久化到会话（`prompt_tokens`、
`completion_tokens`）并由 `/status` 显示。上下文指示器优先用上一次 completion 上报的
`prompt_tokens`（真实分词），provider 不上报时退回本地估算（约 4 个 ASCII 字符 1 token，
非 ASCII 每 rune 1 token）。
