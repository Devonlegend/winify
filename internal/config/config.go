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

// Config is the top-level application configuration.
type Config struct {
	Server      ServerConfig      `yaml:"server"`
	Database    DatabaseConfig    `yaml:"database"`
	Auth        AuthConfig        `yaml:"auth"`
	Credentials CredentialsConfig `yaml:"credentials"`
	Files       FilesConfig       `yaml:"files"`
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
	return nil
}
