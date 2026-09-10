package deployment

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/Devonlegend/winify/internal/config"
	"github.com/Devonlegend/winify/internal/models"
)

// fakeTarget records what the Deployer asked it to do.
type fakeTarget struct {
	deployArtifact   string
	deployErr        error
	rollbackArtifact string
	rollbackErr      error
	release          chan struct{}

	deployed   bool
	rolledBack bool
	closed     bool
	lastJob    deployJob
}

func (f *fakeTarget) Deploy(_ context.Context, job deployJob, _ loggerFunc) (string, error) {
	f.deployed = true
	f.lastJob = job
	if f.release != nil {
		<-f.release
	}
	return f.deployArtifact, f.deployErr
}

func (f *fakeTarget) Rollback(_ context.Context, job deployJob, _ loggerFunc) (string, error) {
	f.rolledBack = true
	f.lastJob = job
	return f.rollbackArtifact, f.rollbackErr
}

func (f *fakeTarget) Close() error { f.closed = true; return nil }

type fakeRegistrar struct {
	hosts []string
	ups   []string
}

func (f *fakeRegistrar) Register(_ context.Context, host, upstream string) error {
	f.hosts = append(f.hosts, host)
	f.ups = append(f.ups, upstream)
	return nil
}

func (f *fakeRegistrar) Deregister(context.Context, string) error { return nil }

type fakeSecrets struct{ value string }

func (f fakeSecrets) Get(context.Context, string) (string, error) { return f.value, nil }

func newTestDeployer(t *testing.T, target Target) (*Deployer, *models.Store, *fakeRegistrar) {
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

	cfg := config.Default()
	cfg.Proxy.Enabled = true
	reg := &fakeRegistrar{}
	factory := func(context.Context, deployJob, SecretResolver) (Target, error) { return target, nil }
	return NewDeployer(cfg, store, fakeSecrets{value: "x"}, reg, factory), store, reg
}

func dockerProject() config.Project {
	return config.Project{
		ID: "proj-001", Name: "Storefront", ServerID: "server-002",
		Strategy: "dockerfile", RepoURL: "https://example.com/repo.git",
		Branch: "main", Domain: "app.example.com", Port: 8080, HealthPath: "/healthz",
	}
}

func dockerServer() config.Server {
	return config.Server{ID: "server-002", Type: config.ServerTypeDocker, SSHHost: "10.0.0.9", SSHPort: 22}
}

func iisProject() config.Project {
	return config.Project{
		ID: "proj-002", Name: "Portal", ServerID: "server-001",
		Strategy: "iis", RepoURL: "https://example.com/portal.git",
		Branch: "main", Domain: "portal.example.com", Port: 80,
		IISPhysicalPath: `C:\inetpub\wwwroot\portal`, IISAppPool: "PortalPool",
	}
}

func iisServer() config.Server {
	return config.Server{
		ID: "server-001", Type: config.ServerTypeIIS,
		WinRMEndpoint: "https://10.0.0.5:5986/wsman", WinRMUser: "deploy", CredentialRef: "vault:server-001-winrm",
	}
}

