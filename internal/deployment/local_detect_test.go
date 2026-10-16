//go:build windows

package deployment

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Devonlegend/winify/internal/detect"
)

// TestLocalCloneAndDetect exercises the real path the detect endpoint uses:
// shallow-clone through the local executor, list the tree, read files and run
// the detector. It needs git and PowerShell and is skipped when git is absent.
func TestLocalCloneAndDetect(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not available")
	}
	src := t.TempDir()
	writeTestFile(t, filepath.Join(src, "requirements.txt"), "fastapi\nuvicorn\n")
	writeTestFile(t, filepath.Join(src, "app.py"), "from fastapi import FastAPI\napp = FastAPI()\n")
	runGit(t, src, "init", "-q")
	runGit(t, src, "add", ".")
	runGit(t, src, "-c", "user.email=test@example.com", "-c", "user.name=test", "commit", "-q", "-m", "init")

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	runner := NewLocalRunner()
	dir := filepath.Join(t.TempDir(), "clone")
	if err := CloneShallow(ctx, runner, dir, src, "", nil, nil); err != nil {
		t.Fatalf("CloneShallow: %v", err)
	}
	tree, err := NewRepoTree(ctx, runner, dir)
	if err != nil {
		t.Fatalf("NewRepoTree: %v", err)
	}
	plan := detect.Detect(tree)
	if plan == nil {
		t.Fatal("Detect = nil, want a plan")
	}
	if plan.Language != detect.LanguagePython || plan.Framework != "FastAPI" {
		t.Fatalf("plan = %+v, want python/FastAPI", plan)
	}
	if !strings.Contains(plan.BuildCommand, "pip install -r requirements.txt") {
		t.Errorf("build command = %q", plan.BuildCommand)
	}
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}
