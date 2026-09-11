package models

import (
	"context"
	"database/sql"
	"encoding/json"
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

// serverColumns is the shared SELECT list for servers.
const serverColumns = `id, name, type, host, winrm_endpoint, winrm_user, winrm_transport, winrm_insecure,
	credential_ref, ssh_host, ssh_port, ssh_user, ssh_key_ref`

// UpsertServer syncs one entry from servers.yaml into the database.
func (s *Store) UpsertServer(ctx context.Context, srv config.Server) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO servers (`+serverColumns+`)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			name = excluded.name,
			type = excluded.type,
			host = excluded.host,
			winrm_endpoint = excluded.winrm_endpoint,
			winrm_user = excluded.winrm_user,
			winrm_transport = excluded.winrm_transport,
			winrm_insecure = excluded.winrm_insecure,
			credential_ref = excluded.credential_ref,
			ssh_host = excluded.ssh_host,
			ssh_port = excluded.ssh_port,
			ssh_user = excluded.ssh_user,
			ssh_key_ref = excluded.ssh_key_ref,
			updated_at = CURRENT_TIMESTAMP`,
		srv.ID, srv.Name, srv.Type, srv.Host, srv.WinRMEndpoint, srv.WinRMUser,
		srv.WinRMTransport, boolToInt(srv.WinRMInsecure), srv.CredentialRef,
		srv.SSHHost, srv.SSHPort, srv.SSHUser, srv.SSHKeyRef)
	if err != nil {
		return fmt.Errorf("upsert server %q: %w", srv.ID, err)
	}
	return nil
}

// ListServers returns all configured targets.
func (s *Store) ListServers(ctx context.Context) ([]config.Server, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+serverColumns+` FROM servers ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("list servers: %w", err)
	}
	defer rows.Close()

	var out []config.Server
	for rows.Next() {
		srv, err := scanServer(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, srv)
	}
	return out, rows.Err()
}

// GetServer loads one target by id.
func (s *Store) GetServer(ctx context.Context, id string) (config.Server, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+serverColumns+` FROM servers WHERE id = ?`, id)
	srv, err := scanServer(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return config.Server{}, ErrNotFound
	}
	if err != nil {
		return config.Server{}, err
	}
	return srv, nil
}

