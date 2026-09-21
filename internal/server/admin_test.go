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

func TestAdminProjectPortsEnvAndHealth(t *testing.T) {
	s, store := newTestServerWithStore(t)
	cookie := login(t, s)
	ctx := context.Background()
	if err := store.UpsertServer(ctx, config.Server{ID: "s1", Type: "docker", SSHHost: "h", SSHUser: "u", SSHKeyRef: "vault:k"}); err != nil {
		t.Fatalf("UpsertServer: %v", err)
	}

	rec := postForm(t, s, "/projects", url.Values{
		"id":                          {"p1"},
		"name":                        {"App"},
		"server_id":                   {"s1"},
		"repo_url":                    {"https://github.com/x/y"},
		"branch":                      {"main"},
		"ports_exposes":               {"3000"},
		"ports_mappings":              {"8080:3000\n9090:9090"},
		"health_path":                 {"/healthz"},
		"health_interval_seconds":     {"5"},
		"health_timeout_seconds":      {"2"},
		"health_retries":              {"4"},
		"health_start_period_seconds": {"1"},
		"env":                         {"A=1"},
		"build_env":                   {"TOKEN=abc"},
	}, cookie)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("save status = %d, want 303 (body: %s)", rec.Code, rec.Body.String())
	}
	p, err := store.GetProject(ctx, "p1")
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	if p.PortsExposes != 3000 || len(p.PortsMappings) != 2 || p.PortsMappings[0] != "8080:3000" {
		t.Fatalf("ports = exposes %d mappings %v", p.PortsExposes, p.PortsMappings)
	}
	if p.EffectiveHostPort() != 8080 {
		t.Fatalf("EffectiveHostPort = %d, want 8080", p.EffectiveHostPort())
	}
	if p.Env["A"] != "1" || p.BuildEnv["TOKEN"] != "abc" {
		t.Fatalf("env = %v build_env = %v", p.Env, p.BuildEnv)
	}
	if p.HealthIntervalSeconds != 5 || p.HealthTimeoutSeconds != 2 || p.HealthRetries != 4 || p.HealthStartPeriodSeconds != 1 {
		t.Fatalf("health timing = %+v", p)
	}
}

func TestAdminProjectRejectsBadPortMapping(t *testing.T) {
	s, store := newTestServerWithStore(t)
	cookie := login(t, s)
	if err := store.UpsertServer(context.Background(), config.Server{ID: "s1", Type: "docker", SSHHost: "h", SSHUser: "u", SSHKeyRef: "vault:k"}); err != nil {
		t.Fatalf("UpsertServer: %v", err)
	}

	rec := postForm(t, s, "/projects", url.Values{
		"id": {"p1"}, "name": {"App"}, "server_id": {"s1"},
		"repo_url": {"https://github.com/x/y"}, "ports_exposes": {"3000"},
		"ports_mappings": {"not-a-port"},
	}, cookie)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestAdminCreateWindowsServiceProject(t *testing.T) {
	s, store := newTestServerWithStore(t)
	cookie := login(t, s)
	ctx := context.Background()

	rec := postForm(t, s, "/servers", url.Values{
		"id":             {"s-ws"},
		"name":           {"Worker host"},
		"type":           {"winsvc"},
		"winrm_endpoint": {"https://10.0.0.6:5986/wsman"},
		"winrm_user":     {"deploy"},
		"credential_ref": {"vault:ws"},
		"nssm_path":      {`C:\tools\nssm.exe`},
	}, cookie)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("server save status = %d, want 303 (body: %s)", rec.Code, rec.Body.String())
	}
	srv, err := store.GetServer(ctx, "s-ws")
	if err != nil {
		t.Fatalf("GetServer: %v", err)
	}
	if srv.NSSMPath != `C:\tools\nssm.exe` {
		t.Fatalf("nssm_path = %q", srv.NSSMPath)
	}

	rec = postForm(t, s, "/projects", url.Values{
		"id":               {"p-ws"},
		"name":             {"Worker"},
		"server_id":        {"s-ws"},
		"repo_url":         {"https://github.com/x/worker"},
		"port":             {"8080"},
		"service_name":     {"MyWorker"},
		"service_exe":      {`C:\apps\worker\worker.exe`},
		"service_work_dir": {`C:\apps\worker`},
		"caddy_mode":       {"static"},
		"runtime":          {"python"},
	}, cookie)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("project save status = %d, want 303 (body: %s)", rec.Code, rec.Body.String())
	}
	p, err := store.GetProject(ctx, "p-ws")
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	if p.Strategy != config.ServerTypeWindowsService {
		t.Fatalf("strategy = %q, want winsvc", p.Strategy)
	}
	if p.ServiceName != "MyWorker" || p.ServiceExe == "" || p.CaddyMode != "static" {
		t.Fatalf("project = %+v", p)
	}
	if p.Runtime != "python" {
		t.Fatalf("runtime = %q, want python", p.Runtime)
	}
}

func TestAdminWindowsServiceProjectValidation(t *testing.T) {
	s, store := newTestServerWithStore(t)
	cookie := login(t, s)
	ctx := context.Background()
	if err := store.UpsertServer(ctx, config.Server{
		ID: "s-ws", Name: "WS", Type: config.ServerTypeWindowsService,
		WinRMEndpoint: "https://10.0.0.6:5986/wsman", WinRMUser: "deploy", CredentialRef: "vault:ws",
	}); err != nil {
		t.Fatalf("UpsertServer: %v", err)
	}

	rec := postForm(t, s, "/projects", url.Values{
		"id": {"p-ws"}, "name": {"Worker"}, "server_id": {"s-ws"},
		"repo_url": {"https://github.com/x/worker"}, "port": {"8080"},
	}, cookie)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "service_name is required") {
		t.Fatalf("missing validation message: %s", rec.Body.String())
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
