package memory

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"

	_ "modernc.org/sqlite"
)

const rrfK = 60

// Embedder converts a batch of texts into vectors in the same order.
type Embedder interface {
	Embed(ctx context.Context, texts []string) ([][]float32, error)
}

// Retriever is the query surface shared by prompt recall, the memory tool and
// the /memory command.
type Retriever interface {
	Search(ctx context.Context, query string, limit int) ([]Note, error)
}

// RetrievalConfig controls hybrid lexical/vector retrieval. Zero values keep
// the feature small and permissive; application config supplies user-facing
// defaults.
type RetrievalConfig struct {
	EmbeddingModel string
	CandidateK     int
	MaxTokens      int
	MinSimilarity  float64
}

// LexicalRetriever adapts Store.Search to the context-aware Retriever API.
type LexicalRetriever struct{ Store *Store }

func (r LexicalRetriever) Search(_ context.Context, query string, limit int) ([]Note, error) {
	if r.Store == nil {
		return nil, nil
	}
	return r.Store.Search(query, limit)
}

// HybridRetriever maintains a rebuildable SQLite vector cache and combines
// vector and lexical ranks with reciprocal-rank fusion.
type HybridRetriever struct {
	store    *Store
	embedder Embedder
	config   RetrievalConfig
	index    *embeddingIndex
	mu       sync.Mutex
}

// NewHybridRetriever opens the vector cache. Markdown notes remain the source
// of truth; deleting this database only causes embeddings to be regenerated.
func NewHybridRetriever(store *Store, indexPath string, embedder Embedder, config RetrievalConfig) (*HybridRetriever, error) {
	if store == nil {
		return nil, errors.New("memory: nil store")
	}
	if config.CandidateK <= 0 {
		config.CandidateK = 12
	}
	if config.MinSimilarity < 0 {
		config.MinSimilarity = 0
	}
	index, err := openEmbeddingIndex(indexPath)
	if err != nil {
		return nil, err
	}
	return &HybridRetriever{store: store, embedder: embedder, config: config, index: index}, nil
}

func (r *HybridRetriever) Close() error {
	if r == nil || r.index == nil {
		return nil
	}
	return r.index.close()
}

func (r *HybridRetriever) Search(ctx context.Context, query string, limit int) ([]Note, error) {
	if r == nil || r.store == nil {
		return nil, nil
	}
	if limit <= 0 {
		limit = 5
	}
	query = strings.TrimSpace(query)
	if query == "" || r.embedder == nil || strings.TrimSpace(r.config.EmbeddingModel) == "" {
		return r.store.Search(query, limit)
	}

	// Index synchronization and SQLite access are serialized. Embed calls are
	// also kept inside this critical section so two concurrent first searches
	// do not pay for the same note batch twice.
	r.mu.Lock()
	defer r.mu.Unlock()

	notes, err := r.store.List()
	if err != nil {
		return nil, err
	}
	lexical := Score(notes, query, r.config.CandidateK)
	if err := r.syncIndex(ctx, notes); err != nil {
		return applyTokenBudget(lexical, limit, r.config.MaxTokens), nil
	}
	queryVectors, err := r.embedder.Embed(ctx, []string{query})
	if err != nil || len(queryVectors) != 1 || len(queryVectors[0]) == 0 {
		return applyTokenBudget(lexical, limit, r.config.MaxTokens), nil
	}

	rows, err := r.index.list(ctx, r.config.EmbeddingModel)
	if err != nil {
		return applyTokenBudget(lexical, limit, r.config.MaxTokens), nil
	}
	byName := make(map[string]Note, len(notes))
	order := make(map[string]int, len(notes))
	for i, note := range notes {
		byName[note.Name] = note
		order[note.Name] = i
	}
	type semanticHit struct {
		note       Note
		similarity float64
	}
	semantic := make([]semanticHit, 0, len(rows))
	for _, row := range rows {
		note, ok := byName[row.name]
		if !ok || len(row.vector) != len(queryVectors[0]) {
			continue
		}
		similarity, ok := cosine(queryVectors[0], row.vector)
		if !ok || similarity < r.config.MinSimilarity {
			continue
		}
		semantic = append(semantic, semanticHit{note: note, similarity: similarity})
	}
	sort.SliceStable(semantic, func(i, j int) bool {
		if semantic[i].similarity == semantic[j].similarity {
			return order[semantic[i].note.Name] < order[semantic[j].note.Name]
		}
		return semantic[i].similarity > semantic[j].similarity
	})
	if len(semantic) > r.config.CandidateK {
		semantic = semantic[:r.config.CandidateK]
	}

	type fused struct {
		note  Note
		score float64
		order int
	}
	fusedByName := make(map[string]*fused, len(lexical)+len(semantic))
	add := func(note Note, rank int) {
		item := fusedByName[note.Name]
		if item == nil {
			item = &fused{note: note, order: order[note.Name]}
			fusedByName[note.Name] = item
		}
		item.score += 1 / float64(rrfK+rank)
	}
	for i, note := range lexical {
		add(note, i+1)
	}
	for i, hit := range semantic {
		add(hit.note, i+1)
	}
	fusedRows := make([]fused, 0, len(fusedByName))
	for _, item := range fusedByName {
		fusedRows = append(fusedRows, *item)
	}
	sort.SliceStable(fusedRows, func(i, j int) bool {
		if fusedRows[i].score == fusedRows[j].score {
			return fusedRows[i].order < fusedRows[j].order
		}
		return fusedRows[i].score > fusedRows[j].score
	})
	ranked := make([]Note, 0, len(fusedRows))
	for _, item := range fusedRows {
		ranked = append(ranked, item.note)
	}
	return applyTokenBudget(ranked, limit, r.config.MaxTokens), nil
}

