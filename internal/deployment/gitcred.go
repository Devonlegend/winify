package deployment

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/Devonlegend/winify/internal/config"
)

// GitAuthKind is how a git credential authenticates.
type GitAuthKind string

const (
	GitAuthNone GitAuthKind = ""
	GitAuthSSH  GitAuthKind = "ssh"  // deploy key (PEM private key)
	GitAuthHTTP GitAuthKind = "http" // HTTPS access token
)

// GitAuth is a resolved git credential staged for one target. For SSH the key
// is uploaded to KeyPath with owner-only permissions; for HTTP the token is
// carried as an http.extraHeader on each git invocation.
type GitAuth struct {
	Kind    GitAuthKind
	KeyPath string // SSH only: key file on the target
	Token   string // HTTP only
}

// Sensitive reports whether command lines built with this credential embed the
// secret (HTTP token auth) and must be redacted from logs and audit.
func (a *GitAuth) Sensitive() bool { return a != nil && a.Kind == GitAuthHTTP }

// gitOption returns the `git -c <opt>` fragment, quoted with quote (psQuote on
// Windows, shellQuote on POSIX). Empty for SSH key-file auth without a path.
func (a *GitAuth) gitOption(quote func(string) string) string {
	if a == nil {
		return ""
	}
	switch a.Kind {
	case GitAuthSSH:
		return "-c " + quote("core.sshCommand=ssh -i "+a.KeyPath+" -o IdentitiesOnly=yes")
	case GitAuthHTTP:
		return "-c " + quote("http.extraHeader=AUTHORIZATION: bearer "+a.Token)
	}
	return ""
}

// PrepareGitAuth resolves the project's git_credential_ref, and for SSH keys
// uploads the key to keyPath on the target with owner-only permissions.
// Returns nil when the project uses no credential.
func PrepareGitAuth(ctx context.Context, runner Runner, secrets SecretResolver, project config.Project, keyPath string, windows bool, logf loggerFunc) (*GitAuth, error) {
	ref := strings.TrimSpace(project.GitCredentialRef)
	if ref == "" {
		return nil, nil
	}
	if secrets == nil {
		return nil, fmt.Errorf("git credential %q configured but no credential store is available", ref)
	}
	value, err := ResolveRef(ctx, secrets, ref)
	if err != nil {
		return nil, fmt.Errorf("resolve git credential: %w", err)
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, fmt.Errorf("git credential %q is empty", ref)
	}
	if !isPrivateKeyPEM(value) {
		return &GitAuth{Kind: GitAuthHTTP, Token: value}, nil
	}
	if keyPath == "" {
		return nil, fmt.Errorf("git ssh key path is required")
	}
	if err := uploadSecretFile(ctx, runner, keyPath, value, windows, logf); err != nil {
		return nil, fmt.Errorf("stage git ssh key: %w", err)
	}
	return &GitAuth{Kind: GitAuthSSH, KeyPath: keyPath}, nil
}

func isPrivateKeyPEM(value string) bool {
	return strings.Contains(value, "PRIVATE KEY-----")
}

// uploadSecretFile writes value to path on the target with owner-only
// permissions. The payload is base64-encoded and the command is marked
// sensitive so nothing lands in deploy logs or the audit log.
func uploadSecretFile(ctx context.Context, runner Runner, path, value string, windows bool, logf loggerFunc) error {
	var script string
	if windows {
		var b strings.Builder
		b.WriteString("$ErrorActionPreference='Stop'\n")
		fmt.Fprintf(&b, "$path = %s\n", psQuote(path))
		b.WriteString("$parent = Split-Path -Parent $path\n")
		b.WriteString("if ($parent) { New-Item -ItemType Directory -Force -Path $parent | Out-Null }\n")
		fmt.Fprintf(&b, "[IO.File]::WriteAllText($path, [Text.Encoding]::UTF8.GetString([Convert]::FromBase64String(%s)))\n", psQuote(base64Encode(value)))
		fmt.Fprintf(&b, "icacls $path /inheritance:r /grant:r 'SYSTEM:F' 'Administrators:F' | Out-Null\n")
		script = b.String()
	} else {
		script = fmt.Sprintf("umask 077; mkdir -p %s && printf %%s %s | base64 -d > %s && chmod 600 %s",
			shellQuote(pathDir(path)), shellQuote(base64Encode(value)), shellQuote(path), shellQuote(path))
	}
	secretCtx := withAuditRedaction(ctx, "write git credential file (contents redacted)")
	_, err := execCmd(secretCtx, runner, script, "write git credential file", logf)
	return err
}

func base64Encode(s string) string {
	return base64.StdEncoding.EncodeToString([]byte(s))
}

func pathDir(p string) string {
	if i := strings.LastIndex(p, "/"); i > 0 {
		return p[:i]
	}
	return "."
}
