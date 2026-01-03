//go:build integration

package executor

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"
)

func TestExecutor_Integration_ExecShellUsed(t *testing.T) {
	origExecShell := os.Getenv("HASH_EXEC_SHELL")
	defer os.Setenv("HASH_EXEC_SHELL", origExecShell)

	// Force use of /bin/sh
	os.Setenv("HASH_EXEC_SHELL", "/bin/sh")

	exec := New()
	var stdout, stderr bytes.Buffer

	// Pass stderr to force non-PTY path (avoids race with async io.Copy)
	result, err := exec.Execute(context.Background(), "echo test", &stdout, &stderr)
	if err != nil {
		t.Fatalf("execution failed: %v", err)
	}

	if result.ExitCode != 0 {
		t.Errorf("expected exit code 0, got %d", result.ExitCode)
	}

	output := strings.TrimSpace(stdout.String())
	if output != "test" {
		t.Errorf("expected 'test', got '%s'", output)
	}
}

func TestExecutor_Integration_ChildEnvHasHashShell(t *testing.T) {
	exec := New()
	var stdout, stderr bytes.Buffer

	// Pass stderr to force non-PTY path (avoids race with async io.Copy)
	// Child process should see SHELL pointing to the executor's shellPath
	_, err := exec.Execute(context.Background(), "echo $SHELL", &stdout, &stderr)
	if err != nil {
		t.Fatalf("execution failed: %v", err)
	}

	output := strings.TrimSpace(stdout.String())
	// SHELL should be set to the executor's shell path (os.Executable())
	// During tests this is the test binary; in production it's "hash"
	expectedPath := exec.ShellPath()
	if output != expectedPath {
		t.Errorf("expected SHELL to be '%s', got '%s'", expectedPath, output)
	}
}
