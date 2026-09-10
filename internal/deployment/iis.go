package deployment

import (
	"context"
	"fmt"
	"strings"

	"github.com/Devonlegend/winify/internal/config"
)

// iisTarget deploys an IIS application on a Windows target over WinRM.
//
// Pipeline: sync repo -> optional build -> validate config/prerequisites ->
// stop service & app pool -> timestamped backup -> copy files -> start service
// & recycle app pool -> HTTP smoke test. Validation and backup happen before
// any live change, so a bad build never takes the site down.
type iisTarget struct {
	cfg    config.Config
	runner Runner
}

// NewIISTarget builds the IIS pipeline over an open Runner.
func NewIISTarget(cfg config.Config, runner Runner) Target {
	return &iisTarget{cfg: cfg, runner: runner}
}

func (t *iisTarget) Close() error { return t.runner.Close() }

// Deploy runs the full IIS pipeline and returns the backup path as the
// deployment artifact.
func (t *iisTarget) Deploy(ctx context.Context, job deployJob, logf loggerFunc) (string, error) {
	p := job.project
	if p.IISPhysicalPath == "" {
		return "", fmt.Errorf("project %s has no iis_physical_path", p.ID)
	}
	if p.IISAppPool == "" {
		return "", fmt.Errorf("project %s has no iis_app_pool", p.ID)
	}
	source := p.IISSourceSubdir
	if source == "" {
		source = "."
	}
	repoDir := winPath(t.cfg.Deploy.IISWorkDir, p.ID)

	logf("iis pipeline: repo=%s source=%s physical=%s pool=%s", repoDir, source, p.IISPhysicalPath, p.IISAppPool)

	// 1. Fetch the requested revision.
	if _, err := execCmd(ctx, t.runner, syncRepoScript(repoDir, p.RepoURL, job.commit), "git clone/fetch + checkout "+shortSHA(job.commit), logf); err != nil {
		return "", fmt.Errorf("clone/checkout: %w", err)
	}

	// 2. Optional build (for example: dotnet publish into the source subdir).
	if p.IISBuildCommand != "" {
		if _, err := execCmd(ctx, t.runner, buildScript(repoDir, p.IISBuildCommand), "iis build command", logf); err != nil {
			return "", fmt.Errorf("build: %w", err)
		}
	}

	// 3. Validate BEFORE touching anything live.
	if _, err := execCmd(ctx, t.runner, validateScript(repoDir, source, p.IISAppPool, p.IISService), "validate configuration", logf); err != nil {
		return "", fmt.Errorf("validation failed: %w", err)
	}

	// 4. Stop the service and app pool.
	if _, err := execCmd(ctx, t.runner, stopScript(p.IISAppPool, p.IISService), "stop service/app pool", logf); err != nil {
		return "", fmt.Errorf("stop: %w", err)
	}

	// 5. Backup the current live files (timestamped).
	backupOut, err := execCmd(ctx, t.runner, backupScript(p.IISPhysicalPath, t.cfg.Deploy.IISBackupDir, p.ID), "backup live files", logf)
	if err != nil {
		return "", fmt.Errorf("backup: %w", err)
	}
	backup := lastLine(backupOut)
	logf("backup created: %s", backup)

	// 6. Deploy the new build files.
	srcDir := winPath(repoDir, source)
	if _, err := execCmd(ctx, t.runner, copyScript(srcDir, p.IISPhysicalPath, "deploy"), "deploy files", logf); err != nil {
		return "", fmt.Errorf("deploy files: %w", err)
	}

	// 7. Start the service and recycle the app pool.
	if _, err := execCmd(ctx, t.runner, startScript(p.IISAppPool, p.IISService), "start service/recycle app pool", logf); err != nil {
		return "", fmt.Errorf("start: %w", err)
	}

	// 8. Smoke test.
	if err := t.smokeTest(ctx, p, logf); err != nil {
		return "", err
	}
	return backup, nil
}

// Rollback restores the most recent timestamped backup and brings the app back
// up. It discovers the backup on the target rather than relying on history, so
// it works even if deploy history was pruned.
func (t *iisTarget) Rollback(ctx context.Context, job deployJob, logf loggerFunc) (string, error) {
	p := job.project
	if p.IISPhysicalPath == "" {
		return "", fmt.Errorf("project %s has no iis_physical_path", p.ID)
	}

	backupOut, err := execCmd(ctx, t.runner, latestBackupScript(t.cfg.Deploy.IISBackupDir, p.ID), "find latest backup", logf)
	if err != nil {
		return "", fmt.Errorf("find backup: %w", err)
	}
	backup := lastLine(backupOut)
	if backup == "" {
		return "", fmt.Errorf("no backup found for project %s", p.ID)
	}
	logf("rollback: restoring %s", backup)

	if _, err := execCmd(ctx, t.runner, stopScript(p.IISAppPool, p.IISService), "stop service/app pool", logf); err != nil {
		return "", fmt.Errorf("stop: %w", err)
	}
	if _, err := execCmd(ctx, t.runner, copyScript(backup, p.IISPhysicalPath, "restore backup"), "restore backup", logf); err != nil {
		return "", fmt.Errorf("restore backup: %w", err)
	}
	if _, err := execCmd(ctx, t.runner, startScript(p.IISAppPool, p.IISService), "start service/recycle app pool", logf); err != nil {
		return "", fmt.Errorf("start: %w", err)
	}
	if err := t.smokeTest(ctx, p, logf); err != nil {
		return "", err
	}
	return backup, nil
}

