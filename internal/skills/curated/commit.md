# commit

Create a focused git commit after finishing a change.

1. Run `git status` and `git diff --staged` to review what will be committed.
2. Stage only the files relevant to the change with `git add <paths>`. Never stage unrelated edits.
3. Write a concise conventional commit message: `type(scope): summary`. Types: feat, fix, docs, refactor, test, chore.
4. Commit with `git commit -m "<message>"`. Keep the summary under 72 chars; add a body for context when useful.
5. Do not push unless explicitly asked.

If tests exist for the touched code, run them before committing.
