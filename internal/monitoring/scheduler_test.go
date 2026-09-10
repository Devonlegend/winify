package monitoring

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/Devonlegend/winify/internal/config"
	"github.com/Devonlegend/winify/internal/deployment"
	"github.com/Devonlegend/winify/internal/models"
)

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

func TestSchedulerPollAll(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	if err := store.UpsertServer(ctx, config.Server{ID: "s1", Type: config.ServerTypeDocker, SSHHost: "10.0.0.9"}); err != nil {
		t.Fatalf("UpsertServer: %v", err)
	}

	r := &fakeRunner{out: linuxSample}
	factory := func(context.Context, config.Server) (deployment.Runner, error) { return r, nil }
	s := NewScheduler(config.Default(), store, NewCollector(factory))

	s.PollAll(ctx)

	latest, err := store.LatestMetrics(ctx)
	if err != nil {
		t.Fatalf("LatestMetrics: %v", err)
	}
	if len(latest) != 1 {
		t.Fatalf("latest = %d rows, want 1", len(latest))
	}
	if !latest[0].Reachable || latest[0].ServerID != "s1" {
		t.Fatalf("latest = %+v", latest[0])
	}

	history, err := store.MetricHistory(ctx, "s1", 10)
	if err != nil {
		t.Fatalf("MetricHistory: %v", err)
	}
	if len(history) != 1 {
		t.Fatalf("history = %d, want 1", len(history))
	}
}

func TestSchedulerPrunesOldSamples(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	if err := store.UpsertServer(ctx, config.Server{ID: "s1", Type: config.ServerTypeDocker, SSHHost: "10.0.0.9"}); err != nil {
		t.Fatalf("UpsertServer: %v", err)
	}

	// A sample well outside the 24h default retention.
	if err := store.InsertMetric(ctx, models.Metric{
		ServerID: "s1", Timestamp: time.Now().Add(-72 * time.Hour), Reachable: true, MemTotal: 100, MemUsed: 10,
	}); err != nil {
		t.Fatalf("InsertMetric: %v", err)
	}

	r := &fakeRunner{out: linuxSample}
	factory := func(context.Context, config.Server) (deployment.Runner, error) { return r, nil }
	NewScheduler(config.Default(), store, NewCollector(factory)).PollAll(ctx)

	history, err := store.MetricHistory(ctx, "s1", 10)
	if err != nil {
		t.Fatalf("MetricHistory: %v", err)
	}
	if len(history) != 1 {
		t.Fatalf("history = %d rows, want 1 (old sample pruned)", len(history))
	}
	if time.Since(history[0].Timestamp) > time.Hour {
		t.Fatalf("retained sample is stale: %v", history[0].Timestamp)
	}
}

func TestSchedulerIntervalFromConfig(t *testing.T) {
	cfg := config.Default()
	cfg.Monitoring.IntervalSeconds = 7
	s := NewScheduler(cfg, nil, nil)
	if s.Interval() != 7*time.Second {
		t.Fatalf("interval = %v, want 7s", s.Interval())
	}
}
