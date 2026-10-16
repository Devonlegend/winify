package deployment

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Devonlegend/winify/internal/config"
)

// iisTarget deploys an IIS application on a Windows target over WinRM.
//
// Pipeline: sync repo -> optional build -> validate config/prerequisites ->
// stop service & app pool -> timestamped backup -> copy files -> start service
// & recycle app pool -> HTTP smoke test. Validation and backup happen before
// any live change, so a bad build never takes the site down.
type iisTarget struct {
	cfg     config.Config
	runner  Runner
	secrets SecretResolver
}

// NewIISTarget builds the IIS pipeline over an open Runner. secrets resolves
// credential refs (WinRM at connection time; git credentials at clone time).
func NewIISTarget(cfg config.Config, runner Runner, secrets ...SecretResolver) Target {
	var s SecretResolver
	if len(secrets) > 0 {
		s = secrets[0]
	}
	return &iisTarget{cfg: cfg, runner: runner, secrets: s}
}

func (t *iisTarget) Close() error { return t.runner.Close() }

// Deploy runs the full IIS pipeline and returns the backup path as the
// deployment artifact.
func (t *iisTarget) Deploy(ctx context.Context, job deployJob, logf loggerFunc) (string, error) {
	if err := validateProjectID(job.project.ID); err != nil {
		return "", err
	}
	if err := validateTargetProjectPaths(t.cfg, job.project, job.server); err != nil {
		return "", err
	}
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

	// 1. Fetch the requested revision. A private repo credential (deploy key or
	// token) is staged first; the key/token never appears in repo_url or logs.
	gitAuth, err := PrepareGitAuth(ctx, t.runner, t.secrets, p, winPath(t.cfg.Deploy.IISWorkDir, p.ID+".gitkey"), true, logf)
	if err != nil {
		return "", err
	}
	syncCtx := ctx
	if gitAuth.Sensitive() {
		syncCtx = withAuditRedaction(ctx, "git clone/fetch + checkout (credential redacted)")
	}
	if _, err := execCmd(syncCtx, t.runner, syncRepoScript(repoDir, p.RepoURL, deployRevision(p, job.commit), gitAuth), "git clone/fetch + checkout "+shortSHA(deployRevision(p, job.commit)), logf); err != nil {
		return "", fmt.Errorf("clone/checkout: %w", err)
	}

	// Make sure the build's toolchain is present, installing it if missing.
	if err := ensureRuntime(ctx, t.runner, p.Runtime, logf); err != nil {
		return "", err
	}

	// 2. Optional build (for example: dotnet publish into the source subdir).
	if p.IISBuildCommand != "" {
		buildCtx := ctx
		if len(p.BuildEnv) > 0 {
			// Build env values can hold secrets; keep them out of the audit log.
			buildCtx = withAuditRedaction(ctx, "iis build command (build env redacted)")
		}
		if _, err := execCmd(buildCtx, t.runner, buildScript(repoDir, p.IISBuildCommand, p.BuildEnv), "iis build command", logf); err != nil {
			return "", fmt.Errorf("build: %w", err)
		}
	}

	// 3. Ensure the IIS site and application pool exist (create if missing).
	if _, err := execCmd(ctx, t.runner, ensureIISScript(p), "ensure IIS site/app pool", logf); err != nil {
		return "", fmt.Errorf("ensure IIS site: %w", err)
	}

	// 4. Validate BEFORE touching anything live.
	if _, err := execCmd(ctx, t.runner, validateScript(repoDir, source, p.IISAppPool, p.IISService), "validate configuration", logf); err != nil {
		return "", fmt.Errorf("validation failed: %w", err)
	}

	if p.IISBlueGreen {
		return t.deployBlueGreen(ctx, p, repoDir, source, logf)
	}

	// 5. Stop the service and app pool.
	if _, err := execCmd(ctx, t.runner, stopScript(p.IISAppPool, p.IISService), "stop service/app pool", logf); err != nil {
		t.recoverLive(ctx, p, "", logf)
		return "", fmt.Errorf("stop: %w", err)
	}

	// 6. Backup the current live files (timestamped). A first deployment has
	// no previous release and therefore has no rollback artifact.
	backup := ""
	backupOut, err := execCmd(ctx, t.runner, backupScript(p.IISPhysicalPath, t.cfg.Deploy.IISBackupDir, p.ID), "backup live files", logf)
	if err != nil {
		t.recoverLive(ctx, p, backup, logf)
		return "", fmt.Errorf("backup: %w", err)
	}
	backup = lastLine(backupOut)
	if backup == "NO_BACKUP" {
		backup = ""
		logf("no previous live files; rollback is not available for this first deployment")
	} else {
		logf("backup created: %s", backup)
	}

	// 7. Deploy the new build files.
	srcDir := winPath(repoDir, source)
	if _, err := execCmd(ctx, t.runner, copyScript(srcDir, p.IISPhysicalPath, "deploy"), "deploy files", logf); err != nil {
		t.recoverLive(ctx, p, backup, logf)
		return "", fmt.Errorf("deploy files: %w", err)
	}

	// 8. Start the service and recycle the app pool.
	if _, err := execCmd(ctx, t.runner, startScript(p.IISAppPool, p.IISService), "start service/recycle app pool", logf); err != nil {
		t.recoverLive(ctx, p, backup, logf)
		return "", fmt.Errorf("start: %w", err)
	}

	// 9. Smoke test.
	if err := t.smokeTest(ctx, p, logf); err != nil {
		t.recoverLive(ctx, p, backup, logf)
		return "", err
	}
	return backup, nil
}

