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

func TestIISPipelineCreatesSiteAndPool(t *testing.T) {
	runner := &fakeRunner{outputs: func(cmd string) string {
		if strings.Contains(cmd, "$stamp = Get-Date") {
			return backupPath + "\n"
		}
		return ""
	}}
	tgt := iisTargetWith(runner)

	if _, err := tgt.Deploy(context.Background(), deployJob{project: iisProject(), commit: "abc1234"}, noopLogf); err != nil {
		t.Fatalf("Deploy: %v", err)
	}
	cmds := runner.joined()
	for _, want := range []string{"New-WebAppPool", "New-Website"} {
		if !strings.Contains(cmds, want) {
			t.Errorf("ensure step missing %q:\n%s", want, cmds)
		}
	}
	// The site/pool must be ensured before validation and any live change.
	ensure := runner.indexOf("New-WebAppPool")
	validate := runner.indexOf("web.config")
	if ensure < 0 || validate < 0 || ensure > validate {
		t.Errorf("ensure (%d) must run before validate (%d)", ensure, validate)
	}
}

func TestSyncRepoScriptTrustsDirectoryAndGuardsFailures(t *testing.T) {
	script := syncRepoScript(`C:\control-center\app`, "https://example.com/app.git", "main", nil)
	for _, want := range []string{
		`-c safe.directory='C:\control-center\app'`,
		"throw 'git fetch: exit ",
		"throw 'git checkout: exit ",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("sync script missing %q:\n%s", want, script)
		}
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

func TestBackupScriptDoesNotCreateEmptyFirstRelease(t *testing.T) {
	script := backupScript(`C:\\inetpub\\wwwroot\\app`, `C:\\backups`, "app")
	if !strings.Contains(script, "NO_BACKUP") || !strings.Contains(script, "PathType Container") {
		t.Fatalf("backup script does not guard a missing live path:\n%s", script)
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

func TestIISBlueGreenDeploySwapsSlot(t *testing.T) {
	runner := &fakeRunner{outputs: func(cmd string) string {
		if strings.Contains(cmd, "Get-Content -Raw -LiteralPath") && strings.Contains(cmd, ".active") {
			return `C:\inetpub\wwwroot\portal` + "\n" // active slot is A
		}
		return ""
	}}
	project := iisProject()
	project.IISBlueGreen = true
	tgt := iisTargetWith(runner)

	artifact, err := tgt.Deploy(context.Background(), deployJob{project: project, commit: "abc1234"}, noopLogf)
	if err != nil {
		t.Fatalf("Deploy: %v", err)
	}
	wantSlot := `C:\inetpub\wwwroot\portal.blue`
	if artifact != wantSlot {
		t.Fatalf("artifact = %q, want slot %q", artifact, wantSlot)
	}
	cmds := runner.joined()
	if strings.Contains(cmds, "Stop-WebAppPool") {
		t.Error("blue-green deploy stopped the app pool (should never stop it)")
	}
	if strings.Contains(cmds, "$stamp = Get-Date") {
		t.Error("blue-green deploy took a backup (slots replace backups)")
	}
	if !strings.Contains(cmds, "Set-ItemProperty") || !strings.Contains(cmds, "Restart-WebAppPool") {
		t.Error("blue-green deploy did not swap/restart")
	}
	if !strings.Contains(cmds, wantSlot) {
		t.Errorf("inactive slot %q not referenced:\n%s", wantSlot, cmds)
	}
	// The swap must happen after the copy to the slot.
	if copyIdx, swapIdx := runner.indexOf("deploy to slot"), runner.indexOf("physicalPath -Value $target"); copyIdx < 0 || swapIdx < 0 || swapIdx < copyIdx {
		t.Errorf("slot copy (cmd %d) must precede the swap (cmd %d)", copyIdx, swapIdx)
	}
}

func TestIISBlueGreenRollbackSwapsBack(t *testing.T) {
	slotB := `C:\inetpub\wwwroot\portal.blue`
	runner := &fakeRunner{outputs: func(cmd string) string {
		if strings.Contains(cmd, ".active") {
			return slotB + "\n"
		}
		return ""
	}}
	project := iisProject()
	project.IISBlueGreen = true
	tgt := iisTargetWith(runner)

	artifact, err := tgt.Rollback(context.Background(), deployJob{project: project, rollback: true}, noopLogf)
	if err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	if artifact != `C:\inetpub\wwwroot\portal` {
		t.Fatalf("artifact = %q, want slot A", artifact)
	}
	cmds := runner.joined()
	if !strings.Contains(cmds, "physicalPath -Value $target") {
		t.Error("rollback did not repoint the site")
	}
	if !strings.Contains(cmds, "Set-Content -LiteralPath $marker") {
		t.Error("rollback did not update the active-slot marker")
	}
}

func TestEnsureIISBlueGreenKeepsActivePath(t *testing.T) {
	project := iisProject()
	project.IISBlueGreen = true
	script := ensureIISScript(project)
	if !strings.Contains(script, "New-Website") {
		t.Error("site creation missing")
	}
	if strings.Contains(script, "Set-ItemProperty \"IIS:\\Sites\\$site\" -Name physicalPath") {
		t.Error("blue-green ensure must not repoint an existing site's physicalPath")
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
