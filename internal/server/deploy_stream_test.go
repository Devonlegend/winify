package server

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Devonlegend/winify/internal/config"
	"github.com/Devonlegend/winify/internal/models"
)

func TestDeployStartJSON(t *testing.T) {
	s, store, deployer := newTestServerFull(t)
	cookie := login(t, s)
	ctx := context.Background()
	if err := store.UpsertServer(ctx, config.Server{ID: "s1", Type: "docker", SSHHost: "h", SSHUser: "u", SSHKeyRef: "vault:k"}); err != nil {
		t.Fatalf("UpsertServer: %v", err)
	}
	if err := store.UpsertProject(ctx, config.Project{ID: "p1", Name: "A", ServerID: "s1", RepoURL: "x", Port: 8080}); err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}

	rec := postForm(t, s, "/deployments/start/p1", url.Values{}, cookie)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 (body: %s)", rec.Code, rec.Body.String())
	}
	if deployer.triggered != 1 {
		t.Fatalf("deployer.triggered = %d, want 1", deployer.triggered)
	}
	if !strings.Contains(rec.Body.String(), "deployment_id") {
		t.Fatalf("body = %s, want deployment_id", rec.Body.String())
	}
}

func TestDeployStartUnknownProject(t *testing.T) {
	s, _ := newTestServerWithStore(t)
	cookie := login(t, s)
	rec := postForm(t, s, "/deployments/start/nope", url.Values{}, cookie)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestDeploymentStreamTerminal(t *testing.T) {
	s, store := newTestServerWithStore(t)
	cookie := login(t, s)
	ctx := context.Background()

	id, err := store.CreateDeployment(ctx, models.Deployment{
		ProjectID: "p1", Status: models.DeployRunning, StartedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}
	if err := store.AppendDeploymentLog(ctx, id, "hello from the deploy\n"); err != nil {
		t.Fatalf("AppendDeploymentLog: %v", err)
	}
	if err := store.FinishDeployment(ctx, id, models.DeploySuccess, "", time.Now()); err != nil {
		t.Fatalf("FinishDeployment: %v", err)
	}

	rec := getWithCookie(t, s, fmt.Sprintf("/deployments/%d/stream", id), cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("Content-Type = %q, want text/event-stream", ct)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "event: log") || !strings.Contains(body, "hello from the deploy") {
		t.Fatalf("stream missing log event: %s", body)
	}
	if !strings.Contains(body, `"status":"success"`) {
		t.Fatalf("stream missing terminal status: %s", body)
	}
}
