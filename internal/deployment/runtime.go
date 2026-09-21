package deployment

import (
	"context"
	"fmt"
	"strings"
)

// refreshPathScript reloads PATH from the registry. The WinRM service caches
// its environment at start, so a runtime installed mid-deploy would otherwise
// be invisible to the very next command; rebuilding PATH from the registry
// fixes that without restarting the service (which would drop the connection).
const refreshPathScript = "$env:Path = [Environment]::GetEnvironmentVariable('Path','Machine') + ';' + [Environment]::GetEnvironmentVariable('Path','User')\n"

// runtimeSpec describes how to detect and install one toolchain on a Windows
// target. Package IDs are pinned and reviewed; a target with neither winget nor
// choco fails instead of guessing.
type runtimeSpec struct {
	probe     string // executable name checked with Get-Command
	wingetID  string
	chocoName string
}

// runtimeSpecs are the toolchains winify can provision. Versions are chosen for
// broad availability; an operator who needs a different version can install it
// on the target and winify will use it.
var runtimeSpecs = map[string]runtimeSpec{
	"python": {probe: "python", wingetID: "Python.Python.3.12", chocoName: "python312"},
	"node":   {probe: "node", wingetID: "OpenJS.NodeJS.LTS", chocoName: "nodejs-lts"},
	"go":     {probe: "go", wingetID: "GoLang.Go", chocoName: "golang"},
	"dotnet": {probe: "dotnet", wingetID: "Microsoft.DotNet.SDK.8", chocoName: "dotnet-8.0-sdk"},
}

// ensureRuntime makes sure the toolchain named by runtime is available on the
// target, installing it with winget (falling back to choco) when missing. An
// empty runtime is a no-op, as is one winify does not manage.
//
// SECURITY: this runs a package manager as the WinRM account, which is an
// administrator on the target, and downloads installers from the internet.
// Only the fixed package IDs in runtimeSpecs are ever installed.
func ensureRuntime(ctx context.Context, runner Runner, runtime string, logf loggerFunc) error {
	runtime = strings.TrimSpace(runtime)
	if runtime == "" {
		return nil
	}
	spec, ok := runtimeSpecs[runtime]
	if !ok {
		return fmt.Errorf("unknown runtime %q (known: python, node, go, dotnet)", runtime)
	}

	out, err := execCmd(ctx, runner, runtimeProbeScript(spec.probe), "check for "+runtime, logf)
	if err != nil {
		return fmt.Errorf("probe %s: %w", runtime, err)
	}
	if strings.Contains(out, "present") {
		logf("%s is already installed", runtime)
		return nil
	}

	logf("%s not found; installing it on the target", runtime)
	if _, err := execCmd(ctx, runner, runtimeInstallScript(spec), "install "+runtime, logf); err != nil {
		return fmt.Errorf("install %s: %w", runtime, err)
	}
	return nil
}

// runtimeProbeScript prints "present" or "missing" for exe. It refreshes PATH
// from the registry first, so a runtime installed earlier in this deploy is
// visible even though the WinRM service cached its environment at start.
func runtimeProbeScript(exe string) string {
	var b strings.Builder
	b.WriteString("$ErrorActionPreference='Stop'\n")
	b.WriteString(refreshPathScript)
	fmt.Fprintf(&b, "if (Get-Command %s -ErrorAction SilentlyContinue) { 'present' } else { 'missing' }\n", psQuote(exe))
	return b.String()
}

// runtimeInstallScript installs a runtime with winget, falling back to choco,
// and fails loudly when neither is available.
func runtimeInstallScript(spec runtimeSpec) string {
	var b strings.Builder
	b.WriteString("$ErrorActionPreference='Stop'\n")
	b.WriteString(refreshPathScript)
	b.WriteString("$installed = $false\n")
	b.WriteString("if (Get-Command winget -ErrorAction SilentlyContinue) {\n")
	fmt.Fprintf(&b, "  winget install --id %s --exact --silent --accept-package-agreements --accept-source-agreements\n",
		psQuote(spec.wingetID))
	b.WriteString("  if ($LASTEXITCODE -eq 0) { $installed = $true }\n}\n")
	b.WriteString("if (-not $installed -and (Get-Command choco -ErrorAction SilentlyContinue)) {\n")
	fmt.Fprintf(&b, "  choco install %s -y --no-progress\n", psQuote(spec.chocoName))
	b.WriteString("  if ($LASTEXITCODE -eq 0) { $installed = $true }\n}\n")
	fmt.Fprintf(&b, "if (-not $installed) { throw %s }\n",
		psQuote("could not install "+spec.wingetID+": neither winget nor choco is available on the target"))
	return b.String()
}
