// Package deployment owns everything that touches target servers: SSH command
// execution, the Docker deploy pipeline, webhook signature verification and
// rollback.
//
// SECURITY: a Runner executes arbitrary shell commands as the configured SSH
// user on a target host. Credentials for targets are resolved from the Phase 1
// encrypted store and are never logged. Only the control-center admin can
// cause a deploy; webhook-triggered deploys are gated by per-project HMAC.
package deployment

import "context"

// Runner executes a shell command on a target server and returns its combined
// stdout/stderr. Implementations are not required to be concurrent-safe.
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
