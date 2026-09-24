package deployment

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/Devonlegend/winify/internal/config"
	"github.com/Devonlegend/winify/internal/models"
	"github.com/Devonlegend/winify/internal/proxy"
)

// ErrDeployInProgress means a deploy for the project is already running.
var ErrDeployInProgress = errors.New("a deployment is already in progress for this project")

// Deployer runs the deploy pipeline and records every attempt. It is
// target-agnostic: Docker and IIS differ only in the Target the factory builds.
type Deployer struct {
	cfg       config.Config
	store     *models.Store
	secrets   SecretResolver
	proxy     proxy.Registrar
	newTarget TargetFactory
	timeout   time.Duration
	inFlight  sync.Map // projectID -> struct{}

	lifecycleCtx context.Context
	cancel       context.CancelFunc
	workers      sync.WaitGroup
	stopOnce     sync.Once
}

// NewDeployer wires the pipeline dependencies.
func NewDeployer(cfg config.Config, store *models.Store, secrets SecretResolver, reg proxy.Registrar, newTarget TargetFactory) *Deployer {
	timeout := time.Duration(cfg.Deploy.TimeoutMinutes) * time.Minute
	if timeout <= 0 {
		timeout = 30 * time.Minute
	}
	lifecycleCtx, cancel := context.WithCancel(context.Background())
	return &Deployer{
		cfg: cfg, store: store, secrets: secrets, proxy: reg, newTarget: newTarget,
		timeout: timeout, lifecycleCtx: lifecycleCtx, cancel: cancel,
	}
}

// Stop cancels active deployment work and waits for compensation/finalization
// to finish. Call it before closing the database during service shutdown.
func (d *Deployer) Stop() {
	d.stopOnce.Do(func() {
		if d.cancel != nil {
			d.cancel()
		}
	})
	d.workers.Wait()
}

// deployJob is one unit of work handed to a Target.
type deployJob struct {
	id       int64
	project  config.Project
	server   config.Server
	trigger  string
	commit   string
	ref      string
	artifact string // Docker image tag, or backup path for a rollback
	rollback bool
}

// Trigger records a queued deployment and runs it asynchronously. It returns
// the deployment id immediately; the caller (webhook handler) responds 202.
func (d *Deployer) Trigger(ctx context.Context, project config.Project, srv config.Server, trigger, commit, ref string) (int64, error) {
	if _, loaded := d.inFlight.LoadOrStore(project.ID, struct{}{}); loaded {
		return 0, ErrDeployInProgress
	}
	id, err := d.store.CreateDeployment(ctx, models.Deployment{
		ProjectID:  project.ID,
		TargetType: srv.Type,
		CommitSHA:  commit,
		Ref:        ref,
		Status:     models.DeployQueued,
		Trigger:    trigger,
		StartedAt:  time.Now(),
	})
	if err != nil {
		d.inFlight.Delete(project.ID)
		return 0, err
	}
	d.workers.Add(1)
	go func() {
		defer d.workers.Done()
		defer d.inFlight.Delete(project.ID)
		runCtx, cancel := context.WithTimeout(d.lifecycleCtx, d.timeout)
		defer cancel()
		d.run(runCtx, deployJob{
			id: id, project: project, server: srv, trigger: trigger, commit: commit, ref: ref,
		})
	}()
	return id, nil
}

