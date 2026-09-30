package mcp

import (
	"strings"
	"testing"
	"time"
)

func TestServerConfigTransportDefaultsToStdio(t *testing.T) {
	cases := []struct {
		name string
		typ  string
		want string
	}{
		{"empty", "", TransportStdio},
		{"explicit stdio", "stdio", TransportStdio},
		{"upper case", "HTTP", TransportHTTP},
		{"padded", "  sse  ", TransportSSE},
		{"mixed case", "Sse", TransportSSE},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := ServerConfig{Type: tc.typ}
			if got := cfg.Transport(); got != tc.want {
				t.Fatalf("Transport() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestServerConfigValidate(t *testing.T) {
	cases := []struct {
		name    string
		cfg     ServerConfig
		wantErr string
	}{
		{name: "stdio with command", cfg: ServerConfig{Command: "gopls"}},
		{name: "http with url", cfg: ServerConfig{Type: TransportHTTP, URL: "https://example.com/mcp"}},
		{name: "sse with url", cfg: ServerConfig{Type: TransportSSE, URL: "https://example.com/sse"}},
		{name: "stdio without command", cfg: ServerConfig{}, wantErr: "command is required"},
		{name: "stdio with blank command", cfg: ServerConfig{Command: "   "}, wantErr: "command is required"},
		{name: "http without url", cfg: ServerConfig{Type: TransportHTTP}, wantErr: "url is required for http"},
		{name: "sse without url", cfg: ServerConfig{Type: TransportSSE, URL: " "}, wantErr: "url is required for sse"},
		{name: "unknown type", cfg: ServerConfig{Type: "websocket", URL: "wss://x"}, wantErr: "unknown transport"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.cfg.Validate()
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate() = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("Validate() = nil, want error containing %q", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("Validate() = %q, want it to contain %q", err.Error(), tc.wantErr)
			}
		})
	}
}

func TestRequestTimeoutDefaults(t *testing.T) {
	if got := (ServerConfig{}).RequestTimeout(); got != DefaultRequestTimeout {
		t.Fatalf("RequestTimeout() = %v, want %v", got, DefaultRequestTimeout)
	}
	cfg := ServerConfig{Timeout: 3 * time.Second}
	if got := cfg.RequestTimeout(); got != 3*time.Second {
		t.Fatalf("RequestTimeout() = %v, want 3s", got)
	}
	// A negative timeout is nonsense rather than "unbounded": fall back.
	if got := (ServerConfig{Timeout: -time.Second}).RequestTimeout(); got != DefaultRequestTimeout {
		t.Fatalf("negative RequestTimeout() = %v, want the default", got)
	}
}

func TestAuthHeaders(t *testing.T) {
	t.Run("plain headers are copied", func(t *testing.T) {
		cfg := ServerConfig{Headers: map[string]string{"X-Tenant": "acme"}}
		got := cfg.AuthHeaders()
		if got["X-Tenant"] != "acme" {
			t.Fatalf("headers = %v, want X-Tenant preserved", got)
		}
		if _, ok := got["Authorization"]; ok {
			t.Fatalf("no token configured, yet Authorization was set: %v", got)
		}
		// The input map must not be mutated: it is shared config.
		if _, ok := cfg.Headers["Authorization"]; ok {
			t.Fatalf("AuthHeaders mutated the configured headers: %v", cfg.Headers)
		}
	})

	t.Run("token becomes bearer", func(t *testing.T) {
		cfg := ServerConfig{Token: "  ghp_secret  "}
		if got := cfg.AuthHeaders()["Authorization"]; got != "Bearer ghp_secret" {
			t.Fatalf("Authorization = %q", got)
		}
	})

	t.Run("token env is resolved", func(t *testing.T) {
		t.Setenv("MINI_OPENCODE_MCP_TEST_TOKEN", "env-secret")
		cfg := ServerConfig{TokenEnv: "MINI_OPENCODE_MCP_TEST_TOKEN"}
		if got := cfg.AuthHeaders()["Authorization"]; got != "Bearer env-secret" {
			t.Fatalf("Authorization = %q", got)
		}
		if got := cfg.ResolvedToken(); got != "env-secret" {
			t.Fatalf("ResolvedToken() = %q", got)
		}
	})

	t.Run("explicit token wins over env", func(t *testing.T) {
		t.Setenv("MINI_OPENCODE_MCP_TEST_TOKEN", "env-secret")
		cfg := ServerConfig{Token: "direct", TokenEnv: "MINI_OPENCODE_MCP_TEST_TOKEN"}
		if got := cfg.ResolvedToken(); got != "direct" {
			t.Fatalf("ResolvedToken() = %q, want direct", got)
		}
	})

	t.Run("unset env yields no header", func(t *testing.T) {
		cfg := ServerConfig{TokenEnv: "MINI_OPENCODE_MCP_DEFINITELY_UNSET"}
		if got, ok := cfg.AuthHeaders()["Authorization"]; ok {
			t.Fatalf("Authorization = %q, want it absent", got)
		}
	})

	t.Run("explicit authorization header is not overwritten", func(t *testing.T) {
		cfg := ServerConfig{
			Token:   "ignored",
			Headers: map[string]string{"authorization": "Basic abc"},
		}
		got := cfg.AuthHeaders()
		if got["authorization"] != "Basic abc" {
			t.Fatalf("authorization = %q, want the configured value", got["authorization"])
		}
		if _, ok := got["Authorization"]; ok {
			t.Fatalf("bearer was added alongside an explicit Authorization: %v", got)
		}
	})
}

func TestEndpoint(t *testing.T) {
	stdio := ServerConfig{Command: "gopls", Args: []string{"mcp", "-rpc.trace"}}
	if got := stdio.Endpoint(); got != "gopls mcp -rpc.trace" {
		t.Fatalf("stdio Endpoint() = %q", got)
	}
	remote := ServerConfig{Type: TransportHTTP, URL: "https://api.github.com/mcp/", Command: "ignored"}
	if got := remote.Endpoint(); got != "https://api.github.com/mcp/" {
		t.Fatalf("http Endpoint() = %q", got)
	}
}

func TestNewClientRejectsBadConfig(t *testing.T) {
	// Boundary: validation runs before any process is spawned or request is
	// sent, so a misconfigured server fails with the field name, not a dial
	// timeout.
	if _, err := NewClient(ServerConfig{}); err == nil {
		t.Fatal("NewClient with no command and no url = nil error, want failure")
	}
	if _, err := NewClient(ServerConfig{Type: "carrier-pigeon"}); err == nil {
		t.Fatal("NewClient with an unknown transport = nil error, want failure")
	}
}
