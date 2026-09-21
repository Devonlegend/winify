package deployment

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestLocalRunnerRunsScript(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("local execution is Windows-only")
	}
	out, err := NewLocalRunner().Run(context.Background(), "Write-Output 'hello'")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(out, "hello") {
		t.Fatalf("output = %q", out)
	}
}

// TestLocalRunnerMultiLineScript guards the bug where a multi-line script block
// fed to `powershell -Command -` silently produced no output.
func TestLocalRunnerMultiLineScript(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("local execution is Windows-only")
	}
	script := "$ErrorActionPreference='Stop'\nif ($true) {\n  'MULTILINE_OK'\n} else {\n  'BAD'\n}\n"
	out, err := NewLocalRunner().Run(context.Background(), script)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(out, "MULTILINE_OK") {
		t.Fatalf("output = %q, want MULTILINE_OK", out)
	}
}

// TestLocalRunnerLargeScript guards against passing the script as a command-line
// argument: Windows rejects command lines above ~32K characters, and the binary
// upload chunks are 100 KB.
func TestLocalRunnerLargeScript(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("local execution is Windows-only")
	}
	big := strings.Repeat("A", 200000)
	script := "$s = '" + big + "'; Write-Output $s.Length"
	out, err := NewLocalRunner().Run(context.Background(), script)
	if err != nil {
		t.Fatalf("Run large script: %v", err)
	}
	if !strings.Contains(out, "200000") {
		t.Fatalf("output = %q, want length 200000", out)
	}
}

// TestEnsureBinaryLocalRunner exercises the chunked binary upload (the path that
// failed when the script was passed as a command-line argument) against the
// local runner.
func TestEnsureBinaryLocalRunner(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("local execution is Windows-only")
	}
	dest := filepath.Join(t.TempDir(), "payload.bin")
	data := bytes.Repeat([]byte{0xAB}, 150000) // > one 100K base64 chunk

	if _, err := EnsureBinary(context.Background(), NewLocalRunner(), dest, data, func(string, ...any) {}); err != nil {
		t.Fatalf("EnsureBinary: %v", err)
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read dest: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Fatalf("uploaded %d bytes, want %d", len(got), len(data))
	}
}
