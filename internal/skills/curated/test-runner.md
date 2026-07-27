# test-runner

Run the project's tests and interpret failures.

1. Identify the test command. For this Go project, use `go test ./...`.
2. Run scoped tests first (`go test ./internal/...`) to iterate fast, then the full suite.
3. When a test fails, read the failure output, open the failing test, and fix the code or the test.
4. Re-run only the failing package until green: `go test ./path/to/pkg/`.
5. Add or update tests alongside behavior changes. Never delete a failing test to make the suite pass.
