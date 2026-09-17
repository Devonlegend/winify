package bootstrap

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fakeRunner struct {
	commands []string
	outputs  func(cmd string) string
	failOn   string
}

func (f *fakeRunner) Run(_ context.Context, cmd string) (string, error) {
	f.commands = append(f.commands, cmd)
	if f.failOn != "" && strings.Contains(cmd, f.failOn) {
		return "boom", errors.New("command failed")
	}
	if f.outputs != nil {
		return f.outputs(cmd), nil
	}
	return "", nil
}

func (f *fakeRunner) Close() error { return nil }

type fakeMeta struct{ m map[string]string }

func newFakeMeta() *fakeMeta { return &fakeMeta{m: map[string]string{}} }

func (f *fakeMeta) GetMeta(_ context.Context, key string) (string, error) { return f.m[key], nil }
func (f *fakeMeta) SetMeta(_ context.Context, key, value string) error {
	f.m[key] = value
	return nil
}

func testOptions(runner *fakeRunner, meta *fakeMeta) Options {
	return Options{
		Paths:       DefaultPaths(`C:\ProgramData\winify`),
		EnableWinRM: true,
		Meta:        meta,
		Runner:      runner,
	}
}

func TestRunAppliesPendingSteps(t *testing.T) {
	runner := &fakeRunner{outputs: func(cmd string) string {
		if strings.Contains(cmd, "Get-Service -Name WinRM") {
			return "pending"
		}
		if strings.Contains(cmd, "Test-Path") {
			return "pending"
		}
		return ""
	}}
	meta := newFakeMeta()
	b := New(testOptions(runner, meta))

	results, err := b.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("got %d results, want 3 (dirs, nssm, winrm)", len(results))
	}
	for _, r := range results {
		if r.Status != StatusApplied {
			t.Errorf("step %s = %s, want applied", r.Name, r.Status)
		}
	}
	if meta.m["bootstrap.dirs"] != "done" || meta.m["bootstrap.winrm"] != "done" {
		t.Fatalf("state not recorded: %v", meta.m)
	}
	if !strings.Contains(strings.Join(runner.commands, "\n"), "New-Item -ItemType Directory") {
		t.Error("dirs step did not create directories")
	}
}

func TestRunSkipsDoneSteps(t *testing.T) {
	runner := &fakeRunner{outputs: func(cmd string) string {
		if strings.Contains(cmd, "Get-Service -Name WinRM") || strings.Contains(cmd, "Test-Path") {
			return "done"
		}
		return ""
	}}
	meta := newFakeMeta()
	b := New(testOptions(runner, meta))

	results, err := b.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, r := range results {
		if r.Status != StatusDone {
			t.Errorf("step %s = %s, want done", r.Name, r.Status)
		}
	}
	// One Check per step, and no Apply commands.
	if len(runner.commands) != 3 {
		t.Fatalf("ran %d commands, want 3 checks only:\n%s", len(runner.commands), strings.Join(runner.commands, "\n"))
	}
}

func TestRunStopsOnFailureAndRecordsError(t *testing.T) {
	runner := &fakeRunner{
		outputs: func(cmd string) string { return "pending" },
		failOn:  "Enable-PSRemoting",
	}
	meta := newFakeMeta()
	b := New(testOptions(runner, meta))

	_, err := b.Run(context.Background())
	if err == nil {
		t.Fatal("Run succeeded despite a failing step")
	}
	if !strings.HasPrefix(meta.m["bootstrap.winrm"], "error:") {
		t.Fatalf("winrm error not recorded: %q", meta.m["bootstrap.winrm"])
	}
}

func TestDryRunAppliesNothing(t *testing.T) {
	runner := &fakeRunner{outputs: func(cmd string) string { return "pending" }}
	meta := newFakeMeta()
	opts := testOptions(runner, meta)
	opts.DryRun = true
	b := New(opts)

	results, err := b.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, r := range results {
		if r.Status != StatusPending {
			t.Errorf("step %s = %s, want pending", r.Name, r.Status)
		}
	}
	if len(meta.m) != 0 {
		t.Fatalf("dry-run recorded state: %v", meta.m)
	}
}

func TestStatusReadsRecordedState(t *testing.T) {
	meta := newFakeMeta()
	meta.m["bootstrap.dirs"] = "done"
	meta.m["bootstrap.nssm"] = "error: hash mismatch"
	b := New(testOptions(&fakeRunner{}, meta))

	statuses, err := b.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if len(statuses) != 3 {
		t.Fatalf("got %d statuses, want 3", len(statuses))
	}
	if !statuses[0].Done {
		t.Errorf("dirs not done: %+v", statuses[0])
	}
	if statuses[1].Error != "hash mismatch" {
		t.Errorf("nssm error = %q", statuses[1].Error)
	}
	if done, _ := b.Complete(context.Background()); done {
		t.Error("Complete = true with an errored step")
	}
}

func TestSelfServiceStep(t *testing.T) {
	runner := &fakeRunner{outputs: func(cmd string) string {
		if strings.Contains(cmd, "Win32_Service") {
			return "pending"
		}
		return ""
	}}
	step := selfServiceStep{
		name:   "winify",
		exe:    `C:\ProgramData\winify\winify.exe`,
		config: `C:\ProgramData\winify\config.yaml`,
	}

	done, err := step.Check(context.Background(), runner)
	if err != nil || done {
		t.Fatalf("Check = %v, %v; want false, nil", done, err)
	}
	if err := step.Apply(context.Background(), runner); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	joined := strings.Join(runner.commands, "\n")
	for _, want := range []string{"New-Service -Name 'winify'", "StartupType Automatic", "sc.exe failure 'winify'"} {
		if !strings.Contains(joined, want) {
			t.Errorf("self-service script missing %q:\n%s", want, joined)
		}
	}
}

