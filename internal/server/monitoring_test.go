package server

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Devonlegend/winify/internal/config"
	"github.com/Devonlegend/winify/internal/models"
)

func TestMonitoringPageAndAPI(t *testing.T) {
	s, store := newTestServerWithStore(t)
	ctx := context.Background()

	if err := store.UpsertServer(ctx, config.Server{ID: "server-002", Type: config.ServerTypeDocker, SSHHost: "10.0.0.9"}); err != nil {
		t.Fatalf("UpsertServer docker: %v", err)
	}
	if err := store.UpsertServer(ctx, config.Server{ID: "server-001", Type: config.ServerTypeIIS, WinRMEndpoint: "https://10.0.0.5:5986/wsman"}); err != nil {
		t.Fatalf("UpsertServer iis: %v", err)
	}

	// One healthy server and one that is down.
	if err := store.InsertMetric(ctx, models.Metric{
		ServerID: "server-002", Timestamp: time.Now(), Reachable: true,
		CPUPercent: 12.5, MemTotal: 100, MemUsed: 50, DiskTotal: 200, DiskUsed: 100,
	}); err != nil {
		t.Fatalf("InsertMetric healthy: %v", err)
	}
	if err := store.InsertMetric(ctx, models.Metric{
		ServerID: "server-001", Timestamp: time.Now(), Reachable: false,
		Error: "dial tcp 10.0.0.5:5986: connect: connection refused",
	}); err != nil {
		t.Fatalf("InsertMetric down: %v", err)
	}

	cookie := login(t, s)

	api := getWithCookie(t, s, "/api/metrics", cookie)
	if api.Code != http.StatusOK {
		t.Fatalf("/api/metrics status = %d, want 200", api.Code)
	}
	if body := api.Body.String(); !strings.Contains(body, `"cpu_percent":12.5`) || !strings.Contains(body, `"reachable":false`) {
		t.Fatalf("/api/metrics body = %s", body)
	}

	hist := getWithCookie(t, s, "/api/servers/server-002/metrics", cookie)
	if hist.Code != http.StatusOK {
		t.Fatalf("/api/servers/.../metrics status = %d, want 200", hist.Code)
	}
	if !strings.Contains(hist.Body.String(), `"server_id":"server-002"`) {
		t.Fatalf("history body = %s", hist.Body.String())
	}

	// The down server's connection error must be visible on the page, and the
	// card must be marked down (not silently stale).
	page := getWithCookie(t, s, "/monitoring", cookie)
	if page.Code != http.StatusOK {
		t.Fatalf("/monitoring status = %d, want 200", page.Code)
	}
	body := page.Body.String()
	if !strings.Contains(body, "connection refused") {
		t.Error("monitoring page did not surface the connection error")
	}
	if !strings.Contains(body, `data-state="down"`) {
		t.Error("monitoring page did not mark the server as down")
	}
	if !strings.Contains(body, "badge-iis") || !strings.Contains(body, "badge-docker") {
		t.Error("monitoring page missing target-type badges")
	}
}