// Rollback restores the most recent timestamped backup and brings the app back
// up. It discovers the backup on the target rather than relying on history, so
// it works even if deploy history was pruned. Blue-green projects instead
// repoint the site to the other slot.
func (t *iisTarget) Rollback(ctx context.Context, job deployJob, logf loggerFunc) (string, error) {
	if err := validateProjectID(job.project.ID); err != nil {
		return "", err
	}
	if err := validateTargetProjectPaths(t.cfg, job.project, job.server); err != nil {
		return "", err
	}
	p := job.project
	if p.IISPhysicalPath == "" {
		return "", fmt.Errorf("project %s has no iis_physical_path", p.ID)
	}
	if p.IISBlueGreen {
		return t.rollbackBlueGreen(ctx, p, logf)
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
		t.recoverLive(ctx, p, backup, logf)
		return "", fmt.Errorf("stop: %w", err)
	}
	if _, err := execCmd(ctx, t.runner, copyScript(backup, p.IISPhysicalPath, "restore backup"), "restore backup", logf); err != nil {
		t.recoverLive(ctx, p, backup, logf)
		return "", fmt.Errorf("restore backup: %w", err)
	}
	if _, err := execCmd(ctx, t.runner, startScript(p.IISAppPool, p.IISService), "start service/recycle app pool", logf); err != nil {
		t.recoverLive(ctx, p, backup, logf)
		return "", fmt.Errorf("start: %w", err)
	}
	if err := t.smokeTest(ctx, p, logf); err != nil {
		t.recoverLive(ctx, p, backup, logf)
		return "", err
	}
	return backup, nil
}

// ---- blue-green (slot swap) ----
//
// The live directory is never overwritten: the build is copied to the inactive
// slot, validated there, and the site's physicalPath is repointed atomically
// (the app pool restart recycles the worker without stopping it). A marker file
// next to the physical path records which slot is live, so rollback just
// repoints at the other slot — no file copies, no backups needed.

func blueGreenMarkerPath(physical string) string { return physical + ".active" }
func blueGreenSlotB(physical string) string      { return physical + ".blue" }

func blueGreenInactive(physical, active string) string {
	if active == "" || active == blueGreenSlotB(physical) {
		return physical
	}
	return blueGreenSlotB(physical)
}

