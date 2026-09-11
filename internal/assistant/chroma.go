package assistant

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Chroma is a VectorStore backed by ChromaDB's HTTP API (v2, used by Chroma
// 1.x). Documents are embedded server-side by Chroma's default embedding
// function, so the control-center only sends text.
type Chroma struct {
	baseURL    string
	tenant     string
	database   string
	collection string
	client     *http.Client

	mu     sync.Mutex
	collID string
}

// NewChroma builds a client for the given server URL and collection name.
func NewChroma(baseURL, collection string) *Chroma {
	if collection == "" {
		collection = "control-center-docs"
	}
	return &Chroma{
		baseURL:    strings.TrimRight(baseURL, "/"),
		tenant:     "default_tenant",
		database:   "default_database",
		collection: collection,
		client:     &http.Client{Timeout: 30 * time.Second},
	}
}

func (c *Chroma) Name() string { return "chroma" }

// Ping checks that the server is reachable, so the caller can fall back to the
// in-memory store when it is not.
func (c *Chroma) Ping(ctx context.Context) error {
	resp, err := c.do(ctx, http.MethodGet, "/api/v2/heartbeat", nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("chroma heartbeat: status %d", resp.StatusCode)
	}
	return nil
}

// Upsert embeds and stores documents, creating the collection if needed.
func (c *Chroma) Upsert(ctx context.Context, docs []Doc) error {
	if len(docs) == 0 {
		return nil
	}
	id, err := c.ensureCollection(ctx)
	if err != nil {
		return err
	}

	ids := make([]string, len(docs))
	texts := make([]string, len(docs))
	metadatas := make([]map[string]string, len(docs))
	for i, d := range docs {
		ids[i] = d.ID
		texts[i] = d.Text
		metadatas[i] = map[string]string{"title": d.Title, "source": d.Source}
	}
	body := map[string]any{"ids": ids, "documents": texts, "metadatas": metadatas}

	resp, err := c.do(ctx, http.MethodPost, c.collectionPath(id)+"/upsert", body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return c.statusError("upsert", resp)
	}
	return nil
}

// Query retrieves the k nearest chunks for text.
func (c *Chroma) Query(ctx context.Context, text string, k int) ([]Doc, error) {
	if k <= 0 {
		k = 4
	}
	id, err := c.ensureCollection(ctx)
	if err != nil {
		return nil, err
	}
	body := map[string]any{
		"query_texts": []string{text},
		"n_results":   k,
		"include":     []string{"documents", "metadatas", "distances"},
	}
	resp, err := c.do(ctx, http.MethodPost, c.collectionPath(id)+"/query", body)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return nil, c.statusError("query", resp)
	}

	var out struct {
		Documents [][]string            `json:"documents"`
		Metadatas [][]map[string]string `json:"metadatas"`
		Distances [][]float64           `json:"distances"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode chroma query: %w", err)
	}
	if len(out.Documents) == 0 {
		return nil, nil
	}

	docs := make([]Doc, 0, len(out.Documents[0]))
	for i, text := range out.Documents[0] {
		doc := Doc{Text: text}
		if len(out.Metadatas) > 0 && i < len(out.Metadatas[0]) {
			doc.Title = out.Metadatas[0][i]["title"]
			doc.Source = out.Metadatas[0][i]["source"]
		}
		if len(out.Distances) > 0 && i < len(out.Distances[0]) {
			doc.Score = out.Distances[0][i]
		}
		docs = append(docs, doc)
	}
	return docs, nil
}

// ensureCollection returns the collection id, creating it on first use.
func (c *Chroma) ensureCollection(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.collID != "" {
		return c.collID, nil
	}

	path := fmt.Sprintf("/api/v2/tenants/%s/databases/%s/collections", c.tenant, c.database)
	body := map[string]any{"name": c.collection, "get_or_create": true}
	resp, err := c.do(ctx, http.MethodPost, path, body)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return "", c.statusError("get_or_create collection", resp)
	}

	var out struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("decode chroma collection: %w", err)
	}
	if out.ID == "" {
		return "", fmt.Errorf("chroma returned an empty collection id")
	}
	c.collID = out.ID
	return c.collID, nil
}

func (c *Chroma) collectionPath(id string) string {
	return fmt.Sprintf("/api/v2/tenants/%s/databases/%s/collections/%s", c.tenant, c.database, id)
}

func (c *Chroma) do(ctx context.Context, method, path string, body any) (*http.Response, error) {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal chroma request: %w", err)
		}
		reader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return c.client.Do(req)
}

func (c *Chroma) statusError(op string, resp *http.Response) error {
	msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	return fmt.Errorf("chroma %s: status %d: %s", op, resp.StatusCode, strings.TrimSpace(string(msg)))
}