func scanServer(scan func(dest ...any) error) (config.Server, error) {
	var (
		srv      config.Server
		insecure int
	)
	if err := scan(&srv.ID, &srv.Name, &srv.Type, &srv.Host, &srv.WinRMEndpoint,
		&srv.WinRMUser, &srv.WinRMTransport, &insecure, &srv.CredentialRef,
		&srv.SSHHost, &srv.SSHPort, &srv.SSHUser, &srv.SSHKeyRef); err != nil {
		return config.Server{}, fmt.Errorf("scan server: %w", err)
	}
	srv.WinRMInsecure = insecure != 0
	return srv, nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// UpsertProject syncs one entry from projects.yaml into the database.
func (s *Store) UpsertProject(ctx context.Context, p config.Project) error {
	envJSON, err := json.Marshal(p.Env)
	if err != nil {
		return fmt.Errorf("marshal project env: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO projects (`+projectColumns+`)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			name = excluded.name,
			server_id = excluded.server_id,
			strategy = excluded.strategy,
			repo_url = excluded.repo_url,
			dockerfile_path = excluded.dockerfile_path,
			iis_site = excluded.iis_site,
			iis_physical_path = excluded.iis_physical_path,
			iis_app_pool = excluded.iis_app_pool,
			iis_service = excluded.iis_service,
			iis_build_command = excluded.iis_build_command,
			iis_source_subdir = excluded.iis_source_subdir,
			branch = excluded.branch,
			domain = excluded.domain,
			port = excluded.port,
			health_path = excluded.health_path,
			webhook_secret_ref = excluded.webhook_secret_ref,
			env_json = excluded.env_json,
			updated_at = CURRENT_TIMESTAMP`,
		p.ID, p.Name, p.ServerID, p.Strategy, p.RepoURL, p.DockerfilePath, p.IISSite,
		p.IISPhysicalPath, p.IISAppPool, p.IISService, p.IISBuildCommand, p.IISSourceSubdir,
		p.Branch, p.Domain, p.Port, p.HealthPath, p.WebhookSecretRef, string(envJSON))
	if err != nil {
		return fmt.Errorf("upsert project %q: %w", p.ID, err)
	}
	return nil
}

// projectColumns is the shared SELECT list for projects.
const projectColumns = `id, name, server_id, strategy, repo_url, dockerfile_path, iis_site,
	iis_physical_path, iis_app_pool, iis_service, iis_build_command, iis_source_subdir,
	branch, domain, port, health_path, webhook_secret_ref, env_json`

// ListProjects returns all configured projects.
func (s *Store) ListProjects(ctx context.Context) ([]config.Project, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+projectColumns+` FROM projects ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("list projects: %w", err)
	}
	defer rows.Close()

	var out []config.Project
	for rows.Next() {
		p, err := scanProject(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// GetProject loads one project by id.
func (s *Store) GetProject(ctx context.Context, id string) (config.Project, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+projectColumns+` FROM projects WHERE id = ?`, id)
	p, err := scanProject(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return config.Project{}, ErrNotFound
	}
	if err != nil {
		return config.Project{}, err
	}
	return p, nil
}

// scanProject reads the common project column list. It takes a Scan func so it
// works for both *sql.Row and *sql.Rows.
func scanProject(scan func(dest ...any) error) (config.Project, error) {
	var (
		p       config.Project
		envJSON string
	)
	if err := scan(&p.ID, &p.Name, &p.ServerID, &p.Strategy, &p.RepoURL, &p.DockerfilePath,
		&p.IISSite, &p.IISPhysicalPath, &p.IISAppPool, &p.IISService, &p.IISBuildCommand,
		&p.IISSourceSubdir, &p.Branch, &p.Domain, &p.Port, &p.HealthPath, &p.WebhookSecretRef,
		&envJSON); err != nil {
		return config.Project{}, err
	}
	if envJSON != "" {
		if err := json.Unmarshal([]byte(envJSON), &p.Env); err != nil {
			return config.Project{}, fmt.Errorf("decode project %q env: %w", p.ID, err)
		}
	}
	return p, nil
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

// Deployment statuses.
const (
	DeployQueued  = "queued"
	DeployRunning = "running"
	DeploySuccess = "success"
	DeployFailed  = "failed"
)

// Deployment is one recorded deploy attempt, including its accumulated log.
// ImageTag is a generic artifact reference: a Docker image tag for "docker"
// targets, or the pre-deploy backup directory for "iis" targets.
type Deployment struct {
	ID         int64     `json:"id"`
	ProjectID  string    `json:"project_id"`
	TargetType string    `json:"target_type"` // docker | iis
	CommitSHA  string    `json:"commit_sha"`
	Ref        string    `json:"ref"`
	ImageTag   string    `json:"image_tag"`
	Status     string    `json:"status"`
	Trigger    string    `json:"trigger"` // webhook | rollback | manual
	Log        string    `json:"log,omitempty"`
	Error      string    `json:"error,omitempty"`
	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at,omitempty"` // zero while still running
}

// deploymentColumns is the shared SELECT list for deployments.
const deploymentColumns = `id, project_id, target_type, commit_sha, ref, image_tag, status, trigger, log, error, started_at, finished_at`

// CreateDeployment records a new attempt and returns its id. The log starts
// empty and grows via AppendDeploymentLog.
func (s *Store) CreateDeployment(ctx context.Context, d Deployment) (int64, error) {
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO deployments (project_id, target_type, commit_sha, ref, image_tag, status, trigger, started_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		d.ProjectID, d.TargetType, d.CommitSHA, d.Ref, d.ImageTag, d.Status, d.Trigger, d.StartedAt.Unix())
	if err != nil {
		return 0, fmt.Errorf("create deployment: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("deployment id: %w", err)
	}
	return id, nil
}

// SetDeploymentStatus updates status and the artifact reference of an
// in-progress deploy.
func (s *Store) SetDeploymentStatus(ctx context.Context, id int64, status, artifact string) error {
	if _, err := s.db.ExecContext(ctx,
		`UPDATE deployments SET status = ?, image_tag = ? WHERE id = ?`,
		status, artifact, id); err != nil {
		return fmt.Errorf("set deployment status: %w", err)
	}
	return nil
}

// AppendDeploymentLog appends a chunk to the deployment log. SQLite string
// concatenation keeps each write small instead of rewriting the whole log.
func (s *Store) AppendDeploymentLog(ctx context.Context, id int64, chunk string) error {
	if _, err := s.db.ExecContext(ctx,
		`UPDATE deployments SET log = log || ? WHERE id = ?`, chunk, id); err != nil {
		return fmt.Errorf("append deployment log: %w", err)
	}
	return nil
}

// FinishDeployment marks the attempt terminal and stamps the finish time.
func (s *Store) FinishDeployment(ctx context.Context, id int64, status, errMsg string, finished time.Time) error {
	if _, err := s.db.ExecContext(ctx,
		`UPDATE deployments SET status = ?, error = ?, finished_at = ? WHERE id = ?`,
		status, errMsg, finished.Unix(), id); err != nil {
		return fmt.Errorf("finish deployment: %w", err)
	}
	return nil
}

// GetDeployment loads one attempt by id.
func (s *Store) GetDeployment(ctx context.Context, id int64) (Deployment, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+deploymentColumns+` FROM deployments WHERE id = ?`, id)
	d, err := scanDeployment(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return Deployment{}, ErrNotFound
	}
	if err != nil {
		return Deployment{}, err
	}
	return d, nil
}

// ListDeployments returns the most recent attempts for a project, newest first.
func (s *Store) ListDeployments(ctx context.Context, projectID string, limit int) ([]Deployment, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+deploymentColumns+` FROM deployments WHERE project_id = ? ORDER BY id DESC LIMIT ?`,
		projectID, limit)
	if err != nil {
		return nil, fmt.Errorf("list deployments: %w", err)
	}
	defer rows.Close()

	var out []Deployment
	for rows.Next() {
		d, err := scanDeployment(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// SuccessfulDeployments returns successful attempts for a project, newest
// first. Used to find the previous image tag for a Docker rollback.
func (s *Store) SuccessfulDeployments(ctx context.Context, projectID string, limit int) ([]Deployment, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+deploymentColumns+` FROM deployments WHERE project_id = ? AND status = ? ORDER BY id DESC LIMIT ?`,
		projectID, DeploySuccess, limit)
	if err != nil {
		return nil, fmt.Errorf("list successful deployments: %w", err)
	}
	defer rows.Close()

	var out []Deployment
	for rows.Next() {
		d, err := scanDeployment(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// RecentDeployments returns the newest attempts across all projects.
func (s *Store) RecentDeployments(ctx context.Context, limit int) ([]Deployment, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+deploymentColumns+` FROM deployments ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("recent deployments: %w", err)
	}
	defer rows.Close()

	var out []Deployment
	for rows.Next() {
		d, err := scanDeployment(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func scanDeployment(scan func(dest ...any) error) (Deployment, error) {
	var (
		d        Deployment
		started  int64
		finished int64
	)
	if err := scan(&d.ID, &d.ProjectID, &d.TargetType, &d.CommitSHA, &d.Ref, &d.ImageTag,
		&d.Status, &d.Trigger, &d.Log, &d.Error, &started, &finished); err != nil {
		return Deployment{}, err
	}
	d.StartedAt = time.Unix(started, 0)
	if finished > 0 {
		d.FinishedAt = time.Unix(finished, 0)
	}
	return d, nil
}

// ServiceStatus is one monitored service's state, e.g. {"docker","active"}.
type ServiceStatus struct {
	Name  string `json:"name"`
	State string `json:"state"`
}

// Metric is one point-in-time sample from a server. Reachable is false when the
// SSH/WinRM connection (or the remote command) failed; Error then explains why.
// Percent fields are derived from the byte counts for convenience.
type Metric struct {
	ID            int64           `json:"-"`
	ServerID      string          `json:"server_id"`
	Timestamp     time.Time       `json:"ts"`
	Reachable     bool            `json:"reachable"`
	Error         string          `json:"error,omitempty"`
	CPUPercent    float64         `json:"cpu_percent"`
	MemTotal      uint64          `json:"mem_total"`
	MemUsed       uint64          `json:"mem_used"`
	MemPercent    float64         `json:"mem_percent"`
	DiskTotal     uint64          `json:"disk_total"`
	DiskUsed      uint64          `json:"disk_used"`
	DiskPercent   float64         `json:"disk_percent"`
	UptimeSeconds int64           `json:"uptime_seconds"`
	Load1         float64         `json:"load1"`
	Services      []ServiceStatus `json:"services"`
}

const metricColumns = `id, server_id, ts, reachable, error, cpu_percent, mem_total, mem_used, disk_total, disk_used, uptime_seconds, load1, services_json`

// InsertMetric records one sample (successful or failed).
func (s *Store) InsertMetric(ctx context.Context, m Metric) error {
	servicesJSON, err := json.Marshal(m.Services)
	if err != nil {
		return fmt.Errorf("marshal metric services: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO metrics (server_id, ts, reachable, error, cpu_percent, mem_total, mem_used,
			disk_total, disk_used, uptime_seconds, load1, services_json)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		m.ServerID, m.Timestamp.Unix(), boolToInt(m.Reachable), m.Error, m.CPUPercent,
		m.MemTotal, m.MemUsed, m.DiskTotal, m.DiskUsed, m.UptimeSeconds, m.Load1, string(servicesJSON))
	if err != nil {
		return fmt.Errorf("insert metric %q: %w", m.ServerID, err)
	}
	return nil
}

// LatestMetrics returns the most recent sample for every server that has one.
func (s *Store) LatestMetrics(ctx context.Context) ([]Metric, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+metricColumns+` FROM metrics
		WHERE id IN (SELECT MAX(id) FROM metrics GROUP BY server_id)
		ORDER BY server_id`)
	if err != nil {
		return nil, fmt.Errorf("latest metrics: %w", err)
	}
	defer rows.Close()
	return scanMetrics(rows)
}

// MetricHistory returns up to limit samples for one server, oldest first, so
// charts can plot them directly.
func (s *Store) MetricHistory(ctx context.Context, serverID string, limit int) ([]Metric, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+metricColumns+` FROM (
			SELECT `+metricColumns+` FROM metrics WHERE server_id = ? ORDER BY id DESC LIMIT ?
		) ORDER BY id ASC`, serverID, limit)
	if err != nil {
		return nil, fmt.Errorf("metric history %q: %w", serverID, err)
	}
	defer rows.Close()
	return scanMetrics(rows)
}

// PruneMetrics deletes samples older than before.
func (s *Store) PruneMetrics(ctx context.Context, before time.Time) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM metrics WHERE ts < ?`, before.Unix()); err != nil {
		return fmt.Errorf("prune metrics: %w", err)
	}
	return nil
}

func scanMetrics(rows *sql.Rows) ([]Metric, error) {
	var out []Metric
	for rows.Next() {
		var (
			m            Metric
			ts           int64
			reachable    int
			servicesJSON string
		)
		if err := rows.Scan(&m.ID, &m.ServerID, &ts, &reachable, &m.Error, &m.CPUPercent,
			&m.MemTotal, &m.MemUsed, &m.DiskTotal, &m.DiskUsed, &m.UptimeSeconds, &m.Load1,
			&servicesJSON); err != nil {
			return nil, fmt.Errorf("scan metric: %w", err)
		}
		m.Timestamp = time.Unix(ts, 0)
		m.Reachable = reachable != 0
		m.MemPercent = percent(m.MemUsed, m.MemTotal)
		m.DiskPercent = percent(m.DiskUsed, m.DiskTotal)
		if servicesJSON != "" {
			if err := json.Unmarshal([]byte(servicesJSON), &m.Services); err != nil {
				return nil, fmt.Errorf("decode metric services: %w", err)
			}
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func percent(used, total uint64) float64 {
	if total == 0 {
		return 0
	}
	return float64(used) / float64(total) * 100
}