func (t *iisTarget) deployBlueGreen(ctx context.Context, p config.Project, repoDir, source string, logf loggerFunc) (string, error) {
	marker := blueGreenMarkerPath(p.IISPhysicalPath)
	activeOut, err := execCmd(ctx, t.runner, blueGreenActiveScript(p.IISPhysicalPath, marker), "read active slot", logf)
	if err != nil {
		return "", fmt.Errorf("read active slot: %w", err)
	}
	active := lastLine(activeOut)
	target := blueGreenInactive(p.IISPhysicalPath, active)
	logf("blue-green: active=%q target=%q", active, target)

	srcDir := winPath(repoDir, source)
	if _, err := execCmd(ctx, t.runner, copyScript(srcDir, target, "deploy to slot"), "copy build to slot", logf); err != nil {
		return "", fmt.Errorf("copy to slot: %w", err)
	}
	if _, err := execCmd(ctx, t.runner, validateDirScript(target, p.IISAppPool, p.IISService), "validate slot", logf); err != nil {
		return "", fmt.Errorf("slot validation failed: %w", err)
	}
	site := p.IISSite
	if site == "" {
		site = p.ID
	}
	if _, err := execCmd(ctx, t.runner, blueGreenSwapScript(site, p.IISAppPool, p.IISService, target, marker), "swap slot", logf); err != nil {
		t.swapBack(ctx, site, p, active, marker, logf)
		return "", fmt.Errorf("swap slot: %w", err)
	}
	if err := t.smokeTest(ctx, p, logf); err != nil {
		t.swapBack(ctx, site, p, active, marker, logf)
		return "", err
	}
	return target, nil
}

func (t *iisTarget) rollbackBlueGreen(ctx context.Context, p config.Project, logf loggerFunc) (string, error) {
	marker := blueGreenMarkerPath(p.IISPhysicalPath)
	activeOut, err := execCmd(ctx, t.runner, blueGreenActiveScript(p.IISPhysicalPath, marker), "read active slot", logf)
	if err != nil {
		return "", fmt.Errorf("read active slot: %w", err)
	}
	active := lastLine(activeOut)
	if active == "" {
		return "", fmt.Errorf("no active slot recorded for %s; nothing to roll back to", p.ID)
	}
	other := blueGreenInactive(p.IISPhysicalPath, active)
	site := p.IISSite
	if site == "" {
		site = p.ID
	}
	logf("blue-green rollback: %s -> %s", active, other)
	if _, err := execCmd(ctx, t.runner, blueGreenSwapScript(site, p.IISAppPool, p.IISService, other, marker), "swap back", logf); err != nil {
		return "", fmt.Errorf("swap back: %w", err)
	}
	if err := t.smokeTest(ctx, p, logf); err != nil {
		t.swapBack(ctx, site, p, active, marker, logf)
		return "", err
	}
	return other, nil
}

// swapBack restores the previously active slot after a failed swap/smoke test.
func (t *iisTarget) swapBack(ctx context.Context, site string, p config.Project, active, marker string, logf loggerFunc) {
	if active == "" {
		return
	}
	recCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
	defer cancel()
	if _, err := execCmd(recCtx, t.runner, blueGreenSwapScript(site, p.IISAppPool, p.IISService, active, marker), "restore previous slot", logf); err != nil {
		logf("recovery: restore previous slot failed: %v", err)
	}
}

func blueGreenActiveScript(physical, marker string) string {
	var b strings.Builder
	b.WriteString("$ErrorActionPreference='Stop'\n")
	fmt.Fprintf(&b, "$marker = %s\n", psQuote(marker))
	fmt.Fprintf(&b, "$physical = %s\n", psQuote(physical))
	b.WriteString("if (Test-Path -LiteralPath $marker) {\n")
	b.WriteString("  $active = (Get-Content -Raw -LiteralPath $marker).Trim()\n")
	b.WriteString("  if ($active -and (Test-Path -LiteralPath $active -PathType Container)) { Write-Output $active; exit 0 }\n")
	b.WriteString("}\n")
	b.WriteString("if (Test-Path -LiteralPath $physical -PathType Container) { Write-Output $physical }\n")
	return b.String()
}

