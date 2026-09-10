// Package config loads application settings from an optional YAML file and
// from environment variables layered on top, plus the deployment inventory
// (servers.yaml, projects.yaml). It never holds decrypted secrets: server
// entries carry only credential *refs* that internal/auth resolves at
// connection time.
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"

	"gopkg.in/yaml.v3"
)

// Target types. A project's server type selects the deploy pipeline.
const (
	ServerTypeDocker = "docker"
	ServerTypeIIS    = "iis"
)

// Config is the top-level application configuration.
type Config struct {
	Server      ServerConfig      `yaml:"server"`
	Database    DatabaseConfig    `yaml:"database"`
	Auth        AuthConfig        `yaml:"auth"`
	Credentials CredentialsConfig `yaml:"credentials"`
	Files       FilesConfig       `yaml:"files"`
	Proxy       ProxyConfig       `yaml:"proxy"`
	Deploy      DeployConfig      `yaml:"deploy"`
	Monitoring  MonitoringConfig  `yaml:"monitoring"`
}

// ServerConfig controls the HTTP listener.
type ServerConfig struct {
	// Addr is the listen address in host:port form, e.g. ":8080".
	Addr string `yaml:"addr"`
}

// DatabaseConfig locates the SQLite file.
type DatabaseConfig struct {
	// Path is the SQLite database file, created on first run if missing.
	Path string `yaml:"path"`
}

// AuthConfig configures the single v1 admin account and its sessions.
type AuthConfig struct {
	AdminUser string `yaml:"admin_user"`
	// AdminPasswordHash is a bcrypt hash. The plaintext never lives in config;
	// set CC_ADMIN_PASSWORD instead to have the server hash it at startup.
	AdminPasswordHash string `yaml:"admin_password_hash"`
	// CookieSecure sets the Secure flag on the session cookie. Keep true in
	// production; browsers still accept Secure cookies on http://localhost.
	CookieSecure bool `yaml:"cookie_secure"`
	// SessionTTLHours is how long a login lasts.
	SessionTTLHours int `yaml:"session_ttl_hours"`
}

// CredentialsConfig holds the master key used to encrypt credential values.
type CredentialsConfig struct {
	// MasterKey is a base64-encoded 32-byte AES key. Prefer the CC_MASTER_KEY
	// env var; if empty, a key is generated and stored in the data directory.
	MasterKey string `yaml:"master_key"`
}

// FilesConfig points at the deployment inventory files.
type FilesConfig struct {
	Servers  string `yaml:"servers"`
	Projects string `yaml:"projects"`
}

// ProxyConfig configures registration with the reverse proxy (Caddy) that
// fronts every deployment with automatic HTTPS.
type ProxyConfig struct {
	// Enabled turns on proxy registration. When false, deploys run but no
	// public URL is registered (useful for local development without Caddy).
	Enabled bool `yaml:"enabled"`
	// AdminURL is the Caddy admin API endpoint.
	AdminURL string `yaml:"admin_url"`
	// ServerName is the Caddy HTTP server object routes are added to.
	ServerName string `yaml:"server_name"`
}

// MonitoringConfig controls the metrics poller.
type MonitoringConfig struct {
	// Enabled turns the background metrics scheduler on or off.
	Enabled bool `yaml:"enabled"`
	// IntervalSeconds is how often each server is polled.
	IntervalSeconds int `yaml:"interval_seconds"`
	// TimeoutSeconds bounds a single server's collection.
	TimeoutSeconds int `yaml:"timeout_seconds"`
	// RetentionHours is how long samples are kept before pruning.
	RetentionHours int `yaml:"retention_hours"`
	// HistoryPoints caps the points returned for charts.
	HistoryPoints int `yaml:"history_points"`
}

// DeployConfig controls how commands run on target servers.
type DeployConfig struct {
	// WorkDir is the parent directory on Docker/Linux targets for cloned repos.
	WorkDir string `yaml:"work_dir"`
	// IISWorkDir is the parent directory on Windows targets for cloned repos.
	// Separate from WorkDir because the two target types use different path
	// syntaxes (POSIX vs Windows).
	IISWorkDir string `yaml:"iis_work_dir"`
	// IISBackupDir is where timestamped pre-deploy copies of the live IIS
	// directory are stored for rollback.
	IISBackupDir string `yaml:"iis_backup_dir"`
	// KnownHostsFile enables SSH host-key verification. Empty disables it
	// (insecure; only acceptable for throwaway environments).
	KnownHostsFile string `yaml:"known_hosts_file"`
	// HealthTimeoutSeconds bounds how long the post-deploy health check waits.
	HealthTimeoutSeconds int `yaml:"health_timeout_seconds"`
	// HealthIntervalSeconds is the delay between health-check attempts.
	HealthIntervalSeconds int `yaml:"health_interval_seconds"`
}