func waitTerminal(t *testing.T, store *models.Store, id int64) models.Deployment {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		d, err := store.GetDeployment(context.Background(), id)
		if err == nil && (d.Status == models.DeploySuccess || d.Status == models.DeployFailed) {
			return d
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("deployment %d did not reach a terminal state", id)
	return models.Deployment{}
}

func seedSuccess(t *testing.T, store *models.Store, projectID, commit, tag string) {
	t.Helper()
	if _, err := store.CreateDeployment(context.Background(), models.Deployment{
		ProjectID: projectID, TargetType: config.ServerTypeDocker, CommitSHA: commit,
		Ref: "refs/heads/main", ImageTag: tag, Status: models.DeploySuccess,
		Trigger: "webhook", StartedAt: time.Now().Add(-time.Hour),
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
}

func TestTriggerSuccess(t *testing.T) {
	target := &fakeTarget{deployArtifact: "cc/proj-001:abc1234"}
	d, store, reg := newTestDeployer(t, target)

	id, err := d.Trigger(context.Background(), dockerProject(), dockerServer(), "webhook", "abc1234", "refs/heads/main")
	if err != nil {
		t.Fatalf("Trigger: %v", err)
	}
	dep := waitTerminal(t, store, id)

	if dep.Status != models.DeploySuccess {
		t.Fatalf("status = %q, want success (%s)", dep.Status, dep.Error)
	}
	if dep.TargetType != config.ServerTypeDocker {
		t.Fatalf("target type = %q", dep.TargetType)
	}
	if dep.ImageTag != "cc/proj-001:abc1234" {
		t.Fatalf("artifact = %q", dep.ImageTag)
	}
	if !target.closed {
		t.Error("target was not closed")
	}
	if len(reg.hosts) != 1 || reg.hosts[0] != "app.example.com" || reg.ups[0] != "10.0.0.9:8080" {
		t.Fatalf("proxy registration = %v -> %v", reg.hosts, reg.ups)
	}
}

func TestTriggerFailure(t *testing.T) {
	target := &fakeTarget{deployErr: errors.New("build exploded")}
	d, store, _ := newTestDeployer(t, target)

	id, err := d.Trigger(context.Background(), dockerProject(), dockerServer(), "webhook", "abc1234", "refs/heads/main")
	if err != nil {
		t.Fatalf("Trigger: %v", err)
	}
	dep := waitTerminal(t, store, id)
	if dep.Status != models.DeployFailed {
		t.Fatalf("status = %q, want failed", dep.Status)
	}
	if dep.Error == "" {
		t.Error("failure recorded without an error message")
	}
}

func TestRollbackDockerUsesPreviousTag(t *testing.T) {
	target := &fakeTarget{rollbackArtifact: "cc/proj-001:aaa1111"}
	d, store, _ := newTestDeployer(t, target)
	seedSuccess(t, store, "proj-001", "aaa1111", "cc/proj-001:aaa1111")
	seedSuccess(t, store, "proj-001", "bbb2222", "cc/proj-001:bbb2222")

	id, err := d.Rollback(context.Background(), dockerProject(), dockerServer())
	if err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	dep := waitTerminal(t, store, id)

	if dep.Trigger != "rollback" {
		t.Fatalf("trigger = %q", dep.Trigger)
	}
	if target.lastJob.artifact != "cc/proj-001:aaa1111" {
		t.Fatalf("target artifact = %q, want previous tag", target.lastJob.artifact)
	}
	if dep.ImageTag != "cc/proj-001:aaa1111" {
		t.Fatalf("recorded artifact = %q", dep.ImageTag)
	}
}

func TestRollbackIISNeedsNoHistory(t *testing.T) {
	target := &fakeTarget{rollbackArtifact: `C:\control-center\backups\proj-002\20260101-000000`}
	d, store, _ := newTestDeployer(t, target)

	// No prior successful deployments at all.
	id, err := d.Rollback(context.Background(), iisProject(), iisServer())
	if err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	dep := waitTerminal(t, store, id)

	if dep.Status != models.DeploySuccess {
		t.Fatalf("status = %q, want success (%s)", dep.Status, dep.Error)
	}
	if target.lastJob.artifact != "" {
		t.Fatalf("iis rollback should not pass a prior artifact, got %q", target.lastJob.artifact)
	}
}

func TestTriggerRejectsConcurrentDeploy(t *testing.T) {
	target := &fakeTarget{deployArtifact: "cc/proj-001:abc1234", release: make(chan struct{})}
	d, store, _ := newTestDeployer(t, target)
	ctx := context.Background()

	id, err := d.Trigger(ctx, dockerProject(), dockerServer(), "webhook", "abc1234", "refs/heads/main")
	if err != nil {
		t.Fatalf("first Trigger: %v", err)
	}
	if _, err := d.Trigger(ctx, dockerProject(), dockerServer(), "webhook", "abc1235", "refs/heads/main"); !errors.Is(err, ErrDeployInProgress) {
		t.Fatalf("second Trigger err = %v, want ErrDeployInProgress", err)
	}
	close(target.release)
	waitTerminal(t, store, id)
}
