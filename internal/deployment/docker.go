package deployment

import (
	"context"
	"encoding/base64"
	"fmt"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/Devonlegend/winify/internal/config"
)

// dockerTarget builds and runs a Docker workload on a Linux target over SSH.
// It supports three deploy sources: a Dockerfile build, the repository's own
// compose file, or a prebuilt registry image.
type dockerTarget struct {
	cfg     config.Config
	runner  Runner
	secrets SecretResolver
}

// NewDockerTarget builds the Docker pipeline over an open Runner. secrets
// resolves credential refs (SSH key at connection time; git credentials at
// clone time).
func NewDockerTarget(cfg config.Config, runner Runner, secrets ...SecretResolver) Target {
	var s SecretResolver
	if len(secrets) > 0 {
		s = secrets[0]
	}
	return &dockerTarget{cfg: cfg, runner: runner, secrets: s}
}

func (t *dockerTarget) Close() error { return t.runner.Close() }

// dockerSource returns the project's deploy source, defaulting to dockerfile.
func dockerSource(p config.Project) string {
	switch p.Source {
	case config.ProjectSourceCompose, config.ProjectSourceImage, config.ProjectSourceDockerfile:
		return p.Source
	default:
		return config.ProjectSourceDockerfile
	}
}

// Deploy runs the pipeline for the project's source and returns an artifact
// reference for history (a built image tag, a registry image, or a commit).
func (t *dockerTarget) Deploy(ctx context.Context, job deployJob, logf loggerFunc) (string, error) {
	if err := validateProjectID(job.project.ID); err != nil {
		return "", err
	}
	switch dockerSource(job.project) {
	case config.ProjectSourceImage:
		return t.deployImage(ctx, job, logf)
	case config.ProjectSourceCompose:
		return t.deployCompose(ctx, job, logf)
	default:
		return t.deployDockerfile(ctx, job, logf)
	}
}

// Rollback restores the previous known-good state without rebuilding.
func (t *dockerTarget) Rollback(ctx context.Context, job deployJob, logf loggerFunc) (string, error) {
	if err := validateProjectID(job.project.ID); err != nil {
		return "", err
	}
	if dockerSource(job.project) == config.ProjectSourceCompose {
		return t.rollbackCompose(ctx, job, logf)
	}
	// Dockerfile and image sources roll back by re-running the previous image.
	return t.rollbackImage(ctx, job, logf)
}

// deployDockerfile clones the repo, builds the image and runs it.
func (t *dockerTarget) deployDockerfile(ctx context.Context, job deployJob, logf loggerFunc) (string, error) {
	workdir := path.Join(t.cfg.Deploy.WorkDir, job.project.ID)
	logf("docker pipeline: build %s", job.project.ID)

	tag, err := t.cloneAndBuild(ctx, job, workdir, logf)
	if err != nil {
		return "", err
	}
	if err := t.writeGeneratedCompose(ctx, job, workdir, tag, logf); err != nil {
		return "", err
	}
	if err := t.composeUp(ctx, workdir, logf); err != nil {
		t.restorePrevious(ctx, job, logf)
		return "", err
	}
	if err := t.healthCheck(ctx, job, workdir, "docker-compose.yml", logf); err != nil {
		t.restorePrevious(ctx, job, logf)
		return "", err
	}
	return tag, nil
}

// deployImage runs a prebuilt registry image with a generated compose file.
func (t *dockerTarget) restorePrevious(ctx context.Context, job deployJob, logf loggerFunc) {
	if strings.TrimSpace(job.previousArtifact) == "" {
		logf("deployment failed and no previous successful artifact is available for automatic recovery")
		return
	}
	restoreCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Minute)
	defer cancel()
	restoreJob := job
	restoreJob.artifact = job.previousArtifact
	restoreJob.commit = job.previousArtifact
	restoreJob.rollback = true
	var err error
	if dockerSource(job.project) == config.ProjectSourceCompose {
		_, err = t.rollbackCompose(restoreCtx, restoreJob, logf)
	} else {
		_, err = t.rollbackImage(restoreCtx, restoreJob, logf)
	}
	if err != nil {
		logf("WARNING: failed to restore previous successful release: %v", err)
		return
	}
	logf("restored previous successful release %s", job.previousArtifact)
}

