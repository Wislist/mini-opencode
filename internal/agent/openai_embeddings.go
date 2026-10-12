package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// OpenAICompatibleEmbedder calls the OpenAI-compatible /embeddings endpoint.
// It is deliberately separate from Provider: a chat provider is not required
// to expose embeddings, and an embedding model may live on another provider.
type OpenAICompatibleEmbedder struct {
	provider *OpenAICompatibleProvider
}

// NewOpenAICompatibleEmbedder builds an embeddings client with the same
// timeout and retry behavior as the chat-completions client.
func NewOpenAICompatibleEmbedder(config OpenAICompatibleConfig) (*OpenAICompatibleEmbedder, error) {
	provider, err := NewOpenAICompatibleProvider(config)
	if err != nil {
		return nil, err
	}
	return &OpenAICompatibleEmbedder{provider: provider}, nil
}

// Embed returns one vector per input, preserving input order even when the
// server returns data rows out of order.
func (e *OpenAICompatibleEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	body, err := json.Marshal(openAIEmbeddingRequest{
		Model: e.provider.config.Model,
		Input: texts,
	})
	if err != nil {
		return nil, fmt.Errorf("encode embeddings request: %w", err)
	}

	resp, err := e.provider.doWithRetry(ctx, func() (*http.Request, error) {
		url := strings.TrimRight(e.provider.config.BaseURL, "/") + "/embeddings"
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(string(body)))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+e.provider.config.APIKey)
		req.Header.Set("Content-Type", "application/json")
		return req, nil
	})
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read embeddings response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &HTTPStatusError{
			StatusCode: resp.StatusCode,
			Body:       strings.TrimSpace(string(data)),
			Attempts:   1,
		}
	}

	var out openAIEmbeddingResponse
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("decode embeddings response: %w", err)
	}
	if len(out.Data) != len(texts) {
		return nil, fmt.Errorf("embeddings response returned %d vectors for %d inputs", len(out.Data), len(texts))
	}

	vectors := make([][]float32, len(texts))
	seen := make([]bool, len(texts))
	for _, item := range out.Data {
		if item.Index < 0 || item.Index >= len(texts) || seen[item.Index] {
			return nil, fmt.Errorf("embeddings response has invalid index %d", item.Index)
		}
		if len(item.Embedding) == 0 {
			return nil, fmt.Errorf("embeddings response has empty vector at index %d", item.Index)
		}
		vectors[item.Index] = item.Embedding
		seen[item.Index] = true
	}
	return vectors, nil
}

type openAIEmbeddingRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

type openAIEmbeddingResponse struct {
	Data []struct {
		Index     int       `json:"index"`
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
}
