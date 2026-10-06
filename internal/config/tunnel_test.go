package config

import (
	"strings"
	"testing"
)

func TestTunnelDefaultsDisabled(t *testing.T) {
	cfg := Default()
	if cfg.Tunnel.Enabled {
		t.Fatal("tunnel should be disabled by default")
	}
	if cfg.Tunnel.Provider != TunnelProviderCloudflare {
		t.Errorf("provider = %q, want %q", cfg.Tunnel.Provider, TunnelProviderCloudflare)
	}
	if cfg.Tunnel.EffectiveOrigin() != "http://localhost:80" {
		t.Errorf("origin = %q", cfg.Tunnel.EffectiveOrigin())
	}
	if cfg.Tunnel.Connector() != "winify-tunnel" {
		t.Errorf("connector = %q", cfg.Tunnel.Connector())
	}
}

func TestTunnelValidation(t *testing.T) {
	base := func() Config {
		cfg := Default()
		cfg.Tunnel.Enabled = true
		cfg.Tunnel.Domain = "example.com"
		cfg.Tunnel.AccountID = "acct"
		cfg.Tunnel.APITokenRef = "vault:cloudflare-api"
		return cfg
	}

	if err := base().Validate(); err != nil {
		t.Fatalf("valid tunnel config rejected: %v", err)
	}

	for name, mutate := range map[string]func(*Config){
		"missing domain":     func(c *Config) { c.Tunnel.Domain = "" },
		"missing account":    func(c *Config) { c.Tunnel.AccountID = "" },
		"missing token ref":  func(c *Config) { c.Tunnel.APITokenRef = "" },
		"bad provider":       func(c *Config) { c.Tunnel.Provider = "ngrok" },
		"bad origin":         func(c *Config) { c.Tunnel.Origin = "not a url" },
		"insecure connector": func(c *Config) { c.Tunnel.ConnectorURL = "http://example.com/cloudflared.exe" },
	} {
		cfg := base()
		mutate(&cfg)
		if err := cfg.Validate(); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestTunnelDisabledIgnoresIncomplete(t *testing.T) {
	cfg := Default()
	cfg.Tunnel.Domain = "" // would be invalid if enabled
	if err := cfg.Validate(); err != nil {
		t.Fatalf("disabled tunnel should not require fields: %v", err)
	}
	if !strings.HasPrefix(Default().Tunnel.APITokenRef, "vault:") {
		t.Error("api token ref should default to a vault reference")
	}
}