func TestSelfServiceIncludedWhenConfigured(t *testing.T) {
	opts := testOptions(&fakeRunner{}, newFakeMeta())
	opts.ServiceName = "winify"
	opts.ExePath = `C:\ProgramData\winify\winify.exe`
	opts.ConfigPath = `C:\ProgramData\winify\config.yaml`

	statuses, err := New(opts).Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	found := false
	for _, s := range statuses {
		if s.Name == "self-service" {
			found = true
		}
	}
	if !found {
		t.Fatalf("self-service step not included: %+v", statuses)
	}
}

func TestFuncStepRunsClosures(t *testing.T) {
	ran := false
	step := funcStep{
		name:  "local-target",
		check: func(context.Context) (bool, error) { return false, nil },
		apply: func(context.Context) error { ran = true; return nil },
	}
	done, err := step.Check(context.Background(), &fakeRunner{})
	if err != nil || done {
		t.Fatalf("Check = %v, %v; want false, nil", done, err)
	}
	if err := step.Apply(context.Background(), &fakeRunner{}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !ran {
		t.Fatal("apply closure did not run")
	}
}

func TestLocalTargetStepIncluded(t *testing.T) {
	opts := testOptions(&fakeRunner{}, newFakeMeta())
	opts.TargetCheck = func(context.Context) (bool, error) { return false, nil }
	opts.TargetApply = func(context.Context) error { return nil }

	statuses, err := New(opts).Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	for _, s := range statuses {
		if s.Name == "local-target" {
			return
		}
	}
	t.Fatalf("local-target step not included: %+v", statuses)
}

func TestCaddyStepFromLocalSource(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "caddy.exe")
	data := []byte("fake-caddy-binary")
	if err := os.WriteFile(src, data, 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	hashCalls := 0
	runner := &fakeRunner{outputs: func(cmd string) string {
		if strings.Contains(cmd, "Get-FileHash") {
			hashCalls++
			if hashCalls == 1 {
				return ""
			}
			return sha256hex(data) + "\n"
		}
		return ""
	}}
	step := caddyStep{
		path:     `C:\ProgramData\winify\tools\caddy.exe`,
		nssmPath: `C:\ProgramData\winify\tools\nssm.exe`,
		dir:      `C:\ProgramData\winify\caddy`,
		logDir:   `C:\ProgramData\winify\logs`,
		admin:    "127.0.0.1:2019",
		source:   src,
	}

	if err := step.Apply(context.Background(), runner); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	joined := strings.Join(runner.commands, "\n")
	for _, want := range []string{"FromBase64String", "New-Item", "$nssm install 'winify-caddy'", "run --config"} {
		if !strings.Contains(joined, want) {
			t.Errorf("caddy script missing %q:\n%s", want, joined)
		}
	}
}

func TestCaddyStepRequiresSource(t *testing.T) {
	step := caddyStep{path: `C:\x\caddy.exe`, nssmPath: `C:\x\nssm.exe`}
	if err := step.Apply(context.Background(), &fakeRunner{}); err == nil {
		t.Fatal("Apply succeeded without a source or url")
	}
}

func TestFirewallStep(t *testing.T) {
	runner := &fakeRunner{outputs: func(cmd string) string {
		if strings.Contains(cmd, "Get-NetFirewallRule") {
			return "pending"
		}
		return ""
	}}
	step := firewallStep{ports: []int{80, 443}}

	done, err := step.Check(context.Background(), runner)
	if err != nil || done {
		t.Fatalf("Check = %v, %v; want false, nil", done, err)
	}
	if err := step.Apply(context.Background(), runner); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	joined := strings.Join(runner.commands, "\n")
	if !strings.Contains(joined, "New-NetFirewallRule") || !strings.Contains(joined, "-LocalPort 443") {
		t.Fatalf("firewall script incomplete:\n%s", joined)
	}
}

func TestCaddyStepsIncludedWhenEnabled(t *testing.T) {
	opts := testOptions(&fakeRunner{}, newFakeMeta())
	opts.CaddyEnabled = true
	opts.CaddySource = "caddy.exe"

	statuses, err := New(opts).Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	names := map[string]bool{}
	for _, s := range statuses {
		names[s.Name] = true
	}
	if !names["caddy"] || !names["firewall"] {
		t.Fatalf("caddy/firewall steps not included: %+v", statuses)
	}
}

func TestNSSMStepHashAware(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "nssm.exe")
	data := []byte("fake-nssm")
	if err := os.WriteFile(src, data, 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	sum := sha256.Sum256(data)
	want := hex.EncodeToString(sum[:])

	runner := &fakeRunner{outputs: func(cmd string) string {
		if strings.Contains(cmd, "Get-FileHash") {
			return want + "\n"
		}
		return ""
	}}
	step := nssmStep{path: `C:\ProgramData\winify\tools\nssm.exe`, source: src}

	done, err := step.Check(context.Background(), runner)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !done {
		t.Fatal("Check = false when the target hash matches the source")
	}
}
