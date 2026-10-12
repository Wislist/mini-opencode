package app

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/wislist/mini-opencode/internal/agent"
	"github.com/wislist/mini-opencode/internal/config"
	"github.com/wislist/mini-opencode/internal/memory"
)

const memoryIndexFile = "memory-index.db"

// liveMemoryEmbedder resolves provider credentials for every request so a key
// added through /provider takes effect without rebuilding the memory index.
type liveMemoryEmbedder struct {
	cfg *config.Config
}

func (e liveMemoryEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if e.cfg == nil {
		return nil, fmt.Errorf("memory: config unavailable")
	}
	providerName := strings.TrimSpace(e.cfg.Memory.EmbeddingProvider)
	provider, ok := e.cfg.ProviderByName(providerName)
	if !ok {
		return nil, fmt.Errorf("memory: unknown embedding provider %q", providerName)
	}
	if provider.EffectiveType() != config.ProviderTypeOpenAICompatible {
		return nil, fmt.Errorf("memory: embedding provider %q is not OpenAI-compatible", providerName)
	}
	model := strings.TrimSpace(e.cfg.Memory.EmbeddingModel)
	if model == "" {
		return nil, fmt.Errorf("memory: embedding_model is required")
	}
	retries, retriesSet := provider.Retries()
	client, err := agent.NewOpenAICompatibleEmbedder(agent.OpenAICompatibleConfig{
		BaseURL:       provider.BaseURL,
		APIKey:        provider.ResolvedAPIKey(),
		Model:         model,
		Timeout:       provider.RequestTimeout(),
		MaxRetries:    retries,
		MaxRetriesSet: retriesSet,
	})
	if err != nil {
		return nil, fmt.Errorf("memory embedding provider %q: %w", providerName, err)
	}
	return client.Embed(ctx, texts)
}

// newMemoryRetriever always returns a usable retriever. Semantic setup and
// runtime failures degrade to lexical search because memory must not make the
// coding agent unavailable.
func newMemoryRetriever(cfg *config.Config, workingDir string, store *memory.Store) (memory.Retriever, func()) {
	lexical := memory.LexicalRetriever{Store: store}
	if cfg == nil || !cfg.Memory.HybridEnabled() || strings.TrimSpace(cfg.Memory.EmbeddingModel) == "" {
		return lexical, func() {}
	}
	modelKey := strings.TrimSpace(cfg.Memory.EmbeddingProvider) + "/" + strings.TrimSpace(cfg.Memory.EmbeddingModel)
	retriever, err := memory.NewHybridRetriever(
		store,
		filepath.Join(workingDir, ".mini-opencode", memoryIndexFile),
		liveMemoryEmbedder{cfg: cfg},
		memory.RetrievalConfig{
			EmbeddingModel: modelKey,
			CandidateK:     cfg.Memory.EffectiveCandidateK(),
			MaxTokens:      cfg.Memory.EffectiveMaxTokens(),
			MinSimilarity:  cfg.Memory.EffectiveMinSimilarity(),
		},
	)
	if err != nil {
		return lexical, func() {}
	}
	return retriever, func() { _ = retriever.Close() }
}