func (t *dockerTarget) deployImage(ctx context.Context, job deployJob, logf loggerFunc) (string, error) {
	image := strings.TrimSpace(job.project.Image)
	if image == "" {
		return "", fmt.Errorf("project %s has no image configured", job.project.ID)
	}
	if strings.ContainsAny(image, "\r\n\"'`") || strings.ContainsAny(image, " \t") {
		return "", fmt.Errorf("project %s has an invalid image reference", job.project.ID)
	}
	workdir := path.Join(t.cfg.Deploy.WorkDir, job.project.ID)
	logf("docker pipeline: run image %s", image)

	if err := t.writeGeneratedCompose(ctx, job, workdir, image, logf); err != nil {
		return "", err
	}
	if err := t.composeUp(ctx, workdir, logf); err != nil {
		t.restorePrevious(ctx, job, logf)
		return "", err
	}
	if err := t.healthCheck(ctx, job, workdir, "docker-compose.yml", logf); err != nil {
		t.restorePrevious(ctx, job, logf)
		return "", err
	}
	return image, nil
}

// deployCompose deploys the repository's own compose file.
func (t *dockerTarget) deployCompose(ctx context.Context, job deployJob, logf loggerFunc) (string, error) {
	rev := deployRevision(job.project, job.commit)
	workdir := path.Join(t.cfg.Deploy.WorkDir, job.project.ID)
	composePath := job.project.ComposePath
	if composePath == "" {
		composePath = "docker-compose.yml"
	}
	logf("docker pipeline: compose %s @ %s", composePath, shortSHA(rev))

	resolved, err := t.cloneRepo(ctx, job, workdir, rev, logf)
	if err != nil {
		return "", err
	}
	rev = resolved
	if err := t.writeEnvFile(ctx, job, workdir, logf); err != nil {
		return "", err
	}
	if err := t.composeUpRepo(ctx, workdir, composePath, logf); err != nil {
		t.restorePrevious(ctx, job, logf)
		return "", err
	}
	if err := t.healthCheck(ctx, job, workdir, composePath, logf); err != nil {
		t.restorePrevious(ctx, job, logf)
		return "", err
	}
	return rev, nil
}

// rollbackImage re-runs the previous image tag/ref without rebuilding.
func (t *dockerTarget) rollbackImage(ctx context.Context, job deployJob, logf loggerFunc) (string, error) {
	image := job.artifact
	if image == "" {
		return "", fmt.Errorf("no previous image to roll back to")
	}
	workdir := path.Join(t.cfg.Deploy.WorkDir, job.project.ID)
	logf("rollback: reusing image %s (no rebuild)", image)

	if err := t.writeGeneratedCompose(ctx, job, workdir, image, logf); err != nil {
		return "", err
	}
	if err := t.composeUp(ctx, workdir, logf); err != nil {
		return "", err
	}
	if err := t.healthCheck(ctx, job, workdir, "docker-compose.yml", logf); err != nil {
		return "", err
	}
	return image, nil
}

// rollbackCompose checks out the previous commit and re-runs its compose file.
func (t *dockerTarget) rollbackCompose(ctx context.Context, job deployJob, logf loggerFunc) (string, error) {
	rev := job.commit
	if rev == "" {
		return "", fmt.Errorf("no previous commit to roll back to")
	}
	workdir := path.Join(t.cfg.Deploy.WorkDir, job.project.ID)
	composePath := job.project.ComposePath
	if composePath == "" {
		composePath = "docker-compose.yml"
	}
	logf("rollback: compose %s @ %s", composePath, shortSHA(rev))

	resolved, err := t.cloneRepo(ctx, job, workdir, rev, logf)
	if err != nil {
		return "", err
	}
	rev = resolved
	if err := t.writeEnvFile(ctx, job, workdir, logf); err != nil {
		return "", err
	}
	if err := t.composeUpRepo(ctx, workdir, composePath, logf); err != nil {
		return "", err
	}
	if err := t.healthCheck(ctx, job, workdir, composePath, logf); err != nil {
		return "", err
	}
	return rev, nil
}

