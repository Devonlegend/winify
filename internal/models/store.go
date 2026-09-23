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

// ErrAlreadyInitialized means a first-run action was attempted after the system
// already had a user (for example, registering an admin when one exists).
var ErrAlreadyInitialized = errors.New("already initialized")

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

// CountUsers returns the number of admin accounts. It is how the first-run flow
// decides whether to show registration or login.
func (s *Store) CountUsers(ctx context.Context) (int, error) {
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n); err != nil {
		return 0, fmt.Errorf("count users: %w", err)
	}
	return n, nil
}

// CreateFirstUser creates the initial admin account only when no user exists.
// It is a single atomic statement, so two concurrent registrations cannot both
// succeed — the loser gets ErrAlreadyInitialized. It never overwrites an
// existing account (unlike UpsertUser).
func (s *Store) CreateFirstUser(ctx context.Context, username, passwordHash string) (User, error) {
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO users (username, password_hash)
		SELECT ?, ? WHERE NOT EXISTS (SELECT 1 FROM users)`,
		username, passwordHash)
	if err != nil {
		return User{}, fmt.Errorf("create first user: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return User{}, fmt.Errorf("create first user: %w", err)
	}
	if n == 0 {
		return User{}, ErrAlreadyInitialized
	}
	return s.UserByUsername(ctx, username)
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

// GetMeta reads a value from app_meta. A missing key returns "" and no error,
// so callers can treat absence as the zero state.
func (s *Store) GetMeta(ctx context.Context, key string) (string, error) {
	var value string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM app_meta WHERE key = ?`, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("get meta %q: %w", key, err)
	}
	return value, nil
}

