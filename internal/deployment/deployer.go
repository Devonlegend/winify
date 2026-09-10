package deployment

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log"
	"path"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Devonlegend/winify/internal/config"
	"github.com/Devonlegend/winify/internal/models"
	"github.com/Devonlegend/winify/internal/proxy"
)

// ErrDeployInProgress means a deploy for the project is already running.
var ErrDeployInProgress = errors.New("a deployment is already in progress for this project")

// RunnerFactory opens a Runner for a target. It is a field so tests can inject
// a fake; production wires it to DialSSH.
type RunnerFactory func(ctx context.Context, srv config.Server, sshKeyPEM string) (Runner, error)

// Deployer runs the build/deploy pipeline and records every attempt.
type Deployer struct {
	cfg       config.Config
	store     *models.Store
	secrets   SecretResolver
	proxy     proxy.Registrar
	newRunner RunnerFactory
	inFlight  sync.Map // projectID -> struct{}
}

// NewDeployer wires the pipeline dependencies.
func NewDeployer(cfg config.Config, store *models.Store, secrets SecretResolver, reg proxy.Registrar, newRunner RunnerFactory) *Deployer {
	return &Deployer{cfg: cfg, store: store, secrets: secrets, proxy: reg, newRunner: newRunner}
}

// deployJob is one unit of work handed to run.
type deployJob struct {
	id       int64
	project  config.Project
	server   config.Server
	trigger  string
	commit   string
	ref      string
	imageTag string
	build    bool // false for rollback: reuse an existing image tag
}

// Trigger records a queued deployment and runs it asynchronously. It returns
// the deployment id immediately; the caller (webhook handler) responds 202.
func (d *Deployer) Trigger(ctx context.Context, project config.Project, srv config.Server, trigger, commit, ref string) (int64, error) {
	if _, loaded := d.inFlight.LoadOrStore(project.ID, struct{}{}); loaded {
		return 0, ErrDeployInProgress
	}
	id, err := d.store.CreateDeployment(ctx, models.Deployment{
		ProjectID: project.ID,
		CommitSHA: commit,
		Ref:       ref,
		Status:    models.DeployQueued,
		Trigger:   trigger,
		StartedAt: time.Now(),
	})
	if err != nil {
		d.inFlight.Delete(project.ID)
		return 0, err
	}
	go func() {
		defer d.inFlight.Delete(project.ID)
		d.run(context.Background(), deployJob{
			id: id, project: project, server: srv,
			trigger: trigger, commit: commit, ref: ref, build: true,
		})
	}()
	return id, nil
}

// Rollback redeploys the image tag from the previous successful deployment.
func (d *Deployer) Rollback(ctx context.Context, project config.Project, srv config.Server) (int64, error) {
	prev, err := d.previousSuccessful(ctx, project.ID)
	if err != nil {
		return 0, err
	}
	if _, loaded := d.inFlight.LoadOrStore(project.ID, struct{}{}); loaded {
		return 0, ErrDeployInProgress
	}
	id, err := d.store.CreateDeployment(ctx, models.Deployment{
		ProjectID: project.ID,
		CommitSHA: prev.CommitSHA,
		Ref:       prev.Ref,
		ImageTag:  prev.ImageTag,
		Status:    models.DeployQueued,
		Trigger:   "rollback",
		StartedAt: time.Now(),
	})
	if err != nil {
		d.inFlight.Delete(project.ID)
		return 0, err
	}
	go func() {
		defer d.inFlight.Delete(project.ID)
		d.run(context.Background(), deployJob{
			id: id, project: project, server: srv,
			trigger: "rollback", commit: prev.CommitSHA, ref: prev.Ref,
			imageTag: prev.ImageTag, build: false,
		})
	}()
	return id, nil
}

// previousSuccessful returns the successful deployment before the latest one.
func (d *Deployer) previousSuccessful(ctx context.Context, projectID string) (models.Deployment, error) {
	successes, err := d.store.SuccessfulDeployments(ctx, projectID, 2)
	if err != nil {
		return models.Deployment{}, err
	}
	if len(successes) < 2 {
		return models.Deployment{}, fmt.Errorf("no previous successful deployment to roll back to")
	}
	return successes[1], nil
}

