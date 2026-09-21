package deployment

import (
	"context"
	"errors"
	"testing"

	"github.com/Devonlegend/winify/internal/config"
)

func TestTargetFactoryLocalServerNeedsNoCredential(t *testing.T) {
	cfg := config.Default()
	sshDial := func(context.Context, config.Server, string) (Runner, error) {
		return nil, errors.New("ssh must not be dialed for a local target")
	}
	factory := NewTargetFactory(cfg, sshDial, nil)

	srv := config.Server{ID: "local", Type: config.ServerTypeWindowsService, Local: true}
	tgt, err := factory(context.Background(), deployJob{server: srv}, fakeSecrets{value: "unused"})
	if err != nil {
		t.Fatalf("factory: %v", err)
	}
	if tgt == nil {
		t.Fatal("nil target")
	}
	if err := tgt.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestRunnerFactoryLocalWindowsUsesLocalRunner(t *testing.T) {
	sshDial := func(context.Context, config.Server, string) (Runner, error) {
		return nil, errors.New("ssh must not be dialed for a local target")
	}
	factory := NewRunnerFactory(sshDial, nil)

	srv := config.Server{ID: "local", Type: config.ServerTypeWindowsService, Local: true}
	runner, err := factory(context.Background(), srv, fakeSecrets{})
	if err != nil {
		t.Fatalf("factory: %v", err)
	}
	if _, ok := runner.(*LocalRunner); !ok {
		t.Fatalf("runner type = %T, want *LocalRunner", runner)
	}
	runner.Close()
}

func TestRunnerFactoryDockerDialsSSH(t *testing.T) {
	called := false
	sshDial := func(_ context.Context, _ config.Server, key string) (Runner, error) {
		called = true
		if key != "key-material" {
			t.Errorf("key = %q, want the resolved secret", key)
		}
		return &fakeRunner{}, nil
	}
	factory := NewRunnerFactory(sshDial, nil)

	srv := config.Server{ID: "web", Type: config.ServerTypeDocker, SSHHost: "10.0.0.1", SSHKeyRef: "vault:sshkey"}
	runner, err := factory(context.Background(), srv, fakeSecrets{value: "key-material"})
	if err != nil {
		t.Fatalf("factory: %v", err)
	}
	if !called {
		t.Fatal("ssh dialer was not called for a docker target")
	}
	if _, ok := runner.(*fakeRunner); !ok {
		t.Fatalf("runner type = %T, want *fakeRunner", runner)
	}
}

func TestTargetFactoryLocalIISNeedsNoCredential(t *testing.T) {
	cfg := config.Default()
	factory := NewTargetFactory(cfg, nil, nil)

	srv := config.Server{ID: "local-iis", Type: config.ServerTypeIIS, Local: true}
	tgt, err := factory(context.Background(), deployJob{server: srv}, fakeSecrets{})
	if err != nil {
		t.Fatalf("factory: %v", err)
	}
	if tgt == nil {
		t.Fatal("nil target")
	}
	tgt.Close()
}
