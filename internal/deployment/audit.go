package deployment

import (
	"context"
	"regexp"
)

// AuditMeta describes who caused a remote command: which server, what kind of
// action, and which deployment it belongs to.
type AuditMeta struct {
	ServerID     string
	ServerType   string
	Action       string // deploy | rollback | monitor
	DeploymentID int64
}

// AuditRecorder is called after every remote command executes. The command
// string is already free of credentials (SSH/WinRM auth is out of band) and
// sensitive payloads are replaced with a redacted label before recording.
type AuditRecorder func(ctx context.Context, meta AuditMeta, command string, err error)

type auditMetaKey struct{}
type auditRedactionKey struct{}

// WithAudit attaches audit metadata to ctx. The audited runner reads it when it
// records a command.
func WithAudit(ctx context.Context, meta AuditMeta) context.Context {
	return context.WithValue(ctx, auditMetaKey{}, meta)
}

// AuditMetaFromContext returns the audit metadata, if present.
func AuditMetaFromContext(ctx context.Context) (AuditMeta, bool) {
	meta, ok := ctx.Value(auditMetaKey{}).(AuditMeta)
	return meta, ok
}

// withAuditRedaction marks commands as sensitive so the auditor records the
// label instead of the raw command (used for the generated compose file, which
// embeds project env values).
func withAuditRedaction(ctx context.Context, label string) context.Context {
	return context.WithValue(ctx, auditRedactionKey{}, label)
}

func auditRedaction(ctx context.Context) (string, bool) {
	label, ok := ctx.Value(auditRedactionKey{}).(string)
	return label, ok
}

// WithAuditRecorder wraps a Runner so every command is reported. A nil recorder
// returns the runner unchanged.
func WithAuditRecorder(inner Runner, rec AuditRecorder) Runner {
	if rec == nil || inner == nil {
		return inner
	}
	return &auditedRunner{inner: inner, rec: rec}
}

var auditUserInfoPattern = regexp.MustCompile(`(?i)(https?://)[^/\s@]+@`)

// RedactAuditText removes credentials embedded in HTTP(S) repository URLs
// before they are persisted or written to the application log. The normal
// path also rejects these URLs at validation time; this is defense in depth
// for imported inventory, detection, and older database rows.
func RedactAuditText(s string) string {
	return auditUserInfoPattern.ReplaceAllString(s, `${1}[redacted]@`)
}

type auditedRunner struct {
	inner Runner
	rec   AuditRecorder
}

func (a *auditedRunner) Run(ctx context.Context, command string) (string, error) {
	out, err := a.inner.Run(ctx, command)
	meta, _ := AuditMetaFromContext(ctx)
	recorded := command
	if label, ok := auditRedaction(ctx); ok {
		recorded = label
	}
	a.rec(ctx, meta, RedactAuditText(recorded), err)
	return out, err
}

func (a *auditedRunner) Close() error { return a.inner.Close() }