// cloneRepo updates the repo on the target at rev and returns the actual
// checked-out commit. Branch names are resolved through origin/* so a manual
// deploy cannot silently reuse a stale local branch.
func (t *dockerTarget) cloneRepo(ctx context.Context, job deployJob, workdir, rev string, logf loggerFunc) (string, error) {
	if job.project.RepoURL == "" {
		return "", fmt.Errorf("project %s has no repo_url", job.project.ID)
	}
	// Stage a private-repo credential (deploy key or token) when configured.
	gitAuth, err := PrepareGitAuth(ctx, t.runner, t.secrets, job.project, path.Join(t.cfg.Deploy.WorkDir, job.project.ID+".gitkey"), false, logf)
	if err != nil {
		return "", err
	}
	opt := gitAuth.gitOption(shellQuote)
	if opt != "" {
		opt = " " + opt
	}
	checkout := gitCheckoutRef(rev)
	clone := fmt.Sprintf(
		"mkdir -p %s && if [ -d %s/.git ]; then cd %s && git%s fetch --all --prune && git%s checkout --force %s --; "+
			"else git%s clone %s %s && cd %s && git%s checkout --force %s --; fi && git clean -fdx && git rev-parse HEAD",
		shellQuote(workdir), shellQuote(workdir), shellQuote(workdir), opt, opt, shellQuote(checkout),
		opt, shellQuote(job.project.RepoURL), shellQuote(workdir), shellQuote(workdir), opt, shellQuote(checkout))
	execCtx := ctx
	if gitAuth.Sensitive() {
		execCtx = withAuditRedaction(ctx, "git clone/fetch + checkout (credential redacted)")
	}
	out, err := execCmd(execCtx, t.runner, clone, "git clone/fetch + checkout "+shortSHA(rev), logf)
	if err != nil {
		return "", fmt.Errorf("clone/checkout: %w\n%s", err, out)
	}
	if resolved := lastLine(out); resolved != "" {
		return resolved, nil
	}
	// Test doubles and older runners may not emit rev-parse output. Keep the
	// requested revision as a conservative fallback.
	return rev, nil
}

func gitCheckoutRef(rev string) string {
	if isHexRevision(rev) {
		return rev
	}
	return "origin/" + rev
}

func isHexRevision(rev string) bool {
	if len(rev) < 7 || len(rev) > 64 {
		return false
	}
	for _, r := range rev {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')) {
			return false
		}
	}
	return true
}

// cloneAndBuild updates the repo on the target and builds the image there.
// It returns the immutable tag created from the actual checked-out revision.
func (t *dockerTarget) cloneAndBuild(ctx context.Context, job deployJob, workdir string, logf loggerFunc) (string, error) {
	rev, err := t.cloneRepo(ctx, job, workdir, deployRevision(job.project, job.commit), logf)
	if err != nil {
		return "", err
	}
	tag := imageTag(job.project.ID, rev)

	dockerfile := job.project.DockerfilePath
	if dockerfile == "" {
		dockerfile = "Dockerfile"
	}
	build := fmt.Sprintf("docker build -t %s -f %s", shellQuote(tag), shellQuote(path.Join(workdir, dockerfile)))
	for _, k := range sortedKeys(job.project.BuildEnv) {
		build += fmt.Sprintf(" --build-arg %s", shellQuote(k+"="+job.project.BuildEnv[k]))
	}
	build += " " + shellQuote(workdir)

	// Build args can hold secrets, so keep the payload out of the audit log.
	buildCtx := ctx
	if len(job.project.BuildEnv) > 0 {
		buildCtx = withAuditRedaction(ctx, "docker build (build args redacted)")
	}
	if out, err := execCmd(buildCtx, t.runner, build, "docker build -t "+tag, logf); err != nil {
		return "", fmt.Errorf("docker build: %w\n%s", err, out)
	}
	return tag, nil
}

