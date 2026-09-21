package deployment

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/Devonlegend/winify/internal/config"
)

// loggerFunc writes one line to a deployment's persisted log.
type loggerFunc func(format string, args ...any)

// Target is a deployment pipeline for one target type: Docker over SSH, or IIS
// over WinRM. The shared Deployer owns deploy history, the in-flight guard and
// reverse-proxy registration; a Target owns only the target-specific steps.
//
// Implementations: dockerTarget (docker.go) and iisTarget (iis.go).
type Target interface {
	// Deploy performs a fresh deploy and returns an artifact reference for
	// history (a Docker image tag, or the IIS pre-deploy backup path).
	Deploy(ctx context.Context, job deployJob, logf loggerFunc) (string, error)
	// Rollback restores the previous known-good state and returns an artifact
	// reference.
	Rollback(ctx context.Context, job deployJob, logf loggerFunc) (string, error)
	// Close releases any connection held by the target.
	Close() error
}

// TargetFactory connects to a target and builds the pipeline for it.
type TargetFactory func(ctx context.Context, job deployJob, secrets SecretResolver) (Target, error)

// SSHDialer opens a Runner over SSH. It is injected so tests can substitute a
// fake; production wires it to DialSSH.
type SSHDialer func(ctx context.Context, srv config.Server, privateKeyPEM string) (Runner, error)

// NewTargetFactory returns the production factory that selects the pipeline by
// the target server's Type. This is the single branch point between Docker and
// IIS; everything downstream of a Target is target-specific by design. The
// audit recorder wraps the connection so every remote command is logged.
func NewTargetFactory(cfg config.Config, sshDial SSHDialer, audit AuditRecorder) TargetFactory {
	return func(ctx context.Context, job deployJob, secrets SecretResolver) (Target, error) {
		switch job.server.Type {
		case config.ServerTypeIIS, config.ServerTypeWindowsService:
			// A local target runs PowerShell in-process: no WinRM, no credential.
			if job.server.Local {
				runner := WithAuditRecorder(NewLocalRunner(), audit)
				if job.server.Type == config.ServerTypeWindowsService {
					return NewWindowsServiceTarget(cfg, runner), nil
				}
				return NewIISTarget(cfg, runner), nil
			}
			password, err := ResolveRef(ctx, secrets, job.server.CredentialRef)
			if err != nil {
				return nil, fmt.Errorf("resolve winrm credential: %w", err)
			}
			runner, err := DialWinRM(job.server, password)
			if err != nil {
				return nil, fmt.Errorf("connect to %s: %w", job.server.WinRMEndpoint, err)
			}
			if job.server.Type == config.ServerTypeWindowsService {
				return NewWindowsServiceTarget(cfg, WithAuditRecorder(runner, audit)), nil
			}
			return NewIISTarget(cfg, WithAuditRecorder(runner, audit)), nil
		default:
			key, err := ResolveRef(ctx, secrets, job.server.SSHKeyRef)
			if err != nil {
				return nil, fmt.Errorf("resolve ssh key: %w", err)
			}
			runner, err := sshDial(ctx, job.server, key)
			if err != nil {
				return nil, fmt.Errorf("connect to %s: %w", job.server.SSHHost, err)
			}
			return NewDockerTarget(cfg, WithAuditRecorder(runner, audit)), nil
		}
	}
}

// ResolveRef turns a credential reference into its plaintext value. The value
// must never be logged.
func ResolveRef(ctx context.Context, secrets SecretResolver, ref string) (string, error) {
	name := strings.TrimPrefix(ref, "vault:")
	if name == "" {
		return "", fmt.Errorf("no credential reference configured")
	}
	return secrets.Get(ctx, name)
}

// execCmd runs a command, logging a redacted label and the (trimmed) output.
func execCmd(ctx context.Context, runner Runner, cmd, logCmd string, logf loggerFunc) (string, error) {
	logf("$ %s", logCmd)
	out, err := runner.Run(ctx, cmd)
	if trimmed := strings.TrimSpace(out); trimmed != "" {
		logf("%s", trimmed)
	}
	return out, err
}

// targetHost resolves the address the reverse proxy forwards to. It prefers an
// explicit host, then ssh_host, then the host part of the WinRM endpoint.
// This is one of the few places that genuinely needs target-type awareness.
func targetHost(srv config.Server) string {
	if srv.Host != "" {
		return srv.Host
	}
	if srv.SSHHost != "" {
		return srv.SSHHost
	}
	if srv.WinRMEndpoint != "" {
		if u, err := url.Parse(srv.WinRMEndpoint); err == nil && u.Hostname() != "" {
			return u.Hostname()
		}
	}
	return ""
}

// healthURL builds the smoke-test URL for a project on the target's loopback.
func healthURL(project config.Project) string {
	healthPath := project.HealthPath
	if healthPath == "" {
		healthPath = "/"
	}
	if !strings.HasPrefix(healthPath, "/") {
		healthPath = "/" + healthPath
	}
	return fmt.Sprintf("http://127.0.0.1:%d%s", project.EffectiveHostPort(), healthPath)
}

// healthPlan is the resolved health-check timing for one project.
type healthPlan struct {
	interval    int // seconds between attempts
	attempts    int
	timeout     int // seconds allowed per attempt
	startPeriod int // seconds to wait before the first attempt
}

// healthPlanFor resolves per-project health timing, falling back to the global
// deploy config. When the project sets no retry count, attempts is derived from
// the global overall timeout divided by the interval.
func healthPlanFor(cfg config.Config, project config.Project) healthPlan {
	interval := project.HealthIntervalSeconds
	if interval <= 0 {
		interval = cfg.Deploy.HealthIntervalSeconds
	}
	if interval <= 0 {
		interval = 3
	}

	perAttempt := project.HealthTimeoutSeconds
	if perAttempt <= 0 {
		perAttempt = 5
	}

	attempts := project.HealthRetries
	if attempts <= 0 {
		overall := cfg.Deploy.HealthTimeoutSeconds
		if overall <= 0 {
			overall = 60
		}
		attempts = overall / interval
		if attempts < 1 {
			attempts = 1
		}
	}
	return healthPlan{
		interval:    interval,
		attempts:    attempts,
		timeout:     perAttempt,
		startPeriod: project.HealthStartPeriodSeconds,
	}
}

func shortSHA(sha string) string {
	if sha == "" {
		return "unknown"
	}
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

// deployRevision is the git revision to check out and tag: the pushed commit
// when there is one, otherwise the configured branch (manual deploys).
func deployRevision(project config.Project, commit string) string {
	if commit != "" {
		return commit
	}
	if project.Branch != "" {
		return project.Branch
	}
	return "main"
}

// sanitize makes a string safe for a Docker image path component.
func sanitize(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	return b.String()
}

// shellQuote wraps s in single quotes for safe use in a POSIX shell command.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// psQuote wraps s in single quotes for safe use in a PowerShell script.
func psQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// winPath joins Windows path components with backslashes. Components must not
// carry a trailing separator.
func winPath(parts ...string) string {
	return strings.Join(parts, `\`)
}

// winDir returns the directory portion of a Windows path. It splits on both
// separators so it behaves correctly even when the control-center runs on Linux.
func winDir(p string) string {
	if i := strings.LastIndexAny(p, `\/`); i >= 0 {
		return p[:i]
	}
	return "."
}

// lastLine returns the last non-empty line of output, used to read a value the
// remote script printed (for example a backup directory).
func lastLine(out string) string {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if s := strings.TrimSpace(lines[i]); s != "" {
			return s
		}
	}
	return ""
}
