package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/Devonlegend/winify/internal/config"
)

func postForm(t *testing.T, s *Server, path string, form url.Values, cookie *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if cookie != nil {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	return rec
}

func TestAdminCreateServer(t *testing.T) {
	s, store := newTestServerWithStore(t)
	cookie := login(t, s)

	rec := postForm(t, s, "/servers", url.Values{
		"id":          {"s-docker"},
		"name":        {"Docker"},
		"type":        {"docker"},
		"ssh_host":    {"1.2.3.4"},
		"ssh_user":    {"deploy"},
		"ssh_key_ref": {"vault:k"},
	}, cookie)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303 (body: %s)", rec.Code, rec.Body.String())
	}
	srv, err := store.GetServer(context.Background(), "s-docker")
	if err != nil {
		t.Fatalf("GetServer: %v", err)
	}
	if srv.SSHHost != "1.2.3.4" || srv.SSHUser != "deploy" {
		t.Fatalf("server = %+v", srv)
	}
}

func TestAdminCreateServerValidation(t *testing.T) {
	s, _ := newTestServerWithStore(t)
	cookie := login(t, s)

	rec := postForm(t, s, "/servers", url.Values{"id": {"x"}, "name": {"X"}, "type": {"docker"}}, cookie)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "ssh_host is required") {
		t.Fatalf("missing validation message: %s", rec.Body.String())
	}
}

func TestAdminCreateProjectAndDeploy(t *testing.T) {
	s, store, deployer := newTestServerFull(t)
	cookie := login(t, s)
	ctx := context.Background()
	if err := store.UpsertServer(ctx, config.Server{ID: "s1", Type: "docker", SSHHost: "h", SSHUser: "u", SSHKeyRef: "vault:k"}); err != nil {
		t.Fatalf("UpsertServer: %v", err)
	}

	rec := postForm(t, s, "/projects", url.Values{
		"id":          {"p1"},
		"name":        {"App"},
		"server_id":   {"s1"},
		"repo_url":    {"https://github.com/x/y"},
		"branch":      {"main"},
		"port":        {"8080"},
		"health_path": {"/healthz"},
	}, cookie)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("save status = %d, want 303 (body: %s)", rec.Code, rec.Body.String())
	}
	p, err := store.GetProject(ctx, "p1")
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	if p.Strategy != "docker" {
		t.Fatalf("strategy = %q, want docker (derived from server type)", p.Strategy)
	}

	rec = postForm(t, s, "/projects/deploy/p1", url.Values{}, cookie)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("deploy status = %d, want 303", rec.Code)
	}
	if deployer.triggered != 1 {
		t.Fatalf("deployer.triggered = %d, want 1", deployer.triggered)
	}
}

func TestAdminServerDeleteBlockedByProjects(t *testing.T) {
	s, store := newTestServerWithStore(t)
	cookie := login(t, s)
	ctx := context.Background()
	if err := store.UpsertServer(ctx, config.Server{ID: "s1", Type: "docker", SSHHost: "h", SSHUser: "u", SSHKeyRef: "vault:k"}); err != nil {
		t.Fatalf("UpsertServer: %v", err)
	}
	if err := store.UpsertProject(ctx, config.Project{ID: "p1", Name: "A", ServerID: "s1", RepoURL: "x", Branch: "main", Port: 80, HealthPath: "/"}); err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}

	rec := postForm(t, s, "/servers/delete", url.Values{"id": {"s1"}}, cookie)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rec.Code)
	}
	if _, err := store.GetServer(ctx, "s1"); err != nil {
		t.Fatal("server was deleted despite having a project")
	}
}

func TestResourcePageAndEnvSave(t *testing.T) {
	s, store := newTestServerWithStore(t)
	cookie := login(t, s)
	ctx := context.Background()
	if err := store.UpsertServer(ctx, config.Server{ID: "s1", Type: "docker", SSHHost: "h", SSHUser: "u", SSHKeyRef: "vault:k"}); err != nil {
		t.Fatalf("UpsertServer: %v", err)
	}
	if err := store.UpsertProject(ctx, config.Project{
		ID: "r1", Name: "App", ServerID: "s1", RepoURL: "x", Branch: "main",
		Port: 8080, HealthPath: "/", ProjectGroup: "Storefront", Environment: "production",
	}); err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}

	for _, path := range []string{"/projects/r1", "/projects/r1?tab=deployments", "/projects/r1?tab=environment", "/projects/r1?tab=settings"} {
		rec := getWithCookie(t, s, path, cookie)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s status = %d, want 200", path, rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "App") {
			t.Fatalf("GET %s missing resource name", path)
		}
	}

	rec := postForm(t, s, "/projects/env", url.Values{"id": {"r1"}, "env": {"FOO=bar\nBAZ=qux"}}, cookie)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("env save status = %d, want 303", rec.Code)
	}
	p, err := store.GetProject(ctx, "r1")
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	if p.Env["FOO"] != "bar" || p.Env["BAZ"] != "qux" {
		t.Fatalf("env = %v", p.Env)
	}
}

