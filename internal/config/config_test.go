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
	if want := filepath.Join(filepath.Dir(p), "data/control-center.db"); cfg.Database.Path != want {
		t.Errorf("DB path = %q, want %q", cfg.Database.Path, want)
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

func TestLoadPreservesWindowsTargetPathsOnAnyController(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(p, []byte("deploy:\n  iis_work_dir: 'C:\\ProgramData\\winify\\work'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Deploy.IISWorkDir != `C:\ProgramData\winify\work` {
		t.Fatalf("IISWorkDir = %q", cfg.Deploy.IISWorkDir)
	}
}

func TestLoadDoesNotResolveBase64MasterKeyAsPath(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	key := "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
	if err := os.WriteFile(p, []byte("credentials:\n  master_key: '"+key+"'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Credentials.MasterKey != key {
		t.Fatalf("master key = %q, want unchanged base64", cfg.Credentials.MasterKey)
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
