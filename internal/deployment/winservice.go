package deployment

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"os"
	"strings"

	"github.com/Devonlegend/winify/internal/config"
)

// nssmUploadChunk is the number of base64 characters uploaded per WinRM
// command. The WinRM envelope is capped at 153600 bytes (see winrm_client.go),
// so a whole ~300 KB nssm.exe cannot be sent in one command; it is streamed in
// chunks below that limit and reassembled on the target.
const nssmUploadChunk = 100000

const (
	defaultNSSMPath  = `C:\control-center\tools\nssm.exe`
	defaultCaddyPath = `C:\control-center\tools\caddy.exe`
)

// windowsServiceTarget deploys a native Windows executable as a service managed
// by NSSM, over the same WinRM connection the IIS pipeline uses.
//
// Pipeline: sync repo -> optional build -> ensure NSSM -> validate
// prerequisites -> stop service -> timestamped backup -> copy files ->
// install/update service -> start -> optional per-target Caddy -> HTTP smoke
// test. Validation and backup happen before any live change.
//
// SECURITY: this runs PowerShell as the WinRM account, which can install and
// control Windows services, modify the filesystem and (via NSSM) run arbitrary
// executables as LocalSystem by default. Treat the credential as highly
// privileged. Env values and the uploaded binary are kept out of the audit log.
type windowsServiceTarget struct {
	cfg    config.Config
	runner Runner
}

// NewWindowsServiceTarget builds the native-service pipeline over an open Runner.
func NewWindowsServiceTarget(cfg config.Config, runner Runner) Target {
	return &windowsServiceTarget{cfg: cfg, runner: runner}
}

func (t *windowsServiceTarget) Close() error { return t.runner.Close() }

// Deploy runs the full native-service pipeline and returns the pre-deploy
// backup path as the deployment artifact.
func (t *windowsServiceTarget) Deploy(ctx context.Context, job deployJob, logf loggerFunc) (string, error) {
	p := job.project
	if p.ServiceName == "" {
		return "", fmt.Errorf("project %s has no service_name", p.ID)
	}
	if p.ServiceExe == "" {
		return "", fmt.Errorf("project %s has no service_exe", p.ID)
	}
	if p.ServiceWorkDir == "" {
		return "", fmt.Errorf("project %s has no service_work_dir", p.ID)
	}
	source := p.ServiceSourceSubdir
	if source == "" {
		source = "."
	}
	repoDir := winPath(t.cfg.Deploy.IISWorkDir, p.ID)

	logf("winsvc pipeline: repo=%s source=%s install=%s service=%s", repoDir, source, p.ServiceWorkDir, p.ServiceName)

	// 1. Fetch the requested revision.
	if _, err := execCmd(ctx, t.runner, syncRepoScript(repoDir, p.RepoURL, deployRevision(p, job.commit)), "git clone/fetch + checkout "+shortSHA(deployRevision(p, job.commit)), logf); err != nil {
		return "", fmt.Errorf("clone/checkout: %w", err)
	}

	// Make sure the build's toolchain is present, installing it if missing, so
	// the build fails fast and self-heals rather than erroring on a missing
	// interpreter.
	if err := ensureRuntime(ctx, t.runner, p.Runtime, logf); err != nil {
		return "", err
	}

	// 2. Optional build (for example: go build, dotnet publish, npm run build).
	if p.ServiceBuildCommand != "" {
		buildCtx := ctx
		if len(p.BuildEnv) > 0 {
			// Build env values can hold secrets; keep them out of the audit log.
			buildCtx = withAuditRedaction(ctx, "service build command (build env redacted)")
		}
		if _, err := execCmd(buildCtx, t.runner, buildScript(repoDir, p.ServiceBuildCommand, p.BuildEnv), "service build command", logf); err != nil {
			return "", fmt.Errorf("build: %w", err)
		}
	}

	// 3. Make sure NSSM is on the target before anything else.
	nssmPath, err := t.ensureNSSM(ctx, job.server, logf)
	if err != nil {
		return "", err
	}

	// 4. Validate BEFORE touching anything live.
	if _, err := execCmd(ctx, t.runner, validateServiceScript(repoDir, source, p.ServiceWorkDir, p.ServiceExe, nssmPath), "validate configuration", logf); err != nil {
		return "", fmt.Errorf("validation failed: %w", err)
	}

	// 5. Stop the service (no-op on first deploy).
	if _, err := execCmd(ctx, t.runner, stopServiceScript(p.ServiceName, nssmPath), "stop service", logf); err != nil {
		return "", fmt.Errorf("stop: %w", err)
	}

	// 6. Backup the current install directory (timestamped).
	backupOut, err := execCmd(ctx, t.runner, backupScript(p.ServiceWorkDir, t.cfg.Deploy.IISBackupDir, p.ID), "backup live files", logf)
	if err != nil {
		return "", fmt.Errorf("backup: %w", err)
	}
	backup := lastLine(backupOut)
	logf("backup created: %s", backup)

	// 7. Deploy the new build output.
	srcDir := winPath(repoDir, source)
	if _, err := execCmd(ctx, t.runner, copyScript(srcDir, p.ServiceWorkDir, "deploy"), "deploy files", logf); err != nil {
		return "", fmt.Errorf("deploy files: %w", err)
	}

	// 8. Install or update the service definition.
	if _, err := execCmd(ctx, t.runner, installServiceScript(p, nssmPath), "install/update service "+p.ServiceName, logf); err != nil {
		return "", fmt.Errorf("install service: %w", err)
	}
	if envScript := serviceEnvScript(p, nssmPath); envScript != "" {
		// Env values may hold secrets; record a label instead of the payload.
		envCtx := withAuditRedaction(ctx, "set service environment (values redacted)")
		if _, err := execCmd(envCtx, t.runner, envScript, "set service environment", logf); err != nil {
			return "", fmt.Errorf("set service environment: %w", err)
		}
	}

	// 9. Start and wait for Running.
	if _, err := execCmd(ctx, t.runner, startServiceScript(p.ServiceName, nssmPath), "start service "+p.ServiceName, logf); err != nil {
		return "", fmt.Errorf("start service: %w", err)
	}

	// 10. Per-target Caddy (static file serving). Central Caddy still owns TLS.
	if err := t.configureCaddy(ctx, job, logf); err != nil {
		return "", err
	}

	// 11. Smoke test.
	if err := t.smokeTest(ctx, p, logf); err != nil {
		return "", err
	}
	return backup, nil
}

