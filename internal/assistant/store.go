package assistant

import (
	"context"
	"math"
	"sort"
	"strings"
	"sync"
	"unicode"
)

// VectorStore is the retrieval backend. Chroma (chroma.go) is the production
// implementation; MemoryStore is a dependency-free fallback used when Chroma is
// not configured or not reachable.
type VectorStore interface {
	// Upsert inserts or replaces documents by ID.
	Upsert(ctx context.Context, docs []Doc) error
	// Query returns the k most relevant documents for text.
	Query(ctx context.Context, text string, k int) ([]Doc, error)
	// Name identifies the backend for logging and citations.
	Name() string
}

// MemoryStore scores documents by token overlap. It is intentionally simple:
// it keeps the assistant usable for a handful of docs without a running
// ChromaDB, and it is what the tests exercise.
type MemoryStore struct {
	mu   sync.RWMutex
	docs []Doc
}

// NewMemoryStore returns an empty in-process store.
func NewMemoryStore() *MemoryStore { return &MemoryStore{} }

func (m *MemoryStore) Name() string { return "memory" }

// Upsert replaces documents with matching IDs and appends the rest.
func (m *MemoryStore) Upsert(_ context.Context, docs []Doc) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	index := make(map[string]int, len(m.docs))
	for i, d := range m.docs {
		index[d.ID] = i
	}
	for _, d := range docs {
		if i, ok := index[d.ID]; ok {
			m.docs[i] = d
			continue
		}
		index[d.ID] = len(m.docs)
		m.docs = append(m.docs, d)
	}
	return nil
}

// Query ranks documents by how many query terms appear in the title/text.
func (m *MemoryStore) Query(_ context.Context, text string, k int) ([]Doc, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	terms := tokenize(text)
	if len(terms) == 0 || len(m.docs) == 0 {
		return nil, nil
	}

	scored := make([]Doc, 0, len(m.docs))
	for _, d := range m.docs {
		haystack := strings.ToLower(d.Title + "\n" + d.Text)
		hits := 0
		for term := range terms {
			if strings.Contains(haystack, term) {
				hits++
			}
		}
		if hits == 0 {
			continue
		}
		// Normalize by document length so long sections do not always win.
		d.Score = float64(hits) / math.Sqrt(float64(len(haystack)))
		scored = append(scored, d)
	}

	sort.SliceStable(scored, func(i, j int) bool { return scored[i].Score > scored[j].Score })
	if k > 0 && len(scored) > k {
		scored = scored[:k]
	}
	return scored, nil
}

// tokenize lowercases text and keeps words of three or more characters.
func tokenize(text string) map[string]struct{} {
	fields := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	out := make(map[string]struct{}, len(fields))
	for _, f := range fields {
		if len(f) >= 3 {
			out[f] = struct{}{}
		}
	}
	return out
}
