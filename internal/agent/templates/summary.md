You are summarizing a conversation to preserve context for continuing work later.

**Critical**: This summary will be the ONLY context available when the conversation resumes. Assume all previous messages will be lost. Be thorough.

**Required sections**:

## Current State

- What task is being worked on (exact user request)
- Current progress and what's been completed
- What's being worked on right now (incomplete work)
- What remains to be done (specific next steps, not vague)

## Files & Changes

- Files that were modified (with brief description of changes)
- Files that were read/analyzed (why they're relevant)
- Key files not yet touched but will need changes
- File paths and line numbers for important code locations

## Technical Context

- Architecture decisions made and why
- Patterns being followed (with examples)
- Libraries/frameworks being used
- Commands that worked (exact commands with context)
- Commands that failed (what was tried and why it didn't work)
- Environment details (language versions, dependencies, etc.)

## Strategy & Approach

- Overall approach being taken
- Why this approach was chosen over alternatives
- Key insights or gotchas discovered
- Assumptions made
- Any blockers or risks identified

## User Summary

Write 2-5 concise bullets that can be shown directly to the user after `/compact`. This section is a friendly progress recap, not the full handoff. Do not include raw code, diffs, command logs, long file contents, or secret values. Mention only what was preserved and what likely happens next.

## Exact Next Steps

Be specific. Don't write "implement authentication" - write:

1. Add JWT middleware to src/middleware/auth.js:15
2. Update login handler in src/routes/user.js:45 to return token
3. Test with: npm test -- auth.test.js

**Tone**: Write as if briefing a teammate taking over mid-task. Include everything they'd need to continue without asking questions. No emojis ever.

**Code/output discipline**: Do not paste long verbatim code blocks, command logs, diffs, or tool outputs. Preserve the actionable facts instead: file paths, symbols, line ranges, commands, errors, decisions, and tiny snippets only when absolutely necessary.

**Length**: Be complete but avoid dumping raw code or output. Critical context is worth the tokens; redundant verbatim content is not.