func blueGreenSwapScript(site, appPool, service, target, marker string) string {
	var b strings.Builder
	b.WriteString("$ErrorActionPreference='Stop'\n")
	b.WriteString("Import-Module WebAdministration\n")
	fmt.Fprintf(&b, "$site = %s\n", psQuote(site))
	fmt.Fprintf(&b, "$pool = %s\n", psQuote(appPool))
	fmt.Fprintf(&b, "$service = %s\n", psQuote(service))
	fmt.Fprintf(&b, "$target = %s\n", psQuote(target))
	fmt.Fprintf(&b, "$marker = %s\n", psQuote(marker))
	b.WriteString("if (-not (Test-Path -LiteralPath $target -PathType Container)) { throw \"slot not found: $target\" }\n")
	b.WriteString("if (-not (Get-ChildItem -LiteralPath $target -Recurse -File -ErrorAction SilentlyContinue | Select-Object -First 1)) { throw \"slot is empty: $target\" }\n")
	b.WriteString("Set-ItemProperty \"IIS:\\Sites\\$site\" -Name physicalPath -Value $target\n")
	fmt.Fprintf(&b, "$poolPath = 'IIS:\\AppPools\\' + $pool\n")
	b.WriteString("if (Test-Path $poolPath) {\n")
	b.WriteString("  if ((Get-WebAppPoolState -Name $pool).Value -eq 'Stopped') { Start-WebAppPool -Name $pool }\n")
	b.WriteString("  Restart-WebAppPool -Name $pool\n}\n")
	b.WriteString("if ($service -and (Get-Service -Name $service -ErrorAction SilentlyContinue)) { Restart-Service -Name $service -Force }\n")
	b.WriteString("Set-Content -LiteralPath $marker -Value $target -Encoding ASCII -NoNewline\n")
	b.WriteString("Write-Output $target\n")
	return b.String()
}

func (t *iisTarget) smokeTest(ctx context.Context, p config.Project, logf loggerFunc) error {
	if p.DisableHealthCheck {
		logf("health check disabled; skipping smoke test")
		return nil
	}
	if p.EffectiveHostPort() == 0 {
		logf("no port configured; skipping smoke test")
		return nil
	}
	plan := healthPlanFor(t.cfg, p)
	url := healthURL(p)
	if _, err := execCmd(ctx, t.runner, smokeScript(url, plan), "smoke test "+url, logf); err != nil {
		return fmt.Errorf("smoke test failed: %w", err)
	}
	return nil
}

func (t *iisTarget) recoverLive(ctx context.Context, p config.Project, backup string, logf loggerFunc) {
	// Recovery must still run when the deploy deadline caused the failure. Keep
	// audit metadata from the original context, but give compensation its own
	// bounded lifetime.
	recCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
	defer cancel()
	if backup != "" && backup != "NO_BACKUP" {
		if _, err := execCmd(recCtx, t.runner, copyScript(backup, p.IISPhysicalPath, "restore after failed deploy"), "restore previous release", logf); err != nil {
			logf("recovery: restore previous release failed: %v", err)
		}
	}
	if _, err := execCmd(recCtx, t.runner, startScript(p.IISAppPool, p.IISService), "restart after failed deploy", logf); err != nil {
		logf("recovery: restart failed: %v", err)
	}
}

// ---- PowerShell script builders ----

// exitGuard turns a non-zero exit of the preceding native command into a
// terminating error. Windows PowerShell ignores native exit codes even with
// $ErrorActionPreference='Stop', so a failed git/nssm/pip call would otherwise
// be silently ignored and the pipeline would continue with stale state.
func exitGuard(label string) string {
	return fmt.Sprintf("; if ($LASTEXITCODE -ne 0) { throw %s + $LASTEXITCODE }\n", psQuote(label+": exit "))
}

// gitSafe returns the -c safe.directory option for dir. winify usually runs as
// LocalSystem while a clone may have been made by an interactive user; git
// refuses to operate on a repository owned by another account ("dubious
// ownership") unless the directory is explicitly trusted.
func gitSafe(dir string) string {
	return "-c safe.directory=" + psQuote(dir)
}

