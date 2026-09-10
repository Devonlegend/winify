// Package deployment owns everything that touches target servers: command
// execution (SSH for Docker/Linux, WinRM for Windows/IIS), the deploy
// pipelines, webhook signature verification and rollback.
//
// SECURITY: a Runner executes commands in a target's native shell (sh over
// SSH, PowerShell over WinRM) as the configured user. That account can stop
// services, modify IIS and run containers. Credentials are resolved from the
// Phase 1 encrypted store and are never logged. Only the control-center admin
// can cause a deploy; webhook-triggered deploys are gated by per-project HMAC.
package deployment

import "context"

// Runner executes a command in a target's native shell and returns combined
// stdout/stderr. SSH runs sh commands; WinRM runs PowerShell scripts.
// Implementations are not required to be concurrent-safe.
type Runner interface {
	Run(ctx context.Context, command string) (string, error)
	Close() error
}

// SecretResolver resolves an encrypted credential reference (for example
// "vault:server-002-ssh") to its plaintext value. Implemented by
// auth.CredentialStore. The returned value must never be logged.
type SecretResolver interface {
	Get(ctx context.Context, name string) (string, error)
}
