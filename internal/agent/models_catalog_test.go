package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func catalogServer(t *testing.T, body string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, body)
	}))
	t.Cleanup(server.Close)
	return server
}

// The whole point of the catalog is the reported context length: without it a
// custom model falls back to a placeholder window and the ctx indicator reads
// as nearly full from the first turn.
func TestFetchModelCatalogReadsContextWindow(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string // "id:window" pairs, comma separated
	}{
		{
			name: "openrouter style",
			body: `{"data":[{"id":"a","context_length":200000},{"id":"b","context_length":8192}]}`,
			want: "a:200000,b:8192",
		},
		{
			name: "context_window alias",
			body: `{"data":[{"id":"a","context_window":131072}]}`,
			want: "a:131072",
		},
		{
			name: "vllm max_model_len",
			body: `{"data":[{"id":"a","max_model_len":32768}]}`,
			want: "a:32768",
		},
		{
			name: "llama.cpp n_ctx",
			body: `{"data":[{"id":"a","n_ctx":4096}]}`,
			want: "a:4096",
		},
		{
			name: "max_input_tokens",
			body: `{"data":[{"id":"a","max_input_tokens":1000000}]}`,
			want: "a:1000000",
		},
		{
			name: "nested top_provider",
			body: `{"data":[{"id":"a","top_provider":{"context_length":64000,"max_completion_tokens":4096}}]}`,
			want: "a:64000",
		},
		{
			name: "string number is accepted",
			body: `{"data":[{"id":"a","context_length":"128000"}]}`,
			want: "a:128000",
		},
		{
			name: "first positive alias wins",
			body: `{"data":[{"id":"a","context_length":0,"context_window":50000}]}`,
			want: "a:50000",
		},
		{
			name: "no window reported",
			body: `{"data":[{"id":"a","object":"model","owned_by":"local"}]}`,
			want: "a:0",
		},
		{
			name: "negative and zero are ignored",
			body: `{"data":[{"id":"a","context_length":-1},{"id":"b","context_length":0}]}`,
			want: "a:0,b:0",
		},
		{
			name: "plain string entries",
			body: `{"data":["a","b"]}`,
			want: "a:0,b:0",
		},
		{
			name: "top level array",
			body: `[{"id":"a","context_length":9000}]`,
			want: "a:9000",
		},
		{
			name: "duplicate id keeps the first window",
			body: `{"data":[{"id":"a","context_length":9000},{"id":"a","context_length":1}]}`,
			want: "a:9000",
		},
		{
			name: "blank ids are skipped",
			body: `{"data":[{"id":"  ","context_length":1000},{"id":" a ","context_length":2000}]}`,
			want: "a:2000",
		},
		{
			name: "empty list",
			body: `{"data":[]}`,
			want: "",
		},
		{
			name: "nested object without a window",
			body: `{"data":[{"id":"a","top_provider":{}}]}`,
			want: "a:0",
		},
		{
			name: "nested value that is not a number",
			body: `{"data":[{"id":"a","context_length":"unbounded"}]}`,
			want: "a:0",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := catalogServer(t, tc.body)
			catalog, err := FetchModelCatalog(context.Background(), server.URL, "sk", time.Second)
			if err != nil {
				t.Fatalf("FetchModelCatalog: %v", err)
			}
			parts := make([]string, 0, len(catalog))
			for _, info := range catalog {
				parts = append(parts, fmt.Sprintf("%s:%d", info.ID, info.ContextWindow))
			}
			if got := strings.Join(parts, ","); got != tc.want {
				t.Fatalf("catalog = %q, want %q", got, tc.want)
			}
		})
	}
}

// The ids-only helper and the catalog must not drift: they parse one payload.
func TestFetchModelsAgreesWithTheCatalog(t *testing.T) {
	body := `{"data":[{"id":"a","context_length":1000},{"id":"b"},{"id":"a"}]}`
	server := catalogServer(t, body)

	ids, err := FetchModels(context.Background(), server.URL, "sk", time.Second)
	if err != nil {
		t.Fatalf("FetchModels: %v", err)
	}
	catalog, err := FetchModelCatalog(context.Background(), server.URL, "sk", time.Second)
	if err != nil {
		t.Fatalf("FetchModelCatalog: %v", err)
	}
	if len(ids) != len(catalog) {
		t.Fatalf("FetchModels returned %d ids, catalog %d entries", len(ids), len(catalog))
	}
	for i := range ids {
		if ids[i] != catalog[i].ID {
			t.Fatalf("position %d: id %q vs catalog %q", i, ids[i], catalog[i].ID)
		}
	}
}