// Rollback restores the most recent timestamped backup and restarts the service.
// It discovers the backup on the target, so it does not depend on deploy history.
func (t *windowsServiceTarget) Rollback(ctx context.Context, job deployJob, logf loggerFunc) (string, error) {
	p := job.project
	if p.ServiceName == "" || p.ServiceWorkDir == "" {
		return "", fmt.Errorf("project %s is not configured as a Windows service", p.ID)
	}
	nssmPath := nssmPathFor(job.server)

	backupOut, err := execCmd(ctx, t.runner, latestBackupScript(t.cfg.Deploy.IISBackupDir, p.ID), "find latest backup", logf)
	if err != nil {
		return "", fmt.Errorf("find backup: %w", err)
	}
	backup := lastLine(backupOut)
	if backup == "" {
		return "", fmt.Errorf("no backup found for project %s", p.ID)
	}
	logf("rollback: restoring %s", backup)

	if _, err := execCmd(ctx, t.runner, stopServiceScript(p.ServiceName, nssmPath), "stop service", logf); err != nil {
		return "", fmt.Errorf("stop: %w", err)
	}
	if _, err := execCmd(ctx, t.runner, copyScript(backup, p.ServiceWorkDir, "restore backup"), "restore backup", logf); err != nil {
		return "", fmt.Errorf("restore backup: %w", err)
	}
	if _, err := execCmd(ctx, t.runner, startServiceScript(p.ServiceName, nssmPath), "start service "+p.ServiceName, logf); err != nil {
		return "", fmt.Errorf("start service: %w", err)
	}
	if err := t.smokeTest(ctx, p, logf); err != nil {
		return "", err
	}
	return backup, nil
}

