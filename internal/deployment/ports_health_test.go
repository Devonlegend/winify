package deployment

import (
	"testing"

	"github.com/Devonlegend/winify/internal/config"
)

func TestComposePorts(t *testing.T) {
	cases := []struct {
		name string
		p    config.Project
		want []string
	}{
		{"explicit mapping", config.Project{PortsMappings: []string{"8080:3000"}}, []string{"8080:3000"}},
		{"exposes 1:1", config.Project{PortsExposes: 3000}, []string{"3000:3000"}},
		{"legacy port", config.Project{Port: 9000}, []string{"9000:9000"}},
		{"none", config.Project{}, nil},
	}
	for _, c := range cases {
		got := composePorts(c.p)
		if len(got) != len(c.want) {
			t.Fatalf("%s: composePorts = %v, want %v", c.name, got, c.want)
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Fatalf("%s: composePorts = %v, want %v", c.name, got, c.want)
			}
		}
	}
}

func TestHealthPlanFor(t *testing.T) {
	cfg := config.Default()
	cfg.Deploy.HealthTimeoutSeconds = 60
	cfg.Deploy.HealthIntervalSeconds = 3

	// Per-project values win.
	plan := healthPlanFor(cfg, config.Project{
		HealthIntervalSeconds: 5, HealthTimeoutSeconds: 2,
		HealthRetries: 4, HealthStartPeriodSeconds: 10,
	})
	if plan.interval != 5 || plan.timeout != 2 || plan.attempts != 4 || plan.startPeriod != 10 {
		t.Fatalf("explicit plan = %+v", plan)
	}

	// Defaults fall back to config; attempts derive from the overall timeout.
	plan = healthPlanFor(cfg, config.Project{})
	if plan.interval != 3 || plan.attempts != 20 || plan.timeout != 5 || plan.startPeriod != 0 {
		t.Fatalf("default plan = %+v", plan)
	}
}

func TestExpandSharedVars(t *testing.T) {
	vars := map[string]string{
		"project.DB":         "postgres://db",
		"environment.TOKEN":  "s3cr3t",
		"project.MULTI_WORD": "ok",
	}
	out, err := expandSharedVars(map[string]string{
		"DATABASE_URL": "{{project.DB}}",
		"HEADER":       "Bearer {{ environment.TOKEN }}",
	}, vars)
	if err != nil {
		t.Fatalf("expandSharedVars: %v", err)
	}
	if out["DATABASE_URL"] != "postgres://db" || out["HEADER"] != "Bearer s3cr3t" {
		t.Fatalf("expanded = %v", out)
	}

	if _, err := expandSharedVars(map[string]string{"A": "{{project.MISSING}}"}, vars); err == nil {
		t.Fatal("unknown reference did not error")
	}
}
