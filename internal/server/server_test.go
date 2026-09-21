package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Devonlegend/winify/internal/auth"
	"github.com/Devonlegend/winify/internal/bootstrap"
	"github.com/Devonlegend/winify/internal/config"
	"github.com/Devonlegend/winify/internal/models"
)

// noopRunner satisfies deployment.Runner for tests that only read bootstrap
// status (no commands are executed).
type noopRunner struct{}

func (noopRunner) Run(context.Context, string) (string, error) { return "", nil }
func (noopRunner) Close() error                                { return nil }

const testPassword = "correct horse"

// stubSecrets resolves any name to a fixed value; used where a test does not
// care about the secret contents.
type stubSecrets struct{ value string }

func (s stubSecrets) Get(context.Context, string) (string, error) { return s.value, nil }
func (s stubSecrets) Put(context.Context, string, string) error   { return nil }
func (s stubSecrets) Delete(context.Context, string) error        { return nil }

// fakeDeployer records Trigger/Rollback calls and returns a fixed id.
type fakeDeployer struct {
	triggered int
	rolled    int
	err       error
}

func (f *fakeDeployer) Trigger(context.Context, config.Project, config.Server, string, string, string) (int64, error) {
	f.triggered++
	if f.err != nil {
		return 0, f.err
	}
	return 42, nil
}

func (f *fakeDeployer) Rollback(context.Context, config.Project, config.Server) (int64, error) {
	f.rolled++
	if f.err != nil {
		return 0, f.err
	}
	return 43, nil
}

func newTestServer(t *testing.T) *Server {
	t.Helper()
	srv, _, _ := newTestServerFull(t)
	return srv
}

func newTestServerWithStore(t *testing.T) (*Server, *models.Store) {
	t.Helper()
	srv, store, _ := newTestServerFull(t)
	return srv, store
}

func newTestServerFull(t *testing.T) (*Server, *models.Store, *fakeDeployer) {
	t.Helper()
	return newTestServerOpts(t, true)
}

// newTestServerNoUsers builds a server on an empty database, i.e. a fresh
// install where first-run registration is available.
func newTestServerNoUsers(t *testing.T) (*Server, *models.Store) {
	t.Helper()
	srv, store, _ := newTestServerOpts(t, false)
	return srv, store
}

func newTestServerOpts(t *testing.T, seedAdmin bool) (*Server, *models.Store, *fakeDeployer) {
	t.Helper()
	db, err := models.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := models.Migrate(db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	store := models.NewStore(db)

	if seedAdmin {
		hash, err := auth.HashPassword(testPassword)
		if err != nil {
			t.Fatalf("HashPassword: %v", err)
		}
		if err := store.UpsertUser(context.Background(), "admin", hash); err != nil {
			t.Fatalf("UpsertUser: %v", err)
		}
	}

	deployer := &fakeDeployer{}
	boot := bootstrap.New(bootstrap.Options{
		Paths:       bootstrap.DefaultPaths(`C:\ProgramData\winify`),
		EnableWinRM: true,
		Meta:        store,
		Runner:      noopRunner{},
	})
	srv, err := New(Deps{
		Cfg:             config.Default(),
		Store:           store,
		Auth:            auth.NewService(store, false, time.Hour),
		Secrets:         stubSecrets{value: "test-secret"},
		Deployer:        deployer,
		Assistant:       &fakeAssistant{},
		CredentialAdmin: stubSecrets{value: "test-secret"},
		Bootstrap:       boot,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return srv, store, deployer
}

func doRequest(t *testing.T, s *Server, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(method, path, nil))
	return rec
}

// login performs a form login and returns the session cookie.
func login(t *testing.T, s *Server) *http.Cookie {
	t.Helper()
	form := url.Values{"username": {"admin"}, "password": {testPassword}}
	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("login status = %d, want 303 (body: %s)", rec.Code, rec.Body.String())
	}
	cookies := rec.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("login did not set a session cookie")
	}
	return cookies[0]
}