// Rollback restores the previous known-good state. Docker rollback needs the
// previous image tag from history; IIS rollback discovers the latest backup on
// the target, so it does not require two prior successes.
func (d *Deployer) Rollback(ctx context.Context, project config.Project, srv config.Server) (int64, error) {
	var prev models.Deployment
	// IIS and winsvc roll back by restoring a timestamped backup discovered on
	// the target, so they do not need the previous image/commit from history.
	if srv.Type != config.ServerTypeIIS && srv.Type != config.ServerTypeWindowsService {
		p, err := d.previousSuccessful(ctx, project.ID)
		if err != nil {
			return 0, err
		}
		prev = p
	}

	if _, loaded := d.inFlight.LoadOrStore(project.ID, struct{}{}); loaded {
		return 0, ErrDeployInProgress
	}
	rollbackCommit := prev.CommitSHA
	// Compose deployments store their actual checked-out commit in ImageTag
	// because manual deploys have no webhook commit. Use that immutable value
	// for rollback instead of the branch placeholder.
	if project.Source == config.ProjectSourceCompose && prev.ImageTag != "" {
		rollbackCommit = prev.ImageTag
	}
	id, err := d.store.CreateDeployment(ctx, models.Deployment{
		ProjectID:  project.ID,
		TargetType: srv.Type,
		CommitSHA:  rollbackCommit,
		ImageTag:   prev.ImageTag,
		Status:     models.DeployQueued,
		Trigger:    "rollback",
		StartedAt:  time.Now(),
	})
	if err != nil {
		d.inFlight.Delete(project.ID)
		return 0, err
	}
	d.workers.Add(1)
	go func() {
		defer d.workers.Done()
		defer d.inFlight.Delete(project.ID)
		runCtx, cancel := context.WithTimeout(d.lifecycleCtx, d.timeout)
		defer cancel()
		d.run(runCtx, deployJob{
			id: id, project: project, server: srv, trigger: "rollback",
			commit: rollbackCommit, ref: prev.Ref, artifact: prev.ImageTag, rollback: true,
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
// Remote commands use ctx (which may carry a deadline); history writes use a
// cancellation-proof context so a timed-out deploy is still finalized.
func (d *Deployer) run(ctx context.Context, job deployJob) {
	dbCtx := context.WithoutCancel(ctx)
	logf := func(format string, args ...any) {
		line := fmt.Sprintf(format, args...)
		if err := d.store.AppendDeploymentLog(dbCtx, job.id, line+"\n"); err != nil {
			log.Printf("deploy %d: append log: %v", job.id, err)
		}
	}
	fail := func(err error) {
		logf("ERROR: %v", err)
		if ferr := d.store.FinishDeployment(dbCtx, job.id, models.DeployFailed, err.Error(), time.Now()); ferr != nil {
			log.Printf("deploy %d: finish: %v", job.id, ferr)
		}
		log.Printf("deploy %d (%s) failed: %v", job.id, job.project.ID, err)
	}

	if err := d.store.SetDeploymentStatus(dbCtx, job.id, models.DeployRunning, job.artifact); err != nil {
		log.Printf("deploy %d: set running: %v", job.id, err)
		return
	}
	verb := "deploy"
	if job.rollback {
		verb = "rollback"
	}
	logf("%s %d started: project=%s target=%s trigger=%s commit=%s",
		verb, job.id, job.project.ID, job.server.Type, job.trigger, shortSHA(job.commit))

	// Tag every remote command run by this job so the audit log can attribute
	// it to a server, action and deployment.
	ctx = WithAudit(ctx, AuditMeta{
		ServerID:     job.server.ID,
		ServerType:   job.server.Type,
		Action:       verb,
		DeploymentID: job.id,
	})

	// Resolve {{project.KEY}} / {{environment.KEY}} references before the target
	// reads the env maps.
	if err := d.resolveSharedVars(ctx, &job.project); err != nil {
		fail(err)
		return
	}

	target, err := d.newTarget(ctx, job, d.secrets)
	if err != nil {
		fail(err)
		return
	}
	defer target.Close()

	var artifact string
	if job.rollback {
		artifact, err = target.Rollback(ctx, job, logf)
	} else {
		artifact, err = target.Deploy(ctx, job, logf)
	}
	if err != nil {
		fail(err)
		return
	}
	if artifact != "" {
		job.artifact = artifact
		if err := d.store.SetDeploymentStatus(dbCtx, job.id, models.DeployRunning, artifact); err != nil {
			log.Printf("deploy %d: set artifact: %v", job.id, err)
		}
	}

	if err := d.registerProxy(ctx, job, logf); err != nil {
		fail(err)
		return
	}

	logf("%s %d succeeded: artifact=%s", verb, job.id, job.artifact)
	if err := d.store.FinishDeployment(dbCtx, job.id, models.DeploySuccess, "", time.Now()); err != nil {
		log.Printf("deploy %d: finish: %v", job.id, err)
	}
	log.Printf("%s %d (%s) succeeded", verb, job.id, job.project.ID)
}

// resolveSharedVars expands shared-variable references in the project's runtime
// and build env maps. It only queries the store when a reference is present.
func (d *Deployer) resolveSharedVars(ctx context.Context, p *config.Project) error {
	if !hasSharedRefs(p.Env) && !hasSharedRefs(p.BuildEnv) {
		return nil
	}
	vars, err := d.store.SharedVariablesFor(ctx, p.ProjectGroup, p.Environment)
	if err != nil {
		return err
	}
	if p.Env, err = expandSharedVars(p.Env, vars); err != nil {
		return err
	}
	if p.BuildEnv, err = expandSharedVars(p.BuildEnv, vars); err != nil {
		return err
	}
	return nil
}

// registerProxy makes the app live at its domain through the reverse proxy.
// This is shared by Docker and IIS; only the upstream host resolution differs.
func (d *Deployer) registerProxy(ctx context.Context, job deployJob, logf loggerFunc) error {
	if job.project.Domain == "" {
		logf("no domain configured; skipping proxy registration")
		return nil
	}
	if !d.cfg.Proxy.Enabled {
		logf("proxy disabled; skipping registration for %s", job.project.Domain)
		return nil
	}
	host := targetHost(job.server)
	if host == "" {
		return fmt.Errorf("no target host configured for proxy registration")
	}
	upstream := fmt.Sprintf("%s:%d", host, job.project.EffectiveHostPort())
	if err := d.proxy.Register(ctx, job.project.Domain, upstream); err != nil {
		return fmt.Errorf("register %s: %w", job.project.Domain, err)
	}
	logf("registered https://%s -> %s", job.project.Domain, upstream)
	proxyIP := d.cfg.Proxy.PublicIP
	if proxyIP == "" && job.server.Local {
		proxyIP = job.server.PublicIP
	}
	warnIfDomainNotPointedHere(ctx, proxyIP, job.project.Domain, logf)
	return nil
}

// lookupHost resolves a hostname. It is a variable so a test can stub DNS.
var lookupHost = net.DefaultResolver.LookupHost

// warnIfDomainNotPointedHere resolves the domain and warns when it does not
// point at the central Caddy ingress. The target server's public IP is not used
// for remote projects because Caddy terminates TLS on the controller host.
func warnIfDomainNotPointedHere(ctx context.Context, publicIP, domain string, logf loggerFunc) {
	if publicIP == "" {
		return
	}
	lookupCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	ips, err := lookupHost(lookupCtx, domain)
	if err != nil {
		logf("WARNING: %s does not resolve yet; add a DNS A record pointing to the Caddy host (%s) for TLS to be issued", domain, publicIP)
		return
	}
	for _, ip := range ips {
		if ip == publicIP {
			logf("%s resolves to the Caddy host %s", domain, publicIP)
			return
		}
	}
	logf("WARNING: %s resolves to %s, but the Caddy host is %s; add an A record pointing at Caddy for TLS to be issued",
		domain, strings.Join(ips, ", "), publicIP)
}