func (t *windowsServiceTarget) smokeTest(ctx context.Context, p config.Project, logf loggerFunc) error {
	if p.DisableHealthCheck {
		logf("health check disabled; skipping smoke test")
		return nil
	}
	if p.Port == 0 {
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

// ensureNSSM returns the target path to nssm.exe, uploading it from
// deploy.nssm_source first if configured and not already present.
func (t *windowsServiceTarget) ensureNSSM(ctx context.Context, srv config.Server, logf loggerFunc) (string, error) {
	return EnsureNSSM(ctx, t.runner, nssmPathFor(srv),
		strings.TrimSpace(t.cfg.Deploy.NSSMSource), strings.TrimSpace(t.cfg.Deploy.NSSMSHA256), logf)
}

// RemoteFileSHA256 returns the SHA-256 of a file on the target, or "" when the
// file does not exist. Shared by the deploy pipeline and bootstrap.
func RemoteFileSHA256(ctx context.Context, runner Runner, path string) (string, error) {
	out, err := runner.Run(ctx, fileHashScript(path))
	if err != nil {
		return "", fmt.Errorf("hash %s: %w", path, err)
	}
	return strings.TrimSpace(lastLine(out)), nil
}

// EnsureNSSM makes nssm.exe available at nssmPath on the target. When source is
// set (a path on the control-center host) the binary is uploaded, skipping the
// transfer when the target already has a matching SHA-256. pinSHA256 optionally
// pins the expected hash of the source binary. It returns nssmPath.
func EnsureNSSM(ctx context.Context, runner Runner, nssmPath, source, pinSHA256 string, logf func(format string, args ...any)) (string, error) {
	source = strings.TrimSpace(source)
	if source == "" {
		// No upload configured; the caller validates presence separately.
		return nssmPath, nil
	}
	data, err := os.ReadFile(source)
	if err != nil {
		return "", fmt.Errorf("read nssm source %s: %w", source, err)
	}
	if pin := strings.TrimSpace(pinSHA256); pin != "" && !strings.EqualFold(pin, sha256hex(data)) {
		return "", fmt.Errorf("nssm source %s sha256 %s does not match configured nssm_sha256", source, sha256hex(data))
	}
	if _, err := EnsureBinary(ctx, runner, nssmPath, data, logf); err != nil {
		return "", err
	}
	return nssmPath, nil
}

// EnsureBinary makes data available at destPath on the target, uploading it in
// chunks when the target's SHA-256 does not already match. It returns the
// installed file's SHA-256 (hex). Shared by NSSM and Caddy provisioning.
func EnsureBinary(ctx context.Context, runner Runner, destPath string, data []byte, logf func(format string, args ...any)) (string, error) {
	want := sha256hex(data)
	if got, err := RemoteFileSHA256(ctx, runner, destPath); err == nil && strings.EqualFold(got, want) {
		logf("%s already present (sha256 match)", destPath)
		return want, nil
	}

	encoded := base64.StdEncoding.EncodeToString(data)
	tmp := destPath + ".b64"
	if _, err := execCmd(ctx, runner, beginUploadScript(winDir(destPath), tmp), "prepare upload", logf); err != nil {
		return "", fmt.Errorf("prepare upload: %w", err)
	}
	// The chunk commands carry binary payload; keep them out of the audit log.
	chunkCtx := withAuditRedaction(ctx, "upload binary (payload redacted)")
	for i := 0; i < len(encoded); i += nssmUploadChunk {
		end := i + nssmUploadChunk
		if end > len(encoded) {
			end = len(encoded)
		}
		if _, err := execCmd(chunkCtx, runner, appendUploadScript(tmp, encoded[i:end]), "upload chunk", logf); err != nil {
			return "", fmt.Errorf("upload chunk: %w", err)
		}
	}
	if _, err := execCmd(ctx, runner, finishUploadScript(tmp, destPath), "assemble upload", logf); err != nil {
		return "", fmt.Errorf("assemble upload: %w", err)
	}

	out, err := execCmd(ctx, runner, fileHashScript(destPath), "verify upload", logf)
	if err != nil {
		return "", fmt.Errorf("verify upload: %w", err)
	}
	if got := strings.TrimSpace(lastLine(out)); !strings.EqualFold(got, want) {
		return "", fmt.Errorf("uploaded hash mismatch: got %q want %s", got, want)
	}
	logf("%s uploaded", destPath)
	return want, nil
}

// sha256hex returns the hex SHA-256 of data.
func sha256hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// configureCaddy writes a Caddyfile for static serving and reloads the
// per-target Caddy service when the project requests it. Central Caddy remains
// the public TLS terminator; the target Caddy listens on the project port over
// plain HTTP. Proxy mode is not implemented yet and is rejected explicitly.
func (t *windowsServiceTarget) configureCaddy(ctx context.Context, job deployJob, logf loggerFunc) error {
	p := job.project
	switch p.CaddyMode {
	case "", config.CaddyModeNone:
		return nil
	case config.CaddyModeStatic:
	case config.CaddyModeProxy:
		return fmt.Errorf("per-target Caddy proxy mode is not supported yet; use %q or %q", config.CaddyModeNone, config.CaddyModeStatic)
	default:
		return fmt.Errorf("unknown caddy_mode %q", p.CaddyMode)
	}

	caddyPath := caddyPathFor(job.server)
	if _, err := execCmd(ctx, t.runner, fileExistsScript(caddyPath), "check caddy on target", logf); err != nil {
		return fmt.Errorf("caddy.exe not found at %s; install it or set the server caddy_path", caddyPath)
	}

	// Keep the Caddyfile out of the git working tree so a checkout cannot wipe it.
	dir := winPath(t.cfg.Deploy.IISWorkDir, "caddy", sanitize(p.ID))
	file := winPath(dir, "Caddyfile")
	content := caddyFile(p)
	encoded := base64.StdEncoding.EncodeToString([]byte(content))
	write := fmt.Sprintf("New-Item -ItemType Directory -Force -Path %s | Out-Null; [IO.File]::WriteAllBytes(%s, [Convert]::FromBase64String(%s))",
		psQuote(dir), psQuote(file), psQuote(encoded))
	writeCtx := withAuditRedaction(ctx, "write Caddyfile (contents redacted)")
	if _, err := execCmd(writeCtx, t.runner, write, "write Caddyfile", logf); err != nil {
		return fmt.Errorf("write Caddyfile: %w", err)
	}

	nssmPath := nssmPathFor(job.server)
	if _, err := execCmd(ctx, t.runner, ensureCaddyServiceScript(p, caddyPath, nssmPath, dir, file), "ensure caddy service", logf); err != nil {
		return fmt.Errorf("ensure caddy service: %w", err)
	}
	if _, err := execCmd(ctx, t.runner, reloadCaddyScript(p, caddyPath, nssmPath, file), "reload caddy", logf); err != nil {
		return fmt.Errorf("reload caddy: %w", err)
	}
	return nil
}

func nssmPathFor(srv config.Server) string {
	if strings.TrimSpace(srv.NSSMPath) != "" {
		return srv.NSSMPath
	}
	return defaultNSSMPath
}

func caddyPathFor(srv config.Server) string {
	if strings.TrimSpace(srv.CaddyPath) != "" {
		return srv.CaddyPath
	}
	return defaultCaddyPath
}

// relUnder reports whether path is inside base (case-insensitive, Windows) and
// returns the relative remainder.
func relUnder(base, path string) (string, bool) {
	base = strings.TrimRight(base, `\`)
	if len(path) <= len(base)+1 || !strings.EqualFold(path[:len(base)], base) || path[len(base)] != '\\' {
		return "", false
	}
	return path[len(base)+1:], true
}

// ---- PowerShell script builders ----

func fileExistsScript(path string) string {
	return fmt.Sprintf("$ErrorActionPreference='Stop'\nif (-not (Test-Path -LiteralPath %s)) { throw %s }\n",
		psQuote(path), psQuote("not found: "+path))
}

func fileHashScript(path string) string {
	var b strings.Builder
	b.WriteString("$ErrorActionPreference='Stop'\n")
	fmt.Fprintf(&b, "if (Test-Path -LiteralPath %s) { (Get-FileHash -Algorithm SHA256 -LiteralPath %s).Hash }\n", psQuote(path), psQuote(path))
	return b.String()
}

func beginUploadScript(dir, tmp string) string {
	var b strings.Builder
	b.WriteString("$ErrorActionPreference='Stop'\n")
	fmt.Fprintf(&b, "New-Item -ItemType Directory -Force -Path %s | Out-Null\n", psQuote(dir))
	fmt.Fprintf(&b, "Set-Content -LiteralPath %s -Value '' -NoNewline -Encoding ASCII\n", psQuote(tmp))
	return b.String()
}

func appendUploadScript(tmp, chunk string) string {
	var b strings.Builder
	b.WriteString("$ErrorActionPreference='Stop'\n")
	fmt.Fprintf(&b, "Add-Content -LiteralPath %s -Value %s -NoNewline -Encoding ASCII\n", psQuote(tmp), psQuote(chunk))
	return b.String()
}

func finishUploadScript(tmp, dest string) string {
	var b strings.Builder
	b.WriteString("$ErrorActionPreference='Stop'\n")
	fmt.Fprintf(&b, "$b = [Convert]::FromBase64String((Get-Content -Raw -LiteralPath %s))\n", psQuote(tmp))
	fmt.Fprintf(&b, "[IO.File]::WriteAllBytes(%s, $b)\n", psQuote(dest))
	fmt.Fprintf(&b, "Remove-Item -LiteralPath %s -Force\n", psQuote(tmp))
	return b.String()
}

// validateServiceScript checks NSSM, the source tree and (when the executable
// maps into the install directory) that the built exe exists. It must run
// before anything live is stopped or overwritten.
func validateServiceScript(repoDir, source, workDir, serviceExe, nssmPath string) string {
	var b strings.Builder
	b.WriteString("$ErrorActionPreference='Stop'\n")
	fmt.Fprintf(&b, "if (-not (Test-Path -LiteralPath %s)) { throw %s }\n", psQuote(nssmPath),
		psQuote("nssm.exe not found at "+nssmPath+"; set deploy.nssm_source to upload it, or install NSSM on the target"))
	fmt.Fprintf(&b, "$src = Join-Path %s %s\n", psQuote(repoDir), psQuote(source))
	b.WriteString("if (-not (Test-Path -LiteralPath $src)) { throw \"source directory not found: $src\" }\n")
	if rel, ok := relUnder(workDir, serviceExe); ok {
		fmt.Fprintf(&b, "if (-not (Test-Path -LiteralPath (Join-Path $src %s))) { throw %s }\n",
			psQuote(rel), psQuote("service executable not found in build output: "+rel))
	}
	return b.String()
}

func stopServiceScript(name, nssmPath string) string {
	var b strings.Builder
	b.WriteString("$ErrorActionPreference='Stop'\n")
	fmt.Fprintf(&b, "if (Get-Service -Name %s -ErrorAction SilentlyContinue) {\n", psQuote(name))
	fmt.Fprintf(&b, "  & %s stop %s | Out-Null\n", psQuote(nssmPath), psQuote(name))
	b.WriteString("  $deadline = (Get-Date).AddSeconds(30)\n")
	fmt.Fprintf(&b, "  while ((Get-Service -Name %s).Status -ne 'Stopped' -and (Get-Date) -lt $deadline) { Start-Sleep -Milliseconds 500 }\n", psQuote(name))
	b.WriteString("}\n")
	return b.String()
}

func startServiceScript(name, nssmPath string) string {
	var b strings.Builder
	b.WriteString("$ErrorActionPreference='Stop'\n")
	fmt.Fprintf(&b, "& %s start %s | Out-Null\n", psQuote(nssmPath), psQuote(name))
	b.WriteString("$deadline = (Get-Date).AddSeconds(30)\n")
	fmt.Fprintf(&b, "$s = (Get-Service -Name %s).Status\n", psQuote(name))
	fmt.Fprintf(&b, "while ($s -ne 'Running' -and (Get-Date) -lt $deadline) { Start-Sleep -Milliseconds 500; $s = (Get-Service -Name %s).Status }\n", psQuote(name))
	fmt.Fprintf(&b, "if ($s -ne 'Running') { throw \"service %s did not reach Running (status: $s)\" }\n", name)
	return b.String()
}

// installServiceScript installs the service if absent, then sets every managed
// parameter. It is idempotent so redeploys update in place.
func installServiceScript(p config.Project, nssmPath string) string {
	var b strings.Builder
	b.WriteString("$ErrorActionPreference='Stop'\n")
	fmt.Fprintf(&b, "$nssm = %s\n", psQuote(nssmPath))
	fmt.Fprintf(&b, "if (Get-Service -Name %s -ErrorAction SilentlyContinue) {\n", psQuote(p.ServiceName))
	fmt.Fprintf(&b, "  & $nssm set %s Application %s\n", psQuote(p.ServiceName), psQuote(p.ServiceExe))
	b.WriteString("} else {\n")
	fmt.Fprintf(&b, "  & $nssm install %s %s\n", psQuote(p.ServiceName), psQuote(p.ServiceExe))
	b.WriteString("}\n")
	fmt.Fprintf(&b, "& $nssm set %s AppDirectory %s\n", psQuote(p.ServiceName), psQuote(p.ServiceWorkDir))
	if p.ServiceArgs != "" {
		fmt.Fprintf(&b, "& $nssm set %s AppParameters %s\n", psQuote(p.ServiceName), psQuote(p.ServiceArgs))
	}
	if p.ServiceLogDir != "" {
		fmt.Fprintf(&b, "New-Item -ItemType Directory -Force -Path %s | Out-Null\n", psQuote(p.ServiceLogDir))
		fmt.Fprintf(&b, "& $nssm set %s AppStdout %s\n", psQuote(p.ServiceName), psQuote(winPath(p.ServiceLogDir, sanitize(p.ID)+".out.log")))
		fmt.Fprintf(&b, "& $nssm set %s AppStderr %s\n", psQuote(p.ServiceName), psQuote(winPath(p.ServiceLogDir, sanitize(p.ID)+".err.log")))
		fmt.Fprintf(&b, "& $nssm set %s AppRotateFiles 1\n", psQuote(p.ServiceName))
	}
	if p.ServiceAccount != "" {
		fmt.Fprintf(&b, "& $nssm set %s ObjectName %s\n", psQuote(p.ServiceName), psQuote(p.ServiceAccount))
	}
	fmt.Fprintf(&b, "& $nssm set %s AppExit Default Restart\n", psQuote(p.ServiceName))
	fmt.Fprintf(&b, "& $nssm set %s AppThrottle 1500\n", psQuote(p.ServiceName))
	fmt.Fprintf(&b, "& $nssm set %s Start SERVICE_AUTO_START\n", psQuote(p.ServiceName))
	return b.String()
}

// serviceEnvScript renders AppEnvironmentExtra. Returns "" when there is no env.
func serviceEnvScript(p config.Project, nssmPath string) string {
	if len(p.Env) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("$ErrorActionPreference='Stop'\n")
	fmt.Fprintf(&b, "$nssm = %s\n", psQuote(nssmPath))
	b.WriteString("$pairs = @(")
	for i, k := range sortedKeys(p.Env) {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(psQuote(k + "=" + p.Env[k]))
	}
	b.WriteString(")\n")
	fmt.Fprintf(&b, "& $nssm set %s AppEnvironmentExtra @pairs\n", psQuote(p.ServiceName))
	return b.String()
}

// caddyFile renders a minimal static-file Caddyfile for the project.
func caddyFile(p config.Project) string {
	var b strings.Builder
	fmt.Fprintf(&b, ":%d {\n", p.EffectiveHostPort())
	fmt.Fprintf(&b, "\troot * %s\n", p.ServiceWorkDir)
	b.WriteString("\tfile_server\n")
	b.WriteString("\ttry_files {path} /index.html\n")
	b.WriteString("}\n")
	return b.String()
}

// ensureCaddyServiceScript installs the Caddy service under NSSM once.
func ensureCaddyServiceScript(p config.Project, caddyPath, nssmPath, dir, configFile string) string {
	name := caddyServiceName(p)
	args := fmt.Sprintf("run --config %s --adapter caddyfile", configFile)
	var b strings.Builder
	b.WriteString("$ErrorActionPreference='Stop'\n")
	fmt.Fprintf(&b, "$nssm = %s\n", psQuote(nssmPath))
	fmt.Fprintf(&b, "if (-not (Get-Service -Name %s -ErrorAction SilentlyContinue)) {\n", psQuote(name))
	fmt.Fprintf(&b, "  & $nssm install %s %s\n", psQuote(name), psQuote(caddyPath))
	fmt.Fprintf(&b, "  & $nssm set %s AppParameters %s\n", psQuote(name), psQuote(args))
	fmt.Fprintf(&b, "  & $nssm set %s AppDirectory %s\n", psQuote(name), psQuote(dir))
	fmt.Fprintf(&b, "  & $nssm set %s AppExit Default Restart\n", psQuote(name))
	fmt.Fprintf(&b, "  & $nssm set %s Start SERVICE_AUTO_START\n", psQuote(name))
	b.WriteString("}\n")
	return b.String()
}

// reloadCaddyScript starts the Caddy service if needed, then reloads its config.
func reloadCaddyScript(p config.Project, caddyPath, nssmPath, configFile string) string {
	name := caddyServiceName(p)
	var b strings.Builder
	b.WriteString("$ErrorActionPreference='Stop'\n")
	fmt.Fprintf(&b, "if ((Get-Service -Name %s).Status -ne 'Running') { & %s start %s | Out-Null; Start-Sleep -Seconds 1 }\n",
		psQuote(name), psQuote(nssmPath), psQuote(name))
	fmt.Fprintf(&b, "& %s reload --config %s --adapter caddyfile\n", psQuote(caddyPath), psQuote(configFile))
	return b.String()
}

func caddyServiceName(p config.Project) string {
	return "cc-caddy-" + sanitize(p.ID)
}
