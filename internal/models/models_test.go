package models

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestMigrateAppliesAndIsIdempotent(t *testing.T) {
	db := openTestDB(t)

	if err := Migrate(db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	var applied int
	if err := db.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&applied); err != nil {
		t.Fatalf("count schema_migrations: %v", err)
	}
	if applied < 1 {
		t.Fatalf("applied migrations = %d, want >= 1", applied)
	}

	// Migrate is a no-op when called again: versions are recorded already.
	if err := Migrate(db); err != nil {
		t.Fatalf("second Migrate: %v", err)
	}

	// The migration content actually ran: app_meta must exist.
	var n int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='app_meta'`,
	).Scan(&n); err != nil {
		t.Fatalf("check app_meta: %v", err)
	}
	if n != 1 {
		t.Fatalf("app_meta table count = %d, want 1", n)
	}
}

func TestMigrateOpenAllowsRoundTrip(t *testing.T) {
	db := openTestDB(t)
	if err := Migrate(db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO app_meta (key, value) VALUES ('installed_at', datetime('now'))`); err != nil {
		t.Fatalf("insert app_meta: %v", err)
	}
	var v string
	if err := db.QueryRow(`SELECT value FROM app_meta WHERE key = 'installed_at'`).Scan(&v); err != nil {
		t.Fatalf("read app_meta: %v", err)
	}
	if v == "" {
		t.Fatal("read empty value from app_meta")
	}
}

func TestSharedVariablesForScopes(t *testing.T) {
	db := openTestDB(t)
	if err := Migrate(db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	store := NewStore(db)
	ctx := context.Background()

	upsert := func(v SharedVariable) {
		t.Helper()
		if err := store.UpsertSharedVariable(ctx, v); err != nil {
			t.Fatalf("UpsertSharedVariable: %v", err)
		}
	}
	upsert(SharedVariable{Scope: "project", ScopeID: "Default", Key: "A", Value: "project-a"})
	upsert(SharedVariable{Scope: "environment", ScopeID: "Default/production", Key: "A", Value: "env-a"})
	upsert(SharedVariable{Scope: "project", ScopeID: "Other", Key: "B", Value: "other-b"})

	got, err := store.SharedVariablesFor(ctx, "Default", "production")
	if err != nil {
		t.Fatalf("SharedVariablesFor: %v", err)
	}
	if got["project.A"] != "project-a" || got["environment.A"] != "env-a" {
		t.Fatalf("got = %v", got)
	}
	if _, ok := got["project.B"]; ok {
		t.Fatalf("leaked another group's variable: %v", got)
	}
}

func TestCreateFirstUserOnlyOnce(t *testing.T) {
	db := openTestDB(t)
	if err := Migrate(db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	store := NewStore(db)
	ctx := context.Background()

	if n, err := store.CountUsers(ctx); err != nil || n != 0 {
		t.Fatalf("CountUsers = %d, %v; want 0", n, err)
	}
	user, err := store.CreateFirstUser(ctx, "admin", "hash-one")
	if err != nil {
		t.Fatalf("CreateFirstUser: %v", err)
	}
	if user.Username != "admin" || user.ID == 0 {
		t.Fatalf("user = %+v", user)
	}

	// A second attempt must not create or overwrite anything.
	if _, err := store.CreateFirstUser(ctx, "intruder", "hash-two"); !errors.Is(err, ErrAlreadyInitialized) {
		t.Fatalf("second CreateFirstUser err = %v, want ErrAlreadyInitialized", err)
	}
	if n, _ := store.CountUsers(ctx); n != 1 {
		t.Fatalf("CountUsers = %d, want 1", n)
	}
	got, err := store.UserByUsername(ctx, "admin")
	if err != nil {
		t.Fatalf("UserByUsername: %v", err)
	}
	if got.PasswordHash != "hash-one" {
		t.Fatalf("existing admin password was overwritten: %q", got.PasswordHash)
	}
	if _, err := store.UserByUsername(ctx, "intruder"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("intruder was created: %v", err)
	}
}
