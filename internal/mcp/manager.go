package mcp

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/wislist/mini-opencode/internal/agent"
)

// DefaultStartTimeout bounds the initialize + tools/list handshake for one
// server so a wedged server cannot stall startup.
const DefaultStartTimeout = 15 * time.Second

// ServerConfig is the launch configuration for one MCP server.
type ServerConfig struct {
	Enabled bool
	Command string
	Args    []string
}

// ServerStatus is the outcome of starting one MCP server, used by the /mcp
// command and by startup diagnostics.
type ServerStatus struct {
	Name    string
	Enabled bool
	Command string
	Args    []string
	Tools   int
	Err     string
}

// String renders a one-line human-readable status.
func (s ServerStatus) String() string {
	cmdline := strings.TrimSpace(s.Command + " " + strings.Join(s.Args, " "))
	switch {
	case !s.Enabled:
		return fmt.Sprintf("%s  (disabled)  %s", s.Name, cmdline)
	case s.Err != "":
		return fmt.Sprintf("%s  failed: %s", s.Name, s.Err)
	default:
		return fmt.Sprintf("%s  %d tools  %s", s.Name, s.Tools, cmdline)
	}
}

// Manager owns the MCP server processes for one application session. It
// starts every enabled server, exposes their tools to the agent runtime, and
// keeps per-server status for diagnostics. A failing server never aborts
// startup: its error is recorded and the remaining servers still load.
type Manager struct {
	mu       sync.Mutex
	clients  []Client
	tools    []agent.Tool
	statuses []ServerStatus
}

func NewManager() *Manager { return &Manager{} }

// Start launches every enabled server, performs the MCP handshake, and
// registers each advertised tool under "<server>__<tool>". Servers are
// started in name order so diagnostics are stable.
func (m *Manager) Start(ctx context.Context, servers map[string]ServerConfig) {
	if m == nil || len(servers) == 0 {
		return
	}
	names := make([]string, 0, len(servers))
	for name := range servers {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		cfg := servers[name]
		status := ServerStatus{
			Name:    name,
			Enabled: cfg.Enabled,
			Command: cfg.Command,
			Args:    cfg.Args,
		}
		if !cfg.Enabled {
			m.appendStatus(status)
			continue
		}
		if strings.TrimSpace(cfg.Command) == "" {
			status.Err = "command is required"
			m.appendStatus(status)
			continue
		}
		tools, err := m.startServer(ctx, name, cfg)
		if err != nil {
			status.Err = err.Error()
			m.appendStatus(status)
			continue
		}
		status.Tools = len(tools)
		m.mu.Lock()
		m.tools = append(m.tools, tools...)
		m.mu.Unlock()
		m.appendStatus(status)
	}
}

// startServer starts one server and returns the agent tools it advertises.
func (m *Manager) startServer(ctx context.Context, name string, cfg ServerConfig) ([]agent.Tool, error) {
	client, err := NewStdioClient(cfg.Command, cfg.Args...)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	m.clients = append(m.clients, client)
	m.mu.Unlock()

	handshakeCtx, cancel := context.WithTimeout(ctx, DefaultStartTimeout)
	defer cancel()

	if err := client.Initialize(handshakeCtx); err != nil {
		return nil, fmt.Errorf("initialize: %w", err)
	}
	defs, err := client.ListTools(handshakeCtx)
	if err != nil {
		return nil, fmt.Errorf("tools/list: %w", err)
	}
	tools := make([]agent.Tool, 0, len(defs))
	for _, def := range defs {
		tools = append(tools, NewNamespacedToolAdapter(client, def, name))
	}
	return tools, nil
}

func (m *Manager) appendStatus(status ServerStatus) {
	m.mu.Lock()
	m.statuses = append(m.statuses, status)
	m.mu.Unlock()
}

// Tools returns every tool advertised by the started servers.
func (m *Manager) Tools() []agent.Tool {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]agent.Tool, len(m.tools))
	copy(out, m.tools)
	return out
}

// Statuses returns the per-server start results.
func (m *Manager) Statuses() []ServerStatus {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]ServerStatus, len(m.statuses))
	copy(out, m.statuses)
	return out
}

// Close terminates every started server process.
func (m *Manager) Close() {
	if m == nil {
		return
	}
	m.mu.Lock()
	clients := m.clients
	m.clients = nil
	m.mu.Unlock()
	for _, c := range clients {
		_ = c.Close()
	}
}
