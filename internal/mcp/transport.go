package mcp

import (
	"fmt"
	"os"
	"strings"
	"time"
)

// Transport names an MCP wire protocol. Stdio is the default; http is the
// streamable HTTP transport, sse the legacy HTTP+SSE one.
const (
	TransportStdio = "stdio"
	TransportHTTP  = "http"
	TransportSSE   = "sse"
)

// DefaultRequestTimeout bounds one JSON-RPC round trip on a remote transport,
// so a wedged server cannot hang a turn.
const DefaultRequestTimeout = 60 * time.Second

// ServerConfig is the launch configuration for one MCP server.
//
// A stdio server is launched with Command/Args. An http or sse server is
// reached at URL and authenticated with Headers alone, or with Token/TokenEnv,
// which is sent as "Authorization: Bearer <token>". An Authorization header in
// Headers wins over Token, so an exotic scheme is expressible without code.
type ServerConfig struct {
	Enabled bool
	// Type selects the transport. Empty means stdio.
	Type string

	// Stdio.
	Command string
	Args    []string

	// http / sse.
	URL      string
	Headers  map[string]string
	Token    string
	TokenEnv string

	// Timeout bounds one request on a remote transport. Zero uses
	// DefaultRequestTimeout.
	Timeout time.Duration
}

// Transport returns the normalized transport name.
func (c ServerConfig) Transport() string {
	t := strings.ToLower(strings.TrimSpace(c.Type))
	if t == "" {
		return TransportStdio
	}
	return t
}

// Validate reports whether the configuration carries what its transport needs.
// The error text is what /mcp shows for a server that never started, so it
// names the missing field rather than the server.
func (c ServerConfig) Validate() error {
	switch c.Transport() {
	case TransportStdio:
		if strings.TrimSpace(c.Command) == "" {
			return fmt.Errorf("command is required for stdio")
		}
	case TransportHTTP, TransportSSE:
		if strings.TrimSpace(c.URL) == "" {
			return fmt.Errorf("url is required for %s", c.Transport())
		}
	default:
		return fmt.Errorf("unknown transport %q (use stdio, http or sse)", c.Type)
	}
	return nil
}

// RequestTimeout returns the per-request bound for a remote transport.
func (c ServerConfig) RequestTimeout() time.Duration {
	if c.Timeout > 0 {
		return c.Timeout
	}
	return DefaultRequestTimeout
}

// Endpoint is the one-line target shown by /mcp: the command line for a stdio
// server, the URL for a remote one.
func (c ServerConfig) Endpoint() string {
	if c.Transport() == TransportStdio {
		return strings.TrimSpace(c.Command + " " + strings.Join(c.Args, " "))
	}
	return strings.TrimSpace(c.URL)
}

// AuthHeaders returns the headers a remote request must carry. Token and
// TokenEnv are resolved into Authorization unless the caller already supplied
// one, so an explicit header is never silently overwritten.
func (c ServerConfig) AuthHeaders() map[string]string {
	out := make(map[string]string, len(c.Headers)+1)
	for k, v := range c.Headers {
		out[k] = v
	}
	if hasHeader(out, "authorization") {
		return out
	}
	if token := c.ResolvedToken(); token != "" {
		out["Authorization"] = "Bearer " + token
	}
	return out
}

// ResolvedToken returns the configured token, falling back to TokenEnv.
func (c ServerConfig) ResolvedToken() string {
	if strings.TrimSpace(c.Token) != "" {
		return strings.TrimSpace(c.Token)
	}
	if c.TokenEnv == "" {
		return ""
	}
	return strings.TrimSpace(os.Getenv(c.TokenEnv))
}

func hasHeader(headers map[string]string, name string) bool {
	for key := range headers {
		if strings.EqualFold(key, name) {
			return true
		}
	}
	return false
}

// NewClient builds the client for cfg's transport.
func NewClient(cfg ServerConfig) (Client, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	switch cfg.Transport() {
	case TransportStdio:
		return NewStdioClient(cfg.Command, cfg.Args...)
	case TransportHTTP, TransportSSE:
		return newRemoteClient(cfg)
	}
	return nil, fmt.Errorf("unknown transport %q (use stdio, http or sse)", cfg.Type)
}
