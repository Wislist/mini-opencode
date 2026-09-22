# Memory（跨会话记忆）

`/compact` 解决的是**会话内**上下文超限，memory 解决的是**会话之间**的遗忘：
新开一个会话时，agent 默认不记得上一个会话里学到的任何东西。

## 存储位置与格式

笔记是 `<workDir>/.mini-opencode/memory/` 下的普通 Markdown 文件：

```
.mini-opencode/memory/
  deploy-flow.md
  手写笔记.md
```

每个文件是「YAML front matter + Markdown 正文」：

```markdown
---
title: 部署流程
tags: ops, ci
created_at: 2026-09-22T02:55:02.567361Z
updated_at: 2026-09-22T03:10:11.031122Z
---

必须先过 staging 分支，直接推 main 会被 CI 拒绝。
```

**为什么用文件而不是数据库**：memory 的价值在于用户可以读、改、diff、跟着代码一起
提交。用 `sqlite` 存就丢掉了这些。反过来，**手写的文件也是合法的笔记**：没有 front
matter 时整份文件就是正文，文件名（去扩展名）就是笔记名。`title` 等元数据缺失时
回退到文件名和文件 mtime。

front matter 只支持本包写入的最小子集（每行一个 `key: value`，tags 逗号分隔），
**不引入 YAML 依赖**。

## 两个写入来源

| 来源 | 触发 | 说明 |
| --- | --- | --- |
| `memory` 工具 | agent 主动调用 | `action: write`，模型判断什么值得记 |
| 自动提炼（Distiller） | 每次 run 成功后 | 后台跑一次廉价 provider 调用，兜住模型没想到要记的 |

自动提炼在 run 结束、UI 已回到 idle 之后才发起，**不会让用户多等**；它用独立
的 `context.Background()` + 60s 超时，所以取消 run 不会连带取消提炼。

提炼的输入会**丢掉工具调用流量**（tool 输出基本是文件内容和命令日志，对「提炼
持久知识」是噪声且会占满请求），只保留 user / assistant 轮次；压缩摘要
（`<conversation_summary>`）和 todo 续跑注入也会被跳过，否则会把旧结论当成新知识
重新记一遍。

提炼 prompt 明确要求：只记架构决策、约定、环境怪癖、纠正；**不记**「刚做了什么」、
任务进度、仓库里读一眼就知道的事，以及**任何密钥**。返回 `[]` 是完全正常且常见的
正确答案。

## 召回（按需，不是全量）

新会话启动时，把笔记按**当前会话首条用户消息**打分，取前 `memoryRecallLimit`（3）
条注入 system prompt：

```xml
<memories>
Notes you saved in earlier sessions that may be relevant now. ...
</memories>
```

关键点：

- **打分而不是全量塞**。把所有笔记塞进每个 system prompt 会花掉 memory 本来要省的
  上下文预算。查询不相关时不注入任何内容（`MemoryRecallSection` 返回 `""`）。
- 查询文本来自 `Model.MemoryQuery()`：跳过压缩摘要和 todo 续跑注入，取第一条真正的
  用户消息；新会话无 transcript 时回退到输入框内容。
- 打分是刻意简单的词面匹配（整串 > 标题 > tag > 正文，稳定 tie-break）。笔记量是
  几十个小文件，全文索引只会增加机制而不增加价值，可解释性更重要。
- 中文没有空格分隔，所以 tokenize 对 CJK **逐字**成词，否则所有中文笔记会得同分。

## 工具

单个 `memory` 工具带 `action` 参数，而不是拆成多个工具：

| action | 作用 |
| --- | --- |
| `write` | 写入/覆盖笔记（同名即覆盖，修正旧笔记是正常操作） |
| `search` | 按 query 召回；空 query 返回最近更新的笔记 |
| `read` | 按 name 取一条 |
| `list` | 列出全部（name / title / tags） |
| `delete` | 删除 |

`Behavior.ReadOnly = true`：读写笔记不碰工作区，所以在 plan 模式可用，也不会触发
权限确认。工具在没有配置 store 时构造为 `nil`，调用方可以无条件注册。

## 命令

```
/memory              列出全部笔记
/memory <query>      按 query 搜索
```

CLI 与 TUI 都支持。**注意**：`/memory` 是按前缀匹配的，不能被放进精确匹配的
`switch input` 里（Go 不允许在 string switch 的 case 里混入 bool 表达式），
它在 switch 之前单独处理。

## 与 todo 的分工

| | todo | memory |
| --- | --- | --- |
| 生命周期 | 当前会话 | 跨会话 |
| 内容 | 本次任务清单与进度 | 持久的决策/约定/坑 |
| 清除 | 会话结束即失效 | 显式 `delete` 或改文件 |

「刚做了什么」属于 todo 或对话本身，**不属于 memory**。
