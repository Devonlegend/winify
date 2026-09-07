// Package models owns the SQLite persistence layer: the *sql.DB handle and
// the schema. Data model structs and repositories join here in later phases.
// Credentials are stored only as encrypted refs; no plaintext secrets are
// ever written through this layer.
package models

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite" // pure-Go SQLite driver: no CGO toolchain needed on Windows
)

// Open connects to the SQLite database at path, creating the file on first
// use. It applies connection pragmas that matter for correctness.
func Open(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite %s: %w", path, err)
	}

	// SQLite allows one writer at a time. A single connection serializes all
	// access and avoids "database is locked" errors for a single-process v1.
	db.SetMaxOpenConns(1)

	for _, pragma := range []string{
		"PRAGMA journal_mode=WAL;",  // WAL lets readers run during writes
		"PRAGMA busy_timeout=5000;", // wait up to 5s for a contended lock
		"PRAGMA foreign_keys=ON;",
	} {
		if _, err := db.Exec(pragma); err != nil {
			db.Close()
			return nil, fmt.Errorf("apply pragma %q: %w", pragma, err)
		}
	}

	return db, nil
}
