# Skills

Skills extend the agent with specialized instructions. A skill is a `SKILL.md` file stored under the workspace skills directory:

```text
.mini-opencode/skills/<name>/SKILL.md
```

The first `# heading` is the skill name; the first non-heading line becomes the short description injected into the system prompt. The rest of the file is the instruction body the agent follows when the skill applies.

## How the agent sees skills

On startup `LoadSkills` discovers every installed skill and lists it in the system prompt under `<available_skills>`. The prompt tells the agent to load and follow a skill's instruction file (via the `read` tool) when its description matches the task.

## Installing skills

The `install_skill` tool lets the LLM install a skill itself. It accepts three source kinds:

- **Curated name**: an embedded skill, e.g. `commit`, `test-runner`, `review`.
- **Local path**: a path to a `SKILL.md` file, or a directory containing one. Relative paths resolve under the workspace.
- **GitHub reference**: `owner/repo`, `owner/repo/subpath`, or a `github.com/...` / `git@github.com:...` URL. The repo is shallow-cloned and its `SKILL.md` is copied in.

`install_skill` returns the full `SKILL.md` content, so the agent can follow the skill immediately in the same session; it is also auto-listed on the next session start.

The tool is marked dangerous and requires confirmation.

## CLI

`/skills` lists installed skills and the curated names available for installation.

## Packages

- `internal/skills`: skill storage, loader, installer, curated registry, GitHub reference parsing.
- `internal/agent/tools/install_skill.go`: the `install_skill` agent tool.
