// Package monitoring samples CPU, memory, disk, uptime and service state from
// registered servers over the SAME SSH/WinRM connections the deployment package
// already uses — there is no agent to install. A background scheduler polls on
// an interval and persists each sample; the dashboard reads it back.
//
// SECURITY: collection runs commands as the target's configured user (the same
// privileged account as deploys). Credentials are resolved from the Phase 1
// store and are never logged.
package monitoring

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Devonlegend/winify/internal/config"
	"github.com/Devonlegend/winify/internal/deployment"
	"github.com/Devonlegend/winify/internal/models"
)

// RunnerFactory opens a command Runner for a server. It is injected so tests
// can substitute a fake; production reuses the Phase 2/3 dialers.
type RunnerFactory func(ctx context.Context, srv config.Server) (deployment.Runner, error)

// NewRunnerFactory resolves the server's encrypted credential and dials the
// same connection the deploy pipeline uses. This is deliberately the ONLY
// connection logic monitoring owns — it delegates to deployment.DialSSH and
// deployment.DialWinRM rather than re-implementing SSH/WinRM.
func NewRunnerFactory(cfg config.Config, secrets deployment.SecretResolver) RunnerFactory {
	return func(ctx context.Context, srv config.Server) (deployment.Runner, error) {
		switch srv.Type {
		case config.ServerTypeIIS:
			password, err := deployment.ResolveRef(ctx, secrets, srv.CredentialRef)
			if err != nil {
				return nil, fmt.Errorf("resolve winrm credential: %w", err)
			}
			return deployment.DialWinRM(srv, password)
		default:
			key, err := deployment.ResolveRef(ctx, secrets, srv.SSHKeyRef)
			if err != nil {
				return nil, fmt.Errorf("resolve ssh key: %w", err)
			}
			return deployment.DialSSH(ctx, srv.SSHHost, srv.SSHPort, srv.SSHUser, key, cfg.Deploy.KnownHostsFile)
		}
	}
}

// Collector samples one server.
type Collector struct {
	newRunner RunnerFactory
}

// NewCollector builds a collector over the given connection factory.
func NewCollector(newRunner RunnerFactory) *Collector {
	return &Collector{newRunner: newRunner}
}

// Collect returns a sample. Connection and parse failures are recorded as an
// unreachable metric with an error message rather than returned as an error, so
// the scheduler always persists something and the dashboard can explain why the
// server is down instead of silently showing stale numbers.
func (c *Collector) Collect(ctx context.Context, srv config.Server) models.Metric {
	m := models.Metric{ServerID: srv.ID, Timestamp: time.Now()}

	runner, err := c.newRunner(ctx, srv)
	if err != nil {
		m.Error = err.Error()
		return m
	}
	defer runner.Close()

	out, err := runner.Run(ctx, scriptFor(srv))
	if err != nil {
		m.Error = err.Error()
		return m
	}
	if err := parseMetrics(out, &m); err != nil {
		m.Error = "parse metrics: " + err.Error()
		return m
	}
	m.Reachable = true
	return m
}

// parseMetrics reads the key=value lines both scripts emit.
func parseMetrics(out string, m *models.Metric) error {
	values := make(map[string]string)
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		if strings.HasPrefix(key, "service:") {
			m.Services = append(m.Services, models.ServiceStatus{
				Name:  strings.TrimPrefix(key, "service:"),
				State: value,
			})
			continue
		}
		values[key] = value
	}

	m.CPUPercent = parseFloat(values["cpu"])
	m.MemTotal = parseUint(values["mem_total"])
	m.MemUsed = parseUint(values["mem_used"])
	m.DiskTotal = parseUint(values["disk_total"])
	m.DiskUsed = parseUint(values["disk_used"])
	m.UptimeSeconds = parseInt(values["uptime"])
	m.Load1 = parseFloat(values["load1"])

	// If memory is absent the script produced nothing usable; treat it as a
	// failed sample rather than reporting zeros as if they were real.
	if m.MemTotal == 0 {
		return fmt.Errorf("no memory data in output")
	}
	m.MemPercent = float64(m.MemUsed) / float64(m.MemTotal) * 100
	if m.DiskTotal > 0 {
		m.DiskPercent = float64(m.DiskUsed) / float64(m.DiskTotal) * 100
	}
	return nil
}

func parseFloat(s string) float64 {
	v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		return 0
	}
	return v
}

func parseUint(s string) uint64 {
	v, err := strconv.ParseUint(strings.TrimSpace(s), 10, 64)
	if err != nil {
		return 0
	}
	return v
}

func parseInt(s string) int64 {
	v, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil {
		return 0
	}
	return v
}
