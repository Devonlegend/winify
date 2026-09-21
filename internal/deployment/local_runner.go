package deployment

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
)

// LocalRunner runs PowerShell on the machine winify is installed on. It lets a
// "local" target be managed without WinRM or a credential: when winify runs as
// a service it is LocalSystem, already elevated.
//
// SECURITY: commands run with the winify process's privileges and can install
// Windows services and modify the filesystem. Use it only for the host winify
// runs on.
type LocalRunner struct{}

// NewLocalRunner returns a Runner that executes on this host.
func NewLocalRunner() *LocalRunner { return &LocalRunner{} }

// Run executes a PowerShell script and returns combined output.
//
// The script is fed on stdin ("-Command -") rather than as an argument: a
// Windows command line is capped at ~32K characters, which the binary-upload
// chunks (100 KB) exceed.
func (LocalRunner) Run(ctx context.Context, script string) (string, error) {
	if runtime.GOOS != "windows" {
		return "", fmt.Errorf("local execution is only supported on Windows")
	}
	cmd := exec.CommandContext(ctx, "powershell", "-NoProfile", "-NonInteractive", "-Command", "-")
	cmd.Stdin = strings.NewReader(script)
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