func TestProjectsGrouping(t *testing.T) {
	s, store := newTestServerWithStore(t)
	cookie := login(t, s)
	ctx := context.Background()
	if err := store.UpsertServer(ctx, config.Server{ID: "s1", Type: "docker", SSHHost: "h", SSHUser: "u", SSHKeyRef: "vault:k"}); err != nil {
		t.Fatalf("UpsertServer: %v", err)
	}
	for _, p := range []config.Project{
		{ID: "a", Name: "Alpha App", ServerID: "s1", RepoURL: "x", Port: 80, ProjectGroup: "Alpha", Environment: "production"},
		{ID: "b", Name: "Beta App", ServerID: "s1", RepoURL: "x", Port: 81, ProjectGroup: "Beta", Environment: "staging"},
	} {
		if err := store.UpsertProject(ctx, p); err != nil {
			t.Fatalf("UpsertProject: %v", err)
		}
	}

	rec := getWithCookie(t, s, "/projects", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{"Alpha", "Beta", "production", "staging", "Alpha App", "Beta App"} {
		if !strings.Contains(body, want) {
			t.Errorf("projects page missing %q", want)
		}
	}
}

func TestResourceUnknown(t *testing.T) {
	s, _ := newTestServerWithStore(t)
	cookie := login(t, s)
	rec := getWithCookie(t, s, "/projects/nope", cookie)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestAdminProjectImageSourceValidation(t *testing.T) {
	s, store := newTestServerWithStore(t)
	cookie := login(t, s)
	ctx := context.Background()
	if err := store.UpsertServer(ctx, config.Server{ID: "s1", Type: "docker", SSHHost: "h", SSHUser: "u", SSHKeyRef: "vault:k"}); err != nil {
		t.Fatalf("UpsertServer: %v", err)
	}

	// image source without an image is rejected.
	rec := postForm(t, s, "/projects", url.Values{
		"id": {"p1"}, "name": {"A"}, "server_id": {"s1"}, "repo_url": {"x"},
		"port": {"8080"}, "source": {"image"},
	}, cookie)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "image is required") {
		t.Fatalf("expected image validation error, got status=%d body=%s", rec.Code, rec.Body.String())
	}

	// with an image it saves.
	rec = postForm(t, s, "/projects", url.Values{
		"id": {"p1"}, "name": {"A"}, "server_id": {"s1"}, "repo_url": {"x"},
		"port": {"8080"}, "source": {"image"}, "image": {"nginx:1.27"},
	}, cookie)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("save status = %d, want 303", rec.Code)
	}
	p, err := store.GetProject(ctx, "p1")
	if err != nil || p.Source != "image" || p.Image != "nginx:1.27" {
		t.Fatalf("project = %+v err=%v", p, err)
	}
}

func TestAdminCredentialsPageAndSave(t *testing.T) {
	s, store := newTestServerWithStore(t)
	cookie := login(t, s)
	ctx := context.Background()
	if err := store.PutCredential(ctx, "existing-key", []byte("n"), []byte("c")); err != nil {
		t.Fatalf("PutCredential: %v", err)
	}

	rec := getWithCookie(t, s, "/credentials", cookie)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "existing-key") {
		t.Fatalf("credentials page: status=%d", rec.Code)
	}

	rec = postForm(t, s, "/credentials", url.Values{"name": {"new-key"}, "secret": {"s3cr3t"}}, cookie)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("save status = %d, want 303", rec.Code)
	}
}

func TestAdminPagesRequireAuth(t *testing.T) {
	s, _ := newTestServerWithStore(t)
	for _, path := range []string{"/servers", "/projects", "/credentials"} {
		rec := doRequest(t, s, http.MethodGet, path)
		if rec.Code != http.StatusSeeOther {
			t.Errorf("GET %s status = %d, want 303 redirect to login", path, rec.Code)
		}
	}
}
