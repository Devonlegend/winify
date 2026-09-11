package assistant

import (
	"context"
	"strings"
	"testing"
)

func TestLoadDocsCoversCuratedSources(t *testing.T) {
	docs, err := LoadDocs()
	if err != nil {
		t.Fatalf("LoadDocs: %v", err)
	}
	if len(docs) < 8 {
		t.Fatalf("got %d chunks, want a reasonable set", len(docs))
	}

	sources := map[string]bool{}
	for _, d := range docs {
		if d.ID == "" || d.Title == "" || d.Text == "" {
			t.Fatalf("incomplete doc: %+v", d)
		}
		sources[d.Source] = true
	}
	for _, want := range []string{"setup-guide.md", "adding-project-server.md", "troubleshooting.md", "faq.md"} {
		if !sources[want] {
			t.Errorf("missing curated source %q", want)
		}
	}
}

func TestMemoryStoreFindsRollbackDoc(t *testing.T) {
	docs, err := LoadDocs()
	if err != nil {
		t.Fatalf("LoadDocs: %v", err)
	}
	store := NewMemoryStore()
	if err := store.Upsert(context.Background(), docs); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	got, err := store.Query(context.Background(), "how do I roll back a deployment?", 3)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(got) == 0 {
		t.Fatal("no docs retrieved for a rollback question")
	}
	top := strings.ToLower(got[0].Title + " " + got[0].Text)
	if !strings.Contains(top, "roll") {
		t.Fatalf("top doc is not about rollback: %q", got[0].Title)
	}
}

func TestMemoryStoreUpsertIsIdempotent(t *testing.T) {
	docs, _ := LoadDocs()
	store := NewMemoryStore()
	ctx := context.Background()
	if err := store.Upsert(ctx, docs); err != nil {
		t.Fatal(err)
	}
	first, _ := store.Query(ctx, "docker health check", 5)
	if err := store.Upsert(ctx, docs); err != nil {
		t.Fatal(err)
	}
	second, _ := store.Query(ctx, "docker health check", 5)
	if len(first) != len(second) {
		t.Fatalf("re-ingesting changed result count: %d -> %d", len(first), len(second))
	}
}
