package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/wislist/mini-opencode/internal/agent"
)

// TestMain doubles as an MCP server for the manager tests: when the helper
// environment variable is set, this process speaks JSON-RPC over stdio instead
// of running tests. Nothing else may be written to stdout in that mode.
func TestMain(m *testing.M) {
	if os.Getenv(helperEnv) == "1" {
		runMCPHelper()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

const helperEnv = "MINI_OPENCODE_MCP_HELPER"

type helperRequest struct {
	ID     int64           `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

func runMCPHelper() {
	in := bufio.NewScanner(os.Stdin)
	in.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	out := json.NewEncoder(os.Stdout)
	for in.Scan() {
		line := strings.TrimSpace(in.Text())
		if line == "" {
			continue
		}
		var req helperRequest
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			continue
		}
		switch req.Method {
		case "initialize":
			_ = out.Encode(map[string]any{
				"jsonrpc": "2.0", "id": req.ID,
				"result": map[string]any{
					"protocolVersion": protocolVersion,
					"capabilities":    map[string]any{},
					"serverInfo":      map[string]any{"name": "helper", "version": "0.0.1"},
				},
			})
		case "tools/list":
			_ = out.Encode(map[string]any{
				"jsonrpc": "2.0", "id": req.ID,
				"result": map[string]any{
					"tools": []map[string]any{{
						"name":        "echo",
						"description": "echo the input back",
						"inputSchema": map[string]any{"type": "object"},
					}},
				},
			})
		case "tools/call":
			_ = out.Encode(map[string]any{
				"jsonrpc": "2.0", "id": req.ID,
				"result": map[string]any{
					"content": []map[string]any{{"type": "text", "text": "pong"}},
				},
			})
		default:
			_ = out.Encode(map[string]any{
				"jsonrpc": "2.0", "id": req.ID,
				"error": map[string]any{"code": -32601, "message": "method not found"},
			})
		}
	}
}

// helperServerConfig launches this test binary as an MCP stdio server.
func helperServerConfig() ServerConfig {
	return ServerConfig{Enabled: true, Command: os.Args[0]}
}

func TestManagerStartsServerAndRegistersTools(t *testing.T) {
	t.Setenv(helperEnv, "1")

	manager := NewManager()
	defer manager.Close()
	manager.Start(context.Background(), map[string]ServerConfig{
		"helper": helperServerConfig(),
	})

	statuses := manager.Statuses()
	if len(statuses) != 1 {
		t.Fatalf("statuses = %#v", statuses)
	}
	if statuses[0].Err != "" {
		t.Fatalf("server failed to start: %s", statuses[0].Err)
	}
	if statuses[0].Tools != 1 {
		t.Fatalf("tools = %d, want 1", statuses[0].Tools)
	}

	tools := manager.Tools()
	if len(tools) != 1 {
		t.Fatalf("tools = %#v", tools)
	}
	if tools[0].Definition().Name != "helper__echo" {
		t.Fatalf("tool name = %q, want namespaced name", tools[0].Definition().Name)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := tools[0].Run(ctx, agent.ToolInput{
		CallID: "c1", Name: "helper__echo", Arguments: json.RawMessage(`{"text":"hi"}`),
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if out.Content != "pong" {
		t.Fatalf("content = %q, want pong", out.Content)
	}
}

func TestManagerRecordsDisabledAndBrokenServers(t *testing.T) {
	manager := NewManager()
	defer manager.Close()
	manager.Start(context.Background(), map[string]ServerConfig{
		"off":    {Enabled: false, Command: "ignored"},
		"nocmd":  {Enabled: true},
		"broken": {Enabled: true, Command: "mini-opencode-definitely-missing-binary"},
	})

	statuses := manager.Statuses()
	if len(statuses) != 3 {
		t.Fatalf("statuses = %d, want 3", len(statuses))
	}
	byName := map[string]ServerStatus{}
	for _, s := range statuses {
		byName[s.Name] = s
	}
	if byName["off"].Err != "" || byName["off"].Tools != 0 {
		t.Fatalf("disabled server status = %#v", byName["off"])
	}
	if !strings.Contains(byName["nocmd"].Err, "command is required") {
		t.Fatalf("missing command error = %q", byName["nocmd"].Err)
	}
	if byName["broken"].Err == "" {
		t.Fatal("broken server reported no error")
	}
	if len(manager.Tools()) != 0 {
		t.Fatalf("tools = %#v, want none", manager.Tools())
	}
	// A broken server must not panic or stall status rendering.
	if line := byName["broken"].String(); !strings.Contains(line, "failed") {
		t.Fatalf("status line = %q", line)
	}
}
