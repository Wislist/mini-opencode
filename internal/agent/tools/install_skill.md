Install a skill so the agent can follow its instructions.

A skill is a `SKILL.md` file stored under `.mini-opencode/skills/<name>/SKILL.md`. Installed skills are listed in the system prompt and loaded by following their instruction file before doing task work.

`source` accepts three kinds:

- A curated skill name, e.g. `commit`, `test-runner`, `review`.
- A local path to a `SKILL.md` file, or a directory containing one.
- A GitHub reference: `owner/repo`, `owner/repo/subpath`, or a `github.com/...` URL. The repo is shallow-cloned and its `SKILL.md` is copied in.

Use `name` to override the installed skill's directory name. Prefer reusing an existing curated skill over installing a new one when one fits.

The tool returns the full `SKILL.md` content, so you can follow the skill immediately in the same session; it is also auto-loaded on the next session start.
