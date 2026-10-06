// Package tunnel provisions a public ingress path for a winify host that cannot
// accept inbound connections (CGNAT, a private ISP link, a laptop). Given a
// provider API token it creates a tunnel, points the wildcard host at the local
// Caddy reverse proxy, creates the matching proxied DNS record, and returns the
// connector token the host runs to join the tunnel.
//
// This is what turns "install winify on a fresh PC" into "and it gets a public
// HTTPS URL", including behind CGNAT, where sslip.io/public-IP schemes cannot
// work. The wildcard ingress and DNS record are created once; per-deploy winify
// only has to register the host in Caddy, so no provider call happens on each
// deployment.
//
// SECURITY: this package makes authenticated calls to a tunnel provider using an
// operator-supplied API token. That token can create tunnels and DNS records for
// its account and zone, so it must be resolved from the encrypted credential
// store (never from a plaintext file) and must never be logged or included in an
// error string. The returned connector token is an equally sensitive bearer
// credential for the tunnel.
package tunnel

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Provider identifiers.
const (
	ProviderCloudflare = "cloudflare"
	ProviderNone       = "none"
)

// DefaultOrigin is the local Caddy listener a tunnel forwards to. cloudflared
// stops TLS at the edge, so the origin is plain HTTP and Caddy must skip ACME
// for the tunnel host (proxy.acme_skip).
const DefaultOrigin = "http://localhost:80"

// DefaultTunnelName is the stable tunnel name used when one is not configured.
const DefaultTunnelName = "winify"

const cloudflareAPI = "https://api.cloudflare.com/client/v4"

// Config is the resolved input to Provision. APIToken carries a secret and is
// deliberately not part of any String()/log output.
type Config struct {
	Domain     string // apex domain on the provider, e.g. "example.com"
	AccountID  string // provider account id
	ZoneID     string // optional; resolved from Domain when empty
	TunnelName string // stable tunnel name
	APIToken   string // provider API token (secret)
	Origin     string // upstream on this host, e.g. http://localhost:80
}

// Result is what the host needs to run the connector plus what winify records
// for the route. ConnectToken is a secret.
type Result struct {
	Provider     string
	TunnelID     string
	ConnectToken string // connector token (secret)
	Hostname     string // wildcard host routed to the origin, e.g. *.example.com
	ZoneID       string
	DNSRecordID  string
}

// Logger receives human-readable progress. Callers must ensure it never receives
// the API or connector token.
type Logger func(format string, args ...any)

// Provision ensures, in order: the zone resolves, a tunnel named cfg.TunnelName
// exists, its ingress routes the wildcard host to Origin, and a proxied wildcard
// CNAME points at the tunnel. Every call is idempotent, so re-running is safe.
func Provision(ctx context.Context, cfg Config, logf Logger) (Result, error) {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	if err := cfg.validate(); err != nil {
		return Result{}, err
	}
	c := &client{
		baseURL: cloudflareAPI,
		token:   cfg.APIToken,
		http:    &http.Client{Timeout: 30 * time.Second},
	}
	return c.provisionCloudflare(ctx, cfg, logf)
}

func (cfg Config) validate() error {
	switch {
	case strings.TrimSpace(cfg.APIToken) == "":
		return errors.New("tunnel: API token is required")
	case strings.TrimSpace(cfg.AccountID) == "":
		return errors.New("tunnel: account id is required")
	case strings.TrimSpace(cfg.Domain) == "":
		return errors.New("tunnel: domain is required")
	case strings.TrimSpace(cfg.TunnelName) == "":
		return errors.New("tunnel: tunnel name is required")
	}
	return nil
}

// EffectiveOrigin returns the configured upstream or the local Caddy default.
func (cfg Config) EffectiveOrigin() string {
	if strings.TrimSpace(cfg.Origin) == "" {
		return DefaultOrigin
	}
	return cfg.Origin
}

// client is a minimal Cloudflare API v4 client.
type client struct {
	baseURL string
	token   string
	http    *http.Client
}

type cfEnvelope struct {
	Success bool            `json:"success"`
	Errors  []cfErr         `json:"errors"`
	Result  json.RawMessage `json:"result"`
}

type cfErr struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// do performs one API call and unwraps the Cloudflare result envelope. The API
// token is sent in the Authorization header and never appears in an error.
func (c *client) do(ctx context.Context, method, path string, body, out any) error {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("cloudflare: encode request: %w", err)
		}
		reader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("cloudflare %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("cloudflare %s %s: read response: %w", method, path, err)
	}
	var env cfEnvelope
	if err := json.Unmarshal(data, &env); err != nil {
		return fmt.Errorf("cloudflare %s %s: unexpected response (status %d)", method, path, resp.StatusCode)
	}
	if !env.Success {
		msg := fmt.Sprintf("status %d", resp.StatusCode)
		if len(env.Errors) > 0 {
			msg = env.Errors[0].Message
		}
		return fmt.Errorf("cloudflare %s %s: %s", method, path, msg)
	}
	if out != nil && len(env.Result) > 0 {
		if err := json.Unmarshal(env.Result, out); err != nil {
			return fmt.Errorf("cloudflare %s %s: decode result: %w", method, path, err)
		}
	}
	return nil
}