func syncRepoScript(repoDir, repoURL, commit string, auth *GitAuth) string {
	var b strings.Builder
	checkout := gitCheckoutRef(commit)
	opt := auth.gitOption(psQuote)
	if opt != "" {
		opt = " " + opt
	}
	b.WriteString("$ErrorActionPreference='Stop'\n")
	fmt.Fprintf(&b, "New-Item -ItemType Directory -Force -Path %s | Out-Null\n", psQuote(repoDir))
	fmt.Fprintf(&b, "if (Test-Path (Join-Path %s '.git')) {\n", psQuote(repoDir))
	fmt.Fprintf(&b, "  git%s %s -C %s fetch --all --prune%s", opt, gitSafe(repoDir), psQuote(repoDir), exitGuard("git fetch"))
	fmt.Fprintf(&b, "  git%s %s -C %s checkout --force %s --%s", opt, gitSafe(repoDir), psQuote(repoDir), psQuote(checkout), exitGuard("git checkout"))
	b.WriteString("} else {\n")
	fmt.Fprintf(&b, "  git%s clone %s %s%s", opt, psQuote(repoURL), psQuote(repoDir), exitGuard("git clone"))
	fmt.Fprintf(&b, "  git%s %s -C %s checkout --force %s --%s", opt, gitSafe(repoDir), psQuote(repoDir), psQuote(checkout), exitGuard("git checkout"))
	b.WriteString("}\n")
	b.WriteString("git clean -fdx\n")
	b.WriteString("git rev-parse HEAD\n")
	return b.String()
}

func buildScript(repoDir, buildCommand string, env map[string]string) string {
	var b strings.Builder
	b.WriteString("$ErrorActionPreference='Stop'\n")
	// Pick up a runtime installed earlier in this deploy (see refreshPathScript).
	b.WriteString(refreshPathScript)
	fmt.Fprintf(&b, "Set-Location %s\n", psQuote(repoDir))
	for _, k := range sortedKeys(env) {
		fmt.Fprintf(&b, "$env:%s = %s\n", k, psQuote(env[k]))
	}
	fmt.Fprintf(&b, "%s%s", buildCommand, exitGuard("build command"))
	return b.String()
}

// ensureIISScript creates the application pool and site when they are missing,
// and points an existing site at the project's physical path and pool. This is
// what makes an IIS deploy zero-touch: nothing has to be pre-created in IIS.
// SECURITY: it modifies IIS configuration (sites, app pools) as the WinRM or
// local account, which is an administrator on the target.
func ensureIISScript(p config.Project) string {
	site := p.IISSite
	if site == "" {
		site = p.ID
	}
	port := p.EffectiveHostPort()
	if port <= 0 {
		port = 80
	}
	var b strings.Builder
	b.WriteString("$ErrorActionPreference='Stop'\n")
	b.WriteString("Import-Module WebAdministration\n")
	fmt.Fprintf(&b, "$pool = %s\n", psQuote(p.IISAppPool))
	fmt.Fprintf(&b, "$site = %s\n", psQuote(site))
	fmt.Fprintf(&b, "$path = %s\n", psQuote(p.IISPhysicalPath))
	b.WriteString("New-Item -ItemType Directory -Force -Path $path | Out-Null\n")
	b.WriteString("if (-not (Test-Path \"IIS:\\AppPools\\$pool\")) { New-WebAppPool -Name $pool | Out-Null }\n")
	b.WriteString("if (-not (Get-Website -Name $site -ErrorAction SilentlyContinue)) {\n")
	fmt.Fprintf(&b, "  New-Website -Name $site -PhysicalPath $path -Port %d -ApplicationPool $pool | Out-Null\n", port)
	b.WriteString("} else {\n")
	if !p.IISBlueGreen {
		b.WriteString("  Set-ItemProperty \"IIS:\\Sites\\$site\" -Name physicalPath -Value $path\n")
	}
	b.WriteString("  Set-ItemProperty \"IIS:\\Sites\\$site\" -Name applicationPool -Value $pool\n")
	b.WriteString("}\n")
	return b.String()
}

// validateScript checks the source tree, web.config XML and IIS prerequisites.
// It must run before anything live is stopped or overwritten.
func validateScript(repoDir, source, appPool, service string) string {
	var b strings.Builder
	b.WriteString("$ErrorActionPreference='Stop'\n")
	fmt.Fprintf(&b, "$src = Join-Path %s %s\n", psQuote(repoDir), psQuote(source))
	return validateBody(&b, appPool, service)
}

// validateDirScript validates an already-materialized directory (a blue-green
// slot) with the same checks as the source tree.
func validateDirScript(dir, appPool, service string) string {
	var b strings.Builder
	b.WriteString("$ErrorActionPreference='Stop'\n")
	fmt.Fprintf(&b, "$src = %s\n", psQuote(dir))
	return validateBody(&b, appPool, service)
}

