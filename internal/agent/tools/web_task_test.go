package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/wislist/mini-opencode/internal/agent"
)

type fakeTaskRunner struct {
	got   TaskRequest
	reply string
	err   error
}

func (r *fakeTaskRunner) RunTask(_ context.Context, req TaskRequest) (string, error) {
	r.got = req
	return r.reply, r.err
}

func TestTaskToolDelegatesToRunner(t *testing.T) {
	runner := &fakeTaskRunner{reply: "found it in internal/app/app.go"}
	tool := NewTaskTool(TaskOptions{Runner: runner})

	args, _ := json.Marshal(map[string]any{"description": "find entrypoint", "prompt": "where is main?"})
	out, err := tool.Run(context.Background(), agent.ToolInput{Arguments: args})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if out.Content != "found it in internal/app/app.go" {
		t.Fatalf("content = %q", out.Content)
	}
	if runner.got.Prompt != "where is main?" || runner.got.Description != "find entrypoint" {
		t.Fatalf("runner request = %+v", runner.got)
	}
	if !tool.Definition().Behavior.ReadOnly {
		t.Fatal("task tool must be read-only: the subagent cannot modify the workspace")
	}
}

func TestTaskToolRejectsEmptyPromptAndMissingRunner(t *testing.T) {
	tool := NewTaskTool(TaskOptions{Runner: &fakeTaskRunner{}})
	empty, _ := json.Marshal(map[string]any{"prompt": "   "})
	if _, err := tool.Run(context.Background(), agent.ToolInput{Arguments: empty}); err == nil {
		t.Fatal("Run() error = nil, want rejection of an empty prompt")
	}

	noRunner := NewTaskTool(TaskOptions{})
	args, _ := json.Marshal(map[string]any{"prompt": "go"})
	if _, err := noRunner.Run(context.Background(), agent.ToolInput{Arguments: args}); err == nil {
		t.Fatal("Run() error = nil, want missing-runner error")
	}
}

func TestWebFetchExtractsReadableText(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<!DOCTYPE html><html><head><title>x</title>
			<style>body{color:red}</style></head>
			<body><h1>Release notes</h1><script>alert(1)</script>
			<p>Version 2 fixes &amp; improves things.</p></body></html>`))
	}))
	defer server.Close()

	tool := NewWebFetchTool(WebOptions{})
	args, _ := json.Marshal(map[string]any{"url": server.URL})
	out, err := tool.Run(context.Background(), agent.ToolInput{Arguments: args})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !strings.Contains(out.Content, "Release notes") {
		t.Fatalf("headline missing: %q", out.Content)
	}
	if !strings.Contains(out.Content, "Version 2 fixes & improves things.") {
		t.Fatalf("body text/entity decoding missing: %q", out.Content)
	}
	if strings.Contains(out.Content, "alert(1)") || strings.Contains(out.Content, "color:red") {
		t.Fatalf("script/style content leaked: %q", out.Content)
	}
	if !strings.Contains(out.Content, "untrusted web content") {
		t.Fatalf("untrusted-content notice missing: %q", out.Content)
	}
	if out.Metadata["status"] != http.StatusOK {
		t.Fatalf("metadata = %#v", out.Metadata)
	}
}

func TestWebFetchRejectsNonHTTPSchemeAndErrors(t *testing.T) {
	tool := NewWebFetchTool(WebOptions{})
	bad, _ := json.Marshal(map[string]any{"url": "file:///etc/passwd"})
	if _, err := tool.Run(context.Background(), agent.ToolInput{Arguments: bad}); err == nil {
		t.Fatal("Run() error = nil, want scheme rejection")
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()
	missing, _ := json.Marshal(map[string]any{"url": server.URL})
	if _, err := tool.Run(context.Background(), agent.ToolInput{Arguments: missing}); err == nil {
		t.Fatal("Run() error = nil, want status error")
	}
}

func TestWebSearchRequiresConfiguredEndpoint(t *testing.T) {
	tool := NewWebSearchTool(WebOptions{})
	args, _ := json.Marshal(map[string]any{"query": "golang"})
	if _, err := tool.Run(context.Background(), agent.ToolInput{Arguments: args}); err == nil {
		t.Fatal("Run() error = nil, want missing-endpoint error")
	} else if !strings.Contains(err.Error(), "web.search_url") {
		t.Fatalf("error = %v, want configuration guidance", err)
	}
}

func TestWebSearchParsesJSONAndSubstitutesQuery(t *testing.T) {
	var gotQuery string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query().Get("q")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[
			{"title":"Go","url":"https://go.dev","content":"the language"},
			{"name":"Docs","link":"https://go.dev/doc","snippet":"reference"}
		]}`))
	}))
	defer server.Close()

	tool := NewWebSearchTool(WebOptions{SearchURL: server.URL + "/search?format=json&q={query}"})
	args, _ := json.Marshal(map[string]any{"query": "golang concurrency"})
	out, err := tool.Run(context.Background(), agent.ToolInput{Arguments: args})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if gotQuery != "golang concurrency" {
		t.Fatalf("query = %q", gotQuery)
	}
	if !strings.Contains(out.Content, "https://go.dev") || !strings.Contains(out.Content, "the language") {
		t.Fatalf("content = %q", out.Content)
	}
	if !strings.Contains(out.Content, "reference") {
		t.Fatalf("alternate field names not parsed: %q", out.Content)
	}
	if out.Metadata["results"] != 2 {
		t.Fatalf("metadata = %#v", out.Metadata)
	}
}

func TestParseSearchResultsFallsBackToHTML(t *testing.T) {
	html := `<html><body><a href="https://example.com/a">First <b>result</b></a>
		<a href="https://example.com/b">Second</a></body></html>`
	results := parseSearchResults(html)
	if len(results) != 2 {
		t.Fatalf("results = %#v", results)
	}
	if results[0].Title != "First result" || results[0].URL != "https://example.com/a" {
		t.Fatalf("first result = %+v", results[0])
	}
}

func TestReadOnlyToolsExcludeMutatingTooling(t *testing.T) {
	names := map[string]bool{}
	for _, tool := range ReadOnlyTools(FileOptions{WorkDir: t.TempDir()}) {
		names[tool.Definition().Name] = true
	}
	for _, want := range []string{"read", "ls", "glob", "grep"} {
		if !names[want] {
			t.Fatalf("read-only tool set missing %q: %#v", want, names)
		}
	}
	for _, forbidden := range []string{"bash", "write", "edit", "task"} {
		if names[forbidden] {
			t.Fatalf("read-only tool set must not include %q", forbidden)
		}
	}
}
