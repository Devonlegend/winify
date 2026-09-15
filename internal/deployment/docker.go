package deployment

import (
	"context"
	"encoding/base64"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/Devonlegend/winify/internal/config"
)

// dockerTarget builds and runs a Docker workload on a Linux target over SSH.
// It supports three deploy sources: a Dockerfile build, the repository's own
// compose file, or a prebuilt registry image.
type dockerTarget struct {
	cfg    config.Config
	runner Runner
}

// NewDockerTarget builds the Docker pipeline over an open Runner.
func NewDockerTarget(cfg config.Config, runner Runner) Target {
	return &dockerTarget{cfg: cfg, runner: runner}
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
	if dockerSource(job.project) == config.ProjectSourceCompose {
		return t.rollbackCompose(ctx, job, logf)
	}
	// Dockerfile and image sources roll back by re-running the previous image.
	return t.rollbackImage(ctx, job, logf)
}

// deployDockerfile clones the repo, builds the image and runs it.
func (t *dockerTarget) deployDockerfile(ctx context.Context, job deployJob, logf loggerFunc) (string, error) {
	tag := imageTag(job.project.ID, deployRevision(job.project, job.commit))
	workdir := path.Join(t.cfg.Deploy.WorkDir, job.project.ID)
	logf("docker pipeline: build %s", tag)

	if err := t.cloneAndBuild(ctx, job, workdir, tag, logf); err != nil {
		return "", err
	}
	if err := t.writeGeneratedCompose(ctx, job, workdir, tag, logf); err != nil {
		return "", err
	}
	if err := t.composeUp(ctx, workdir, logf); err != nil {
		return "", err
	}
	if err := t.healthCheck(ctx, job, workdir, "docker-compose.yml", logf); err != nil {
		return "", err
	}
	return tag, nil
}

// deployImage runs a prebuilt registry image with a generated compose file.
func (t *dockerTarget) deployImage(ctx context.Context, job deployJob, logf loggerFunc) (string, error) {
	image := strings.TrimSpace(job.project.Image)
	if image == "" {
		return "", fmt.Errorf("project %s has no image configured", job.project.ID)
	}
	workdir := path.Join(t.cfg.Deploy.WorkDir, job.project.ID)
	logf("docker pipeline: run image %s", image)

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

// deployCompose deploys the repository's own compose file.
func (t *dockerTarget) deployCompose(ctx context.Context, job deployJob, logf loggerFunc) (string, error) {
	rev := deployRevision(job.project, job.commit)
	workdir := path.Join(t.cfg.Deploy.WorkDir, job.project.ID)
	composePath := job.project.ComposePath
	if composePath == "" {
		composePath = "docker-compose.yml"
	}
	logf("docker pipeline: compose %s @ %s", composePath, shortSHA(rev))

	if err := t.cloneRepo(ctx, job, workdir, rev, logf); err != nil {
		return "", err
	}
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

	if err := t.cloneRepo(ctx, job, workdir, rev, logf); err != nil {
		return "", err
	}
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

// cloneRepo updates the repo on the target at rev.
func (t *dockerTarget) cloneRepo(ctx context.Context, job deployJob, workdir, rev string, logf loggerFunc) error {
	if job.project.RepoURL == "" {
		return fmt.Errorf("project %s has no repo_url", job.project.ID)
	}
	clone := fmt.Sprintf(
		"mkdir -p %s && if [ -d %s/.git ]; then cd %s && git fetch --all --prune && git checkout --force %s; "+
			"else git clone %s %s && cd %s && git checkout --force %s; fi",
		shellQuote(workdir), shellQuote(workdir), shellQuote(workdir), shellQuote(rev),
		shellQuote(job.project.RepoURL), shellQuote(workdir), shellQuote(workdir), shellQuote(rev))
	if out, err := execCmd(ctx, t.runner, clone, "git clone/fetch + checkout "+shortSHA(rev), logf); err != nil {
		return fmt.Errorf("clone/checkout: %w\n%s", err, out)
	}
	return nil
}

// cloneAndBuild updates the repo on the target and builds the image there.
// Building on the target keeps the Docker layer cache next to where the image
// runs, at the cost of requiring git + outbound network on the target.
func (t *dockerTarget) cloneAndBuild(ctx context.Context, job deployJob, workdir, tag string, logf loggerFunc) error {
	rev := deployRevision(job.project, job.commit)
	if err := t.cloneRepo(ctx, job, workdir, rev, logf); err != nil {
		return err
	}

	dockerfile := job.project.DockerfilePath
	if dockerfile == "" {
		dockerfile = "Dockerfile"
	}
	build := fmt.Sprintf("docker build -t %s -f %s %s",
		shellQuote(tag), shellQuote(path.Join(workdir, dockerfile)), shellQuote(workdir))
	if out, err := execCmd(ctx, t.runner, build, "docker build -t "+tag, logf); err != nil {
		return fmt.Errorf("docker build: %w\n%s", err, out)
	}
	return nil
}

// writeGeneratedCompose writes a compose file for a single image. The file is
// base64-encoded so project env values cannot break shell quoting; the encoded
// command is deliberately not written to the deploy log.
func (t *dockerTarget) writeGeneratedCompose(ctx context.Context, job deployJob, workdir, image string, logf loggerFunc) error {
	content := composeFile(job.project, image)
	encoded := base64.StdEncoding.EncodeToString([]byte(content))
	// mkdir -p covers the image source, which has no repo clone to create the dir.
	write := fmt.Sprintf("mkdir -p %s && echo %s | base64 -d > %s",
		shellQuote(workdir), encoded, shellQuote(path.Join(workdir, "docker-compose.yml")))
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
	if len(job.project.Env) == 0 {
		return nil
	}
	encoded := base64.StdEncoding.EncodeToString([]byte(envFileContent(job.project.Env)))
	cmd := fmt.Sprintf("echo %s | base64 -d > %s", encoded, shellQuote(path.Join(workdir, ".env")))
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
	if job.project.Port == 0 {
		logf("no port configured; skipping health check")
		return nil
	}
	interval, attempts := healthTiming(t.cfg)
	url := healthURL(job.project)
	cmd := fmt.Sprintf(
		"last=''; for i in $(seq 1 %d); do out=$(curl -fsS --max-time 3 %s 2>&1) && { echo healthy; exit 0; }; last=\"$out\"; sleep %d; done; echo \"health check failed: $last\"; exit 1",
		attempts, shellQuote(url), interval)
	if _, err := execCmd(ctx, t.runner, cmd, "health check "+url, logf); err != nil {
		t.diagnose(ctx, workdir, composePath, logf)
		return fmt.Errorf("health check failed for %s: %w (is the app listening on port %d inside the container? set container_port to the app's port, or a PORT env var)",
			url, err, job.project.Port)
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

// composeFile renders a minimal compose file for the app image.
func composeFile(project config.Project, image string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "services:\n  app:\n    image: %s\n    container_name: cc-%s\n    restart: unless-stopped\n",
		image, sanitize(project.ID))
	if project.Port > 0 {
		containerPort := project.ContainerPort
		if containerPort <= 0 {
			containerPort = project.Port
		}
		fmt.Fprintf(&b, "    ports:\n      - \"%d:%d\"\n", project.Port, containerPort)
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
	return fmt.Sprintf("cc/%s:%s", sanitize(projectID), sanitize(shortSHA(revision)))
}
