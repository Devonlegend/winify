package bootstrap

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"strings"

	"github.com/Devonlegend/winify/internal/deployment"
)

// LocalRunner runs PowerShell on the machine winify is installed on. Bootstrap
// uses it to provision the local host; the caller must be elevated.
//
// SECURITY: commands run as the current user (LocalSystem when winify runs as a
// service) and can enable WinRM, install Windows services and write under
// ProgramData. Only run bootstrap on a host you intend to fully provision.
type LocalRunner struct{}

// NewLocalRunner returns a Runner that executes on this host.
func NewLocalRunner() *LocalRunner { return &LocalRunner{} }

// Run executes a PowerShell script and returns combined output. The script is
// passed as a single argument, so no shell quoting is required.
func (LocalRunner) Run(ctx context.Context, script string) (string, error) {
	if runtime.GOOS != "windows" {
		return "", fmt.Errorf("local bootstrap is only supported on Windows")
	}
	cmd := exec.CommandContext(ctx, "powershell", "-NoProfile", "-NonInteractive", "-Command", script)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()

	out := strings.TrimSpace(stdout.String())
	if se := strings.TrimSpace(stderr.String()); se != "" {
		if out != "" {
			out += "\n"
		}
		out += se
	}
	if err != nil {
		return out, fmt.Errorf("powershell: %w", err)
	}
	return out, nil
}

// Close is a no-op; each Run starts a fresh process.
func (LocalRunner) Close() error { return nil }

// IsElevated reports whether the runner can make machine-level changes
// (enable WinRM, create services). Bootstrap refuses to apply without it.
func IsElevated(ctx context.Context, r deployment.Runner) (bool, error) {
	out, err := r.Run(ctx, "([Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)")
	if err != nil {
		return false, err
	}
	return strings.EqualFold(lastLine(out), "True"), nil
}
