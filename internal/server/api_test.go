package server

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Devonlegend/winify/internal/auth"
	"github.com/Devonlegend/winify/internal/config"
)

func apiRequest(t *testing.T, s *Server, method, path, token, body string) *httptest.ResponseRecorder {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	return rec
}

func seedToken(t *testing.T, s *Server, scope string) string {
	t.Helper()
	plaintext, hash, err := auth.NewAPIToken()
	if err != nil {
		t.Fatalf("NewAPIToken: %v", err)
	}
	if _, err := s.store.CreateAPIToken(context.Background(), "test-"+scope, hash, scope); err != nil {
		t.Fatalf("CreateAPIToken: %v", err)
	}
	return plaintext
}

func TestAPIV1RequiresToken(t *testing.T) {
	s, _ := newTestServerWithStore(t)
	rec := apiRequest(t, s, http.MethodGet, "/api/v1/projects", "", "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}

	rec = apiRequest(t, s, http.MethodGet, "/api/v1/projects", "not-a-real-token", "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("invalid token status = %d, want 401", rec.Code)
	}
}

func TestAPIV1ReadAndWriteScopes(t *testing.T) {
	s, store, deployer := newTestServerFull(t)
	ctx := context.Background()
	if err := store.UpsertServer(ctx, config.Server{ID: "s1", Type: "docker", SSHHost: "h", SSHUser: "u", SSHKeyRef: "vault:k"}); err != nil {
		t.Fatalf("UpsertServer: %v", err)
	}
	readToken := seedToken(t, s, "read")
	writeToken := seedToken(t, s, "write")

	// Read token can list.
	rec := apiRequest(t, s, http.MethodGet, "/api/v1/servers", readToken, "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "s1") {
		t.Fatalf("read list: status=%d body=%s", rec.Code, rec.Body.String())
	}

	// Read token cannot write.
	rec = apiRequest(t, s, http.MethodPost, "/api/v1/projects", readToken,
		`{"id":"p1","name":"App","server_id":"s1","repo_url":"https://x/y","port":8080}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("read write status = %d, want 403", rec.Code)
	}

	// Write token can create a project.
	rec = apiRequest(t, s, http.MethodPost, "/api/v1/projects", writeToken,
		`{"id":"p1","name":"App","server_id":"s1","repo_url":"https://x/y","port":8080}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("write create status = %d, body=%s", rec.Code, rec.Body.String())
	}
	if _, err := store.GetProject(ctx, "p1"); err != nil {
		t.Fatalf("project not created: %v", err)
	}

	// Trigger a deploy.
	rec = apiRequest(t, s, http.MethodPost, "/api/v1/projects/p1/deploy", writeToken, `{}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("deploy status = %d, body=%s", rec.Code, rec.Body.String())
	}
	if deployer.triggered != 1 {
		t.Fatalf("deployer.triggered = %d, want 1", deployer.triggered)
	}

	// History is readable.
	rec = apiRequest(t, s, http.MethodGet, "/api/v1/projects/p1/deployments", readToken, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("history status = %d", rec.Code)
	}
}

func TestAPIV1UnknownProject(t *testing.T) {
	s, _ := newTestServerWithStore(t)
	token := seedToken(t, s, "write")
	rec := apiRequest(t, s, http.MethodPost, "/api/v1/projects/nope/deploy", token, `{}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestAPIProjectRedactsEnvironmentValues(t *testing.T) {
	s, store := newTestServerWithStore(t)
	ctx := context.Background()
	if err := store.UpsertProject(ctx, config.Project{
		ID: "p-secret", Name: "Secret App", ServerID: "s1", Source: "image",
		Image: "nginx", DisableHealthCheck: true,
		Env:      map[string]string{"PASSWORD": "do-not-return"},
		BuildEnv: map[string]string{"TOKEN": "also-secret"},
	}); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	token := seedToken(t, s, "read")
	rec := apiRequest(t, s, http.MethodGet, "/api/v1/projects/p-secret", token, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "do-not-return") || strings.Contains(rec.Body.String(), "also-secret") {
		t.Fatalf("environment secret leaked: %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "[redacted]") {
		t.Fatalf("redaction marker missing: %s", rec.Body.String())
	}
}

func TestAPIDeployRejectsMalformedBody(t *testing.T) {
	s, store, _ := newTestServerFull(t)
	ctx := context.Background()
	if err := store.UpsertServer(ctx, config.Server{ID: "s1", Name: "Server", Type: "docker", SSHHost: "h", SSHUser: "u", SSHKeyRef: "vault:k"}); err != nil {
		t.Fatalf("seed server: %v", err)
	}
	if err := store.UpsertProject(ctx, config.Project{ID: "p1", Name: "App", ServerID: "s1", Source: "image", Image: "nginx", DisableHealthCheck: true}); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	token := seedToken(t, s, "write")
	rec := apiRequest(t, s, http.MethodPost, "/api/v1/projects/p1/deploy", token, `{"commit":`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestTokensPageRequiresAuth(t *testing.T) {
	s, _ := newTestServerWithStore(t)
	rec := doRequest(t, s, http.MethodGet, "/tokens")
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303 redirect to login", rec.Code)
	}
}

func TestTokensPageAndCreate(t *testing.T) {
	s, _ := newTestServerWithStore(t)
	cookie := login(t, s)

	rec := getWithCookie(t, s, "/tokens", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("tokens page status = %d", rec.Code)
	}

	rec = postForm(t, s, "/tokens", map[string][]string{"name": {"ci"}, "scope": {"read"}}, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("create status = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "cc_") {
		t.Fatalf("new token was not revealed: %s", rec.Body.String())
	}
}
