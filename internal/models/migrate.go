package models

import (
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"strconv"
	"strings"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Migrate applies every pending migration file in migrations/, in filename
// order, exactly once. Applied versions are recorded in schema_migrations.
func Migrate(db *sql.DB) error {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		version    INTEGER PRIMARY KEY,
		name       TEXT NOT NULL,
		applied_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	entries, err := fs.ReadDir(migrationsFS, "migrations") // fs.ReadDir sorts by name
	if err != nil {
		return fmt.Errorf("list migrations: %w", err)
	}

	for _, entry := range entries {
		name := entry.Name()

		// File format: NNNN_description.sql
		verStr, _, ok := strings.Cut(name, "_")
		if !ok || !strings.HasSuffix(name, ".sql") {
			return fmt.Errorf("migration %q: want NNNN_name.sql", name)
		}
		version, err := strconv.Atoi(verStr)
		if err != nil {
			return fmt.Errorf("migration %q: non-numeric version: %w", name, err)
		}

		var n int
		if err := db.QueryRow(
			`SELECT COUNT(*) FROM schema_migrations WHERE version = ?`, version,
		).Scan(&n); err != nil {
			return fmt.Errorf("check migration %q: %w", name, err)
		}
		if n > 0 {
			continue // already applied
		}

		body, err := migrationsFS.ReadFile("migrations/" + name)
		if err != nil {
			return fmt.Errorf("read migration %q: %w", name, err)
		}

		// Each migration runs inside a transaction so a partial failure rolls
		// back instead of leaving the schema half-applied.
		tx, err := db.Begin()
		if err != nil {
			return fmt.Errorf("begin migration %q: %w", name, err)
		}
		if _, err := tx.Exec(string(body)); err != nil {
			tx.Rollback()
			return fmt.Errorf("apply migration %q: %w", name, err)
		}
		if _, err := tx.Exec(
			`INSERT INTO schema_migrations (version, name) VALUES (?, ?)`,
			version, name,
		); err != nil {
			tx.Rollback()
			return fmt.Errorf("record migration %q: %w", name, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %q: %w", name, err)
		}
	}

	return nil
}
