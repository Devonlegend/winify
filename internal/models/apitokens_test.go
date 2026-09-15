package models

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestAPITokenRoundTrip(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := Migrate(db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	store := NewStore(db)
	ctx := context.Background()

	id, err := store.CreateAPIToken(ctx, "ci", "hash-1", "read")
	if err != nil {
		t.Fatalf("CreateAPIToken: %v", err)
	}

	got, err := store.APITokenByHash(ctx, "hash-1")
	if err != nil {
		t.Fatalf("APITokenByHash: %v", err)
	}
	if got.Name != "ci" || got.Scope != "read" || got.ID != id {
		t.Fatalf("token = %+v", got)
	}

	if err := store.TouchAPIToken(ctx, id, time.Now()); err != nil {
		t.Fatalf("TouchAPIToken: %v", err)
	}
	got, _ = store.APITokenByHash(ctx, "hash-1")
	if got.LastUsedAt.IsZero() {
		t.Fatal("LastUsedAt was not recorded")
	}

	list, err := store.ListAPITokens(ctx)
	if err != nil || len(list) != 1 {
		t.Fatalf("ListAPITokens = %v, %v", list, err)
	}

	if err := store.DeleteAPIToken(ctx, id); err != nil {
		t.Fatalf("DeleteAPIToken: %v", err)
	}
	if _, err := store.APITokenByHash(ctx, "hash-1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("after delete err = %v, want ErrNotFound", err)
	}
}
