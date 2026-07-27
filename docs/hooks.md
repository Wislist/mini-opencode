# Hooks

Hooks intercept the runtime lifecycle to guard against dangerous agent behavior and wasted tokens. They are a runtime-level layer that fires *before* the permission policy, so a hook can hard-veto a call that would otherwise be allowed or merely prompt for confirmation.

## Lifecycle

`Hook` implements three points:

- `OnRunStart`: called once per `Run` so stateful hooks reset their per-run state.
- `BeforeToolCall(ctx, call, definition)`: fires before each tool execution. Returns one of:
  - `HookContinue` — proceed normally.
  - `HookDeny` — block this call, record a denial tool result, and let the run continue so the model can recover.
  - `HookStop` — abort the whole run immediately.
- `AfterTurn(ctx, turn, history)`: fires after each turn's tool results are recorded. Returning `HookStop` aborts the run.

`HookChain` runs hooks in order; the first non-continue decision wins, and `Stop` always wins over `Deny`.

## Built-in hooks

### SafetyHook

A danger guard for destructive actions such as deleting the repository or dropping a database. `NewSafetyHook(workDir)` installs two pattern sets, matched case-insensitively against `bash` commands:

- Stop patterns (catastrophic): `rm -rf /`, `mkfs`, fork bombs, `dd if=/dev/zero`, `chmod -R 000`.
- Deny patterns (destructive but local): `drop database`, `drop table`, `truncate table`, `git push --force`, `git push -f`, `git clean -fdx`, `git reset --hard`, `find . -delete`, `rm -rf .git`, `rm -rf .`, `rm -rf ~`, `shred -u`.

It also refuses `write`/`edit` paths that target the workspace root or the `.git` directory. Both pattern lists are configurable fields on the struct.

### LoopGuardHook

Detects wasted tokens from repeated reasoning. `NewLoopGuardHook()` (default `MaxRepeats = 3`) stops the run when:

- the same tool call (name + canonical arguments) repeats `MaxRepeats` times, or
- entire turns (assistant text + tool calls) repeat identically `MaxRepeats` times.

This catches the common failure where the agent re-issues the same call or re-states the same plan each turn without making progress.

## Events

The runtime emits two new event types so the UI can surface hook decisions:

- `EventHookDenied` — a hook blocked a single tool call; the denial is recorded as a tool result.
- `EventHookStopped` — a hook aborted the run.

The CLI prints `hook blocked: ...` and `hook stopped run: ...` respectively.

## Wiring

`app.go` registers both hooks on every runtime:

```go
agent.WithHook(agent.NewSafetyHook(workingDir)),
agent.WithHook(agent.NewLoopGuardHook()),
```

Custom hooks can be added with `agent.WithHook(...)`.