// SetMeta writes a value to app_meta, creating or replacing the key.
func (s *Store) SetMeta(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO app_meta (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	if err != nil {
		return fmt.Errorf("set meta %q: %w", key, err)
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
	credential_ref, ssh_host, ssh_port, ssh_user, ssh_key_ref, nssm_path, caddy_path, public_ip, local`

// UpsertServer syncs one entry from servers.yaml into the database.
func (s *Store) UpsertServer(ctx context.Context, srv config.Server) error {
	if srv.SSHPort == 0 {
		srv.SSHPort = 22
	}
	if srv.WinRMTransport == "" {
		srv.WinRMTransport = "ntlm"
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO servers (`+serverColumns+`)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
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
			nssm_path = excluded.nssm_path,
			caddy_path = excluded.caddy_path,
			public_ip = excluded.public_ip,
			local = excluded.local,
			updated_at = CURRENT_TIMESTAMP`,
		srv.ID, srv.Name, srv.Type, srv.Host, srv.WinRMEndpoint, srv.WinRMUser,
		srv.WinRMTransport, boolToInt(srv.WinRMInsecure), srv.CredentialRef,
		srv.SSHHost, srv.SSHPort, srv.SSHUser, srv.SSHKeyRef, srv.NSSMPath, srv.CaddyPath, srv.PublicIP,
		boolToInt(srv.Local))
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
		local    int
	)
	if err := scan(&srv.ID, &srv.Name, &srv.Type, &srv.Host, &srv.WinRMEndpoint,
		&srv.WinRMUser, &srv.WinRMTransport, &insecure, &srv.CredentialRef,
		&srv.SSHHost, &srv.SSHPort, &srv.SSHUser, &srv.SSHKeyRef, &srv.NSSMPath, &srv.CaddyPath, &srv.PublicIP,
		&local); err != nil {
		return config.Server{}, fmt.Errorf("scan server: %w", err)
	}
	srv.WinRMInsecure = insecure != 0
	srv.Local = local != 0
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
	if p.Branch == "" {
		p.Branch = "main"
	}
	if p.HealthPath == "" {
		p.HealthPath = "/"
	}
	if p.Port == 0 {
		p.Port = p.EffectiveHostPort()
	}
	if p.ProjectGroup == "" {
		p.ProjectGroup = "Default"
	}
	if p.Environment == "" {
		p.Environment = "production"
	}
	envJSON, err := json.Marshal(p.Env)
	if err != nil {
		return fmt.Errorf("marshal project env: %w", err)
	}
	buildEnvJSON, err := json.Marshal(p.BuildEnv)
	if err != nil {
		return fmt.Errorf("marshal project build env: %w", err)
	}
	mappingsJSON, err := json.Marshal(p.PortsMappings)
	if err != nil {
		return fmt.Errorf("marshal project ports mappings: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO projects (`+projectColumns+`)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			name = excluded.name,
			server_id = excluded.server_id,
			strategy = excluded.strategy,
			source = excluded.source,
			repo_url = excluded.repo_url,
			dockerfile_path = excluded.dockerfile_path,
			compose_path = excluded.compose_path,
			image = excluded.image,
			ports_exposes = excluded.ports_exposes,
			iis_site = excluded.iis_site,
			iis_physical_path = excluded.iis_physical_path,
			iis_app_pool = excluded.iis_app_pool,
			iis_service = excluded.iis_service,
			iis_build_command = excluded.iis_build_command,
			iis_source_subdir = excluded.iis_source_subdir,
			service_name = excluded.service_name,
			service_exe = excluded.service_exe,
			service_args = excluded.service_args,
			service_work_dir = excluded.service_work_dir,
			service_build_command = excluded.service_build_command,
			service_source_subdir = excluded.service_source_subdir,
			service_log_dir = excluded.service_log_dir,
			service_account = excluded.service_account,
			caddy_mode = excluded.caddy_mode,
			ports_mappings = excluded.ports_mappings,
			build_env_json = excluded.build_env_json,
			branch = excluded.branch,
			domain = excluded.domain,
			port = excluded.port,
			health_path = excluded.health_path,
			health_interval_seconds = excluded.health_interval_seconds,
			health_timeout_seconds = excluded.health_timeout_seconds,
			health_retries = excluded.health_retries,
			health_start_period_seconds = excluded.health_start_period_seconds,
			webhook_secret_ref = excluded.webhook_secret_ref,
			env_json = excluded.env_json,
			project_group = excluded.project_group,
			environment = excluded.environment,
			disable_health_check = excluded.disable_health_check,
			runtime = excluded.runtime,
			updated_at = CURRENT_TIMESTAMP`,
		p.ID, p.Name, p.ServerID, p.Strategy, p.Source, p.RepoURL, p.DockerfilePath, p.ComposePath,
		p.Image, p.PortsExposes, p.IISSite, p.IISPhysicalPath, p.IISAppPool, p.IISService,
		p.IISBuildCommand, p.IISSourceSubdir,
		p.ServiceName, p.ServiceExe, p.ServiceArgs, p.ServiceWorkDir, p.ServiceBuildCommand,
		p.ServiceSourceSubdir, p.ServiceLogDir, p.ServiceAccount, p.CaddyMode,
		string(mappingsJSON), string(buildEnvJSON),
		p.Branch, p.Domain, p.Port, p.HealthPath,
		p.HealthIntervalSeconds, p.HealthTimeoutSeconds, p.HealthRetries, p.HealthStartPeriodSeconds,
		p.WebhookSecretRef, string(envJSON), p.ProjectGroup, p.Environment, boolToInt(p.DisableHealthCheck), p.Runtime)
	if err != nil {
		return fmt.Errorf("upsert project %q: %w", p.ID, err)
	}
	return nil
}

// projectColumns is the shared SELECT list for projects.
const projectColumns = `id, name, server_id, strategy, source, repo_url, dockerfile_path, compose_path, image, ports_exposes, iis_site,
	iis_physical_path, iis_app_pool, iis_service, iis_build_command, iis_source_subdir,
	service_name, service_exe, service_args, service_work_dir, service_build_command, service_source_subdir,
	service_log_dir, service_account, caddy_mode,
	ports_mappings, build_env_json,
	branch, domain, port, health_path, health_interval_seconds, health_timeout_seconds, health_retries, health_start_period_seconds,
	webhook_secret_ref, env_json, project_group, environment, disable_health_check, runtime`

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
		p            config.Project
		envJSON      string
		buildEnvJSON string
		mappingsJSON string
		disabled     int
	)
	if err := scan(&p.ID, &p.Name, &p.ServerID, &p.Strategy, &p.Source, &p.RepoURL,
		&p.DockerfilePath, &p.ComposePath, &p.Image, &p.PortsExposes, &p.IISSite, &p.IISPhysicalPath,
		&p.IISAppPool, &p.IISService, &p.IISBuildCommand, &p.IISSourceSubdir,
		&p.ServiceName, &p.ServiceExe, &p.ServiceArgs, &p.ServiceWorkDir, &p.ServiceBuildCommand,
		&p.ServiceSourceSubdir, &p.ServiceLogDir, &p.ServiceAccount, &p.CaddyMode,
		&mappingsJSON, &buildEnvJSON, &p.Branch,
		&p.Domain, &p.Port, &p.HealthPath,
		&p.HealthIntervalSeconds, &p.HealthTimeoutSeconds, &p.HealthRetries, &p.HealthStartPeriodSeconds,
		&p.WebhookSecretRef, &envJSON,
		&p.ProjectGroup, &p.Environment, &disabled, &p.Runtime); err != nil {
		return config.Project{}, err
	}
	p.DisableHealthCheck = disabled != 0
	if err := decodeJSONMap(envJSON, &p.Env); err != nil {
		return config.Project{}, fmt.Errorf("decode project %q env: %w", p.ID, err)
	}
	if err := decodeJSONMap(buildEnvJSON, &p.BuildEnv); err != nil {
		return config.Project{}, fmt.Errorf("decode project %q build env: %w", p.ID, err)
	}
	if mappingsJSON != "" && mappingsJSON != "null" {
		if err := json.Unmarshal([]byte(mappingsJSON), &p.PortsMappings); err != nil {
			return config.Project{}, fmt.Errorf("decode project %q ports mappings: %w", p.ID, err)
		}
	}
	return p, nil
}

// decodeJSONMap unmarshals a JSON object string into a map, treating empty and
// "null" as an absent map.
func decodeJSONMap(raw string, out *map[string]string) error {
	if raw == "" || raw == "null" {
		return nil
	}
	return json.Unmarshal([]byte(raw), out)
}

// SharedVariable is a value referenced from project env/build env as
// {{project.KEY}} (scope "project", scope_id = project group) or
// {{environment.KEY}} (scope "environment", scope_id = "group/environment").
type SharedVariable struct {
	Scope   string `json:"scope"`
	ScopeID string `json:"scope_id"`
	Key     string `json:"key"`
	Value   string `json:"value"`
}

// ListSharedVariables returns all shared variables ordered by scope and key.
func (s *Store) ListSharedVariables(ctx context.Context) ([]SharedVariable, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT scope, scope_id, key, value FROM shared_variables ORDER BY scope, scope_id, key`)
	if err != nil {
		return nil, fmt.Errorf("list shared variables: %w", err)
	}
	defer rows.Close()

	var out []SharedVariable
	for rows.Next() {
		var v SharedVariable
		if err := rows.Scan(&v.Scope, &v.ScopeID, &v.Key, &v.Value); err != nil {
			return nil, fmt.Errorf("scan shared variable: %w", err)
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// UpsertSharedVariable stores one shared variable.
func (s *Store) UpsertSharedVariable(ctx context.Context, v SharedVariable) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO shared_variables (scope, scope_id, key, value) VALUES (?, ?, ?, ?)
		ON CONFLICT(scope, scope_id, key) DO UPDATE SET
			value = excluded.value,
			updated_at = CURRENT_TIMESTAMP`,
		v.Scope, v.ScopeID, v.Key, v.Value)
	if err != nil {
		return fmt.Errorf("upsert shared variable: %w", err)
	}
	return nil
}

// DeleteSharedVariable removes one shared variable.
func (s *Store) DeleteSharedVariable(ctx context.Context, scope, scopeID, key string) error {
	if _, err := s.db.ExecContext(ctx,
		`DELETE FROM shared_variables WHERE scope = ? AND scope_id = ? AND key = ?`,
		scope, scopeID, key); err != nil {
		return fmt.Errorf("delete shared variable: %w", err)
	}
	return nil
}

// SharedVariablesFor returns the values visible to a project, keyed by their
// reference name ("project.KEY" / "environment.KEY"). Environment-scoped values
// override project-scoped ones with the same key.
func (s *Store) SharedVariablesFor(ctx context.Context, projectGroup, environment string) (map[string]string, error) {
	out := make(map[string]string)
	rows, err := s.db.QueryContext(ctx, `
		SELECT scope, key, value FROM shared_variables
		WHERE (scope = 'project' AND scope_id = ?)
		   OR (scope = 'environment' AND scope_id = ?)`,
		projectGroup, projectGroup+"/"+environment)
	if err != nil {
		return nil, fmt.Errorf("shared variables for project: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var scope, key, value string
		if err := rows.Scan(&scope, &key, &value); err != nil {
			return nil, fmt.Errorf("scan shared variable: %w", err)
		}
		out[scope+"."+key] = value
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

// RemoteCommand is one audited remote command execution. Command never contains
// a credential (authentication is out of band) and sensitive payloads such as a
// generated compose file are recorded as a redacted label.
type RemoteCommand struct {
	ID           int64     `json:"id"`
	ServerID     string    `json:"server_id"`
	ServerType   string    `json:"server_type"`
	Action       string    `json:"action"` // deploy | rollback | monitor
	DeploymentID int64     `json:"deployment_id"`
	Command      string    `json:"command"`
	Error        string    `json:"error,omitempty"`
	ExecutedAt   time.Time `json:"executed_at"`
}

const remoteCommandColumns = `id, server_id, server_type, action, deployment_id, command, error, executed_at`

// InsertRemoteCommand appends one audit record.
func (s *Store) InsertRemoteCommand(ctx context.Context, rc RemoteCommand) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO remote_commands (server_id, server_type, action, deployment_id, command, error, executed_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		rc.ServerID, rc.ServerType, rc.Action, rc.DeploymentID, rc.Command, rc.Error, rc.ExecutedAt.Unix())
	if err != nil {
		return fmt.Errorf("insert remote command: %w", err)
	}
	return nil
}

// ListRemoteCommands returns the most recent audited commands, newest first.
func (s *Store) ListRemoteCommands(ctx context.Context, limit int) ([]RemoteCommand, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+remoteCommandColumns+` FROM remote_commands ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("list remote commands: %w", err)
	}
	defer rows.Close()

	var out []RemoteCommand
	for rows.Next() {
		var (
			rc       RemoteCommand
			executed int64
		)
		if err := rows.Scan(&rc.ID, &rc.ServerID, &rc.ServerType, &rc.Action, &rc.DeploymentID,
			&rc.Command, &rc.Error, &executed); err != nil {
			return nil, fmt.Errorf("scan remote command: %w", err)
		}
		rc.ExecutedAt = time.Unix(executed, 0)
		out = append(out, rc)
	}
	return out, rows.Err()
}

// CountServers returns how many servers exist (used to seed from YAML only on a
// fresh install).
func (s *Store) CountServers(ctx context.Context) (int, error) {
	return s.count(ctx, `SELECT COUNT(*) FROM servers`)
}

// CountProjects returns how many projects exist.
func (s *Store) CountProjects(ctx context.Context) (int, error) {
	return s.count(ctx, `SELECT COUNT(*) FROM projects`)
}

// CountProjectsByServer counts projects bound to a server, so a server with
// dependents is not deleted by accident.
func (s *Store) CountProjectsByServer(ctx context.Context, serverID string) (int, error) {
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM projects WHERE server_id = ?`, serverID).Scan(&n); err != nil {
		return 0, fmt.Errorf("count projects for %q: %w", serverID, err)
	}
	return n, nil
}

func (s *Store) count(ctx context.Context, query string) (int, error) {
	var n int
	if err := s.db.QueryRowContext(ctx, query).Scan(&n); err != nil {
		return 0, fmt.Errorf("count: %w", err)
	}
	return n, nil
}

// DeleteServer removes a target. Callers should check for dependent projects.
func (s *Store) DeleteServer(ctx context.Context, id string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM servers WHERE id = ?`, id); err != nil {
		return fmt.Errorf("delete server %q: %w", id, err)
	}
	return nil
}

// DeleteProject removes a project.
func (s *Store) DeleteProject(ctx context.Context, id string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM projects WHERE id = ?`, id); err != nil {
		return fmt.Errorf("delete project %q: %w", id, err)
	}
	return nil
}

// DeleteCredential removes an encrypted credential by name.
func (s *Store) DeleteCredential(ctx context.Context, name string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM credentials WHERE name = ?`, name); err != nil {
		return fmt.Errorf("delete credential %q: %w", name, err)
	}
	return nil
}

