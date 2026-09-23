package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDefaultsWhenFileMissing(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "does-not-exist.yaml"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Server.Addr != "127.0.0.1:8080" {
		t.Errorf("Addr = %q, want %q", cfg.Server.Addr, "127.0.0.1:8080")
	}
	if cfg.Database.Path != "data/control-center.db" {
		t.Errorf("DB path = %q, want %q", cfg.Database.Path, "data/control-center.db")
	}
}

func TestProxyEnabledByDefault(t *testing.T) {
	if !Default().Proxy.Enabled {
		t.Fatal("Default().Proxy.Enabled = false, want true (bootstrap installs Caddy)")
	}
}

func TestLoadMergesPartialYAMLOverDefaults(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(p, []byte("server:\n  addr: \":9090\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Server.Addr != ":9090" {
		t.Errorf("Addr = %q, want %q", cfg.Server.Addr, ":9090")
	}
	if cfg.Database.Path != "data/control-center.db" {
		t.Errorf("DB path = %q, want default", cfg.Database.Path)
	}
}

func TestLoadEnvOverridesYAML(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(p, []byte("database:\n  path: \"from-file.db\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CC_DB_PATH", "from-env.db")
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Database.Path != "from-env.db" {
		t.Errorf("DB path = %q, want %q", cfg.Database.Path, "from-env.db")
	}
}

func TestLoadBadYAMLReturnsError(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(p, []byte("server: [not: valid\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(p); err == nil {
		t.Fatal("Load = nil error, want error for malformed YAML")
	}
}
