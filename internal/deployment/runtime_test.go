package deployment

import (
	"context"
	"strings"
	"testing"
)

func TestEnsureRuntimeNoOpWhenEmpty(t *testing.T) {
	runner := &fakeRunner{}
	if err := ensureRuntime(context.Background(), runner, "", noopLogf); err != nil {
		t.Fatalf("ensureRuntime: %v", err)
	}
	if len(runner.commands) != 0 {
		t.Fatalf("commands ran for an empty runtime: %v", runner.commands)
	}
}

func TestEnsureRuntimePresentSkipsInstall(t *testing.T) {
	runner := &fakeRunner{outputs: func(string) string { return "present\n" }}
	if err := ensureRuntime(context.Background(), runner, "node", noopLogf); err != nil {
		t.Fatalf("ensureRuntime: %v", err)
	}
	if strings.Contains(runner.joined(), "winget") {
		t.Errorf("install ran even though the runtime is present:\n%s", runner.joined())
	}
}

func TestEnsureRuntimeInstallsWhenMissing(t *testing.T) {
	runner := &fakeRunner{outputs: func(string) string { return "missing\n" }}
	if err := ensureRuntime(context.Background(), runner, "go", noopLogf); err != nil {
		t.Fatalf("ensureRuntime: %v", err)
	}
	cmds := runner.joined()
	for _, want := range []string{"winget install --id 'GoLang.Go'", "choco install 'golang'"} {
		if !strings.Contains(cmds, want) {
			t.Errorf("install script missing %q:\n%s", want, cmds)
		}
	}
}

func TestEnsureRuntimeUnknownFails(t *testing.T) {
	runner := &fakeRunner{}
	if err := ensureRuntime(context.Background(), runner, "ruby", noopLogf); err == nil {
		t.Fatal("ensureRuntime accepted an unknown runtime")
	}
	if len(runner.commands) != 0 {
		t.Fatalf("commands ran for an unknown runtime: %v", runner.commands)
	}
}

func TestWindowsServiceEnsuresRuntimeBeforeBuild(t *testing.T) {
	runner := &fakeRunner{outputs: func(cmd string) string {
		switch {
		case strings.Contains(cmd, "$stamp = Get-Date"):
			return backupPath + "\n"
		case strings.Contains(cmd, "Get-Command 'python'"):
			return "present\n"
		}
		return ""
	}}
	project := winsvcProject()
	project.Runtime = "python"
	project.ServiceBuildCommand = "python -m venv .venv"
	tgt := winsvcTargetWith(runner)

	if _, err := tgt.Deploy(context.Background(), deployJob{project: project, commit: "abc1234"}, noopLogf); err != nil {
		t.Fatalf("Deploy: %v", err)
	}
	probe := runner.indexOf("Get-Command 'python'")
	build := runner.indexOf("python -m venv .venv")
	if probe < 0 || build < 0 {
		t.Fatalf("runtime probe (%d) or build (%d) missing:\n%s", probe, build, runner.joined())
	}
	if probe > build {
		t.Errorf("runtime probe ran after the build (probe %d, build %d)", probe, build)
	}
}
