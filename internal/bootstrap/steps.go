package bootstrap

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Devonlegend/winify/internal/deployment"
)

// psQuote wraps s in single quotes for a PowerShell script.
func psQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// lastLine returns the last non-empty line of output.
func lastLine(out string) string {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if s := strings.TrimSpace(lines[i]); s != "" {
			return s
		}
	}
	return ""
}

// dirsStep creates the winify directory tree (under ProgramData on Windows).
type dirsStep struct{ paths []string }

func (s dirsStep) Name() string     { return "dirs" }
func (s dirsStep) Privileged() bool { return true }

func (s dirsStep) Check(ctx context.Context, r deployment.Runner) (bool, error) {
	var b strings.Builder
	b.WriteString("$ErrorActionPreference='Stop'\n$ok=$true\n")
	for _, p := range s.paths {
		fmt.Fprintf(&b, "if (-not (Test-Path -LiteralPath %s)) { $ok=$false }\n", psQuote(p))
	}
	b.WriteString("if ($ok) { 'done' } else { 'pending' }\n")
	out, err := r.Run(ctx, b.String())
	if err != nil {
		return false, err
	}
	return strings.Contains(out, "done"), nil
}

func (s dirsStep) Apply(ctx context.Context, r deployment.Runner) error {
	var b strings.Builder
	b.WriteString("$ErrorActionPreference='Stop'\n")
	for _, p := range s.paths {
		fmt.Fprintf(&b, "New-Item -ItemType Directory -Force -Path %s | Out-Null\n", psQuote(p))
	}
	_, err := r.Run(ctx, b.String())
	return err
}

// nssmStep ensures nssm.exe exists at a stable path, uploading it from the
// control-center host when a source is configured.
type nssmStep struct {
	path   string
	root   string // install root, to resolve a relative source when cwd differs
	source string
	sha256 string
	logf   func(format string, args ...any)
}

func (s nssmStep) Name() string     { return "nssm" }
func (s nssmStep) Privileged() bool { return true }

// resolve returns the source path: as given when it exists (cwd-relative, e.g.
// a dev checkout), otherwise relative to the install root. Services start with
// the working directory set to System32, so a relative config path would not
// resolve without this.
func (s nssmStep) resolve() string {
	src := strings.TrimSpace(s.source)
	if src == "" || filepath.IsAbs(src) {
		return src
	}
	if _, err := os.Stat(src); err == nil {
		return src
	}
	return filepath.Join(s.root, src)
}

func (s nssmStep) Check(ctx context.Context, r deployment.Runner) (bool, error) {
	expected := strings.TrimSpace(s.sha256)
	if expected != "" {
		decoded, err := hex.DecodeString(expected)
		if err != nil || len(decoded) != sha256.Size {
			return false, fmt.Errorf("invalid nssm sha256 %q", expected)
		}
	}
	source := s.resolve()
	if source == "" {
		if expected != "" {
			got, err := deployment.RemoteFileSHA256(ctx, r, s.path)
			if err != nil {
				return false, err
			}
			return strings.EqualFold(got, expected), nil
		}
		out, err := r.Run(ctx, fmt.Sprintf("if (Test-Path -LiteralPath %s) { 'done' } else { 'pending' }", psQuote(s.path)))
		if err != nil {
			return false, err
		}
		return strings.Contains(out, "done"), nil
	}
	// Hash-aware: re-upload when the source binary differs from the target's.
	data, err := os.ReadFile(source)
	if err != nil {
		// A service account may legitimately use the copy already installed
		// on the target even when the controller's source path is unavailable.
		if got, hashErr := deployment.RemoteFileSHA256(ctx, r, s.path); hashErr == nil && got != "" {
			return expected == "" || strings.EqualFold(got, expected), nil
		}
		return false, nil
	}
	sum := sha256.Sum256(data)
	want := hex.EncodeToString(sum[:])
	if expected != "" && !strings.EqualFold(expected, want) {
		return false, fmt.Errorf("nssm source sha256 %s does not match configured pin %s", want, expected)
	}
	got, err := deployment.RemoteFileSHA256(ctx, r, s.path)
	if err != nil {
		return false, err
	}
	return strings.EqualFold(got, want), nil
}

