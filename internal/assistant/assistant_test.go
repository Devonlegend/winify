package assistant

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Devonlegend/winify/internal/config"
	"github.com/Devonlegend/winify/internal/models"
)

type fakeLLM struct {
	response string
	err      error
	prompt   string
}

func (f *fakeLLM) Generate(_ context.Context, prompt string) (string, error) {
	f.prompt = prompt
	return f.response, f.err
}

func newTestStore(t *testing.T) *models.Store {
	t.Helper()
	db, err := models.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := models.Migrate(db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return models.NewStore(db)
}

func seedProject(t *testing.T, store *models.Store) {
	t.Helper()
	ctx := context.Background()
	if err := store.UpsertServer(ctx, config.Server{ID: "server-002", Type: config.ServerTypeDocker, SSHHost: "10.0.0.9"}); err != nil {
		t.Fatalf("UpsertServer: %v", err)
	}
	if err := store.UpsertProject(ctx, config.Project{ID: "proj-001", Name: "Storefront", ServerID: "server-002"}); err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}
	id, err := store.CreateDeployment(ctx, models.Deployment{
		ProjectID: "proj-001", TargetType: config.ServerTypeDocker, CommitSHA: "abc1234",
		Status: models.DeploySuccess, Trigger: "webhook", StartedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}
	if err := store.FinishDeployment(ctx, id, models.DeploySuccess, "", time.Now()); err != nil {
		t.Fatalf("FinishDeployment: %v", err)
	}
}

func newTestService(t *testing.T, store *models.Store, llm LLM) *Service {
	t.Helper()
	docs, err := LoadDocs()
	if err != nil {
		t.Fatalf("LoadDocs: %v", err)
	}
	svc := NewService(config.Default(), store, nil, NewMemoryStore(), llm, docs)
	if err := svc.Ingest(context.Background()); err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	return svc
}

func TestAskDocsQuestion(t *testing.T) {
	store := newTestStore(t)
	llm := &fakeLLM{response: "Click Roll back on the Deployment tab. Sources: Platform User Guide"}
	svc := newTestService(t, store, llm)

	ans, err := svc.Ask(context.Background(), "how do I roll back a deployment?")
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if ans.Intent != "docs" {
		t.Fatalf("intent = %q, want docs", ans.Intent)
	}
	if ans.Live {
		t.Error("docs question marked as live")
	}
	if !ans.DocsUsed || len(ans.Sources) == 0 {
		t.Fatalf("no docs cited: %+v", ans)
	}
	if !strings.Contains(strings.Join(ans.Sources, " "), "doc:") {
		t.Fatalf("sources not attributed to docs: %v", ans.Sources)
	}
	// The retrieved context must be the rollback documentation, not a guess.
	if !strings.Contains(llm.prompt, "DOCUMENTATION") || !strings.Contains(strings.ToLower(llm.prompt), "roll") {
		t.Fatalf("prompt not grounded in rollback docs:\n%s", llm.prompt)
	}
}

func TestAskDeployQuestionUsesHistory(t *testing.T) {
	store := newTestStore(t)
	seedProject(t, store)
	llm := &fakeLLM{response: "Yes, it succeeded. Sources: deploy history: proj-001 #1"}
	svc := newTestService(t, store, llm)

	ans, err := svc.Ask(context.Background(), "did the last deploy to proj-001 succeed?")
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if ans.Intent != "deploy" {
		t.Fatalf("intent = %q, want deploy", ans.Intent)
	}
	if !ans.Live {
		t.Fatal("deploy question did not use live data")
	}
	if !strings.Contains(strings.Join(ans.Sources, " "), "deploy history: proj-001 #1") {
		t.Fatalf("deploy history not cited: %v", ans.Sources)
	}
	if !strings.Contains(llm.prompt, "LIVE DATA") || !strings.Contains(llm.prompt, "status=success") {
		t.Fatalf("prompt missing live deploy data:\n%s", llm.prompt)
	}
}

func TestAskServerQuestionUsesMetrics(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	if err := store.UpsertServer(ctx, config.Server{ID: "server-002", Type: config.ServerTypeDocker, SSHHost: "10.0.0.9"}); err != nil {
		t.Fatalf("UpsertServer: %v", err)
	}
	if err := store.InsertMetric(ctx, models.Metric{
		ServerID: "server-002", Timestamp: time.Now(), Reachable: true,
		CPUPercent: 42.5, MemTotal: 100, MemUsed: 50, DiskTotal: 200, DiskUsed: 100,
	}); err != nil {
		t.Fatalf("InsertMetric: %v", err)
	}
	llm := &fakeLLM{response: "CPU is 42.5%. Sources: monitoring: server-002"}
	svc := newTestService(t, store, llm)

	ans, err := svc.Ask(ctx, "what's server-002's CPU right now")
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if ans.Intent != "server" {
		t.Fatalf("intent = %q, want server", ans.Intent)
	}
	if !ans.Live {
		t.Fatal("monitoring question did not use live data")
	}
	if !strings.Contains(llm.prompt, "cpu=42.5") {
		t.Fatalf("prompt missing live metrics:\n%s", llm.prompt)
	}
	if !strings.Contains(strings.Join(ans.Sources, " "), "monitoring: server-002") {
		t.Fatalf("monitoring source not cited: %v", ans.Sources)
	}
}

func TestAskFallsBackWhenModelUnavailable(t *testing.T) {
	store := newTestStore(t)
	seedProject(t, store)
	llm := &fakeLLM{err: errors.New("ollama down")}
	svc := newTestService(t, store, llm)

	ans, err := svc.Ask(context.Background(), "did the last deploy to proj-001 succeed?")
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	// Even without the model, the deterministic live summary must be returned.
	if !strings.Contains(ans.Answer, "status=success") {
		t.Fatalf("fallback answer not grounded in live data: %q", ans.Answer)
	}
	if len(ans.Sources) == 0 {
		t.Fatal("fallback answer has no sources")
	}
}

func TestAskSurfacesUnreachableServer(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	if err := store.UpsertServer(ctx, config.Server{ID: "server-002", Type: config.ServerTypeDocker, SSHHost: "10.0.0.9"}); err != nil {
		t.Fatalf("UpsertServer: %v", err)
	}
	if err := store.InsertMetric(ctx, models.Metric{
		ServerID: "server-002", Timestamp: time.Now(), Reachable: false,
		Error: "dial tcp 10.0.0.9:22: connection refused",
	}); err != nil {
		t.Fatalf("InsertMetric: %v", err)
	}
	svc := newTestService(t, store, &fakeLLM{err: errors.New("model down")})

	ans, err := svc.Ask(ctx, "what's server-002's CPU right now")
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if !strings.Contains(ans.Answer, "UNREACHABLE") || !strings.Contains(ans.Answer, "connection refused") {
		t.Fatalf("unreachable state not surfaced: %q", ans.Answer)
	}
}
