package server

import (
	"context"
	"net/http"
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

func TestSetupPageListsSteps(t *testing.T) {
	s, store := newTestServerWithStore(t)
	cookie := login(t, s)
	if err := store.SetMeta(context.Background(), "bootstrap.dirs", "done"); err != nil {
		t.Fatalf("SetMeta: %v", err)
	}

	rec := getWithCookie(t, s, "/setup", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /setup = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, step := range []string{"dirs", "nssm", "winrm"} {
		if !strings.Contains(body, step) {
			t.Errorf("setup page missing step %q", step)
		}
	}
	if !strings.Contains(body, "done") {
		t.Error("setup page did not render the recorded status")
	}
}

func TestSetupLocalTargetCreatesServer(t *testing.T) {
	s, store := newTestServerWithStore(t)
	cookie := login(t, s)

	rec := getWithCookie(t, s, "/setup", cookie)
	if !strings.Contains(rec.Body.String(), "Create local target") {
		t.Fatalf("local target form not shown:\n%s", rec.Body.String())
	}

	rec = postForm(t, s, "/setup/local-target", url.Values{
		"public_ip": {"203.0.113.5"},
	}, cookie)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("POST /setup/local-target = %d, want 303 (body: %s)", rec.Code, rec.Body.String())
	}

	srv, err := store.GetServer(context.Background(), "local")
	if err != nil {
		t.Fatalf("local server not created: %v", err)
	}
	if srv.Type != config.ServerTypeWindowsService || !srv.Local {
		t.Fatalf("server = %+v, want a local winsvc server", srv)
	}
	if srv.PublicIP != "203.0.113.5" {
		t.Fatalf("public_ip = %q", srv.PublicIP)
	}

	rec = getWithCookie(t, s, "/setup", cookie)
	if strings.Contains(rec.Body.String(), "Create local target") {
		t.Error("local target form still shown after creation")
	}
}

func TestSetupRunInvokesBootstrapWhenElevated(t *testing.T) {
	db, err := models.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := models.Migrate(db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	store := models.NewStore(db)
	hash, err := auth.HashPassword(testPassword)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if err := store.UpsertUser(context.Background(), "admin", hash); err != nil {
		t.Fatalf("UpsertUser: %v", err)
	}

	ran := make(chan struct{}, 1)
	boot := bootstrap.New(bootstrap.Options{
		Paths:  bootstrap.DefaultPaths(`C:\ProgramData\winify`),
		Meta:   store,
		Runner: noopRunner{},
	})
	srv, err := New(Deps{
		Cfg:             config.Default(),
		Store:           store,
		Auth:            auth.NewService(store, false, time.Hour),
		Secrets:         stubSecrets{value: "x"},
		Deployer:        &fakeDeployer{},
		Assistant:       &fakeAssistant{},
		CredentialAdmin: stubSecrets{value: "x"},
		Bootstrap:       boot,
		BootstrapRun: func(context.Context) error {
			ran <- struct{}{}
			return nil
		},
		BootstrapElevated: func(context.Context) (bool, error) { return true, nil },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	cookie := login(t, srv)
	rec := postForm(t, srv, "/setup/run", url.Values{}, cookie)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("POST /setup/run = %d, want 303 (body: %s)", rec.Code, rec.Body.String())
	}
	select {
	case <-ran:
	case <-time.After(2 * time.Second):
		t.Fatal("bootstrap was not run when elevated")
	}
}
