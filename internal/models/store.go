package models

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/Devonlegend/winify/internal/config"
)

// ErrNotFound is returned when a lookup matches no row. Callers compare with
// errors.Is instead of checking sql.ErrNoRows, so the SQL detail stays here.
var ErrNotFound = errors.New("not found")

// Store is the typed data-access layer over *sql.DB. Handlers and services use
// it instead of writing SQL inline.
type Store struct {
	db *sql.DB
}

// NewStore wraps an open database handle.
func NewStore(db *sql.DB) *Store { return &Store{db: db} }

// DB exposes the raw handle for migrations and diagnostics.
func (s *Store) DB() *sql.DB { return s.db }

// User is an admin account row. PasswordHash is bcrypt; plaintext is never
// stored anywhere.
type User struct {
	ID           int64
	Username     string
	PasswordHash string
}

// UpsertUser creates the user or replaces its password hash.
func (s *Store) UpsertUser(ctx context.Context, username, passwordHash string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO users (username, password_hash) VALUES (?, ?)
		ON CONFLICT(username) DO UPDATE SET password_hash = excluded.password_hash`,
		username, passwordHash)
	if err != nil {
		return fmt.Errorf("upsert user: %w", err)
	}
	return nil
}

// UserByUsername loads one account.
func (s *Store) UserByUsername(ctx context.Context, username string) (User, error) {
	var u User
	err := s.db.QueryRowContext(ctx,
		`SELECT id, username, password_hash FROM users WHERE username = ?`, username,
	).Scan(&u.ID, &u.Username, &u.PasswordHash)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, fmt.Errorf("get user: %w", err)
	}
	return u, nil
}

// CreateSession stores a session row. tokenHash is a SHA-256 of the raw token
// the browser holds, so a database leak does not expose usable cookies.
func (s *Store) CreateSession(ctx context.Context, tokenHash string, userID int64, expires time.Time) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO sessions (id, user_id, created_at, expires_at) VALUES (?, ?, ?, ?)`,
		tokenHash, userID, time.Now().Unix(), expires.Unix())
	if err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	return nil
}

// UsernameBySession returns the username for a live session, or ErrNotFound if
// the session is unknown or expired.
func (s *Store) UsernameBySession(ctx context.Context, tokenHash string, now time.Time) (string, error) {
	var username string
	err := s.db.QueryRowContext(ctx, `
		SELECT u.username
		FROM sessions s JOIN users u ON u.id = s.user_id
		WHERE s.id = ? AND s.expires_at > ?`,
		tokenHash, now.Unix(),
	).Scan(&username)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("lookup session: %w", err)
	}
	return username, nil
}

// DeleteSession removes one session (logout).
func (s *Store) DeleteSession(ctx context.Context, tokenHash string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE id = ?`, tokenHash); err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	return nil
}

// DeleteExpiredSessions prunes stale rows; called at startup.
func (s *Store) DeleteExpiredSessions(ctx context.Context, now time.Time) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at <= ?`, now.Unix()); err != nil {
		return fmt.Errorf("prune sessions: %w", err)
	}
	return nil
}

// UpsertServer syncs one entry from servers.yaml into the database.
func (s *Store) UpsertServer(ctx context.Context, srv config.Server) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO servers (id, name, type, winrm_endpoint, credential_ref, ssh_host, ssh_user, ssh_key_ref)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			name = excluded.name,
			type = excluded.type,
			winrm_endpoint = excluded.winrm_endpoint,
			credential_ref = excluded.credential_ref,
			ssh_host = excluded.ssh_host,
			ssh_user = excluded.ssh_user,
			ssh_key_ref = excluded.ssh_key_ref,
			updated_at = CURRENT_TIMESTAMP`,
		srv.ID, srv.Name, srv.Type, srv.WinRMEndpoint, srv.CredentialRef,
		srv.SSHHost, srv.SSHUser, srv.SSHKeyRef)
	if err != nil {
		return fmt.Errorf("upsert server %q: %w", srv.ID, err)
	}
	return nil
}

// ListServers returns all configured targets.
func (s *Store) ListServers(ctx context.Context) ([]config.Server, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, type, winrm_endpoint, credential_ref, ssh_host, ssh_user, ssh_key_ref
		FROM servers ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("list servers: %w", err)
	}
	defer rows.Close()

	var out []config.Server
	for rows.Next() {
		var srv config.Server
		if err := rows.Scan(&srv.ID, &srv.Name, &srv.Type, &srv.WinRMEndpoint,
			&srv.CredentialRef, &srv.SSHHost, &srv.SSHUser, &srv.SSHKeyRef); err != nil {
			return nil, fmt.Errorf("scan server: %w", err)
		}
		out = append(out, srv)
	}
	return out, rows.Err()
}

// UpsertProject syncs one entry from projects.yaml into the database.
func (s *Store) UpsertProject(ctx context.Context, p config.Project) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO projects (id, name, server_id, strategy, repo_url, dockerfile_path, iis_site)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			name = excluded.name,
			server_id = excluded.server_id,
			strategy = excluded.strategy,
			repo_url = excluded.repo_url,
			dockerfile_path = excluded.dockerfile_path,
			iis_site = excluded.iis_site,
			updated_at = CURRENT_TIMESTAMP`,
		p.ID, p.Name, p.ServerID, p.Strategy, p.RepoURL, p.DockerfilePath, p.IISSite)
	if err != nil {
		return fmt.Errorf("upsert project %q: %w", p.ID, err)
	}
	return nil
}

// ListProjects returns all configured projects.
func (s *Store) ListProjects(ctx context.Context) ([]config.Project, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, server_id, strategy, repo_url, dockerfile_path, iis_site
		FROM projects ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("list projects: %w", err)
	}
	defer rows.Close()

	var out []config.Project
	for rows.Next() {
		var p config.Project
		if err := rows.Scan(&p.ID, &p.Name, &p.ServerID, &p.Strategy, &p.RepoURL,
			&p.DockerfilePath, &p.IISSite); err != nil {
			return nil, fmt.Errorf("scan project: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// PutCredential stores an encrypted secret (nonce + ciphertext). The plaintext
// is never passed to this layer, so it cannot end up in a log or a query log.
func (s *Store) PutCredential(ctx context.Context, name string, nonce, ciphertext []byte) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO credentials (name, nonce, ciphertext) VALUES (?, ?, ?)
		ON CONFLICT(name) DO UPDATE SET
			nonce = excluded.nonce,
			ciphertext = excluded.ciphertext,
			updated_at = CURRENT_TIMESTAMP`,
		name, nonce, ciphertext)
	if err != nil {
		return fmt.Errorf("put credential %q: %w", name, err)
	}
	return nil
}

// Credential loads the encrypted blob for name. Callers decrypt in memory.
func (s *Store) Credential(ctx context.Context, name string) (nonce, ciphertext []byte, err error) {
	err = s.db.QueryRowContext(ctx,
		`SELECT nonce, ciphertext FROM credentials WHERE name = ?`, name,
	).Scan(&nonce, &ciphertext)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, ErrNotFound
	}
	if err != nil {
		return nil, nil, fmt.Errorf("get credential %q: %w", name, err)
	}
	return nonce, ciphertext, nil
}

// CredentialNames lists stored credential names (never their values).
func (s *Store) CredentialNames(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT name FROM credentials ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("list credentials: %w", err)
	}
	defer rows.Close()

	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, fmt.Errorf("scan credential name: %w", err)
		}
		names = append(names, n)
	}
	return names, rows.Err()
}