// APIToken is a named bearer token for the REST API. The token value is never
// stored — only its SHA-256 hash — and is never returned by any API.
type APIToken struct {
	ID         int64     `json:"id"`
	Name       string    `json:"name"`
	Scope      string    `json:"scope"` // read | write
	CreatedAt  time.Time `json:"created_at"`
	LastUsedAt time.Time `json:"last_used_at,omitempty"`
}

// CreateAPIToken stores a token hash and returns its id.
func (s *Store) CreateAPIToken(ctx context.Context, name, tokenHash, scope string) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO api_tokens (name, token_hash, scope, created_at) VALUES (?, ?, ?, ?)`,
		name, tokenHash, scope, time.Now().Unix())
	if err != nil {
		return 0, fmt.Errorf("create api token: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("api token id: %w", err)
	}
	return id, nil
}

// ListAPITokens returns token metadata (never the value), newest first.
func (s *Store) ListAPITokens(ctx context.Context) ([]APIToken, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, name, scope, created_at, last_used_at FROM api_tokens ORDER BY id DESC`)
	if err != nil {
		return nil, fmt.Errorf("list api tokens: %w", err)
	}
	defer rows.Close()

	var out []APIToken
	for rows.Next() {
		var (
			t        APIToken
			created  int64
			lastUsed int64
		)
		if err := rows.Scan(&t.ID, &t.Name, &t.Scope, &created, &lastUsed); err != nil {
			return nil, fmt.Errorf("scan api token: %w", err)
		}
		t.CreatedAt = time.Unix(created, 0)
		if lastUsed > 0 {
			t.LastUsedAt = time.Unix(lastUsed, 0)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// APITokenByHash looks up a token by its hash, or ErrNotFound.
func (s *Store) APITokenByHash(ctx context.Context, tokenHash string) (APIToken, error) {
	var (
		t        APIToken
		created  int64
		lastUsed int64
	)
	err := s.db.QueryRowContext(ctx,
		`SELECT id, name, scope, created_at, last_used_at FROM api_tokens WHERE token_hash = ?`, tokenHash,
	).Scan(&t.ID, &t.Name, &t.Scope, &created, &lastUsed)
	if errors.Is(err, sql.ErrNoRows) {
		return APIToken{}, ErrNotFound
	}
	if err != nil {
		return APIToken{}, fmt.Errorf("lookup api token: %w", err)
	}
	t.CreatedAt = time.Unix(created, 0)
	if lastUsed > 0 {
		t.LastUsedAt = time.Unix(lastUsed, 0)
	}
	return t, nil
}

// TouchAPIToken records the last time a token was used.
func (s *Store) TouchAPIToken(ctx context.Context, id int64, when time.Time) error {
	if _, err := s.db.ExecContext(ctx, `UPDATE api_tokens SET last_used_at = ? WHERE id = ?`, when.Unix(), id); err != nil {
		return fmt.Errorf("touch api token: %w", err)
	}
	return nil
}

// DeleteAPIToken revokes a token.
func (s *Store) DeleteAPIToken(ctx context.Context, id int64) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM api_tokens WHERE id = ?`, id); err != nil {
		return fmt.Errorf("delete api token: %w", err)
	}
	return nil
}
