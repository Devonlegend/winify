package proxy

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Caddy is a Registrar backed by Caddy's admin API. Each app gets a route with
// an "@id" derived from its host, so redeploying the same app replaces the
// route rather than duplicating it. Caddy's automatic HTTPS issues and renews
// the certificate for the host matcher.
type Caddy struct {
	adminURL string
	server   string
	acmeSkip []string
	client   *http.Client
	mu       sync.Mutex
}

// NewCaddy builds a client for the Caddy admin API (default port 2019).
// acmeSkip lists host patterns Caddy should serve over HTTP without ACME;
// when empty it defaults to the shared sslip.io zone.
func NewCaddy(adminURL, serverName string, acmeSkip ...string) *Caddy {
	if serverName == "" {
		serverName = "srv0"
	}
	return &Caddy{
		adminURL: strings.TrimRight(adminURL, "/"),
		server:   serverName,
		acmeSkip: acmeSkip,
		client:   &http.Client{Timeout: 10 * time.Second},
	}
}

// skipPatterns returns the configured ACME-skip host patterns, defaulting to
// the sslip.io zone (which cannot obtain certificates) when none are set.
func (c *Caddy) skipPatterns() []string {
	if len(c.acmeSkip) == 0 {
		return []string{sslipIOSkip}
	}
	return c.acmeSkip
}

// Reachable reports whether the Caddy admin API answers. It lets the control
// center warn at startup when proxy registration is enabled but Caddy is not
// running, instead of failing every deploy later with a registration error.
func (c *Caddy) Reachable(ctx context.Context) error {
	resp, err := c.request(ctx, http.MethodGet, "/config/", nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("caddy admin %s: status %d", c.adminURL, resp.StatusCode)
	}
	return nil
}

func routeID(host string) string {
	canonical := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
	sum := sha256.Sum256([]byte(canonical))
	return "cc-route-" + hex.EncodeToString(sum[:])[:24]
}

// Register ensures the HTTP server exists, then upserts a host-matched route
// that reverse-proxies to upstream (host:port).
func (c *Caddy) Register(ctx context.Context, host, upstream string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if host == "" {
		return fmt.Errorf("caddy: empty host")
	}
	if strings.ContainsAny(host, "\r\n/") {
		return fmt.Errorf("caddy: invalid host")
	}
	if strings.TrimSpace(upstream) == "" {
		return fmt.Errorf("caddy: empty upstream")
	}
	if err := c.ensureServer(ctx); err != nil {
		return err
	}

	id := routeID(host)
	// Keep a copy so a failed replacement can be compensated. This narrows the
	// delete/add window and avoids leaving a project offline when Caddy rejects
	// the new route.
	oldRoute, hadOld, err := c.routeSnapshot(ctx, id)
	if err != nil {
		return fmt.Errorf("caddy read old route %s: %w", host, err)
	}
	if err := c.do(ctx, http.MethodDelete, "/id/"+id, nil, http.StatusOK, http.StatusNotFound); err != nil {
		return fmt.Errorf("caddy remove old route %s: %w", host, err)
	}

	route := map[string]any{
		"@id":   id,
		"match": []any{map[string]any{"host": []string{host}}},
		"handle": []any{map[string]any{
			"handler":   "reverse_proxy",
			"upstreams": []any{map[string]any{"dial": upstream}},
		}},
		"terminal": true,
	}
	path := "/config/apps/http/servers/" + c.server + "/routes"
	if err := c.do(ctx, http.MethodPost, path, route, http.StatusOK); err != nil {
		registerErr := fmt.Errorf("caddy register %s: %w", host, err)
		if hadOld {
			if restoreErr := c.do(ctx, http.MethodPost, path, json.RawMessage(oldRoute), http.StatusOK); restoreErr != nil {
				return fmt.Errorf("%w (also failed to restore previous route: %v)", registerErr, restoreErr)
			}
		}
		return registerErr
	}
	return nil
}

// Deregister removes the route for host. A missing route is not an error.
func (c *Caddy) Deregister(ctx context.Context, host string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if host == "" {
		return nil
	}
	return c.do(ctx, http.MethodDelete, "/id/"+routeID(host), nil, http.StatusOK, http.StatusNotFound)
}

// ensureServer creates the HTTP server object once. It checks first so an
// existing server (and its routes) is never overwritten.
func (c *Caddy) ensureServer(ctx context.Context) error {
	path := "/config/apps/http/servers/" + c.server
	resp, err := c.request(ctx, http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		// Already exists; still make sure auto-HTTPS is configured below.
	case http.StatusNotFound, http.StatusBadRequest:
		// Caddy's admin API returns 400 (not 404) when a GET targets a config
		// path that does not exist yet, so both codes mean "not created".
		server := map[string]any{
			"listen":          []string{":80", ":443"},
			"routes":          []any{},
			"automatic_https": map[string]any{"skip": c.skipPatterns()},
		}
		if err := c.do(ctx, http.MethodPut, path, server, http.StatusOK); err != nil {
			return fmt.Errorf("caddy create server: %w", err)
		}
	default:
		return fmt.Errorf("caddy get server %q: unexpected status %d", c.server, resp.StatusCode)
	}
	return c.ensureAutoHTTPSSkip(ctx)
}

// sslipIOSkip is the shared sslip.io zone. Let's Encrypt rate-limits it, so
// certificates for dynamically generated sslip.io hostnames cannot be issued;
// Caddy skips ACME for the whole zone and serves those hosts over HTTP only.
// Real domains on the same server still get automatic HTTPS.
const sslipIOSkip = "*.sslip.io"

// ensureAutoHTTPSSkip makes Caddy skip certificate management for the
// configured ACME-skip hosts (sslip.io by default, plus any tunnel-fronted
// custom domains). Without this, Caddy would redirect :80 to :443 for a host
// behind an external TLS terminator and loop. This only touches the
// automatic_https object, never the routes. A server created elsewhere may lack
// the key, so a failed PATCH falls back to a PUT that creates it.
func (c *Caddy) ensureAutoHTTPSSkip(ctx context.Context) error {
	path := "/config/apps/http/servers/" + c.server + "/automatic_https"
	body := map[string]any{"skip": c.skipPatterns()}
	if err := c.do(ctx, http.MethodPatch, path, body, http.StatusOK); err == nil {
		return nil
	}
	if err := c.do(ctx, http.MethodPut, path, body, http.StatusOK); err != nil {
		return fmt.Errorf("caddy set automatic_https skip: %w", err)
	}
	return nil
}

func (c *Caddy) routeSnapshot(ctx context.Context, id string) ([]byte, bool, error) {
	resp, err := c.request(ctx, http.MethodGet, "/id/"+id, nil)
	if err != nil {
		return nil, false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, false, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, false, fmt.Errorf("status %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, false, err
	}
	return data, true, nil
}

func (c *Caddy) do(ctx context.Context, method, path string, body any, want ...int) error {
	resp, err := c.request(ctx, method, path, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	for _, w := range want {
		if resp.StatusCode == w {
			return nil
		}
	}
	msg, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
	return fmt.Errorf("%s %s: status %d: %s", method, path, resp.StatusCode, strings.TrimSpace(string(msg)))
}

func (c *Caddy) request(ctx context.Context, method, path string, body any) (*http.Response, error) {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal caddy body: %w", err)
		}
		reader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.adminURL+path, reader)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return c.client.Do(req)
}
