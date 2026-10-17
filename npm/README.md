# @wislist/mini-opencode

Local coding agent terminal implemented in Go with a Bubble Tea TUI and CLI.

## Install

```bash
npm install --global @wislist/mini-opencode
# or
pnpm add --global @wislist/mini-opencode
```

Then run:

```bash
mini-opencode
```

Supported targets are macOS and Windows 10/11 on arm64 or x64. Windows users
should use Windows Terminal with PowerShell 7 when possible.

The wrapper package installs only the native binary for the current platform.
Do not disable optional dependencies when installing it.

## Update notifications

When a newer version is published, the wrapper says so before starting and
offers to update:

```text
A new version of mini-opencode is available: 0.5.3 -> 0.5.4
Update now? [y/N]
```

Answering `y` runs the global install for the package manager you used
(`npm install --global`, `pnpm add --global`, or `yarn global add`) and then
starts the freshly installed binary.

The check runs at most once a day and only in an interactive terminal. Saying
no silences that version until a newer one shows up. To turn the check off
entirely, set `MINI_OPENCODE_NO_UPDATE_CHECK=1` (or the conventional
`NO_UPDATE_NOTIFIER=1`); it is also skipped when `CI` is set. A failing or
offline check never blocks startup.

Source and documentation: https://github.com/Wislist/mini-opencode
