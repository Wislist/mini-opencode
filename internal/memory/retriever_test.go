package memory

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"sync"
	"testing"
)

type fakeEmbedder struct {
	mu      sync.Mutex
	vectors map[string][]float32
	err     error
	inputs  []string
}

func (f *fakeEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.inputs = append(f.inputs, texts...)
	if f.err != nil {
		return nil, f.err
	}
	out := make([][]float32, len(texts))
	for i, text := range texts {
		out[i] = slices.Clone(f.vectors[text])
	}
	return out, nil
}

func (f *fakeEmbedder) count(text string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	count := 0
	for _, input := range f.inputs {
		if input == text {
			count++
		}
	}
	return count
}

func writeTestNote(t *testing.T, store *Store, name, title, body string) Note {
	t.Helper()
	note, err := store.Write(Note{Name: name, Title: title, Body: body})
	if err != nil {
		t.Fatalf("Write(%s) error = %v", name, err)
	}
	return note
}

func testNoteText(note Note) string {
	return note.Title + "\n" + note.Body
}

func TestHybridRetrieverFindsSemanticMatchWithoutLexicalOverlap(t *testing.T) {
	workDir := t.TempDir()
	store := NewStore(workDir)
	semantic := writeTestNote(t, store, "release", "Release process", "Promote staging after the smoke suite passes.")
	writeTestNote(t, store, "colors", "Terminal palette", "All colors live in styles.go.")

	embedder := &fakeEmbedder{vectors: map[string][]float32{
		"how do we ship safely":                           {1, 0},
		testNoteText(semantic):                            {0.99, 0.01},
		"Terminal palette\nAll colors live in styles.go.": {0, 1},
	}}
	retriever, err := NewHybridRetriever(store, filepath.Join(workDir, ".mini-opencode", "memory-index.db"), embedder, RetrievalConfig{
		EmbeddingModel: "test-embedding",
		CandidateK:     10,
		MinSimilarity:  0.5,
	})
	if err != nil {
		t.Fatalf("NewHybridRetriever() error = %v", err)
	}
	t.Cleanup(func() { _ = retriever.Close() })

	hits, err := retriever.Search(context.Background(), "how do we ship safely", 3)
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(hits) == 0 || hits[0].Name != "release" {
		t.Fatalf("hits = %#v, want semantic release note first", hits)
	}
}

func TestHybridRetrieverKeepsExactSymbolNearTop(t *testing.T) {
	workDir := t.TempDir()
	store := NewStore(workDir)
	exact := writeTestNote(t, store, "symbol", "Runtime.AdoptStateFrom", "Preserves messages while rebuilding the runtime.")
	semantic := writeTestNote(t, store, "switching", "Provider switching", "Keep the conversation when selecting another backend.")

	embedder := &fakeEmbedder{vectors: map[string][]float32{
		"Runtime.AdoptStateFrom": {1, 0},
		testNoteText(exact):      {0.7, 0.7},
		testNoteText(semantic):   {1, 0},
	}}
	retriever, err := NewHybridRetriever(store, filepath.Join(workDir, ".mini-opencode", "memory-index.db"), embedder, RetrievalConfig{
		EmbeddingModel: "test-embedding",
		CandidateK:     10,
		MinSimilarity:  0,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = retriever.Close() })

	hits, err := retriever.Search(context.Background(), "Runtime.AdoptStateFrom", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 2 || hits[0].Name != "symbol" {
		t.Fatalf("hits = %#v, want exact symbol first", hits)
	}
}

func TestHybridRetrieverFallsBackToLexicalSearch(t *testing.T) {
	store := NewStore(t.TempDir())
	writeTestNote(t, store, "sqlite", "SQLite tuning", "Use WAL mode for concurrent readers.")
	retriever, err := NewHybridRetriever(store, filepath.Join(t.TempDir(), "index.db"), &fakeEmbedder{err: errors.New("offline")}, RetrievalConfig{
		EmbeddingModel: "test-embedding",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = retriever.Close() })

	hits, err := retriever.Search(context.Background(), "SQLite", 3)
	if err != nil {
		t.Fatalf("Search() should degrade without surfacing embedding error: %v", err)
	}
	if len(hits) != 1 || hits[0].Name != "sqlite" {
		t.Fatalf("fallback hits = %#v", hits)
	}
}

func TestHybridRetrieverNilEmbedderUsesLexicalSearch(t *testing.T) {
	store := NewStore(t.TempDir())
	writeTestNote(t, store, "sqlite", "SQLite tuning", "Use WAL mode.")
	retriever, err := NewHybridRetriever(store, filepath.Join(t.TempDir(), "index.db"), nil, RetrievalConfig{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = retriever.Close() })

	hits, err := retriever.Search(context.Background(), "SQLite", 3)
	if err != nil || len(hits) != 1 || hits[0].Name != "sqlite" {
		t.Fatalf("Search() = %#v, %v", hits, err)
	}
}

func TestHybridRetrieverIncrementallyMaintainsIndex(t *testing.T) {
	workDir := t.TempDir()
	store := NewStore(workDir)
	first := writeTestNote(t, store, "first", "First", "alpha body")
	second := writeTestNote(t, store, "second", "Second", "beta body")
	embedder := &fakeEmbedder{vectors: map[string][]float32{
		"alpha":               {1, 0},
		testNoteText(first):   {1, 0},
		testNoteText(second):  {0, 1},
		"First\nchanged body": {0.8, 0.2},
	}}
	retriever, err := NewHybridRetriever(store, filepath.Join(workDir, ".mini-opencode", "memory-index.db"), embedder, RetrievalConfig{
		EmbeddingModel: "test-embedding",
		CandidateK:     10,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = retriever.Close() })

	if _, err := retriever.Search(context.Background(), "alpha", 3); err != nil {
		t.Fatal(err)
	}
	if _, err := retriever.Search(context.Background(), "alpha", 3); err != nil {
		t.Fatal(err)
	}
	if got := embedder.count(testNoteText(first)); got != 1 {
		t.Fatalf("unchanged first note embedded %d times, want 1", got)
	}
	if got := embedder.count(testNoteText(second)); got != 1 {
		t.Fatalf("unchanged second note embedded %d times, want 1", got)
	}

	if _, err := store.Write(Note{Name: "first", Title: "First", Body: "changed body"}); err != nil {
		t.Fatal(err)
	}
	if _, err := retriever.Search(context.Background(), "alpha", 3); err != nil {
		t.Fatal(err)
	}
	if got := embedder.count("First\nchanged body"); got != 1 {
		t.Fatalf("changed note embedded %d times, want 1", got)
	}
	if got := embedder.count(testNoteText(second)); got != 1 {
		t.Fatalf("unchanged sibling re-embedded %d times, want 1", got)
	}

	if err := store.Delete("second"); err != nil {
		t.Fatal(err)
	}
	if _, err := retriever.Search(context.Background(), "alpha", 3); err != nil {
		t.Fatal(err)
	}
	count, err := retriever.index.countModel(context.Background(), "test-embedding")
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("indexed rows after delete = %d, want 1", count)
	}
}

