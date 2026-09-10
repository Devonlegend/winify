package deployment

import (
	"context"
	"strings"
	"testing"

	"github.com/Devonlegend/winify/internal/config"
)

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
