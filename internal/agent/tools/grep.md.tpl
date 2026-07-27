Search text in workspace files.

<usage_notes>
- Use this instead of shell `grep`.
- Query is required.
- Include path and line number for each match.
- Ignore binary files and generated/cache directories by default.
{{- if .RgAvailable }}
- Ripgrep is available; prefer rg-compatible matching semantics.
{{- end }}
- Return at most {{ .MaxResults }} matches.
</usage_notes>

