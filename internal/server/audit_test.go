package server

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Devonlegend/winify/internal/models"
)

func TestAuditAPIReturnsCommands(t *testing.T) {
	s, store := newTestServerWithStore(t)
	ctx := context.Background()
	if err := store.InsertRemoteCommand(ctx, models.RemoteCommand{
		ServerID: "server-002", ServerType: "docker", Action: "deploy",
		DeploymentID: 9, Command: "docker build -t app .", ExecutedAt: time.Now(),
	}); err != nil {
		t.Fatalf("InsertRemoteCommand: %v", err)
	}

	cookie := login(t, s)
	rec := getWithCookie(t, s, "/api/audit", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "docker build -t app .") {
		t.Fatalf("audit body missing command: %s", rec.Body.String())
	}
}

func TestAuditAPIRequiresAuth(t *testing.T) {
	s, _ := newTestServerWithStore(t)
	rec := doRequest(t, s, http.MethodGet, "/api/audit")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}
