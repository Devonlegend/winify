// Package config loads application settings from an optional YAML file and
// from environment variables layered on top. It does not touch credentials
// for remote servers; those are encrypted refs handled by the deployment
// package in a later phase.
package config

import (
	"errors"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Config is the top-level application configuration.
type Config struct {
	Server   ServerConfig   `yaml:"server"`
	Database DatabaseConfig `yaml:"database"`
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

// Default returns the built-in configuration used when a field is not set
// in YAML and not overridden by the environment.
func Default() Config {
	return Config{
		Server:   ServerConfig{Addr: ":8080"},
		Database: DatabaseConfig{Path: "data/control-center.db"},
	}
}

// Load reads the YAML file at path (if it exists) over the defaults, then
// layers environment overrides on top. envKey documents each override.
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

	cfg.applyEnv()
	return cfg, nil
}

// applyEnv overrides selected fields from environment variables, so secrets
// and per-environment values can avoid the config file entirely.
func (cfg *Config) applyEnv() {
	if v := os.Getenv("CC_ADDR"); v != "" {
		cfg.Server.Addr = v
	}
	if v := os.Getenv("CC_DB_PATH"); v != "" {
		cfg.Database.Path = v
	}
}
