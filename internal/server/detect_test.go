package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Devonlegend/winify/internal/auth"
	"github.com/Devonlegend/winify/internal/config"
	"github.com/Devonlegend/winify/internal/deployment"
	"github.com/Devonlegend/winify/internal/detect"
	"github.com/Devonlegend/winify/internal/models"
)

// fakeDetectRunner serves a canned repository listing and file contents.
type fakeDetectRunner struct {
	listed string
	files  map[string]string
}

func (f *fakeDetectRunner) Run(_ context.Context, cmd string) (string, error) {
	switch {
	case strings.Contains(cmd, "Get-ChildItem"):
		return f.listed, nil
	case strings.Contains(cmd, "ToBase64String"):
		for name, content := range f.files {
			if strings.Contains(cmd, name) {
				return base64.StdEncoding.EncodeToString([]byte(content)), nil
			}
		}
		return "", errors.New("file not found")
	}
	return "", nil
}

func (f *fakeDetectRunner) Close() error { return nil }

func newDetectTestServer(t *testing.T, servers []config.Server, runner deployment.Runner) *Server {
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
	ctx := context.Background()
	hash, err := auth.HashPassword(testPassword)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if err := store.UpsertUser(ctx, "admin", hash); err != nil {
		t.Fatalf("UpsertUser: %v", err)
	}
	for _, srv := range servers {
		if err := store.UpsertServer(ctx, srv); err != nil {
			t.Fatalf("UpsertServer: %v", err)
		}
	}
	s, err := New(Deps{
		Cfg:             config.Default(),
		Store:           store,
		Auth:            auth.NewService(store, false, time.Hour),
		Secrets:         stubSecrets{value: "x"},
		Deployer:        &fakeDeployer{},
		Assistant:       &fakeAssistant{},
		CredentialAdmin: stubSecrets{value: "x"},
		RunnerFactory: func(context.Context, config.Server, deployment.SecretResolver) (deployment.Runner, error) {
			return runner, nil
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s
}

func TestProjectDetectReturnsPlan(t *testing.T) {
	runner := &fakeDetectRunner{
		listed: "requirements.txt\napp.py\n",
		files: map[string]string{
			"requirements.txt": "fastapi\nuvicorn\n",
			"app.py":           "from fastapi import FastAPI\napp = FastAPI()\n",
		},
	}
	s := newDetectTestServer(t, []config.Server{
		{ID: "local", Type: config.ServerTypeWindowsService, Local: true},
	}, runner)
	cookie := login(t, s)

	rec := postForm(t, s, "/projects/detect", url.Values{
		"repo_url":  {"https://example.com/app.git"},
		"server_id": {"local"},
	}, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /projects/detect = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var resp struct {
		Detected bool         `json:"detected"`
		Plan     *detect.Plan `json:"plan"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v (body: %s)", err, rec.Body.String())
	}
	if !resp.Detected || resp.Plan == nil {
		t.Fatalf("response = %s, want a detected plan", rec.Body.String())
	}
	if resp.Plan.Language != detect.LanguagePython || resp.Plan.Framework != "FastAPI" {
		t.Fatalf("plan = %+v, want python/FastAPI", resp.Plan)
	}
}

func TestProjectDetectValidatesInput(t *testing.T) {
	s := newDetectTestServer(t, []config.Server{
		{ID: "local", Type: config.ServerTypeWindowsService, Local: true},
	}, &fakeDetectRunner{})
	cookie := login(t, s)

	rec := postForm(t, s, "/projects/detect", url.Values{"server_id": {"local"}}, cookie)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing repo_url = %d, want 400", rec.Code)
	}
}

func TestProjectDetectRejectsNonWindowsTarget(t *testing.T) {
	s := newDetectTestServer(t, []config.Server{
		{ID: "web", Type: config.ServerTypeDocker},
	}, &fakeDetectRunner{})
	cookie := login(t, s)

	rec := postForm(t, s, "/projects/detect", url.Values{
		"repo_url":  {"https://example.com/app.git"},
		"server_id": {"web"},
	}, cookie)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("docker target = %d, want 400", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Windows") {
		t.Errorf("error should explain the restriction: %s", rec.Body.String())
	}
}