func validateBody(b *strings.Builder, appPool, service string) string {
	b.WriteString("if (-not (Test-Path $src)) { throw \"source directory not found: $src\" }\n")
	b.WriteString("$wc = Join-Path $src 'web.config'\n")
	b.WriteString("if (Test-Path $wc) { $null = [xml](Get-Content -Raw -LiteralPath $wc); Write-Output 'web.config is valid XML' }\n")
	b.WriteString("else { Write-Output 'no web.config present; skipping XML validation' }\n")
	b.WriteString("Import-Module WebAdministration\n")
	fmt.Fprintf(b, "$poolPath = 'IIS:\\AppPools\\' + %s\n", psQuote(appPool))
	fmt.Fprintf(b, "if (-not (Test-Path $poolPath)) { throw \"IIS app pool not found: %s\" }\n", appPool)
	if service != "" {
		fmt.Fprintf(b, "if (-not (Get-Service -Name %s -ErrorAction SilentlyContinue)) { throw \"service not found: %s\" }\n", psQuote(service), service)
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
// full path as the last line of output. A first deployment has no previous
// release, so it prints NO_BACKUP instead of creating an empty rollback target.
func backupScript(physicalPath, backupRoot, projectID string) string {
	var b strings.Builder
	b.WriteString("$ErrorActionPreference='Stop'\n")
	b.WriteString("$stamp = Get-Date -Format 'yyyyMMdd-HHmmss'\n")
	fmt.Fprintf(&b, "if (-not (Test-Path -LiteralPath %s -PathType Container)) { Write-Output 'NO_BACKUP'; exit 0 }\n", psQuote(physicalPath))
	fmt.Fprintf(&b, "$dest = Join-Path %s (%s + $stamp)\n", psQuote(backupRoot), psQuote(projectID+`\`))
	b.WriteString("New-Item -ItemType Directory -Force -Path $dest | Out-Null\n")
	fmt.Fprintf(&b, "robocopy %s $dest /MIR /NFL /NDL /NJH /NJS /NP | Out-Null\n", psQuote(physicalPath))
	b.WriteString("if ($LASTEXITCODE -ge 8) { throw \"backup robocopy failed: $LASTEXITCODE\" }\n")
	b.WriteString("Write-Output $dest\n")
	return b.String()
}

func latestBackupScript(backupRoot, projectID string) string {
	var b strings.Builder
	b.WriteString("$ErrorActionPreference='Stop'\n")
	fmt.Fprintf(&b, "$root = Join-Path %s %s\n", psQuote(backupRoot), psQuote(projectID))
	fmt.Fprintf(&b, "if (-not (Test-Path $root)) { throw \"no backups found for %s\" }\n", projectID)
	b.WriteString("$latest = Get-ChildItem -LiteralPath $root -Directory | Where-Object { @(Get-ChildItem -LiteralPath $_.FullName -Recurse -File -ErrorAction SilentlyContinue).Count -gt 0 } | Sort-Object Name -Descending | Select-Object -First 1\n")
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

func smokeScript(url string, plan healthPlan) string {
	var b strings.Builder
	b.WriteString("$ErrorActionPreference='Stop'\n")
	fmt.Fprintf(&b, "$url = %s\n", psQuote(url))
	if plan.startPeriod > 0 {
		fmt.Fprintf(&b, "Start-Sleep -Seconds %d\n", plan.startPeriod)
	}
	fmt.Fprintf(&b, "for ($i=0; $i -lt %d; $i++) {\n", plan.attempts)
	b.WriteString("  try {\n")
	fmt.Fprintf(&b, "    $r = Invoke-WebRequest -UseBasicParsing -Uri $url -TimeoutSec %d\n", plan.timeout)
	b.WriteString("    if ($r.StatusCode -ge 200 -and $r.StatusCode -lt 400) { Write-Output 'smoke test ok'; exit 0 }\n")
	b.WriteString("  } catch { }\n")
	fmt.Fprintf(&b, "  Start-Sleep -Seconds %d\n", plan.interval)
	b.WriteString("}\n")
	fmt.Fprintf(&b, "throw \"smoke test failed: $url\"\n")
	return b.String()
}