func (r *HybridRetriever) syncIndex(ctx context.Context, notes []Note) error {
	existing, err := r.index.hashes(ctx, r.config.EmbeddingModel)
	if err != nil {
		return err
	}
	live := make(map[string]struct{}, len(notes))
	var changed []Note
	var texts []string
	for _, note := range notes {
		live[note.Name] = struct{}{}
		text := noteEmbeddingText(note)
		hash := fmt.Sprintf("%x", sha256.Sum256([]byte(text)))
		if existing[note.Name] == hash {
			continue
		}
		changed = append(changed, note)
		texts = append(texts, text)
	}
	if len(changed) > 0 {
		vectors, err := r.embedder.Embed(ctx, texts)
		if err != nil {
			return err
		}
		if len(vectors) != len(changed) {
			return fmt.Errorf("memory: embedder returned %d vectors for %d notes", len(vectors), len(changed))
		}
		for i, note := range changed {
			if len(vectors[i]) == 0 {
				if err := r.index.delete(ctx, note.Name, r.config.EmbeddingModel); err != nil {
					return err
				}
				continue
			}
			hash := fmt.Sprintf("%x", sha256.Sum256([]byte(texts[i])))
			if err := r.index.upsert(ctx, note.Name, hash, r.config.EmbeddingModel, vectors[i]); err != nil {
				return err
			}
		}
	}
	return r.index.deleteStale(ctx, r.config.EmbeddingModel, live)
}

func noteEmbeddingText(note Note) string {
	parts := []string{strings.TrimSpace(note.Title)}
	if len(note.Tags) > 0 {
		parts = append(parts, strings.Join(note.Tags, " "))
	}
	parts = append(parts, strings.TrimSpace(note.Body))
	return strings.Join(parts, "\n")
}

func cosine(a, b []float32) (float64, bool) {
	if len(a) == 0 || len(a) != len(b) {
		return 0, false
	}
	var dot, normA, normB float64
	for i := range a {
		x, y := float64(a[i]), float64(b[i])
		dot += x * y
		normA += x * x
		normB += y * y
	}
	if normA == 0 || normB == 0 {
		return 0, false
	}
	return dot / (math.Sqrt(normA) * math.Sqrt(normB)), true
}

func applyTokenBudget(notes []Note, limit, maxTokens int) []Note {
	if limit <= 0 {
		limit = 5
	}
	out := make([]Note, 0, min(limit, len(notes)))
	used := 0
	for _, note := range notes {
		cost := approximateNoteTokens(note)
		if maxTokens > 0 && used+cost > maxTokens {
			continue
		}
		out = append(out, note)
		used += cost
		if len(out) == limit {
			break
		}
	}
	return out
}

