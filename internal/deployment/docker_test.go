package deployment

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/Devonlegend/winify/internal/config"
)

// decodeGeneratedCompose extracts and decodes the base64 payload of a
// "echo <b64> | base64 -d > file" command.
func decodeGeneratedCompose(t *testing.T, cmd string) string {
	t.Helper()
	start := strings.Index(cmd, "echo ")
	end := strings.Index(cmd, " | base64 -d")
	if start < 0 || end < 0 {
		t.Fatalf("unexpected write command: %s", cmd)
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(cmd[start+len("echo ") : end]))
	if err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	return string(decoded)
}

// composePayload returns the decoded content of the first generated-file write.
func composePayload(t *testing.T, runner *fakeRunner) string {
	t.Helper()
	for _, cmd := range runner.commands {
		if strings.Contains(cmd, "base64 -d") && strings.Contains(cmd, "docker-compose.yml") {
			return decodeGeneratedCompose(t, cmd)
		}
	}
	t.Fatal("no generated compose write found")
	return ""
}

func noopLogf(string, ...any) {}

func TestDockerDeploySuccess(t *testing.T) {
	cfg := config.Default()
	runner := &fakeRunner{}
	tgt := NewDockerTarget(cfg, runner)

	artifact, err := tgt.Deploy(context.Background(), deployJob{project: dockerProject(), commit: "abcdef1234567"}, noopLogf)
	if err != nil {
		t.Fatalf("Deploy: %v", err)
	}
	if artifact != "cc/proj-001:abcdef1" {
		t.Fatalf("artifact = %q", artifact)
	}
	cmds := runner.joined()
	for _, want := range []string{"git clone", "docker build", "docker compose", "curl"} {
		if !strings.Contains(cmds, want) {
			t.Errorf("commands missing %q:\n%s", want, cmds)
		}
	}
	if !runner.closed {
		// dockerTarget.Close is called by the Deployer, not by Deploy itself.
		t.Log("runner not closed by target (closed by Deployer)")
	}
}

func TestDockerBuildFailureStopsPipeline(t *testing.T) {
	runner := &fakeRunner{failOn: "docker build"}
	tgt := NewDockerTarget(config.Default(), runner)

	if _, err := tgt.Deploy(context.Background(), deployJob{project: dockerProject(), commit: "abc1234"}, noopLogf); err == nil {
		t.Fatal("Deploy succeeded despite build failure")
	}
	if strings.Contains(runner.joined(), "docker compose") {
		t.Error("compose ran after a failed build")
	}
}

func TestDockerRollbackSkipsBuild(t *testing.T) {
	runner := &fakeRunner{}
	tgt := NewDockerTarget(config.Default(), runner)

	artifact, err := tgt.Rollback(context.Background(), deployJob{
		project: dockerProject(), artifact: "cc/proj-001:aaa1111", rollback: true,
	}, noopLogf)
	if err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	if artifact != "cc/proj-001:aaa1111" {
		t.Fatalf("artifact = %q", artifact)
	}
	cmds := runner.joined()
	if strings.Contains(cmds, "docker build") {
		t.Errorf("rollback rebuilt the image:\n%s", cmds)
	}
	if !strings.Contains(cmds, "docker compose") {
		t.Errorf("rollback did not compose up:\n%s", cmds)
	}
}

func TestDockerHealthCheckFailure(t *testing.T) {
	runner := &fakeRunner{failOn: "curl"}
	tgt := NewDockerTarget(config.Default(), runner)

	if _, err := tgt.Deploy(context.Background(), deployJob{project: dockerProject(), commit: "abc1234"}, noopLogf); err == nil {
		t.Fatal("Deploy succeeded despite a failed health check")
	}
}

func TestDockerHealthCheckOptional(t *testing.T) {
	runner := &fakeRunner{failOn: "curl"}
	tgt := NewDockerTarget(config.Default(), runner)
	p := dockerProject()
	p.DisableHealthCheck = true

	artifact, err := tgt.Deploy(context.Background(), deployJob{project: p, commit: "abc1234"}, noopLogf)
	if err != nil {
		t.Fatalf("Deploy with the health check disabled failed: %v", err)
	}
	if artifact == "" {
		t.Fatal("no artifact returned")
	}
	if strings.Contains(runner.joined(), "curl") {
		t.Error("health check ran even though it was disabled")
	}
}

func TestDockerImageSource(t *testing.T) {
	runner := &fakeRunner{}
	tgt := NewDockerTarget(config.Default(), runner)
	p := dockerProject()
	p.Source = config.ProjectSourceImage
	p.Image = "nginx:1.27"

	artifact, err := tgt.Deploy(context.Background(), deployJob{project: p, commit: "abc1234"}, noopLogf)
	if err != nil {
		t.Fatalf("Deploy: %v", err)
	}
	if artifact != "nginx:1.27" {
		t.Fatalf("artifact = %q, want the image ref", artifact)
	}
	cmds := runner.joined()
	if strings.Contains(cmds, "git clone") {
		t.Error("image source should not clone a repo")
	}
	if compose := composePayload(t, runner); !strings.Contains(compose, "image: nginx:1.27") {
		t.Errorf("generated compose does not reference the image:\n%s", compose)
	}
	if !strings.Contains(cmds, "docker compose") || !strings.Contains(cmds, "curl") {
		t.Errorf("compose up / health check missing:\n%s", cmds)
	}
}

func TestDockerComposeSource(t *testing.T) {
	runner := &fakeRunner{}
	tgt := NewDockerTarget(config.Default(), runner)
	p := dockerProject()
	p.Source = config.ProjectSourceCompose
	p.ComposePath = "deploy/docker-compose.yml"
	p.Env = map[string]string{"FOO": "bar"}

	artifact, err := tgt.Deploy(context.Background(), deployJob{project: p, commit: "abc1234"}, noopLogf)
	if err != nil {
		t.Fatalf("Deploy: %v", err)
	}
	if artifact != "abc1234" {
		t.Fatalf("artifact = %q, want the commit", artifact)
	}
	cmds := runner.joined()
	for _, want := range []string{"git clone", "deploy/docker-compose.yml", "--build", ".env", "curl"} {
		if !strings.Contains(cmds, want) {
			t.Errorf("compose pipeline missing %q:\n%s", want, cmds)
		}
	}
	if strings.Contains(cmds, "docker build -t") {
		t.Errorf("compose source should not run docker build directly:\n%s", cmds)
	}
}

func TestDockerComposeRollback(t *testing.T) {
	runner := &fakeRunner{}
	tgt := NewDockerTarget(config.Default(), runner)
	p := dockerProject()
	p.Source = config.ProjectSourceCompose

	artifact, err := tgt.Rollback(context.Background(), deployJob{
		project: p, commit: "prevsha", rollback: true,
	}, noopLogf)
	if err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	if artifact != "prevsha" {
		t.Fatalf("artifact = %q, want the previous commit", artifact)
	}
	if !strings.Contains(runner.joined(), "git clone") {
		t.Error("compose rollback should check out the previous commit")
	}
}
