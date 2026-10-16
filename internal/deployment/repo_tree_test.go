package deployment

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"
)

func TestRepoTreeListsAndReads(t *testing.T) {
	runner := &fakeRunner{outputs: func(cmd string) string {
		switch {
		case strings.Contains(cmd, "Get-ChildItem"):
			return "go.mod\nmain.go\n"
		case strings.Contains(cmd, "ToBase64String"):
			return base64.StdEncoding.EncodeToString([]byte("package main\n"))
		}
		return ""
	}}
	tree, err := NewRepoTree(context.Background(), runner, `C:\control-center\detect\local`)
	if err != nil {
		t.Fatalf("NewRepoTree: %v", err)
	}
	got := tree.List()
	if len(got) != 2 || got[0] != "go.mod" || got[1] != "main.go" {
		t.Fatalf("List = %v, want [go.mod main.go]", got)
	}
	data, err := tree.Read("main.go")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if string(data) != "package main\n" {
		t.Fatalf("Read = %q", data)
	}
}

func TestRepoTreeListErrorIsReturned(t *testing.T) {
	runner := &fakeRunner{failOn: "Get-ChildItem"}
	if _, err := NewRepoTree(context.Background(), runner, `C:\x`); err == nil {
		t.Fatal("NewRepoTree succeeded, want a listing error")
	}
}

func TestCloneShallowScript(t *testing.T) {
	runner := &fakeRunner{}
	if err := CloneShallow(context.Background(), runner, `C:\control-center\detect\local`, "https://example.com/app.git", "main", nil, nil); err != nil {
		t.Fatalf("CloneShallow: %v", err)
	}
	cmd := runner.joined()
	for _, want := range []string{"git clone --depth 1 --branch 'main'", "Remove-Item", "https://example.com/app.git"} {
		if !strings.Contains(cmd, want) {
			t.Errorf("clone script missing %q:\n%s", want, cmd)
		}
	}
}

func TestCloneShallowWithoutBranch(t *testing.T) {
	runner := &fakeRunner{}
	if err := CloneShallow(context.Background(), runner, `C:\x`, "url", "", nil, nil); err != nil {
		t.Fatalf("CloneShallow: %v", err)
	}
	if strings.Contains(runner.joined(), "--branch") {
		t.Errorf("empty branch should omit --branch:\n%s", runner.joined())
	}
}
