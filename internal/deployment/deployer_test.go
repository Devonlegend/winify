package deployment

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Devonlegend/winify/internal/config"
	"github.com/Devonlegend/winify/internal/models"
)

// ---- fakes ----

type fakeRunner struct {
	commands []string
	failOn   string
	release  chan struct{}
	closed   bool
}

func (f *fakeRunner) Run(ctx context.Context, command string) (string, error) {
	if f.release != nil {
		<-f.release
	}
	f.commands = append(f.commands, command)
	if f.failOn != "" && strings.Contains(command, f.failOn) {
		return "boom", errors.New("command failed")
	}
	return "", nil
}

func (f *fakeRunner) Close() error { f.closed = true; return nil }

func (f *fakeRunner) joined() string { return strings.Join(f.commands, "\n") }

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

// ---- harness ----

func newTestDeployer(t *testing.T) (*Deployer, *models.Store, *fakeRunner, *fakeRegistrar) {
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
	cfg.Deploy.WorkDir = "/opt/control-center"
	cfg.Proxy.Enabled = true

	runner := &fakeRunner{}
	registrar := &fakeRegistrar{}
	factory := func(context.Context, config.Server, string) (Runner, error) { return runner, nil }
	d := NewDeployer(cfg, store, fakeSecrets{value: "key"}, registrar, factory)
	return d, store, runner, registrar
}

func sampleProject() config.Project {
	return config.Project{
		ID:             "proj-001",
		Name:           "Storefront",
		ServerID:       "server-002",
		Strategy:       "dockerfile",
		RepoURL:        "https://github.com/acme/storefront.git",
		DockerfilePath: "deploy/Dockerfile",
		Branch:         "main",
		Domain:         "app.example.com",
		Port:           8080,
		HealthPath:     "/healthz",
	}
}

func sampleServer() config.Server {
	return config.Server{
		ID: "server-002", Name: "Docker Host", Type: "docker",
		SSHHost: "10.0.0.9", SSHPort: 22, SSHUser: "deploy",
		SSHKeyRef: "vault:server-002-ssh",
	}
}

