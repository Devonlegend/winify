package deployment

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
// The script is written to a temporary .ps1 file and run with -File. It is not
// passed on the command line (Windows caps a command line at ~32K characters,
// and the binary-upload chunks are 100 KB) and not fed on stdin: with
// `powershell -Command -`, a multi-line script block silently produces no
// output, which broke every multi-line pipeline script.
func (LocalRunner) Run(ctx context.Context, script string) (string, error) {
	if runtime.GOOS != "windows" {
		return "", fmt.Errorf("local execution is only supported on Windows")
	}
	dir, err := os.MkdirTemp("", "winify-script-")
	if err != nil {
		return "", fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(dir)

	file := filepath.Join(dir, "run.ps1")
	// The UTF-8 BOM makes Windows PowerShell 5.1 read the file as UTF-8.
	if err := os.WriteFile(file, append([]byte{0xEF, 0xBB, 0xBF}, script...), 0o600); err != nil {
		return "", fmt.Errorf("write script: %w", err)
	}

	cmd := exec.CommandContext(ctx, "powershell", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", file)
	var output syncBuffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	err = cmd.Run()

	out := strings.TrimSpace(output.String())
	if err != nil {
		return out, fmt.Errorf("powershell: %w", err)
	}
	return out, nil
}

// Close is a no-op; each Run starts a fresh process.
func (LocalRunner) Close() error { return nil }