func (s nssmStep) Apply(ctx context.Context, r deployment.Runner) error {
	logf := s.logf
	if logf == nil {
		logf = func(string, ...any) {}
	}
	_, err := deployment.EnsureNSSM(ctx, r, s.path, s.resolve(), s.sha256, logf)
	return err
}

// selfServiceStep installs winify as a native Windows service so it starts on
// boot. New-Service is used instead of sc.exe to avoid command-line quoting.
type selfServiceStep struct {
	name   string
	exe    string
	config string
}

func (s selfServiceStep) Name() string     { return "self-service" }
func (s selfServiceStep) Privileged() bool { return true }

func (s selfServiceStep) Check(ctx context.Context, r deployment.Runner) (bool, error) {
	script := fmt.Sprintf(
		"$svc = Get-CimInstance Win32_Service -Filter %s -ErrorAction SilentlyContinue\n"+
			"if ($null -ne $svc) { 'done' } else { 'pending' }\n",
		psQuote("Name='"+s.name+"'"))
	out, err := r.Run(ctx, script)
	if err != nil {
		return false, err
	}
	return strings.Contains(out, "done"), nil
}

func (s selfServiceStep) Apply(ctx context.Context, r deployment.Runner) error {
	var b strings.Builder
	b.WriteString("$ErrorActionPreference='Stop'\n")
	fmt.Fprintf(&b, "$exe = %s\n", psQuote(s.exe))
	fmt.Fprintf(&b, "$cfg = %s\n", psQuote(s.config))
	b.WriteString("$bin = '\"' + $exe + '\" serve -config \"' + $cfg + '\"'\n")
	fmt.Fprintf(&b, "if (Get-Service -Name %s -ErrorAction SilentlyContinue) {\n", psQuote(s.name))
	fmt.Fprintf(&b, "  Stop-Service -Name %s -Force -ErrorAction SilentlyContinue\n", psQuote(s.name))
	fmt.Fprintf(&b, "  & sc.exe delete %s | Out-Null; if ($LASTEXITCODE -ne 0) { throw 'sc delete failed' }\n", psQuote(s.name))
	b.WriteString("  Start-Sleep -Seconds 1\n}\n")
	fmt.Fprintf(&b, "New-Service -Name %s -BinaryPathName $bin -StartupType Automatic -DisplayName %s | Out-Null; if ($LASTEXITCODE -ne 0) { throw 'New-Service failed' }\n",
		psQuote(s.name), psQuote(s.name))
	fmt.Fprintf(&b, "& sc.exe failure %s reset= 86400 actions= restart/5000/restart/5000/restart/5000 | Out-Null; if ($LASTEXITCODE -ne 0) { throw 'sc failure configuration failed' }\n",
		psQuote(s.name))
	_, err := r.Run(ctx, b.String())
	return err
}

// winrmStep enables PowerShell Remoting so the host can be a deploy target.
type winrmStep struct{}

func (winrmStep) Name() string     { return "winrm" }
func (winrmStep) Privileged() bool { return true }

func (winrmStep) Check(ctx context.Context, r deployment.Runner) (bool, error) {
	script := "$ErrorActionPreference='Stop'\n" +
		"$svc = Get-Service -Name WinRM -ErrorAction SilentlyContinue\n" +
		"if ($null -ne $svc -and $svc.Status -eq 'Running') { 'done' } else { 'pending' }\n"
	out, err := r.Run(ctx, script)
	if err != nil {
		return false, err
	}
	return strings.Contains(out, "done"), nil
}

func (winrmStep) Apply(ctx context.Context, r deployment.Runner) error {
	// Do not bypass Windows' public-network protection. Operators must place
	// the host on a trusted management profile before explicitly enabling this
	// step.
	script := "$ErrorActionPreference='Stop'\n" +
		"Enable-PSRemoting -Force\n"
	_, err := r.Run(ctx, script)
	return err
}