func getWithCookie(t *testing.T, s *Server, path string, cookie *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	return rec
}

func TestHealthzOK(t *testing.T) {
	s := newTestServer(t)
	rec := doRequest(t, s, http.MethodGet, "/healthz")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("Content-Type = %q, want application/json", ct)
	}
	if !strings.Contains(rec.Body.String(), `"status":"ok"`) {
		t.Fatalf("body = %q, want ok status", rec.Body.String())
	}
}

func TestProtectedRoutesRedirectWhenAnonymous(t *testing.T) {
	s := newTestServer(t)
	for _, path := range []string{"/", "/dashboard", "/deployment", "/monitoring", "/assistant"} {
		rec := doRequest(t, s, http.MethodGet, path)
		if rec.Code != http.StatusSeeOther {
			t.Errorf("GET %s status = %d, want 303", path, rec.Code)
		}
		if loc := rec.Header().Get("Location"); loc != "/login" {
			t.Errorf("GET %s Location = %q, want /login", path, loc)
		}
	}
}

func TestLoginRejectsBadCredentials(t *testing.T) {
	s := newTestServer(t)
	form := url.Values{"username": {"admin"}, "password": {"nope"}}
	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Invalid username or password") {
		t.Fatalf("body = %q, want invalid-credentials message", rec.Body.String())
	}
}

func TestDashboardShellWithTabs(t *testing.T) {
	s := newTestServer(t)
	cookie := login(t, s)
	rec := getWithCookie(t, s, "/dashboard", cookie)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{"Deployment", "Monitoring", "Assistant", "Log out"} {
		if !strings.Contains(body, want) {
			t.Errorf("dashboard body missing %q", want)
		}
	}
}

func TestSectionRoutesRender(t *testing.T) {
	s := newTestServer(t)
	cookie := login(t, s)
	for _, path := range []string{"/deployment", "/monitoring", "/assistant"} {
		rec := getWithCookie(t, s, path, cookie)
		if rec.Code != http.StatusOK {
			t.Errorf("GET %s status = %d, want 200", path, rec.Code)
		}
	}
}

func TestLogoutRevokesSession(t *testing.T) {
	s := newTestServer(t)
	cookie := login(t, s)

	req := httptest.NewRequest(http.MethodPost, "/logout", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("logout status = %d, want 303", rec.Code)
	}

	after := getWithCookie(t, s, "/dashboard", cookie)
	if after.Code != http.StatusSeeOther {
		t.Fatalf("dashboard after logout = %d, want 303", after.Code)
	}
}

func TestProjectFormAndWizardRender(t *testing.T) {
	s, store := newTestServerWithStore(t)
	cookie := login(t, s)
	if err := store.UpsertProject(context.Background(), config.Project{
		ID: "p1", Name: "App", ServerID: "s1", RepoURL: "https://x/y", Branch: "main",
		PortsExposes: 3000, PortsMappings: []string{"8080:3000"},
		Env: map[string]string{"A": "1"}, BuildEnv: map[string]string{"TOKEN": "x"},
		HealthIntervalSeconds: 5, HealthRetries: 4,
	}); err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}

	rec := getWithCookie(t, s, "/projects/new", cookie)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Ports exposes") {
		t.Fatalf("wizard = %d, want 200 with ports_exposes field", rec.Code)
	}
	for _, marker := range []string{"wz-detect", "wz-runtime", "Detect build"} {
		if !strings.Contains(rec.Body.String(), marker) {
			t.Errorf("wizard missing detect control %q", marker)
		}
	}

	rec = getWithCookie(t, s, "/projects/p1?tab=settings", cookie)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "8080:3000") {
		t.Fatalf("settings tab = %d, want 200 with the port mapping", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "TOKEN=x") {
		t.Fatalf("settings tab did not render build env")
	}
}

func TestAssetServed(t *testing.T) {
	s := newTestServer(t)
	rec := doRequest(t, s, http.MethodGet, "/assets/css/app.css")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "background") {
		t.Fatalf("body = %q, want css content", rec.Body.String())
	}
}