// run executes the pipeline for one job and records status + logs throughout.
func (d *Deployer) run(ctx context.Context, job deployJob) {
	logf := func(format string, args ...any) {
		line := fmt.Sprintf(format, args...)
		if err := d.store.AppendDeploymentLog(ctx, job.id, line+"\n"); err != nil {
			log.Printf("deploy %d: append log: %v", job.id, err)
		}
	}
	fail := func(err error) {
		logf("ERROR: %v", err)
		if ferr := d.store.FinishDeployment(ctx, job.id, models.DeployFailed, err.Error(), time.Now()); ferr != nil {
			log.Printf("deploy %d: finish: %v", job.id, ferr)
		}
		log.Printf("deploy %d (%s) failed: %v", job.id, job.project.ID, err)
	}

	if job.imageTag == "" {
		job.imageTag = imageTag(job.project.ID, job.commit)
	}
	if err := d.store.SetDeploymentStatus(ctx, job.id, models.DeployRunning, job.imageTag); err != nil {
		log.Printf("deploy %d: set running: %v", job.id, err)
		return
	}
	logf("deploy %d started: project=%s trigger=%s commit=%s", job.id, job.project.ID, job.trigger, shortSHA(job.commit))

	sshKey, err := d.resolveSecret(ctx, job.server.SSHKeyRef)
	if err != nil {
		fail(fmt.Errorf("resolve ssh key: %w", err))
		return
	}
	runner, err := d.newRunner(ctx, job.server, sshKey)
	if err != nil {
		fail(fmt.Errorf("connect to %s: %w", job.server.SSHHost, err))
		return
	}
	defer runner.Close()
	logf("connected to %s@%s:%d", job.server.SSHUser, job.server.SSHHost, sshPort(job.server))

	workdir := path.Join(d.cfg.Deploy.WorkDir, job.project.ID)

	if job.build {
		if err := d.cloneAndBuild(ctx, runner, job, workdir, logf); err != nil {
			fail(err)
			return
		}
	} else {
		logf("rollback: skipping clone/build, reusing image %s", job.imageTag)
	}

	if err := d.writeCompose(ctx, runner, job, workdir, logf); err != nil {
		fail(err)
		return
	}
	if out, err := d.exec(ctx, runner,
		fmt.Sprintf("cd %s && docker compose -f docker-compose.yml up -d --remove-orphans", shellQuote(workdir)),
		"docker compose up -d", logf); err != nil {
		fail(fmt.Errorf("compose up: %w\n%s", err, out))
		return
	}

	if err := d.healthCheck(ctx, runner, job, logf); err != nil {
		fail(err)
		return
	}
	if err := d.registerProxy(ctx, job, logf); err != nil {
		fail(err)
		return
	}

	logf("deploy %d succeeded: image=%s", job.id, job.imageTag)
	if err := d.store.FinishDeployment(ctx, job.id, models.DeploySuccess, "", time.Now()); err != nil {
		log.Printf("deploy %d: finish: %v", job.id, err)
	}
	log.Printf("deploy %d (%s) succeeded", job.id, job.project.ID)
}

// cloneAndBuild updates the repo on the target and builds the image there.
// Building on the target keeps the Docker layer cache next to where the image
// runs, at the cost of requiring git + outbound network on the target.
func (d *Deployer) cloneAndBuild(ctx context.Context, runner Runner, job deployJob, workdir string, logf loggerFunc) error {
	if job.project.RepoURL == "" {
		return fmt.Errorf("project %s has no repo_url", job.project.ID)
	}
	clone := fmt.Sprintf(
		"mkdir -p %s && if [ -d %s/.git ]; then cd %s && git fetch --all --prune && git checkout --force %s; "+
			"else git clone %s %s && cd %s && git checkout --force %s; fi",
		shellQuote(workdir), shellQuote(workdir), shellQuote(workdir), shellQuote(job.commit),
		shellQuote(job.project.RepoURL), shellQuote(workdir), shellQuote(workdir), shellQuote(job.commit))
	if out, err := d.exec(ctx, runner, clone, "git clone/fetch + checkout "+shortSHA(job.commit), logf); err != nil {
		return fmt.Errorf("clone/checkout: %w\n%s", err, out)
	}

	dockerfile := job.project.DockerfilePath
	if dockerfile == "" {
		dockerfile = "Dockerfile"
	}
	build := fmt.Sprintf("docker build -t %s -f %s %s",
		shellQuote(job.imageTag), shellQuote(path.Join(workdir, dockerfile)), shellQuote(workdir))
	if out, err := d.exec(ctx, runner, build, "docker build -t "+job.imageTag, logf); err != nil {
		return fmt.Errorf("docker build: %w\n%s", err, out)
	}
	return nil
}