// writeGeneratedCompose writes a compose file for a single image. The file is
// base64-encoded so project env values cannot break shell quoting; the encoded
// command is deliberately not written to the deploy log.
func (t *dockerTarget) writeGeneratedCompose(ctx context.Context, job deployJob, workdir, image string, logf loggerFunc) error {
	content := composeFile(job.project, image)
	encoded := base64.StdEncoding.EncodeToString([]byte(content))
	// mkdir -p covers the image source, which has no repo clone to create the dir.
	write := fmt.Sprintf("umask 077; mkdir -p %s && echo %s | base64 -d > %s && chmod 600 %s",
		shellQuote(workdir), encoded, shellQuote(path.Join(workdir, "docker-compose.yml")), shellQuote(path.Join(workdir, "docker-compose.yml")))
	// The generated file embeds project env values, so mark only this command
	// sensitive: the audit log records a label, never the payload.
	writeCtx := withAuditRedaction(ctx, "write docker-compose.yml (contents redacted)")
	if out, err := execCmd(writeCtx, t.runner, write, "write docker-compose.yml", logf); err != nil {
		return fmt.Errorf("write compose file: %w\n%s", err, out)
	}
	return nil
}

// writeEnvFile writes project env values to .env so the repo's compose file can
// reference them. Contents are redacted from the audit log.
func (t *dockerTarget) writeEnvFile(ctx context.Context, job deployJob, workdir string, logf loggerFunc) error {
	// The .env feeds compose variable substitution, so build-time values that
	// the compose file references under build.args must be included too.
	merged := make(map[string]string, len(job.project.Env)+len(job.project.BuildEnv))
	for k, v := range job.project.Env {
		merged[k] = v
	}
	for k, v := range job.project.BuildEnv {
		merged[k] = v
	}
	if len(merged) == 0 {
		// Remove stale values from a previous deployment. Leaving an old .env
		// in the checkout can silently reintroduce removed secrets.
		cmd := fmt.Sprintf("umask 077; : > %s && chmod 600 %s", shellQuote(path.Join(workdir, ".env")), shellQuote(path.Join(workdir, ".env")))
		writeCtx := withAuditRedaction(ctx, "clear .env (contents redacted)")
		if out, err := execCmd(writeCtx, t.runner, cmd, "clear .env", logf); err != nil {
			return fmt.Errorf("clear .env: %w\n%s", err, out)
		}
		return nil
	}
	encoded := base64.StdEncoding.EncodeToString([]byte(envFileContent(merged)))
	cmd := fmt.Sprintf("umask 077; echo %s | base64 -d > %s && chmod 600 %s", encoded, shellQuote(path.Join(workdir, ".env")), shellQuote(path.Join(workdir, ".env")))
	writeCtx := withAuditRedaction(ctx, "write .env (contents redacted)")
	if out, err := execCmd(writeCtx, t.runner, cmd, "write .env", logf); err != nil {
		return fmt.Errorf("write .env: %w\n%s", err, out)
	}
	return nil
}

// composeUp brings up the generated compose file.
func (t *dockerTarget) composeUp(ctx context.Context, workdir string, logf loggerFunc) error {
	up := fmt.Sprintf("cd %s && docker compose -f docker-compose.yml up -d --remove-orphans", shellQuote(workdir))
	if out, err := execCmd(ctx, t.runner, up, "docker compose up -d", logf); err != nil {
		return fmt.Errorf("compose up: %w\n%s", err, out)
	}
	return nil
}

// composeUpRepo brings up the repository's compose file, building if needed.
func (t *dockerTarget) composeUpRepo(ctx context.Context, workdir, composePath string, logf loggerFunc) error {
	up := fmt.Sprintf("cd %s && docker compose -f %s up -d --build --remove-orphans",
		shellQuote(workdir), shellQuote(composePath))
	if out, err := execCmd(ctx, t.runner, up, "docker compose -f "+composePath+" up -d --build", logf); err != nil {
		return fmt.Errorf("compose up: %w\n%s", err, out)
	}
	return nil
}

