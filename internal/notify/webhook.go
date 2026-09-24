package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// WebhookNotifier POSTs the DeployEvent JSON to a generic webhook endpoint
// (works with most receivers: Power Automate, Discord-style gateways, etc.).
type WebhookNotifier struct {
	URL    string
	Client *http.Client
}

func (n *WebhookNotifier) httpClient() *http.Client { return httpClientOr(n.Client) }

func (n *SlackNotifier) httpClient() *http.Client { return httpClientOr(n.Client) }

func httpClientOr(c *http.Client) *http.Client {
	if c != nil {
		return c
	}
	return &http.Client{Timeout: 10 * time.Second}
}

// NotifyDeploy POSTs the event payload as application/json.
func (n *WebhookNotifier) NotifyDeploy(ctx context.Context, ev DeployEvent) error {
	if n.URL == "" {
		return nil
	}
	return postJSON(ctx, n.httpClient(), n.URL, ev)
}

// SlackNotifier posts to a Slack incoming webhook. The text mirrors the event.
type SlackNotifier struct {
	URL    string
	Client *http.Client
}

// NotifyDeploy posts a Slack-compatible {text} payload.
func (n *SlackNotifier) NotifyDeploy(ctx context.Context, ev DeployEvent) error {
	if n.URL == "" {
		return nil
	}
	text := fmt.Sprintf("winify: %s — %s (%s) is %s", ev.ProjectName, ev.ProjectID, shortSHA(ev.CommitSHA), ev.Status)
	if ev.Error != "" {
		text += ": " + ev.Error
	}
	if ev.URL != "" {
		text += " — " + ev.URL
	}
	return postJSON(ctx, n.httpClient(), n.URL, map[string]string{"text": text})
}

func postJSON(ctx context.Context, client *http.Client, url string, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("notify webhook: status %d", resp.StatusCode)
	}
	return nil
}

func shortSHA(s string) string {
	if len(s) > 7 {
		return s[:7]
	}
	return s
}