func (t *iisTarget) smokeTest(ctx context.Context, p config.Project, logf loggerFunc) error {
	if p.Port == 0 {
		logf("no port configured; skipping smoke test")
		return nil
	}
	interval, attempts := healthTiming(t.cfg)
	url := healthURL(p)
	if _, err := execCmd(ctx, t.runner, smokeScript(url, attempts, interval), "smoke test "+url, logf); err != nil {
		return fmt.Errorf("smoke test failed: %w", err)
	}
	return nil
}

// ---- PowerShell script builders ----

func syncRepoScript(repoDir, repoURL, commit string) string {
	var b strings.Builder
	b.WriteString("$ErrorActionPreference='Stop'\n")
	fmt.Fprintf(&b, "New-Item -ItemType Directory -Force -Path %s | Out-Null\n", psQuote(repoDir))
	fmt.Fprintf(&b, "if (Test-Path (Join-Path %s '.git')) {\n", psQuote(repoDir))
	fmt.Fprintf(&b, "  git -C %s fetch --all --prune\n", psQuote(repoDir))
	if commit != "" {
		fmt.Fprintf(&b, "  git -C %s checkout --force %s\n", psQuote(repoDir), psQuote(commit))
	} else {
		fmt.Fprintf(&b, "  git -C %s pull --ff-only\n", psQuote(repoDir))
	}
	b.WriteString("} else {\n")
	fmt.Fprintf(&b, "  git clone %s %s\n", psQuote(repoURL), psQuote(repoDir))
	if commit != "" {
		fmt.Fprintf(&b, "  git -C %s checkout --force %s\n", psQuote(repoDir), psQuote(commit))
	}
	b.WriteString("}\n")
	return b.String()
}

func buildScript(repoDir, buildCommand string) string {
	return fmt.Sprintf("$ErrorActionPreference='Stop'\nSet-Location %s\n%s\n", psQuote(repoDir), buildCommand)
}

// validateScript checks the source tree, web.config XML and IIS prerequisites.
// It must run before anything live is stopped or overwritten.
func validateScript(repoDir, source, appPool, service string) string {
	var b strings.Builder
	b.WriteString("$ErrorActionPreference='Stop'\n")
	fmt.Fprintf(&b, "$src = Join-Path %s %s\n", psQuote(repoDir), psQuote(source))
	b.WriteString("if (-not (Test-Path $src)) { throw \"source directory not found: $src\" }\n")
	b.WriteString("$wc = Join-Path $src 'web.config'\n")
	b.WriteString("if (Test-Path $wc) { $null = [xml](Get-Content -Raw -LiteralPath $wc); Write-Output 'web.config is valid XML' }\n")
	b.WriteString("else { Write-Output 'no web.config present; skipping XML validation' }\n")
	b.WriteString("Import-Module WebAdministration\n")
	fmt.Fprintf(&b, "$poolPath = 'IIS:\\AppPools\\' + %s\n", psQuote(appPool))
	fmt.Fprintf(&b, "if (-not (Test-Path $poolPath)) { throw \"IIS app pool not found: %s\" }\n", appPool)
	if service != "" {
		fmt.Fprintf(&b, "if (-not (Get-Service -Name %s -ErrorAction SilentlyContinue)) { throw \"service not found: %s\" }\n", psQuote(service), service)
	}
	return b.String()
}

func stopScript(appPool, service string) string {
	var b strings.Builder
	b.WriteString("$ErrorActionPreference='Stop'\n")
	if service != "" {
		fmt.Fprintf(&b, "if (Get-Service -Name %s -ErrorAction SilentlyContinue) { Stop-Service -Name %s -Force }\n", psQuote(service), psQuote(service))
	}
	b.WriteString("Import-Module WebAdministration\n")
	fmt.Fprintf(&b, "$poolPath = 'IIS:\\AppPools\\' + %s\n", psQuote(appPool))
	fmt.Fprintf(&b, "if (Test-Path $poolPath) { if ((Get-WebAppPoolState -Name %s).Value -ne 'Stopped') { Stop-WebAppPool -Name %s } }\n", psQuote(appPool), psQuote(appPool))
	return b.String()
}

