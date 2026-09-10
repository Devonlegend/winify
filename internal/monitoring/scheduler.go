package monitoring

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/Devonlegend/winify/internal/config"
	"github.com/Devonlegend/winify/internal/models"
)

// Scheduler polls every registered server on an interval and stores the result.
type Scheduler struct {
	store     *models.Store
	collector *Collector
	interval  time.Duration
	timeout   time.Duration
	retention time.Duration

	stop     chan struct{}
	done     chan struct{}
	startOne sync.Once
	stopOne  sync.Once
}

// NewScheduler builds a scheduler from config, applying safe defaults.
func NewScheduler(cfg config.Config, store *models.Store, collector *Collector) *Scheduler {
	interval := time.Duration(cfg.Monitoring.IntervalSeconds) * time.Second
	if interval <= 0 {
		interval = 30 * time.Second
	}
	timeout := time.Duration(cfg.Monitoring.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	retention := time.Duration(cfg.Monitoring.RetentionHours) * time.Hour
	if retention <= 0 {
		retention = 24 * time.Hour
	}
	return &Scheduler{
		store:     store,
		collector: collector,
		interval:  interval,
		timeout:   timeout,
		retention: retention,
		stop:      make(chan struct{}),
		done:      make(chan struct{}),
	}
}

// Interval is how often servers are polled (used by the UI for "stale" logic).
func (s *Scheduler) Interval() time.Duration { return s.interval }

// Start launches the polling loop; it polls once immediately, then on a ticker.
func (s *Scheduler) Start() {
	s.startOne.Do(func() { go s.loop() })
}

// Stop halts the loop and waits for the in-flight poll to finish.
func (s *Scheduler) Stop() {
	s.stopOne.Do(func() { close(s.stop) })
	<-s.done
}

func (s *Scheduler) loop() {
	defer close(s.done)
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	s.PollAll(context.Background())
	for {
		select {
		case <-s.stop:
			return
		case <-ticker.C:
			s.PollAll(context.Background())
		}
	}
}

// PollAll samples every registered server concurrently and prunes old samples.
func (s *Scheduler) PollAll(ctx context.Context) {
	servers, err := s.store.ListServers(ctx)
	if err != nil {
		log.Printf("monitor: list servers: %v", err)
		return
	}

	var wg sync.WaitGroup
	for _, srv := range servers {
		wg.Add(1)
		go func(srv config.Server) {
			defer wg.Done()
			s.pollOne(ctx, srv)
		}(srv)
	}
	wg.Wait()

	if s.retention > 0 {
		if err := s.store.PruneMetrics(ctx, time.Now().Add(-s.retention)); err != nil {
			log.Printf("monitor: prune metrics: %v", err)
		}
	}
}

func (s *Scheduler) pollOne(ctx context.Context, srv config.Server) {
	pollCtx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	// Some remote calls (notably the WinRM HTTP transport) do not honor context
	// cancellation promptly. Run collection in a goroutine and enforce the
	// deadline here, so a down host is recorded as unreachable on time instead
	// of stalling the cycle and leaving the dashboard stale. The buffered
	// channel means the abandoned goroutine never blocks when it finally ends.
	done := make(chan models.Metric, 1)
	go func() { done <- s.collector.Collect(pollCtx, srv) }()

	var m models.Metric
	select {
	case m = <-done:
	case <-pollCtx.Done():
		m = models.Metric{
			ServerID:  srv.ID,
			Timestamp: time.Now(),
			Error:     "collection timed out after " + s.timeout.String(),
		}
	}

	if err := s.store.InsertMetric(ctx, m); err != nil {
		log.Printf("monitor: store %s: %v", srv.ID, err)
		return
	}
	if !m.Reachable {
		log.Printf("monitor: %s unreachable: %s", srv.ID, m.Error)
	}
}
