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

// dockerTarget builds and runs a Docker image on a Linux target over SSH.
type dockerTarget struct {
	cfg    config.Config
	runner Runner
}

// NewDockerTarget builds the Docker pipeline over an open Runner.
func NewDockerTarget(cfg config.Config, runner Runner) Target {
	return &dockerTarget{cfg: cfg, runner: runner}
}

func (t *dockerTarget) Close() error { return t.runner.Close() }

// Deploy clones the repo on the target, builds the image there, brings it up
// with Compose and health-checks it. Returns the image tag.
func (t *dockerTarget) Deploy(ctx context.Context, job deployJob, logf loggerFunc) (string, error) {
	tag := imageTag(job.project.ID, job.commit)
	workdir := path.Join(t.cfg.Deploy.WorkDir, job.project.ID)
	logf("docker pipeline: build %s", tag)

	if err := t.cloneAndBuild(ctx, job, workdir, tag, logf); err != nil {
		return "", err
	}
	if err := t.composeUp(ctx, job, workdir, tag, logf); err != nil {
		return "", err
	}
	if err := t.healthCheck(ctx, job, logf); err != nil {
		return "", err
	}
	return tag, nil
}

// Rollback re-runs the previous image tag without rebuilding.
func (t *dockerTarget) Rollback(ctx context.Context, job deployJob, logf loggerFunc) (string, error) {
	tag := job.artifact
	if tag == "" {
		return "", fmt.Errorf("no previous image tag to roll back to")
	}
	workdir := path.Join(t.cfg.Deploy.WorkDir, job.project.ID)
	logf("rollback: reusing image %s (no rebuild)", tag)

	if err := t.composeUp(ctx, job, workdir, tag, logf); err != nil {
		return "", err
	}
	if err := t.healthCheck(ctx, job, logf); err != nil {
		return "", err
	}
	return tag, nil
}

// cloneAndBuild updates the repo on the target and builds the image there.
// Building on the target keeps the Docker layer cache next to where the image
// runs, at the cost of requiring git + outbound network on the target.
func (t *dockerTarget) cloneAndBuild(ctx context.Context, job deployJob, workdir, tag string, logf loggerFunc) error {
	if job.project.RepoURL == "" {
		return fmt.Errorf("project %s has no repo_url", job.project.ID)
	}
	clone := fmt.Sprintf(
		"mkdir -p %s && if [ -d %s/.git ]; then cd %s && git fetch --all --prune && git checkout --force %s; "+
			"else git clone %s %s && cd %s && git checkout --force %s; fi",
		shellQuote(workdir), shellQuote(workdir), shellQuote(workdir), shellQuote(job.commit),
		shellQuote(job.project.RepoURL), shellQuote(workdir), shellQuote(workdir), shellQuote(job.commit))
	if out, err := execCmd(ctx, t.runner, clone, "git clone/fetch + checkout "+shortSHA(job.commit), logf); err != nil {
		return fmt.Errorf("clone/checkout: %w\n%s", err, out)
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

// composeUp writes a generated compose file and brings the stack up. The file
// is base64-encoded so project env values cannot break shell quoting; the
// encoded command is deliberately not written to the deploy log.
func (t *dockerTarget) composeUp(ctx context.Context, job deployJob, workdir, tag string, logf loggerFunc) error {
	content := composeFile(job.project, tag)
	encoded := base64.StdEncoding.EncodeToString([]byte(content))
	write := fmt.Sprintf("echo %s | base64 -d > %s", encoded, shellQuote(path.Join(workdir, "docker-compose.yml")))
	if out, err := execCmd(ctx, t.runner, write, "write docker-compose.yml", logf); err != nil {
		return fmt.Errorf("write compose file: %w\n%s", err, out)
	}

	up := fmt.Sprintf("cd %s && docker compose -f docker-compose.yml up -d --remove-orphans", shellQuote(workdir))
	if out, err := execCmd(ctx, t.runner, up, "docker compose up -d", logf); err != nil {
		return fmt.Errorf("compose up: %w\n%s", err, out)
	}
	return nil
}

// healthCheck polls the running container through the published port until it
// answers or the timeout elapses.
func (t *dockerTarget) healthCheck(ctx context.Context, job deployJob, logf loggerFunc) error {
	if job.project.Port == 0 {
		logf("no port configured; skipping health check")
		return nil
	}
	interval, attempts := healthTiming(t.cfg)
	url := healthURL(job.project)
	cmd := fmt.Sprintf(
		"for i in $(seq 1 %d); do if curl -fsS --max-time 3 %s >/dev/null 2>&1; then echo healthy; exit 0; fi; sleep %d; done; echo 'health check failed'; exit 1",
		attempts, shellQuote(url), interval)
	if out, err := execCmd(ctx, t.runner, cmd, "health check "+url, logf); err != nil {
		return fmt.Errorf("health check failed: %w\n%s", err, out)
	}
	return nil
}

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
