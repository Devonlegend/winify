package monitoring

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Devonlegend/winify/internal/config"
	"github.com/Devonlegend/winify/internal/deployment"
)

const linuxSample = `cpu=12.34
mem_total=1000000
mem_used=250000
disk_total=2000000
disk_used=500000
uptime=123456
load1=0.42
service:docker=active
service:nginx=inactive
`

const windowsSample = `cpu=7.5
mem_total=2000000
mem_used=1000000
disk_total=4000000
disk_used=3000000
uptime=999
load1=0
service:W3SVC=Running
`

type fakeRunner struct {
	out    string
	err    error
	closed bool
	cmd    string
}

func (f *fakeRunner) Run(_ context.Context, cmd string) (string, error) {
	f.cmd = cmd
	return f.out, f.err
}

func (f *fakeRunner) Close() error { f.closed = true; return nil }

func collectorWith(r *fakeRunner) *Collector {
	factory := func(context.Context, config.Server) (deployment.Runner, error) { return r, nil }
	return NewCollector(factory)
}

func TestCollectLinux(t *testing.T) {
	r := &fakeRunner{out: linuxSample}
	c := collectorWith(r)
	srv := config.Server{ID: "s1", Type: config.ServerTypeDocker, Services: []string{"docker", "nginx"}}

	m := c.Collect(context.Background(), srv)
	if !m.Reachable {
		t.Fatalf("reachable = false, error = %q", m.Error)
	}
	if m.CPUPercent != 12.34 {
		t.Errorf("cpu = %v, want 12.34", m.CPUPercent)
	}
	if m.MemPercent != 25 {
		t.Errorf("mem percent = %v, want 25", m.MemPercent)
	}
	if m.DiskPercent != 25 {
		t.Errorf("disk percent = %v, want 25", m.DiskPercent)
	}
	if m.UptimeSeconds != 123456 {
		t.Errorf("uptime = %d", m.UptimeSeconds)
	}
	if len(m.Services) != 2 || m.Services[0].Name != "docker" || m.Services[0].State != "active" {
		t.Errorf("services = %+v", m.Services)
	}
	if !r.closed {
		t.Error("runner was not closed")
	}
}

func TestCollectWindows(t *testing.T) {
	r := &fakeRunner{out: windowsSample}
	c := collectorWith(r)
	srv := config.Server{ID: "s2", Type: config.ServerTypeIIS, Services: []string{"W3SVC"}}

	m := c.Collect(context.Background(), srv)
	if !m.Reachable {
		t.Fatalf("reachable = false, error = %q", m.Error)
	}
	if m.MemPercent != 50 {
		t.Errorf("mem percent = %v, want 50", m.MemPercent)
	}
	if len(m.Services) != 1 || m.Services[0].State != "Running" {
		t.Errorf("services = %+v", m.Services)
	}
}

func TestCollectCommandErrorIsUnreachable(t *testing.T) {
	r := &fakeRunner{err: errors.New("dial tcp 10.0.0.9:22: connection refused")}
	c := collectorWith(r)

	m := c.Collect(context.Background(), config.Server{ID: "s1", Type: config.ServerTypeDocker})
	if m.Reachable {
		t.Fatal("reachable = true despite command error")
	}
	if !strings.Contains(m.Error, "connection refused") {
		t.Fatalf("error = %q, want connection error", m.Error)
	}
}

func TestCollectDialErrorIsUnreachable(t *testing.T) {
	factory := func(context.Context, config.Server) (deployment.Runner, error) {
		return nil, errors.New("resolve ssh key: not found")
	}
	c := NewCollector(factory)

	m := c.Collect(context.Background(), config.Server{ID: "s1", Type: config.ServerTypeDocker})
	if m.Reachable {
		t.Fatal("reachable = true despite dial error")
	}
	if !strings.Contains(m.Error, "resolve ssh key") {
		t.Fatalf("error = %q", m.Error)
	}
}

func TestCollectEmptyOutputIsUnreachable(t *testing.T) {
	c := collectorWith(&fakeRunner{out: ""})
	m := c.Collect(context.Background(), config.Server{ID: "s1"})
	if m.Reachable {
		t.Fatal("reachable = true for empty output")
	}
	if !strings.Contains(m.Error, "parse metrics") {
		t.Fatalf("error = %q, want parse error", m.Error)
	}
}

func TestScriptsUseConfiguredServicesAndDisk(t *testing.T) {
	srv := config.Server{
		Type:     config.ServerTypeDocker,
		Services: []string{"docker"},
		DiskPath: "/data",
	}
	linux := linuxScript(srv)
	for _, want := range []string{"systemctl is-active 'docker'", "'/data'", "/proc/stat", "df -kP"} {
		if !strings.Contains(linux, want) {
			t.Errorf("linux script missing %q:\n%s", want, linux)
		}
	}

	iis := config.Server{Type: config.ServerTypeIIS, Services: []string{"W3SVC"}, DiskPath: "D:"}
	win := windowsScript(iis)
	// psQuote escapes the inner quotes: DeviceID='D:' becomes 'DeviceID=''D:'''.
	for _, want := range []string{"Get-Counter", "Get-CimInstance", "Get-Service -Name 'W3SVC'", "DeviceID=''D:''"} {
		if !strings.Contains(win, want) {
			t.Errorf("windows script missing %q:\n%s", want, win)
		}
	}
}