func TestFetchModelCatalogErrors(t *testing.T) {
	t.Run("non 2xx", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprint(w, `{"error":"bad key sk-leaky"}`)
		}))
		defer server.Close()

		_, err := FetchModelCatalog(context.Background(), server.URL, "sk-leaky", time.Second)
		if err == nil {
			t.Fatal("FetchModelCatalog = nil error for a 401")
		}
		if !strings.Contains(err.Error(), "401") {
			t.Fatalf("error %q should carry the status", err.Error())
		}
		if strings.Contains(err.Error(), "sk-leaky") {
			t.Fatalf("error leaked the api key: %q", err.Error())
		}
	})

	t.Run("empty base url", func(t *testing.T) {
		if _, err := FetchModelCatalog(context.Background(), "  ", "sk", time.Second); err == nil {
			t.Fatal("empty base_url accepted")
		}
	})

	t.Run("not a model list", func(t *testing.T) {
		server := catalogServer(t, `{"error":"unauthorized"}`)
		if _, err := FetchModelCatalog(context.Background(), server.URL, "sk", time.Second); err == nil {
			t.Fatal("an error envelope was accepted as a model list")
		}
	})

	t.Run("timeout", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			time.Sleep(80 * time.Millisecond)
			fmt.Fprint(w, `{"data":[]}`)
		}))
		defer server.Close()

		start := time.Now()
		if _, err := FetchModelCatalog(context.Background(), server.URL, "sk", 10*time.Millisecond); err == nil {
			t.Fatal("slow endpoint did not time out")
		}
		if elapsed := time.Since(start); elapsed > time.Second {
			t.Fatalf("timeout took %v", elapsed)
		}
	})
}

// parseModelWindow is the parser the catalog is built on, so its boundary
// behaviour is pinned directly as well.
func TestParseModelWindow(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want int
	}{
		{name: "context_length", raw: `{"id":"a","context_length":200000}`, want: 200000},
		{name: "context_window", raw: `{"id":"a","context_window":1000}`, want: 1000},
		{name: "max_context_length", raw: `{"id":"a","max_context_length":2000}`, want: 2000},
		{name: "max_model_len", raw: `{"id":"a","max_model_len":3000}`, want: 3000},
		{name: "n_ctx", raw: `{"id":"a","n_ctx":4000}`, want: 4000},
		{name: "context_size", raw: `{"id":"a","context_size":5000}`, want: 5000},
		{name: "max_input_tokens", raw: `{"id":"a","max_input_tokens":6000}`, want: 6000},
		{name: "input_token_limit", raw: `{"id":"a","input_token_limit":7000}`, want: 7000},
		{name: "nested", raw: `{"id":"a","top_provider":{"context_length":8000}}`, want: 8000},
		{name: "top level beats nested", raw: `{"id":"a","context_length":9,"top_provider":{"context_length":8}}`, want: 9},
		{name: "absent", raw: `{"id":"a"}`, want: 0},
		{name: "zero", raw: `{"id":"a","context_length":0}`, want: 0},
		{name: "negative", raw: `{"id":"a","context_length":-5}`, want: 0},
		{name: "float is truncated", raw: `{"id":"a","context_length":1000.9}`, want: 1000},
		{name: "string", raw: `{"id":"a","context_length":"2048"}`, want: 2048},
		{name: "non numeric string", raw: `{"id":"a","context_length":"many"}`, want: 0},
		{name: "null", raw: `{"id":"a","context_length":null}`, want: 0},
		{name: "wrong type", raw: `{"id":"a","context_length":{"n":1}}`, want: 0},
		{name: "nested wrong type", raw: `{"id":"a","top_provider":"none"}`, want: 0},
		{name: "huge value is kept", raw: `{"id":"a","context_length":10000000}`, want: 10000000},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := parseModelWindow(json.RawMessage(tc.raw)); got != tc.want {
				t.Fatalf("parseModelWindow(%s) = %d, want %d", tc.raw, got, tc.want)
			}
		})
	}
}