// writeCompose writes a generated docker-compose.yml on the target. The file is
// base64-encoded so project env values cannot break shell quoting; the encoded
// command is deliberately not written to the deploy log.
func (d *Deployer) writeCompose(ctx context.Context, runner Runner, job deployJob, workdir string, logf loggerFunc) error {
	content := composeFile(job.project, job.imageTag)
	encoded := base64.StdEncoding.EncodeToString([]byte(content))
	cmd := fmt.Sprintf("echo %s | base64 -d > %s", encoded, shellQuote(path.Join(workdir, "docker-compose.yml")))
	if out, err := d.exec(ctx, runner, cmd, "write docker-compose.yml", logf); err != nil {
		return fmt.Errorf("write compose file: %w\n%s", err, out)
	}
	return nil
}

// healthCheck polls the running container through the published port until it
// answers or the timeout elapses.
func (d *Deployer) healthCheck(ctx context.Context, runner Runner, job deployJob, logf loggerFunc) error {
	if job.project.Port == 0 {
		logf("no port configured; skipping health check")
		return nil
	}
	healthPath := job.project.HealthPath
	if healthPath == "" {
		healthPath = "/"
	}
	if !strings.HasPrefix(healthPath, "/") {
		healthPath = "/" + healthPath
	}
	timeout := d.cfg.Deploy.HealthTimeoutSeconds
	if timeout <= 0 {
		timeout = 60
	}
	interval := d.cfg.Deploy.HealthIntervalSeconds
	if interval <= 0 {
		interval = 3
	}
	attempts := timeout / interval
	if attempts < 1 {
		attempts = 1
	}
	url := fmt.Sprintf("http://127.0.0.1:%d%s", job.project.Port, healthPath)
	cmd := fmt.Sprintf(
		"for i in $(seq 1 %d); do if curl -fsS --max-time 3 %s >/dev/null 2>&1; then echo healthy; exit 0; fi; sleep %d; done; echo 'health check failed'; exit 1",
		attempts, shellQuote(url), interval)
	if out, err := d.exec(ctx, runner, cmd, "health check "+url, logf); err != nil {
		return fmt.Errorf("health check failed: %w\n%s", err, out)
	}
	return nil
}

// registerProxy makes the app live at its domain through Caddy.
func (d *Deployer) registerProxy(ctx context.Context, job deployJob, logf loggerFunc) error {
	if job.project.Domain == "" {
		logf("no domain configured; skipping proxy registration")
		return nil
	}
	if !d.cfg.Proxy.Enabled {
		logf("proxy disabled; skipping registration for %s", job.project.Domain)
		return nil
	}
	upstream := fmt.Sprintf("%s:%d", job.server.SSHHost, job.project.Port)
	if err := d.proxy.Register(ctx, job.project.Domain, upstream); err != nil {
		return fmt.Errorf("register %s: %w", job.project.Domain, err)
	}
	logf("registered https://%s -> %s", job.project.Domain, upstream)
	return nil
}

// exec runs a command, logging a redacted label and the (trimmed) output.
func (d *Deployer) exec(ctx context.Context, runner Runner, cmd, logCmd string, logf loggerFunc) (string, error) {
	logf("$ %s", logCmd)
	out, err := runner.Run(ctx, cmd)
	if trimmed := strings.TrimSpace(out); trimmed != "" {
		logf("%s", trimmed)
	}
	return out, err
}

// resolveSecret turns a credential reference into its plaintext value.
func (d *Deployer) resolveSecret(ctx context.Context, ref string) (string, error) {
	name := strings.TrimPrefix(ref, "vault:")
	if name == "" {
		return "", fmt.Errorf("no credential reference configured")
	}
	return d.secrets.Get(ctx, name)
}

// loggerFunc matches the local logf closure signature.
type loggerFunc func(format string, args ...any)

// composeFile renders a minimal compose file for the app image.
func composeFile(project config.Project, imageTag string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "services:\n  app:\n    image: %s\n    container_name: cc-%s\n    restart: unless-stopped\n",
		imageTag, sanitize(project.ID))
	if project.Port > 0 {
		fmt.Fprintf(&b, "    ports:\n      - \"%d:%d\"\n", project.Port, project.Port)
	}
	if len(project.Env) > 0 {
		b.WriteString("    environment:\n")
		keys := make([]string, 0, len(project.Env))
		for k := range project.Env {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(&b, "      %s: %q\n", k, project.Env[k])
		}
	}
	return b.String()
}

func imageTag(projectID, commit string) string {
	return fmt.Sprintf("cc/%s:%s", sanitize(projectID), shortSHA(commit))
}

func shortSHA(sha string) string {
	if sha == "" {
		return "unknown"
	}
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

func sshPort(srv config.Server) int {
	if srv.SSHPort == 0 {
		return 22
	}
	return srv.SSHPort
}

// sanitize makes a string safe for a Docker image path component.
func sanitize(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	return b.String()
}

// shellQuote wraps s in single quotes for safe use in a POSIX shell command.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
