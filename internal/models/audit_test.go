package models

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestRemoteCommandRoundTrip(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := Migrate(db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	store := NewStore(db)
	ctx := context.Background()

	rc := RemoteCommand{
		ServerID: "server-002", ServerType: "docker", Action: "deploy",
		DeploymentID: 3, Command: "docker build -t app .", ExecutedAt: time.Now(),
	}
	if err := store.InsertRemoteCommand(ctx, rc); err != nil {
		t.Fatalf("InsertRemoteCommand: %v", err)
	}

	list, err := store.ListRemoteCommands(ctx, 10)
	if err != nil {
		t.Fatalf("ListRemoteCommands: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("got %d rows, want 1", len(list))
	}
	if list[0].Command != "docker build -t app ." || list[0].DeploymentID != 3 || list[0].Action != "deploy" {
		t.Fatalf("row = %+v", list[0])
	}
}
