package models

import (
	"database/sql"
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
