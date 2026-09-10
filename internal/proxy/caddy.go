package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Caddy is a Registrar backed by Caddy's admin API. Each app gets a route with
// an "@id" derived from its host, so redeploying the same app replaces the
// route rather than duplicating it. Caddy's automatic HTTPS issues and renews
// the certificate for the host matcher.
type Caddy struct {
	adminURL string
	server   string
	client   *http.Client
}

// NewCaddy builds a client for the Caddy admin API (default port 2019).
func NewCaddy(adminURL, serverName string) *Caddy {
	if serverName == "" {
		serverName = "srv0"
	}
	return &Caddy{
		adminURL: strings.TrimRight(adminURL, "/"),
		server:   serverName,
		client:   &http.Client{Timeout: 10 * time.Second},
	}
}

func routeID(host string) string {
	replacer := strings.NewReplacer(".", "-", ":", "-", "*", "wildcard")
	return "cc-route-" + replacer.Replace(host)
}

// Register ensures the HTTP server exists, then upserts a host-matched route
// that reverse-proxies to upstream (host:port).
func (c *Caddy) Register(ctx context.Context, host, upstream string) error {
	if host == "" {
		return fmt.Errorf("caddy: empty host")
	}
	if err := c.ensureServer(ctx); err != nil {
		return err
	}

	id := routeID(host)
	// Idempotent upsert: drop any existing route with this id, then add it.
	_ = c.do(ctx, http.MethodDelete, "/id/"+id, nil, http.StatusOK, http.StatusNotFound)

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
		return fmt.Errorf("caddy register %s: %w", host, err)
	}
	return nil
}

// Deregister removes the route for host. A missing route is not an error.
func (c *Caddy) Deregister(ctx context.Context, host string) error {
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
		return nil
	case http.StatusNotFound:
		server := map[string]any{
			"listen":          []string{":80", ":443"},
			"routes":          []any{},
			"automatic_https": map[string]any{},
		}
		if err := c.do(ctx, http.MethodPut, path, server, http.StatusOK); err != nil {
			return fmt.Errorf("caddy create server: %w", err)
		}
		return nil
	default:
		return fmt.Errorf("caddy get server %q: unexpected status %d", c.server, resp.StatusCode)
	}
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