func TestHybridRetrieverRemovesStaleVectorWhenChangedNoteReturnsEmptyEmbedding(t *testing.T) {
	workDir := t.TempDir()
	store := NewStore(workDir)
	original := writeTestNote(t, store, "release", "Release", "ship through staging")
	embedder := &fakeEmbedder{vectors: map[string][]float32{
		"shipping":              {1, 0},
		testNoteText(original):  {1, 0},
		"Release\nnew workflow": nil,
	}}
	retriever, err := NewHybridRetriever(store, filepath.Join(workDir, ".mini-opencode", "memory-index.db"), embedder, RetrievalConfig{
		EmbeddingModel: "test-embedding",
		CandidateK:     10,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = retriever.Close() })

	if _, err := retriever.Search(context.Background(), "shipping", 3); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Write(Note{Name: "release", Title: "Release", Body: "new workflow"}); err != nil {
		t.Fatal(err)
	}
	if _, err := retriever.Search(context.Background(), "shipping", 3); err != nil {
		t.Fatal(err)
	}

	count, err := retriever.index.countModel(context.Background(), "test-embedding")
	if err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("indexed rows after invalid replacement = %d, want 0", count)
	}
}

func TestHybridRetrieverConcurrentWriteAndSearch(t *testing.T) {
	workDir := t.TempDir()
	store := NewStore(workDir)
	writeTestNote(t, store, "shared", "Shared", "version zero")
	embedder := &fakeEmbedder{vectors: map[string][]float32{
		"shared":               {1, 0},
		"Shared\nversion zero": {1, 0},
		"Shared\nversion one":  {0.9, 0.1},
		"Shared\nversion two":  {0.8, 0.2},
	}}
	retriever, err := NewHybridRetriever(store, filepath.Join(workDir, ".mini-opencode", "memory-index.db"), embedder, RetrievalConfig{
		EmbeddingModel: "test-embedding",
		CandidateK:     10,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = retriever.Close() })

	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		for i := 0; i < 50; i++ {
			body := "version one"
			if i%2 == 0 {
				body = "version two"
			}
			if _, err := store.Write(Note{Name: "shared", Title: "Shared", Body: body}); err != nil {
				errs <- err
				return
			}
		}
	}()
	go func() {
		defer wg.Done()
		<-start
		for i := 0; i < 50; i++ {
			if _, err := retriever.Search(context.Background(), "shared", 3); err != nil {
				errs <- err
				return
			}
		}
	}()
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent operation error = %v", err)
	}
}

func TestHybridRetrieverSkipsInvalidVectorsAndHonorsBudget(t *testing.T) {
	workDir := t.TempDir()
	store := NewStore(workDir)
	writeTestNote(t, store, "zero", "Zero", "zero vector")
	writeTestNote(t, store, "wrong", "Wrong", "wrong dimensions")
	short := writeTestNote(t, store, "short", "Short", "small")
	writeTestNote(t, store, "long", "Long", "this body is intentionally much longer than the configured retrieval budget")

	embedder := &fakeEmbedder{vectors: map[string][]float32{
		"meaning":                 {1, 0},
		"Zero\nzero vector":       {0, 0},
		"Wrong\nwrong dimensions": {1, 0, 0},
		testNoteText(short):       {1, 0},
		"Long\nthis body is intentionally much longer than the configured retrieval budget": {0.9, 0.1},
	}}
	retriever, err := NewHybridRetriever(store, filepath.Join(workDir, ".mini-opencode", "memory-index.db"), embedder, RetrievalConfig{
		EmbeddingModel: "test-embedding",
		CandidateK:     10,
		MinSimilarity:  0.1,
		MaxTokens:      4,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = retriever.Close() })

	hits, err := retriever.Search(context.Background(), "meaning", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].Name != "short" {
		t.Fatalf("hits = %#v, want only budget-fitting valid vector", hits)
	}
}
