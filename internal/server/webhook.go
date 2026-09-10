package server

import (
	"context"
	"errors"
	"io"
	"log"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/Devonlegend/winify/internal/config"
	"github.com/Devonlegend/winify/internal/deployment"
	"github.com/Devonlegend/winify/internal/models"
)

// maxWebhookBody caps how much of a webhook body we will read.
const maxWebhookBody = 1 << 20 // 1 MiB

func (s *Server) handleGitHubWebhook(w http.ResponseWriter, r *http.Request) {
	s.handleWebhook(w, r, "github")
}

func (s *Server) handleGitLabWebhook(w http.ResponseWriter, r *http.Request) {
	s.handleWebhook(w, r, "gitlab")
}

// handleWebhook verifies the provider signature against the project's secret
// before doing anything else. Unsigned or tampered requests are rejected with
// 401; ignored (non-push, wrong branch) events return 202.
func (s *Server) handleWebhook(w http.ResponseWriter, r *http.Request, provider string) {
	ctx := r.Context()
	projectID := chi.URLParam(r, "projectID")

	project, err := s.store.GetProject(ctx, projectID)
	if errors.Is(err, models.ErrNotFound) {
		http.Error(w, "unknown project", http.StatusNotFound)
		return
	}
	if err != nil {
		log.Printf("webhook: get project: %v", err)
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}

	secret, err := s.webhookSecret(ctx, project)
	if err != nil {
		log.Printf("webhook %s: %v", projectID, err)
		http.Error(w, "project not configured for webhooks", http.StatusNotFound)
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxWebhookBody))
	if err != nil {
		http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
		return
	}

	var event deployment.PushEvent
	switch provider {
	case "github":
		if r.Header.Get("X-GitHub-Event") != "push" {
			writeJSON(w, http.StatusAccepted, map[string]string{"status": "ignored", "reason": "not a push event"})
			return
		}
		if err := deployment.VerifyGitHubSignature(secret, body, r.Header.Get("X-Hub-Signature-256")); err != nil {
			http.Error(w, "invalid signature", http.StatusUnauthorized)
			return
		}
		if event, err = deployment.ParseGitHubPush(body); err != nil {
			http.Error(w, "invalid payload", http.StatusBadRequest)
			return
		}
	case "gitlab":
		if r.Header.Get("X-Gitlab-Event") != "Push Hook" {
			writeJSON(w, http.StatusAccepted, map[string]string{"status": "ignored", "reason": "not a push event"})
			return
		}
		if err := deployment.VerifyGitLabToken(secret, r.Header.Get("X-Gitlab-Token")); err != nil {
			http.Error(w, "invalid signature", http.StatusUnauthorized)
			return
		}
		if event, err = deployment.ParseGitLabPush(body); err != nil {
			http.Error(w, "invalid payload", http.StatusBadRequest)
			return
		}
	default:
		http.Error(w, "unsupported provider", http.StatusBadRequest)
		return
	}

	if project.Branch != "" && deployment.BranchOf(event.Ref) != project.Branch {
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "ignored", "reason": "branch mismatch"})
		return
	}

	srv, err := s.store.GetServer(ctx, project.ServerID)
	if err != nil {
		log.Printf("webhook %s: get server: %v", projectID, err)
		http.Error(w, "project target not configured", http.StatusInternalServerError)
		return
	}

	id, err := s.deployer.Trigger(ctx, project, srv, "webhook", event.Commit, event.Ref)
	if errors.Is(err, deployment.ErrDeployInProgress) {
		http.Error(w, "a deployment is already in progress", http.StatusConflict)
		return
	}
	if err != nil {
		log.Printf("webhook %s: trigger: %v", projectID, err)
		http.Error(w, "failed to start deployment", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"status": "accepted", "deployment_id": id})
}

// webhookSecret resolves the project's encrypted webhook secret. The value is
// used only for verification and is never logged.
func (s *Server) webhookSecret(ctx context.Context, p config.Project) (string, error) {
	if p.WebhookSecretRef == "" {
		return "", errors.New("no webhook_secret_ref configured")
	}
	name := strings.TrimPrefix(p.WebhookSecretRef, "vault:")
	if name == "" {
		return "", errors.New("empty webhook secret ref")
	}
	return s.secrets.Get(ctx, name)
}