// healthCheck polls the running container through the published port until it
// answers or the timeout elapses. On failure it dumps container state and logs
// so the cause (usually a container-port mismatch) is visible in the deploy log.
func (t *dockerTarget) healthCheck(ctx context.Context, job deployJob, workdir, composePath string, logf loggerFunc) error {
	if job.project.DisableHealthCheck {
		logf("health check disabled; skipping")
		return nil
	}
	hostPort := job.project.EffectiveHostPort()
	if hostPort == 0 {
		logf("no port configured; skipping health check")
		return nil
	}
	plan := healthPlanFor(t.cfg, job.project)
	url := healthURL(job.project)
	start := ""
	if plan.startPeriod > 0 {
		start = fmt.Sprintf("sleep %d; ", plan.startPeriod)
	}
	cmd := fmt.Sprintf(
		"%slast=''; for i in $(seq 1 %d); do out=$(curl -fsS --max-time %d %s 2>&1) && { echo healthy; exit 0; }; last=\"$out\"; sleep %d; done; echo \"health check failed: $last\"; exit 1",
		start, plan.attempts, plan.timeout, shellQuote(url), plan.interval)
	if _, err := execCmd(ctx, t.runner, cmd, "health check "+url, logf); err != nil {
		t.diagnose(ctx, workdir, composePath, logf)
		return fmt.Errorf("health check failed for %s: %w (is the app listening on port %d inside the container? set ports_exposes to the app's port, or a PORT env var)",
			url, err, hostPort)
	}
	return nil
}

// diagnose appends the container list and recent logs to the deploy log so a
// failed health check explains itself.
func (t *dockerTarget) diagnose(ctx context.Context, workdir, composePath string, logf loggerFunc) {
	ps := fmt.Sprintf("cd %s && docker compose -f %s ps", shellQuote(workdir), shellQuote(composePath))
	if _, err := execCmd(ctx, t.runner, ps, "compose ps (diagnostics)", logf); err != nil {
		logf("diagnostics: %v", err)
	}
	logs := fmt.Sprintf("cd %s && docker compose -f %s logs --tail=30 --no-color 2>&1", shellQuote(workdir), shellQuote(composePath))
	if _, err := execCmd(ctx, t.runner, logs, "compose logs (diagnostics)", logf); err != nil {
		logf("diagnostics: %v", err)
	}
}

// composePorts returns the port publish entries for a generated compose file.
// Explicit mappings win; otherwise the exposed port is published 1:1 so the
// central reverse proxy can reach the workload.
func composePorts(project config.Project) []string {
	if len(project.PortsMappings) > 0 {
		out := make([]string, 0, len(project.PortsMappings))
		for _, mapping := range project.PortsMappings {
			host, container, err := config.ParsePortMapping(mapping)
			if err != nil {
				// Validation should reject this before deployment. Preserve the
				// value so the remote Compose error remains visible if an old
				// database row bypassed validation.
				out = append(out, mapping)
				continue
			}
			out = append(out, fmt.Sprintf("%d:%d", host, container))
		}
		return out
	}
	if project.PortsExposes > 0 {
		return []string{fmt.Sprintf("%d:%d", project.PortsExposes, project.PortsExposes)}
	}
	if project.Port > 0 {
		return []string{fmt.Sprintf("%d:%d", project.Port, project.Port)}
	}
	return nil
}

// composeFile renders a minimal compose file for the app image.
func composeFile(project config.Project, image string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "services:\n  app:\n    image: %s\n    container_name: cc-%s\n    restart: unless-stopped\n",
		image, sanitize(project.ID))
	if ports := composePorts(project); len(ports) > 0 {
		b.WriteString("    ports:\n")
		for _, port := range ports {
			fmt.Fprintf(&b, "      - %q\n", port)
		}
	}
	if len(project.Env) > 0 {
		b.WriteString("    environment:\n")
		for _, k := range sortedKeys(project.Env) {
			fmt.Fprintf(&b, "      %s: %q\n", k, project.Env[k])
		}
	}
	return b.String()
}

// envFileContent renders KEY=VALUE lines for a .env file.
func envFileContent(env map[string]string) string {
	var b strings.Builder
	for _, k := range sortedKeys(env) {
		fmt.Fprintf(&b, "%s=%s\n", k, env[k])
	}
	return b.String()
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func imageTag(projectID, revision string) string {
	return fmt.Sprintf("cc/%s:%s", sanitize(projectID), sanitize(revision))
}
