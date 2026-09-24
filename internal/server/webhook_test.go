package server

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Devonlegend/winify/internal/config"
	"github.com/Devonlegend/winify/internal/deployment"
	"github.com/Devonlegend/winify/internal/models"
)

const webhookSecret = "test-secret"

func signBody(body []byte) string {
	mac := hmac.New(sha256.New, []byte(webhookSecret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func seedWebhookProject(t *testing.T, store *models.Store) {
	t.Helper()
	ctx := context.Background()
	if err := store.UpsertServer(ctx, config.Server{
		ID: "server-002", Type: "docker", SSHHost: "10.0.0.9",
		SSHUser: "deploy", SSHKeyRef: "vault:server-002-ssh", SSHPort: 22,
	}); err != nil {
		t.Fatalf("UpsertServer: %v", err)
	}
	if err := store.UpsertProject(ctx, config.Project{
		ID: "proj-001", Name: "Storefront", ServerID: "server-002",
		Strategy: "dockerfile", RepoURL: "https://example.com/repo.git",
		Branch: "main", WebhookSecretRef: "vault:proj-001-webhook",
	}); err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}
}

func postWebhook(t *testing.T, s *Server, path string, body []byte, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	return rec
}

func githubHeaders(body []byte) map[string]string {
	return map[string]string{
		"X-GitHub-Event":      "push",
		"X-Hub-Signature-256": signBody(body),
	}
}

func TestGitHubWebhookValid(t *testing.T) {
	s, store, deployer := newTestServerFull(t)
	seedWebhookProject(t, store)
	body := []byte(`{"ref":"refs/heads/main","after":"abc123"}`)

	rec := postWebhook(t, s, "/webhooks/github/proj-001", body, githubHeaders(body))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 (body: %s)", rec.Code, rec.Body.String())
	}
	if deployer.triggered != 1 {
		t.Fatalf("triggered = %d, want 1", deployer.triggered)
	}
}

func TestGitHubWebhookRejectsDuplicateDelivery(t *testing.T) {
	s, store, deployer := newTestServerFull(t)
	seedWebhookProject(t, store)
	body := []byte(`{"ref":"refs/heads/main","after":"abc123"}`)
	headers := githubHeaders(body)
	headers["X-GitHub-Delivery"] = "delivery-123"

	first := postWebhook(t, s, "/webhooks/github/proj-001", body, headers)
	second := postWebhook(t, s, "/webhooks/github/proj-001", body, headers)
	if first.Code != http.StatusAccepted || second.Code != http.StatusAccepted {
		t.Fatalf("statuses = %d, %d, want 202/202", first.Code, second.Code)
	}
	if deployer.triggered != 1 {
		t.Fatalf("triggered = %d, want 1 for a duplicate delivery", deployer.triggered)
	}
	if !bytes.Contains(second.Body.Bytes(), []byte("duplicate delivery")) {
		t.Fatalf("duplicate response = %s", second.Body.String())
	}
}

func TestGitHubWebhookBadSignature(t *testing.T) {
	s, store, deployer := newTestServerFull(t)
	seedWebhookProject(t, store)
	body := []byte(`{"ref":"refs/heads/main","after":"abc123"}`)

	headers := githubHeaders(body)
	headers["X-Hub-Signature-256"] = "sha256=" + hex.EncodeToString([]byte("deadbeef"))
	rec := postWebhook(t, s, "/webhooks/github/proj-001", body, headers)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if deployer.triggered != 0 {
		t.Fatalf("triggered = %d, want 0", deployer.triggered)
	}
}

func TestGitHubWebhookMissingSignature(t *testing.T) {
	s, store, deployer := newTestServerFull(t)
	seedWebhookProject(t, store)
	body := []byte(`{"ref":"refs/heads/main","after":"abc123"}`)

	rec := postWebhook(t, s, "/webhooks/github/proj-001", body, map[string]string{"X-GitHub-Event": "push"})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if deployer.triggered != 0 {
		t.Fatalf("triggered = %d, want 0", deployer.triggered)
	}
}

func TestGitHubWebhookTamperedBody(t *testing.T) {
	s, store, deployer := newTestServerFull(t)
	seedWebhookProject(t, store)
	signed := []byte(`{"ref":"refs/heads/main","after":"abc123"}`)
	headers := githubHeaders(signed)

	// Send a different body with the signature for the original.
	tampered := []byte(`{"ref":"refs/heads/main","after":"evil999"}`)
	rec := postWebhook(t, s, "/webhooks/github/proj-001", tampered, headers)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if deployer.triggered != 0 {
		t.Fatalf("triggered = %d, want 0", deployer.triggered)
	}
}

func TestWebhookBranchMismatchIgnored(t *testing.T) {
	s, store, deployer := newTestServerFull(t)
	seedWebhookProject(t, store)
	body := []byte(`{"ref":"refs/heads/dev","after":"abc123"}`)

	rec := postWebhook(t, s, "/webhooks/github/proj-001", body, githubHeaders(body))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", rec.Code)
	}
	if deployer.triggered != 0 {
		t.Fatalf("triggered = %d, want 0 for wrong branch", deployer.triggered)
	}
}

func TestWebhookNonPushIgnored(t *testing.T) {
	s, store, deployer := newTestServerFull(t)
	seedWebhookProject(t, store)
	body := []byte(`{"zen":"keep it simple"}`)

	rec := postWebhook(t, s, "/webhooks/github/proj-001", body, map[string]string{"X-GitHub-Event": "ping"})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", rec.Code)
	}
	if deployer.triggered != 0 {
		t.Fatalf("triggered = %d, want 0", deployer.triggered)
	}
}

func TestWebhookUnknownProject(t *testing.T) {
	s, _, _ := newTestServerFull(t)
	body := []byte(`{"ref":"refs/heads/main"}`)
	rec := postWebhook(t, s, "/webhooks/github/nope", body, githubHeaders(body))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestGitLabWebhook(t *testing.T) {
	s, store, deployer := newTestServerFull(t)
	seedWebhookProject(t, store)
	body := []byte(`{"ref":"refs/heads/main","checkout_sha":"def456"}`)

	valid := postWebhook(t, s, "/webhooks/gitlab/proj-001", body, map[string]string{
		"X-Gitlab-Event": "Push Hook",
		"X-Gitlab-Token": webhookSecret,
	})
	if valid.Code != http.StatusAccepted || deployer.triggered != 1 {
		t.Fatalf("valid gitlab: status=%d triggered=%d", valid.Code, deployer.triggered)
	}

	invalid := postWebhook(t, s, "/webhooks/gitlab/proj-001", body, map[string]string{
		"X-Gitlab-Event": "Push Hook",
		"X-Gitlab-Token": "wrong",
	})
	if invalid.Code != http.StatusUnauthorized {
		t.Fatalf("invalid gitlab: status=%d, want 401", invalid.Code)
	}
	if deployer.triggered != 1 {
		t.Fatalf("triggered = %d, want 1", deployer.triggered)
	}
}

func TestWebhookDeployInProgress(t *testing.T) {
	s, store, deployer := newTestServerFull(t)
	seedWebhookProject(t, store)
	deployer.err = deployment.ErrDeployInProgress
	body := []byte(`{"ref":"refs/heads/main","after":"abc123"}`)

	rec := postWebhook(t, s, "/webhooks/github/proj-001", body, githubHeaders(body))
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rec.Code)
	}
}
