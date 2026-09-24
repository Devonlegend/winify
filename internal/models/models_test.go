package models

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Devonlegend/winify/internal/config"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

type testSecretCodec struct{}

func (testSecretCodec) Encrypt(name, value string) (string, error) {
	return base64.RawURLEncoding.EncodeToString([]byte(name + "\x00" + value)), nil
}
func (testSecretCodec) Decrypt(name, value string) (string, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return "", err
	}
	parts := strings.SplitN(string(decoded), "\x00", 2)
	if len(parts) != 2 || parts[0] != name {
		return "", errors.New("codec AAD mismatch")
	}
	return parts[1], nil
}

func TestSensitiveConfigValuesAreEncryptedAtRest(t *testing.T) {
	db := openTestDB(t)
	if err := Migrate(db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	store := NewStore(db)
	store.SetSecretCodec(testSecretCodec{})
	ctx := context.Background()
	if err := store.UpsertProject(ctx, config.Project{ID: "p1", Name: "App", ServerID: "s1", Env: map[string]string{"SECRET": "do-not-store"}, DisableHealthCheck: true}); err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}
	var raw string
	if err := db.QueryRow(`SELECT env_json FROM projects WHERE id='p1'`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, "do-not-store") || !strings.HasPrefix(raw, encryptedSecretPrefix) {
		t.Fatalf("project env was not encrypted: %s", raw)
	}
	got, err := store.GetProject(ctx, "p1")
	if err != nil || got.Env["SECRET"] != "do-not-store" {
		t.Fatalf("decrypted project = %+v, %v", got, err)
	}
	if err := store.UpsertSharedVariable(ctx, SharedVariable{Scope: "project", ScopeID: "Default", Key: "TOKEN", Value: "shared-secret"}); err != nil {
		t.Fatalf("UpsertSharedVariable: %v", err)
	}
	if err := db.QueryRow(`SELECT value FROM shared_variables WHERE key='TOKEN'`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, "shared-secret") {
		t.Fatalf("shared variable was not encrypted: %s", raw)
	}
}

func TestMigrateAppliesAndIsIdempotent(t *testing.T) {
	db := openTestDB(t)

	if err := Migrate(db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	var applied int
	if err := db.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&applied); err != nil {
		t.Fatalf("count schema_migrations: %v", err)
	}
	if applied < 1 {
		t.Fatalf("applied migrations = %d, want >= 1", applied)
	}

	// Migrate is a no-op when called again: versions are recorded already.
	if err := Migrate(db); err != nil {
		t.Fatalf("second Migrate: %v", err)
	}

	// The migration content actually ran: app_meta must exist.
	var n int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='app_meta'`,
	).Scan(&n); err != nil {
		t.Fatalf("check app_meta: %v", err)
	}
	if n != 1 {
		t.Fatalf("app_meta table count = %d, want 1", n)
	}
}

func TestMigrateOpenAllowsRoundTrip(t *testing.T) {
	db := openTestDB(t)
	if err := Migrate(db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO app_meta (key, value) VALUES ('installed_at', datetime('now'))`); err != nil {
		t.Fatalf("insert app_meta: %v", err)
	}
	var v string
	if err := db.QueryRow(`SELECT value FROM app_meta WHERE key = 'installed_at'`).Scan(&v); err != nil {
		t.Fatalf("read app_meta: %v", err)
	}
	if v == "" {
		t.Fatal("read empty value from app_meta")
	}
}