// runJob creates a queued deployment and runs the pipeline synchronously.
func runJob(t *testing.T, d *Deployer, p config.Project, srv config.Server, commit, trigger string, build bool, tag string) int64 {
	t.Helper()
	id, err := d.store.CreateDeployment(context.Background(), models.Deployment{
		ProjectID: p.ID, CommitSHA: commit, Ref: "refs/heads/main",
		ImageTag: tag, Status: models.DeployQueued, Trigger: trigger, StartedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}
	d.run(context.Background(), deployJob{
		id: id, project: p, server: srv, trigger: trigger,
		commit: commit, ref: "refs/heads/main", imageTag: tag, build: build,
	})
	return id
}

func waitTerminal(t *testing.T, store *models.Store, id int64) models.Deployment {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
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

// ---- tests ----

func TestRunSuccess(t *testing.T) {
	d, store, runner, registrar := newTestDeployer(t)
	p, srv := sampleProject(), sampleServer()

	id := runJob(t, d, p, srv, "abcdef1234567", "webhook", true, "")

	dep, err := store.GetDeployment(context.Background(), id)
	if err != nil {
		t.Fatalf("GetDeployment: %v", err)
	}
	if dep.Status != models.DeploySuccess {
		t.Fatalf("status = %q, want success (error: %s)", dep.Status, dep.Error)
	}
	if dep.ImageTag != "cc/proj-001:abcdef1" {
		t.Fatalf("image tag = %q", dep.ImageTag)
	}
	if !runner.closed {
		t.Error("runner was not closed")
	}

	cmds := runner.joined()
	for _, want := range []string{"git clone", "docker build", "docker compose", "curl"} {
		if !strings.Contains(cmds, want) {
			t.Errorf("commands missing %q:\n%s", want, cmds)
		}
	}
	if len(registrar.hosts) != 1 || registrar.hosts[0] != "app.example.com" {
		t.Fatalf("proxy hosts = %v", registrar.hosts)
	}
	if registrar.ups[0] != "10.0.0.9:8080" {
		t.Fatalf("proxy upstream = %q", registrar.ups[0])
	}
	if !strings.Contains(dep.Log, "succeeded") {
		t.Errorf("log missing success line:\n%s", dep.Log)
	}
}

func TestRunBuildFailure(t *testing.T) {
	d, store, runner, _ := newTestDeployer(t)
	runner.failOn = "docker build"
	p, srv := sampleProject(), sampleServer()

	id := runJob(t, d, p, srv, "abcdef1234567", "webhook", true, "")

	dep, _ := store.GetDeployment(context.Background(), id)
	if dep.Status != models.DeployFailed {
		t.Fatalf("status = %q, want failed", dep.Status)
	}
	if !strings.Contains(dep.Error, "docker build") {
		t.Fatalf("error = %q, want docker build failure", dep.Error)
	}
	if strings.Contains(runner.joined(), "docker compose") {
		t.Error("compose ran after a failed build")
	}
}

func TestRollbackUsesPreviousImage(t *testing.T) {
	d, store, runner, _ := newTestDeployer(t)
	p, srv := sampleProject(), sampleServer()
	ctx := context.Background()

	// Two prior successes: aaa (older), bbb (newer/current).
	for _, seed := range []struct{ commit, tag string }{
		{"aaa1111", "cc/proj-001:aaa1111"},
		{"bbb2222", "cc/proj-001:bbb2222"},
	} {
		if _, err := store.CreateDeployment(ctx, models.Deployment{
			ProjectID: p.ID, CommitSHA: seed.commit, Ref: "refs/heads/main",
			ImageTag: seed.tag, Status: models.DeploySuccess, Trigger: "webhook",
			StartedAt: time.Now().Add(-time.Hour),
		}); err != nil {
			t.Fatalf("seed deployment: %v", err)
		}
	}

	id, err := d.Rollback(ctx, p, srv)
	if err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	dep := waitTerminal(t, store, id)

	if dep.Status != models.DeploySuccess {
		t.Fatalf("rollback status = %q, want success (error: %s)", dep.Status, dep.Error)
	}
	if dep.Trigger != "rollback" {
		t.Fatalf("trigger = %q, want rollback", dep.Trigger)
	}
	if dep.ImageTag != "cc/proj-001:aaa1111" {
		t.Fatalf("rolled back to %q, want previous image cc/proj-001:aaa1111", dep.ImageTag)
	}
	cmds := runner.joined()
	if strings.Contains(cmds, "docker build") {
		t.Errorf("rollback rebuilt the image:\n%s", cmds)
	}
	if !strings.Contains(cmds, "docker compose") {
		t.Errorf("rollback did not compose up:\n%s", cmds)
	}
}

func TestRollbackWithoutHistoryFails(t *testing.T) {
	d, store, _, _ := newTestDeployer(t)
	p, srv := sampleProject(), sampleServer()
	ctx := context.Background()

	if _, err := store.CreateDeployment(ctx, models.Deployment{
		ProjectID: p.ID, CommitSHA: "aaa1111", ImageTag: "cc/proj-001:aaa1111",
		Status: models.DeploySuccess, Trigger: "webhook", StartedAt: time.Now(),
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := d.Rollback(ctx, p, srv); err == nil {
		t.Fatal("Rollback with a single success returned nil error")
	}
}

func TestTriggerRejectsConcurrentDeploy(t *testing.T) {
	d, _, runner, _ := newTestDeployer(t)
	p, srv := sampleProject(), sampleServer()
	runner.release = make(chan struct{})
	ctx := context.Background()

	id, err := d.Trigger(ctx, p, srv, "webhook", "abcdef1", "refs/heads/main")
	if err != nil {
		t.Fatalf("first Trigger: %v", err)
	}
	if _, err := d.Trigger(ctx, p, srv, "webhook", "abcdef2", "refs/heads/main"); !errors.Is(err, ErrDeployInProgress) {
		t.Fatalf("second Trigger err = %v, want ErrDeployInProgress", err)
	}

	close(runner.release)
	waitTerminal(t, d.store, id)
}
