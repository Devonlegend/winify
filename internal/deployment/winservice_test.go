package deployment

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Devonlegend/winify/internal/config"
)

func winsvcTargetWith(runner Runner) Target {
	cfg := config.Default()
	cfg.Deploy.NSSMSource = "" // no upload; NSSM assumed present
	return NewWindowsServiceTarget(cfg, runner)
}

func winsvcProject() config.Project {
	return config.Project{
		ID: "proj-003", Name: "Worker", ServerID: "server-003",
		Strategy: config.ServerTypeWindowsService, RepoURL: "https://example.com/worker.git",
		Branch: "main", Domain: "worker.example.com", Port: 8080,
		ServiceName:    "MyWorker",
		ServiceExe:     `C:\control-center\apps\proj-003\worker.exe`,
		ServiceWorkDir: `C:\control-center\apps\proj-003`,
	}
}

func TestWindowsServiceDeployPipelineOrder(t *testing.T) {
	runner := &fakeRunner{outputs: func(cmd string) string {
		if strings.Contains(cmd, "$stamp = Get-Date") {
			return backupPath + "\n"
		}
		return ""
	}}
	tgt := winsvcTargetWith(runner)

	artifact, err := tgt.Deploy(context.Background(), deployJob{project: winsvcProject(), commit: "abc1234"}, noopLogf)
	if err != nil {
		t.Fatalf("Deploy: %v", err)
	}
	if artifact != backupPath {
		t.Fatalf("artifact = %q, want backup path", artifact)
	}

	steps := []struct{ name, marker string }{
		{"validate", "nssm.exe not found"},
		{"stop", "nssm.exe' stop"},
		{"backup", "$stamp = Get-Date"},
		{"deploy", "deploy robocopy failed"},
		{"install", "$nssm install"},
		{"start", "nssm.exe' start"},
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

func TestWindowsServiceValidateFailureBlocksLiveChanges(t *testing.T) {
	runner := &fakeRunner{failOn: "nssm.exe not found"}
	tgt := winsvcTargetWith(runner)

	if _, err := tgt.Deploy(context.Background(), deployJob{project: winsvcProject(), commit: "abc1234"}, noopLogf); err == nil {
		t.Fatal("Deploy succeeded despite missing NSSM")
	}
	if strings.Contains(runner.joined(), "nssm.exe' stop") {
		t.Error("service was stopped even though validation failed")
	}
	if strings.Contains(runner.joined(), "$stamp = Get-Date") {
		t.Error("backup ran even though validation failed")
	}
}

func TestWindowsServiceMissingConfigFailsFast(t *testing.T) {
	runner := &fakeRunner{}
	project := winsvcProject()
	project.ServiceName = ""
	tgt := winsvcTargetWith(runner)

	if _, err := tgt.Deploy(context.Background(), deployJob{project: project, commit: "abc1234"}, noopLogf); err == nil {
		t.Fatal("Deploy succeeded without a service name")
	}
	if len(runner.commands) != 0 {
		t.Fatalf("commands ran before validation: %v", runner.commands)
	}
}

func TestWindowsServiceRollbackRestoresLatestBackup(t *testing.T) {
	runner := &fakeRunner{outputs: func(cmd string) string {
		if strings.Contains(cmd, "Get-ChildItem -LiteralPath") {
			return backupPath + "\n"
		}
		return ""
	}}
	tgt := winsvcTargetWith(runner)

	artifact, err := tgt.Rollback(context.Background(), deployJob{project: winsvcProject(), rollback: true}, noopLogf)
	if err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	if artifact != backupPath {
		t.Fatalf("artifact = %q, want %q", artifact, backupPath)
	}
	cmds := runner.joined()
	if !strings.Contains(cmds, "nssm.exe' stop") {
		t.Error("rollback did not stop the service")
	}
	if !strings.Contains(cmds, "nssm.exe' start") {
		t.Error("rollback did not start the service")
	}
	if !strings.Contains(cmds, "Invoke-WebRequest") {
		t.Error("rollback did not smoke test")
	}
	if !strings.Contains(cmds, backupPath) {
		t.Error("rollback did not reference the backup path")
	}
}

func TestWindowsServiceUploadsNSSM(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "nssm.exe")
	data := []byte("fake-nssm-binary-payload")
	if err := os.WriteFile(src, data, 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	sum := sha256.Sum256(data)
	want := hex.EncodeToString(sum[:])

	hashCalls := 0
	runner := &fakeRunner{outputs: func(cmd string) string {
		switch {
		case strings.Contains(cmd, "Get-FileHash"):
			hashCalls++
			if hashCalls == 1 {
				return "" // not present yet on the target
			}
			return want + "\n"
		case strings.Contains(cmd, "$stamp = Get-Date"):
			return backupPath + "\n"
		default:
			return ""
		}
	}}

	cfg := config.Default()
	cfg.Deploy.NSSMSource = src
	tgt := NewWindowsServiceTarget(cfg, runner)

	if _, err := tgt.Deploy(context.Background(), deployJob{project: winsvcProject(), commit: "abc1234"}, noopLogf); err != nil {
		t.Fatalf("Deploy: %v", err)
	}
	cmds := runner.joined()
	for _, marker := range []string{"Set-Content", "Add-Content", "FromBase64String"} {
		if !strings.Contains(cmds, marker) {
			t.Errorf("upload step missing (marker %q)", marker)
		}
	}
}

func TestValidateServiceScriptChecksBuiltExe(t *testing.T) {
	script := validateServiceScript(
		`C:\control-center\proj-003`, "publish",
		`C:\control-center\apps\proj-003`,
		`C:\control-center\apps\proj-003\worker.exe`,
		`C:\control-center\tools\nssm.exe`)
	if !strings.Contains(script, "Join-Path $src 'worker.exe'") {
		t.Fatalf("validate script does not check the built exe:\n%s", script)
	}

	// An exe outside the install directory cannot be mapped to the source tree,
	// so no source check is emitted.
	outside := validateServiceScript(
		`C:\control-center\proj-003`, ".",
		`C:\control-center\apps\proj-003`,
		`D:\other\worker.exe`,
		`C:\control-center\tools\nssm.exe`)
	if strings.Contains(outside, "worker.exe") {
		t.Fatalf("validate script checked an unmappable exe:\n%s", outside)
	}
}