func TestServerMonitoringFieldsRoundTrip(t *testing.T) {
	db := openTestDB(t)
	if err := Migrate(db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	store := NewStore(db)
	want := config.Server{ID: "s1", Name: "Server", Type: config.ServerTypeDocker, SSHHost: "host", Services: []string{"docker", "nginx"}, DiskPath: "/data"}
	if err := store.UpsertServer(context.Background(), want); err != nil {
		t.Fatalf("UpsertServer: %v", err)
	}
	got, err := store.GetServer(context.Background(), "s1")
	if err != nil {
		t.Fatalf("GetServer: %v", err)
	}
	if got.DiskPath != want.DiskPath || len(got.Services) != 2 || got.Services[1] != "nginx" {
		t.Fatalf("server monitoring fields = %+v", got)
	}
}

func TestPruneHistory(t *testing.T) {
	db := openTestDB(t)
	if err := Migrate(db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	store := NewStore(db)
	ctx := context.Background()
	old := time.Now().Add(-100 * 24 * time.Hour)
	if _, err := store.CreateDeployment(ctx, Deployment{ProjectID: "p", Status: DeploySuccess, StartedAt: old}); err != nil {
		t.Fatal(err)
	}
	if err := store.InsertRemoteCommand(ctx, RemoteCommand{ExecutedAt: old}); err != nil {
		t.Fatal(err)
	}
	if err := store.PruneDeployments(ctx, time.Now().Add(-30*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := store.PruneRemoteCommands(ctx, time.Now().Add(-30*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	deploys, _ := store.ListDeployments(ctx, "p", 10)
	commands, _ := store.ListRemoteCommands(ctx, 10)
	if len(deploys) != 0 || len(commands) != 0 {
		t.Fatalf("old history remains: deployments=%d commands=%d", len(deploys), len(commands))
	}
}

func TestRetiredProxyDomainsLifecycle(t *testing.T) {
	db := openTestDB(t)
	if err := Migrate(db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	store := NewStore(db)
	ctx := context.Background()
	if err := store.RetireProxyDomain(ctx, "p1", "old.example.com"); err != nil {
		t.Fatalf("RetireProxyDomain: %v", err)
	}
	domains, err := store.ListRetiredProxyDomains(ctx, "p1")
	if err != nil || len(domains) != 1 || domains[0] != "old.example.com" {
		t.Fatalf("retired domains = %v, %v", domains, err)
	}
	if err := store.DeleteRetiredProxyDomain(ctx, "p1", "old.example.com"); err != nil {
		t.Fatalf("DeleteRetiredProxyDomain: %v", err)
	}
	domains, err = store.ListRetiredProxyDomains(ctx, "p1")
	if err != nil || len(domains) != 0 {
		t.Fatalf("retired domains after delete = %v, %v", domains, err)
	}
}

func TestFailInterruptedDeployments(t *testing.T) {
	db := openTestDB(t)
	if err := Migrate(db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	store := NewStore(db)
	if _, err := store.CreateDeployment(context.Background(), Deployment{ProjectID: "p", Status: DeployRunning, StartedAt: time.Now()}); err != nil {
		t.Fatalf("create deployment: %v", err)
	}
	n, err := store.FailInterruptedDeployments(context.Background(), time.Now())
	if err != nil || n != 1 {
		t.Fatalf("FailInterruptedDeployments = %d, %v", n, err)
	}
	deploys, err := store.ListDeployments(context.Background(), "p", 10)
	if err != nil || len(deploys) != 1 || deploys[0].Status != DeployFailed || !deploys[0].FinishedAt.After(time.Time{}) {
		t.Fatalf("deployment was not finalized: %+v, %v", deploys, err)
	}
}

func TestProjectRuntimeRoundTrip(t *testing.T) {
	db := openTestDB(t)
	if err := Migrate(db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	store := NewStore(db)
	want := config.Project{
		ID: "app", Name: "App", ServerID: "local",
		RepoURL: "https://example.com/app.git", Branch: "main",
		PortsExposes: 8000, Runtime: "python",
	}
	if err := store.UpsertProject(context.Background(), want); err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}
	got, err := store.GetProject(context.Background(), "app")
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	if got.Runtime != "python" {
		t.Fatalf("runtime = %q, want python", got.Runtime)
	}
}

func TestSharedVariablesForScopes(t *testing.T) {
	db := openTestDB(t)
	if err := Migrate(db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	store := NewStore(db)
	ctx := context.Background()

	upsert := func(v SharedVariable) {
		t.Helper()
		if err := store.UpsertSharedVariable(ctx, v); err != nil {
			t.Fatalf("UpsertSharedVariable: %v", err)
		}
	}
	upsert(SharedVariable{Scope: "project", ScopeID: "Default", Key: "A", Value: "project-a"})
	upsert(SharedVariable{Scope: "environment", ScopeID: "Default/production", Key: "A", Value: "env-a"})
	upsert(SharedVariable{Scope: "project", ScopeID: "Other", Key: "B", Value: "other-b"})

	got, err := store.SharedVariablesFor(ctx, "Default", "production")
	if err != nil {
		t.Fatalf("SharedVariablesFor: %v", err)
	}
	if got["project.A"] != "project-a" || got["environment.A"] != "env-a" {
		t.Fatalf("got = %v", got)
	}
	if _, ok := got["project.B"]; ok {
		t.Fatalf("leaked another group's variable: %v", got)
	}
}

func TestCreateFirstUserOnlyOnce(t *testing.T) {
	db := openTestDB(t)
	if err := Migrate(db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	store := NewStore(db)
	ctx := context.Background()

	if n, err := store.CountUsers(ctx); err != nil || n != 0 {
		t.Fatalf("CountUsers = %d, %v; want 0", n, err)
	}
	user, err := store.CreateFirstUser(ctx, "admin", "hash-one")
	if err != nil {
		t.Fatalf("CreateFirstUser: %v", err)
	}
	if user.Username != "admin" || user.ID == 0 {
		t.Fatalf("user = %+v", user)
	}

	// A second attempt must not create or overwrite anything.
	if _, err := store.CreateFirstUser(ctx, "intruder", "hash-two"); !errors.Is(err, ErrAlreadyInitialized) {
		t.Fatalf("second CreateFirstUser err = %v, want ErrAlreadyInitialized", err)
	}
	if n, _ := store.CountUsers(ctx); n != 1 {
		t.Fatalf("CountUsers = %d, want 1", n)
	}
	got, err := store.UserByUsername(ctx, "admin")
	if err != nil {
		t.Fatalf("UserByUsername: %v", err)
	}
	if got.PasswordHash != "hash-one" {
		t.Fatalf("existing admin password was overwritten: %q", got.PasswordHash)
	}
	if _, err := store.UserByUsername(ctx, "intruder"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("intruder was created: %v", err)
	}
}
