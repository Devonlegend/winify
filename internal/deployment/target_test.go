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
