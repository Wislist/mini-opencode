# Providers

`mini-opencode` currently supports two provider modes:

- `echo`: local fallback provider used for development
- `deepseek` / `openai-compatible`: OpenAI-compatible chat completions API

## Echo

If `config.json` is missing, the app uses `echo` automatically.

```json
{
  "provider": {
    "name": "echo"
  }
}
```

## DeepSeek

Create `config.json` in `04-mini-opencode`:

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

Then run:

```bash
export DEEPSEEK_API_KEY="your-key"
go run ./cmd/mini-opencode
```

The provider uses the OpenAI-compatible `/chat/completions` endpoint, supports
tool-call conversion, and streams responses over SSE. Streaming requests send
`stream_options.include_usage` so the provider reports token usage on the
trailing chunk.

## Retries and timeouts

A request that fails with a network error, a 408/409/429, or any 5xx is retried
with exponential backoff (500ms doubling, capped at 8s). Non-retryable statuses
fail immediately. Both are configurable:

```json
{
  "provider": {
    "name": "deepseek",
    "model": "deepseek-chat",
    "timeout_seconds": 180,
    "max_retries": 4
  }
}
```

`timeout_seconds` defaults to 120 and `max_retries` to 2; `"max_retries": 0`
disables retries explicitly.

## Stream visibility

A streaming response that cannot be parsed no longer degrades into an empty
answer: each unparsable chunk becomes a warning (the first three, with the
payload quoted) that the runtime raises as a `provider_warning` event, which the
TUI shows inline and the CLI prints. A stream that yields no content, no tool
calls, and either unparsable chunks or no `[DONE]` terminator fails loudly.

## Token accounting

Provider-reported `usage` is accumulated on the runtime, persisted on the
session (`prompt_tokens`, `completion_tokens`) and shown by `/status`. The
context indicator prefers the provider-reported `prompt_tokens` of the last
completion — the real tokenization — and falls back to a local estimate (about
4 ASCII characters per token, one token per non-ASCII rune) when a provider
reports nothing.

## Local key storage

Do not commit API keys. `config.json` and `.mini-opencode/` are gitignored.

If `provider.name` is `deepseek` and no key is found from `api_key`, `api_key_env`, or local storage, the CLI asks for the key before entering the prompt loop and saves it to:

```text
.mini-opencode/secrets.json
```

Inside the CLI, you can also run:

```text
/key sk-...
```

or:

```text
/key
```

The command switches the local provider config to DeepSeek, writes non-secret provider settings to `config.json`, stores the key in `.mini-opencode/secrets.json`, and rebuilds the runtime.
