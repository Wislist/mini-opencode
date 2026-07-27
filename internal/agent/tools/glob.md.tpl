Find files by glob pattern.

<usage_notes>
- Use this instead of shell `find`.
- Pattern is required and evaluated relative to working_dir when not absolute.
- Ignore `.git`, `.gocache`, vendor, build, and dependency directories by default.
- Return at most {{ .MaxResults }} matches.
</usage_notes>

