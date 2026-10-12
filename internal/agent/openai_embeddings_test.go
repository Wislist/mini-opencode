package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOpenAICompatibleEmbedderRequestAndResponseOrder(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/embeddings" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Fatalf("Authorization = %q", got)
		}
		var body struct {
			Model string   `json:"model"`
			Input []string `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.Model != "embed-model" || strings.Join(body.Input, ",") != "first,second" {
			t.Fatalf("body = %#v", body)
		}
		_, _ = w.Write([]byte(`{"data":[{"index":1,"embedding":[0,1]},{"index":0,"embedding":[1,0]}]}`))
	}))
	defer server.Close()

	embedder, err := NewOpenAICompatibleEmbedder(OpenAICompatibleConfig{
		BaseURL: server.URL + "/v1", APIKey: "test-key", Model: "embed-model", MaxRetries: 0, MaxRetriesSet: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	vectors, err := embedder.Embed(context.Background(), []string{"first", "second"})
	if err != nil {
		t.Fatal(err)
	}
	if len(vectors) != 2 || vectors[0][0] != 1 || vectors[1][1] != 1 {
		t.Fatalf("vectors = %#v", vectors)
	}
}

func TestOpenAICompatibleEmbedderRejectsBadResponses(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{name: "http error", status: http.StatusBadRequest, body: `{"error":"bad model"}`, want: "provider error 400"},
		{name: "malformed json", status: http.StatusOK, body: `{`, want: "decode embeddings response"},
		{name: "missing item", status: http.StatusOK, body: `{"data":[]}`, want: "returned 0 vectors for 1 inputs"},
		{name: "empty vector", status: http.StatusOK, body: `{"data":[{"index":0,"embedding":[]}]}`, want: "empty vector"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer server.Close()
			embedder, err := NewOpenAICompatibleEmbedder(OpenAICompatibleConfig{
				BaseURL: server.URL, APIKey: "key", Model: "model", MaxRetries: 0, MaxRetriesSet: true,
			})
			if err != nil {
				t.Fatal(err)
			}
			_, err = embedder.Embed(context.Background(), []string{"input"})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want containing %q", err, tt.want)
			}
		})
	}
}

func TestOpenAICompatibleEmbedderEmptyInputDoesNotRequest(t *testing.T) {
	embedder, err := NewOpenAICompatibleEmbedder(OpenAICompatibleConfig{
		BaseURL: "https://unused.invalid", APIKey: "key", Model: "model",
	})
	if err != nil {
		t.Fatal(err)
	}
	vectors, err := embedder.Embed(context.Background(), nil)
	if err != nil || len(vectors) != 0 {
		t.Fatalf("Embed(nil) = %#v, %v", vectors, err)
	}
}
