package deployment

import (
	"context"
	"testing"
)

func TestAuditedRunnerRecordsMeta(t *testing.T) {
	inner := &fakeRunner{}
	var (
		calls   int
		gotCmd  string
		gotMeta AuditMeta
	)
	rec := func(_ context.Context, meta AuditMeta, command string, _ error) {
		calls++
		gotCmd = command
		gotMeta = meta
	}
	runner := WithAuditRecorder(inner, rec)
	ctx := WithAudit(context.Background(), AuditMeta{
		ServerID: "server-002", ServerType: "docker", Action: "deploy", DeploymentID: 7,
	})

	if _, err := runner.Run(ctx, "docker build -t app ."); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if calls != 1 || gotCmd != "docker build -t app ." {
		t.Fatalf("calls=%d cmd=%q", calls, gotCmd)
	}
	if gotMeta.ServerID != "server-002" || gotMeta.Action != "deploy" || gotMeta.DeploymentID != 7 {
		t.Fatalf("meta = %+v", gotMeta)
	}
}

func TestAuditedRunnerRedactsSensitiveCommand(t *testing.T) {
	inner := &fakeRunner{}
	var gotCmd string
	rec := func(_ context.Context, _ AuditMeta, command string, _ error) { gotCmd = command }
	runner := WithAuditRecorder(inner, rec)

	ctx := withAuditRedaction(context.Background(), "write docker-compose.yml (contents redacted)")
	if _, err := runner.Run(ctx, "echo c2VjcmV0 | base64 -d > docker-compose.yml"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if gotCmd != "write docker-compose.yml (contents redacted)" {
		t.Fatalf("recorded %q, want the redacted label", gotCmd)
	}
}

func TestAuditedRunnerNilRecorderIsPassThrough(t *testing.T) {
	inner := &fakeRunner{}
	if got := WithAuditRecorder(inner, nil); got != inner {
		t.Fatal("nil recorder should return the inner runner unchanged")
	}
}