func approximateNoteTokens(note Note) int {
	chars := utf8.RuneCountInString(noteEmbeddingText(note))
	if chars == 0 {
		return 0
	}
	return max(1, (chars+3)/4)
}

type embeddingIndex struct{ db *sql.DB }

type embeddingRow struct {
	name   string
	vector []float32
}

func openEmbeddingIndex(path string) (*embeddingIndex, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("memory: index path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, fmt.Errorf("memory: create index dir: %w", err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("memory: open index: %w", err)
	}
	db.SetMaxOpenConns(1)
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS memory_embeddings (
		note_name TEXT NOT NULL,
		content_hash TEXT NOT NULL,
		embedding_model TEXT NOT NULL,
		dimensions INTEGER NOT NULL,
		embedding BLOB NOT NULL,
		PRIMARY KEY (note_name, embedding_model)
	)`)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("memory: initialize index: %w", err)
	}
	return &embeddingIndex{db: db}, nil
}

func (i *embeddingIndex) close() error { return i.db.Close() }

func (i *embeddingIndex) hashes(ctx context.Context, model string) (map[string]string, error) {
	rows, err := i.db.QueryContext(ctx, `SELECT note_name, content_hash FROM memory_embeddings WHERE embedding_model = ?`, model)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var name, hash string
		if err := rows.Scan(&name, &hash); err != nil {
			return nil, err
		}
		out[name] = hash
	}
	return out, rows.Err()
}

func (i *embeddingIndex) upsert(ctx context.Context, name, hash, model string, vector []float32) error {
	_, err := i.db.ExecContext(ctx, `INSERT INTO memory_embeddings
		(note_name, content_hash, embedding_model, dimensions, embedding)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(note_name, embedding_model) DO UPDATE SET
		content_hash = excluded.content_hash,
		dimensions = excluded.dimensions,
		embedding = excluded.embedding`, name, hash, model, len(vector), encodeVector(vector))
	return err
}

func (i *embeddingIndex) delete(ctx context.Context, name, model string) error {
	_, err := i.db.ExecContext(ctx, `DELETE FROM memory_embeddings WHERE note_name = ? AND embedding_model = ?`, name, model)
	return err
}

func (i *embeddingIndex) deleteStale(ctx context.Context, model string, live map[string]struct{}) error {
	rows, err := i.db.QueryContext(ctx, `SELECT note_name FROM memory_embeddings WHERE embedding_model = ?`, model)
	if err != nil {
		return err
	}
	var stale []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return err
		}
		if _, ok := live[name]; !ok {
			stale = append(stale, name)
		}
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, name := range stale {
		if err := i.delete(ctx, name, model); err != nil {
			return err
		}
	}
	return nil
}

func (i *embeddingIndex) list(ctx context.Context, model string) ([]embeddingRow, error) {
	rows, err := i.db.QueryContext(ctx, `SELECT note_name, dimensions, embedding FROM memory_embeddings WHERE embedding_model = ?`, model)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []embeddingRow
	for rows.Next() {
		var name string
		var dimensions int
		var data []byte
		if err := rows.Scan(&name, &dimensions, &data); err != nil {
			return nil, err
		}
		vector, ok := decodeVector(data, dimensions)
		if ok {
			out = append(out, embeddingRow{name: name, vector: vector})
		}
	}
	return out, rows.Err()
}

func (i *embeddingIndex) countModel(ctx context.Context, model string) (int, error) {
	var count int
	err := i.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM memory_embeddings WHERE embedding_model = ?`, model).Scan(&count)
	return count, err
}

func encodeVector(vector []float32) []byte {
	data := make([]byte, len(vector)*4)
	for i, value := range vector {
		binary.LittleEndian.PutUint32(data[i*4:], math.Float32bits(value))
	}
	return data
}

func decodeVector(data []byte, dimensions int) ([]float32, bool) {
	if dimensions <= 0 || len(data) != dimensions*4 {
		return nil, false
	}
	vector := make([]float32, dimensions)
	for i := range vector {
		vector[i] = math.Float32frombits(binary.LittleEndian.Uint32(data[i*4:]))
	}
	return vector, true
}