// Default returns the built-in configuration used when a field is not set
// in YAML and not overridden by the environment.
func Default() Config {
	return Config{
		Server:   ServerConfig{Addr: ":8080"},
		Database: DatabaseConfig{Path: "data/control-center.db"},
		Auth: AuthConfig{
			AdminUser:       "admin",
			CookieSecure:    true,
			SessionTTLHours: 12,
		},
		Files: FilesConfig{Servers: "servers.yaml", Projects: "projects.yaml"},
		Proxy: ProxyConfig{
			Enabled:    false,
			AdminURL:   "http://127.0.0.1:2019",
			ServerName: "srv0",
		},
		Deploy: DeployConfig{
			WorkDir:               "/opt/control-center",
			IISWorkDir:            `C:\control-center`,
			IISBackupDir:          `C:\control-center\backups`,
			HealthTimeoutSeconds:  60,
			HealthIntervalSeconds: 3,
		},
		Monitoring: MonitoringConfig{
			Enabled:         true,
			IntervalSeconds: 30,
			TimeoutSeconds:  15,
			RetentionHours:  24,
			HistoryPoints:   200,
		},
	}
}

// Load reads the YAML file at path (if it exists) over the defaults, then
// layers environment overrides on top.
func Load(path string) (Config, error) {
	cfg := Default()

	data, err := os.ReadFile(path)
	switch {
	case err == nil:
		// yaml.Unmarshal merges into cfg: keys absent from the file keep
		// their default values, so a partial config file is fine.
		if err := yaml.Unmarshal(data, &cfg); err != nil {
			return cfg, fmt.Errorf("parse %s: %w", path, err)
		}
	case errors.Is(err, os.ErrNotExist):
		// No file: run on defaults. Not an error.
	default:
		return cfg, fmt.Errorf("read %s: %w", path, err)
	}

	if err := cfg.applyEnv(); err != nil {
		return cfg, err
	}
	return cfg, nil
}

// applyEnv overrides selected fields from environment variables, so secrets
// and per-environment values can avoid the config file entirely. Secret
// values (master key, admin password) are read here or in main, never logged.
func (cfg *Config) applyEnv() error {
	if v := os.Getenv("CC_ADDR"); v != "" {
		cfg.Server.Addr = v
	}
	if v := os.Getenv("CC_DB_PATH"); v != "" {
		cfg.Database.Path = v
	}
	if v := os.Getenv("CC_ADMIN_USER"); v != "" {
		cfg.Auth.AdminUser = v
	}
	if v := os.Getenv("CC_MASTER_KEY"); v != "" {
		cfg.Credentials.MasterKey = v
	}
	if v := os.Getenv("CC_SERVERS_FILE"); v != "" {
		cfg.Files.Servers = v
	}
	if v := os.Getenv("CC_PROJECTS_FILE"); v != "" {
		cfg.Files.Projects = v
	}
	if v := os.Getenv("CC_COOKIE_SECURE"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return fmt.Errorf("CC_COOKIE_SECURE: %w", err)
		}
		cfg.Auth.CookieSecure = b
	}
	if v := os.Getenv("CC_PROXY_ENABLED"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return fmt.Errorf("CC_PROXY_ENABLED: %w", err)
		}
		cfg.Proxy.Enabled = b
	}
	if v := os.Getenv("CC_PROXY_ADMIN_URL"); v != "" {
		cfg.Proxy.AdminURL = v
	}
	if v := os.Getenv("CC_DEPLOY_WORKDIR"); v != "" {
		cfg.Deploy.WorkDir = v
	}
	if v := os.Getenv("CC_DEPLOY_IIS_WORKDIR"); v != "" {
		cfg.Deploy.IISWorkDir = v
	}
	if v := os.Getenv("CC_DEPLOY_IIS_BACKUP_DIR"); v != "" {
		cfg.Deploy.IISBackupDir = v
	}
	if v := os.Getenv("CC_DEPLOY_KNOWN_HOSTS"); v != "" {
		cfg.Deploy.KnownHostsFile = v
	}
	if v := os.Getenv("CC_MONITOR_ENABLED"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return fmt.Errorf("CC_MONITOR_ENABLED: %w", err)
		}
		cfg.Monitoring.Enabled = b
	}
	if v := os.Getenv("CC_MONITOR_INTERVAL"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return fmt.Errorf("CC_MONITOR_INTERVAL: %w", err)
		}
		cfg.Monitoring.IntervalSeconds = n
	}
	if v := os.Getenv("CC_MONITOR_TIMEOUT"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return fmt.Errorf("CC_MONITOR_TIMEOUT: %w", err)
		}
		cfg.Monitoring.TimeoutSeconds = n
	}
	if v := os.Getenv("CC_MONITOR_RETENTION_HOURS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return fmt.Errorf("CC_MONITOR_RETENTION_HOURS: %w", err)
		}
		cfg.Monitoring.RetentionHours = n
	}
	return nil
}