func startScript(appPool, service string) string {
	var b strings.Builder
	b.WriteString("$ErrorActionPreference='Stop'\n")
	b.WriteString("Import-Module WebAdministration\n")
	fmt.Fprintf(&b, "$poolPath = 'IIS:\\AppPools\\' + %s\n", psQuote(appPool))
	fmt.Fprintf(&b, "if (Test-Path $poolPath) {\n")
	fmt.Fprintf(&b, "  if ((Get-WebAppPoolState -Name %s).Value -eq 'Stopped') { Start-WebAppPool -Name %s }\n", psQuote(appPool), psQuote(appPool))
	fmt.Fprintf(&b, "  Restart-WebAppPool -Name %s\n}\n", psQuote(appPool))
	if service != "" {
		fmt.Fprintf(&b, "if (Get-Service -Name %s -ErrorAction SilentlyContinue) { Start-Service -Name %s }\n", psQuote(service), psQuote(service))
	}
	return b.String()
}

// backupScript copies the live directory to a timestamped folder and prints its
// full path as the last line of output.
func backupScript(physicalPath, backupRoot, projectID string) string {
	var b strings.Builder
	b.WriteString("$ErrorActionPreference='Stop'\n")
	b.WriteString("$stamp = Get-Date -Format 'yyyyMMdd-HHmmss'\n")
	fmt.Fprintf(&b, "$dest = Join-Path %s (%s + $stamp)\n", psQuote(backupRoot), psQuote(projectID+`\`))
	b.WriteString("New-Item -ItemType Directory -Force -Path $dest | Out-Null\n")
	fmt.Fprintf(&b, "if (Test-Path %s) {\n", psQuote(physicalPath))
	fmt.Fprintf(&b, "  robocopy %s $dest /MIR /NFL /NDL /NJH /NJS /NP | Out-Null\n", psQuote(physicalPath))
	b.WriteString("  if ($LASTEXITCODE -ge 8) { throw \"backup robocopy failed: $LASTEXITCODE\" }\n}\n")
	b.WriteString("Write-Output $dest\n")
	return b.String()
}

func latestBackupScript(backupRoot, projectID string) string {
	var b strings.Builder
	b.WriteString("$ErrorActionPreference='Stop'\n")
	fmt.Fprintf(&b, "$root = Join-Path %s %s\n", psQuote(backupRoot), psQuote(projectID))
	fmt.Fprintf(&b, "if (-not (Test-Path $root)) { throw \"no backups found for %s\" }\n", projectID)
	b.WriteString("$latest = Get-ChildItem -LiteralPath $root -Directory | Sort-Object Name -Descending | Select-Object -First 1\n")
	fmt.Fprintf(&b, "if ($null -eq $latest) { throw \"no backups found for %s\" }\n", projectID)
	b.WriteString("Write-Output $latest.FullName\n")
	return b.String()
}

// copyScript mirrors src into dst with robocopy. robocopy exit codes below 8
// are success (1 means files copied).
func copyScript(src, dst, label string) string {
	var b strings.Builder
	b.WriteString("$ErrorActionPreference='Stop'\n")
	fmt.Fprintf(&b, "New-Item -ItemType Directory -Force -Path %s | Out-Null\n", psQuote(dst))
	fmt.Fprintf(&b, "robocopy %s %s /MIR /NFL /NDL /NJH /NJS /NP | Out-Null\n", psQuote(src), psQuote(dst))
	fmt.Fprintf(&b, "if ($LASTEXITCODE -ge 8) { throw \"%s robocopy failed: $LASTEXITCODE\" }\n", label)
	return b.String()
}

func smokeScript(url string, attempts, interval int) string {
	var b strings.Builder
	b.WriteString("$ErrorActionPreference='Stop'\n")
	fmt.Fprintf(&b, "$url = %s\n", psQuote(url))
	fmt.Fprintf(&b, "for ($i=0; $i -lt %d; $i++) {\n", attempts)
	b.WriteString("  try {\n")
	b.WriteString("    $r = Invoke-WebRequest -UseBasicParsing -Uri $url -TimeoutSec 3\n")
	b.WriteString("    if ($r.StatusCode -ge 200 -and $r.StatusCode -lt 400) { Write-Output 'smoke test ok'; exit 0 }\n")
	b.WriteString("  } catch { }\n")
	fmt.Fprintf(&b, "  Start-Sleep -Seconds %d\n", interval)
	b.WriteString("}\n")
	fmt.Fprintf(&b, "throw \"smoke test failed: $url\"\n")
	return b.String()
}
