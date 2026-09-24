// Package githubapp implements the GitHub App protocol pieces winify needs:
// RS256 JWT signing for app authentication, installation-token exchange and
// repository webhook management. It contains no credentials itself; callers
// supply the App ID and the PEM private key (decrypted from the credential
// store) per call.
package githubapp

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client talks to the GitHub REST API (or GitHub Enterprise Server).
type Client struct {
	apiURL string
	http   *http.Client
	now    func() time.Time
}

// NewClient builds a client for apiURL (default https://api.github.com).
func NewClient(apiURL string) *Client {
	apiURL = strings.TrimRight(strings.TrimSpace(apiURL), "/")
	if apiURL == "" {
		apiURL = "https://api.github.com"
	}
	return &Client{apiURL: apiURL, http: &http.Client{Timeout: 30 * time.Second}, now: time.Now}
}

// parsePEMPrivateKey accepts PKCS#1 and PKCS#8 PEM private keys.
func parsePEMPrivateKey(pemBytes []byte) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, fmt.Errorf("github app private key: not a PEM block")
	}
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("github app private key: %w", err)
	}
	rsaKey, ok := key.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("github app private key: not RSA")
	}
	return rsaKey, nil
}

// appJWT signs a short-lived RS256 JWT with the app's private key.
func appJWT(appID, pemKey string, now time.Time) (string, error) {
	key, err := parsePEMPrivateKey([]byte(pemKey))
	if err != nil {
		return "", err
	}
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	payload, err := json.Marshal(map[string]any{
		"iat": now.Add(-60 * time.Second).Unix(), // clock skew allowance
		"exp": now.Add(9 * time.Minute).Unix(),
		"iss": appID,
	})
	if err != nil {
		return "", err
	}
	signing := header + "." + base64.RawURLEncoding.EncodeToString(payload)
	digest := sha256.Sum256([]byte(signing))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		return "", fmt.Errorf("sign app jwt: %w", err)
	}
	return signing + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}

// do performs one JSON API call with a bearer token. A token may be empty for
// endpoints that allow app-JWT auth (installation token exchange).
func (c *Client) do(ctx context.Context, method, path, token string, body, out any) error {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = strings.NewReader(string(data))
	}
	req, err := http.NewRequestWithContext(ctx, method, c.apiURL+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("github %s %s: status %d: %s", method, path, resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	if out != nil {
		if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(out); err != nil {
			return fmt.Errorf("decode github response: %w", err)
		}
	}
	return nil
}

// InstallationToken exchanges the app's JWT for an installation access token.
func (c *Client) InstallationToken(ctx context.Context, appID, pemKey string, installationID int64) (string, error) {
	jwt, err := appJWT(appID, pemKey, c.now())
	if err != nil {
		return "", err
	}
	var out struct {
		Token string `json:"token"`
	}
	path := fmt.Sprintf("/app/installations/%d/access_tokens", installationID)
	if err := c.do(ctx, http.MethodPost, path, jwt, map[string]any{}, &out); err != nil {
		return "", err
	}
	if out.Token == "" {
		return "", fmt.Errorf("github returned an empty installation token")
	}
	return out.Token, nil
}

// Hook is a repository webhook as the GitHub API models it.
type Hook struct {
	ID     int64    `json:"id"`
	Events []string `json:"events"`
	Config struct {
		URL    string `json:"url"`
		Secret string `json:"secret"`
	} `json:"config"`
}

// EnsurePushHook creates (or updates) the repository webhook that points at
// url with the given secret and the push event. ownerRepo is "owner/repo".
func (c *Client) EnsurePushHook(ctx context.Context, token, ownerRepo, url, secret string) (Hook, error) {
	owner, repo, ok := strings.Cut(strings.Trim(ownerRepo, "/"), "/")
	if !ok || owner == "" || repo == "" {
		return Hook{}, fmt.Errorf("repository must be owner/repo, got %q", ownerRepo)
	}

	var hooks []Hook
	if err := c.do(ctx, http.MethodGet, "/repos/"+owner+"/"+repo+"/hooks", token, nil, &hooks); err != nil {
		return Hook{}, err
	}
	config := map[string]any{"url": url, "content_type": "json", "secret": secret, "insecure_ssl": "0"}
	for _, h := range hooks {
		if h.Config.URL != url {
			continue
		}
		var updated Hook
		err := c.do(ctx, http.MethodPatch, fmt.Sprintf("/repos/%s/%s/hooks/%d", owner, repo, h.ID), token,
			map[string]any{"events": []string{"push"}, "config": config, "active": true}, &updated)
		return updated, err
	}

	var created Hook
	err := c.do(ctx, http.MethodPost, "/repos/"+owner+"/"+repo+"/hooks", token,
		map[string]any{"name": "web", "events": []string{"push"}, "config": config, "active": true}, &created)
	return created, err
}

// CommitStatus is the payload for the commit statuses API.
type CommitStatus struct {
	State       string `json:"state"` // error | failure | pending | success
	Context     string `json:"context"`
	Description string `json:"description,omitempty"`
	TargetURL   string `json:"target_url,omitempty"`
}

// SetCommitStatus posts a commit status (the check mark on commits/PRs).
func (c *Client) SetCommitStatus(ctx context.Context, token, ownerRepo, sha string, st CommitStatus) error {
	owner, repo, ok := strings.Cut(strings.Trim(ownerRepo, "/"), "/")
	if !ok || owner == "" || repo == "" {
		return fmt.Errorf("repository must be owner/repo, got %q", ownerRepo)
	}
	return c.do(ctx, http.MethodPost, fmt.Sprintf("/repos/%s/%s/statuses/%s", owner, repo, sha), token, st, nil)
}

// Ping triggers the test delivery for a hook, surfacing connectivity issues.
func (c *Client) PingHook(ctx context.Context, token, ownerRepo string, hookID int64) error {
	owner, repo, _ := strings.Cut(ownerRepo, "/")
	return c.do(ctx, http.MethodPost, fmt.Sprintf("/repos/%s/%s/hooks/%d/tests", owner, repo, hookID), token, map[string]any{}, nil)
}