func (c *client) provisionCloudflare(ctx context.Context, cfg Config, logf Logger) (Result, error) {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	zoneID := cfg.ZoneID
	if zoneID == "" {
		var zones []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		}
		if err := c.do(ctx, http.MethodGet, "/zones?name="+url.QueryEscape(cfg.Domain), nil, &zones); err != nil {
			return Result{}, err
		}
		if len(zones) == 0 {
			return Result{}, fmt.Errorf("tunnel: zone %q not found in this account", cfg.Domain)
		}
		zoneID = zones[0].ID
		logf("tunnel: resolved zone %s -> %s", cfg.Domain, zoneID)
	}

	host := "*." + cfg.Domain
	tunnelID, err := c.ensureTunnel(ctx, cfg.AccountID, cfg.TunnelName, logf)
	if err != nil {
		return Result{}, err
	}
	token, err := c.tunnelToken(ctx, cfg.AccountID, tunnelID)
	if err != nil {
		return Result{}, err
	}
	if err := c.setIngress(ctx, cfg.AccountID, tunnelID, host, cfg.EffectiveOrigin()); err != nil {
		return Result{}, err
	}
	logf("tunnel: ingress %s -> %s", host, cfg.EffectiveOrigin())

	recordID, err := c.ensureDNS(ctx, zoneID, host, tunnelID)
	if err != nil {
		return Result{}, err
	}
	logf("tunnel: DNS %s -> %s.cfargotunnel.com (proxied)", host, tunnelID)

	return Result{
		Provider:     ProviderCloudflare,
		TunnelID:     tunnelID,
		ConnectToken: token,
		Hostname:     host,
		ZoneID:       zoneID,
		DNSRecordID:  recordID,
	}, nil
}

// ensureTunnel returns the id of the named tunnel, creating it when absent.
func (c *client) ensureTunnel(ctx context.Context, accountID, name string, logf Logger) (string, error) {
	var tunnels []struct {
		ID        string `json:"id"`
		Name      string `json:"name"`
		DeletedAt string `json:"deleted_at"`
	}
	path := "/accounts/" + url.PathEscape(accountID) + "/cfd_tunnel?is_deleted=false"
	if err := c.do(ctx, http.MethodGet, path, nil, &tunnels); err != nil {
		return "", err
	}
	for _, t := range tunnels {
		if t.Name == name {
			return t.ID, nil
		}
	}
	var created struct {
		ID string `json:"id"`
	}
	body := map[string]any{"name": name, "config_src": "cloudflare"}
	if err := c.do(ctx, http.MethodPost, "/accounts/"+url.PathEscape(accountID)+"/cfd_tunnel", body, &created); err != nil {
		return "", err
	}
	if created.ID == "" {
		return "", errors.New("tunnel: create returned no id")
	}
	logf("tunnel: created tunnel %q (%s)", name, created.ID)
	return created.ID, nil
}

// tunnelToken fetches the connector token for a tunnel. The token is a secret;
// it is returned to the caller and never logged.
func (c *client) tunnelToken(ctx context.Context, accountID, tunnelID string) (string, error) {
	var token string
	path := "/accounts/" + url.PathEscape(accountID) + "/cfd_tunnel/" + url.PathEscape(tunnelID) + "/token"
	if err := c.do(ctx, http.MethodGet, path, nil, &token); err != nil {
		return "", err
	}
	if token == "" {
		return "", errors.New("tunnel: empty connector token")
	}
	return token, nil
}

// setIngress replaces the tunnel's ingress with the wildcard host pointing at
// origin, plus a catch-all 404 (Cloudflare requires a terminal rule).
func (c *client) setIngress(ctx context.Context, accountID, tunnelID, host, origin string) error {
	body := map[string]any{
		"config": map[string]any{
			"ingress": []any{
				map[string]any{"hostname": host, "service": origin},
				map[string]any{"service": "http_status:404"},
			},
		},
	}
	path := "/accounts/" + url.PathEscape(accountID) + "/cfd_tunnel/" + url.PathEscape(tunnelID) + "/configurations"
	return c.do(ctx, http.MethodPut, path, body, nil)
}

// ensureDNS creates the proxied wildcard CNAME for host when it is missing and
// returns the record id.
func (c *client) ensureDNS(ctx context.Context, zoneID, host, tunnelID string) (string, error) {
	var records []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	list := "/zones/" + url.PathEscape(zoneID) + "/dns_records?type=CNAME&name=" + url.QueryEscape(host)
	if err := c.do(ctx, http.MethodGet, list, nil, &records); err != nil {
		return "", err
	}
	if len(records) > 0 {
		return records[0].ID, nil
	}
	body := map[string]any{
		"type":    "CNAME",
		"name":    host,
		"content": tunnelID + ".cfargotunnel.com",
		"proxied": true,
	}
	var created struct {
		ID string `json:"id"`
	}
	path := "/zones/" + url.PathEscape(zoneID) + "/dns_records"
	if err := c.do(ctx, http.MethodPost, path, body, &created); err != nil {
		return "", err
	}
	return created.ID, nil
}
