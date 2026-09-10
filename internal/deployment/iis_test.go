package deployment

import (
	"context"
	"strings"
	"testing"

	"github.com/Devonlegend/winify/internal/config"
)

const backupPath = `C:\control-center\backups\proj-002\20260101-000000`

func iisTargetWith(runner Runner) Target {
	return NewIISTarget(config.Default(), runner)
}

func TestIISDeployPipelineOrder(t *testing.T) {
	runner := &fakeRunner{outputs: func(cmd string) string {
		if strings.Contains(cmd, "$stamp = Get-Date") {
			return backupPath + "\n"
		}
		return ""
	}}
	project := iisProject()
	project.IISService = "W3SVC"
	tgt := iisTargetWith(runner)

	artifact, err := tgt.Deploy(context.Background(), deployJob{project: project, commit: "abc1234"}, noopLogf)
	if err != nil {
		t.Fatalf("Deploy: %v", err)
	}
	if artifact != backupPath {
		t.Fatalf("artifact = %q, want backup path", artifact)
	}

	// The pipeline must be ordered: validate -> stop -> backup -> deploy ->
	// restart/recycle -> smoke.
	steps := []struct{ name, marker string }{
		{"validate", "web.config"},
		{"stop", "Stop-WebAppPool"},
		{"backup", "$stamp = Get-Date"},
		{"deploy", "deploy robocopy failed"},
		{"restart/recycle", "Restart-WebAppPool"},
		{"smoke", "Invoke-WebRequest"},
	}
	prev := -1
	for _, s := range steps {
		idx := runner.indexOf(s.marker)
		if idx < 0 {
			t.Fatalf("pipeline missing %s step (marker %q):\n%s", s.name, s.marker, runner.joined())
		}
		if idx <= prev {
			t.Fatalf("step %s out of order (index %d after %d)", s.name, idx, prev)
		}
		prev = idx
	}
}

func TestIISValidateFailureBlocksLiveChanges(t *testing.T) {
	runner := &fakeRunner{failOn: "web.config"}
	tgt := iisTargetWith(runner)

	if _, err := tgt.Deploy(context.Background(), deployJob{project: iisProject(), commit: "abc1234"}, noopLogf); err == nil {
		t.Fatal("Deploy succeeded despite invalid web.config")
	}
	if strings.Contains(runner.joined(), "Stop-WebAppPool") {
		t.Error("app pool was stopped even though validation failed")
	}
	if strings.Contains(runner.joined(), "$stamp = Get-Date") {
		t.Error("backup ran even though validation failed")
	}
}

func TestIISBuildCommandRuns(t *testing.T) {
	runner := &fakeRunner{outputs: func(cmd string) string {
		if strings.Contains(cmd, "$stamp = Get-Date") {
			return backupPath + "\n"
		}
		return ""
	}}
	project := iisProject()
	project.IISBuildCommand = "dotnet publish -c Release -o publish"
	tgt := iisTargetWith(runner)

	if _, err := tgt.Deploy(context.Background(), deployJob{project: project, commit: "abc1234"}, noopLogf); err != nil {
		t.Fatalf("Deploy: %v", err)
	}
	if !strings.Contains(runner.joined(), "dotnet publish -c Release -o publish") {
		t.Errorf("build command did not run:\n%s", runner.joined())
	}
}

func TestIISMissingAppPoolFailsFast(t *testing.T) {
	runner := &fakeRunner{}
	project := iisProject()
	project.IISAppPool = ""
	tgt := iisTargetWith(runner)

	if _, err := tgt.Deploy(context.Background(), deployJob{project: project, commit: "abc1234"}, noopLogf); err == nil {
		t.Fatal("Deploy succeeded without an app pool")
	}
	if len(runner.commands) != 0 {
		t.Fatalf("commands ran before validation: %v", runner.commands)
	}
}

func TestIISRollbackRestoresLatestBackup(t *testing.T) {
	runner := &fakeRunner{outputs: func(cmd string) string {
		if strings.Contains(cmd, "Get-ChildItem -LiteralPath") {
			return backupPath + "\n"
		}
		return ""
	}}
	project := iisProject()
	project.IISService = "W3SVC"
	tgt := iisTargetWith(runner)

	artifact, err := tgt.Rollback(context.Background(), deployJob{project: project, rollback: true}, noopLogf)
	if err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	if artifact != backupPath {
		t.Fatalf("artifact = %q, want %q", artifact, backupPath)
	}
	cmds := runner.joined()
	if !strings.Contains(cmds, "Stop-WebAppPool") {
		t.Error("rollback did not stop the app pool")
	}
	if !strings.Contains(cmds, "Restart-WebAppPool") {
		t.Error("rollback did not recycle the app pool")
	}
	if !strings.Contains(cmds, "Invoke-WebRequest") {
		t.Error("rollback did not smoke test")
	}
	// The restore must copy FROM the backup path.
	if !strings.Contains(cmds, backupPath) {
		t.Error("rollback did not reference the backup path")
	}
}
