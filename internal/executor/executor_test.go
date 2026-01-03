package executor

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

func TestExecute_SimpleCommand(t *testing.T) {
	exec := New()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var stdout bytes.Buffer
	result, err := exec.Execute(ctx, "echo hello", &stdout, nil)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if result.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0", result.ExitCode)
	}

	got := strings.TrimSpace(stdout.String())
	if got != "hello" {
		t.Errorf("stdout = %q, want %q", got, "hello")
	}
}

func TestExecute_ExitCode(t *testing.T) {
	exec := New()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result, err := exec.Execute(ctx, "exit 42", nil, nil)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if result.ExitCode != 42 {
		t.Errorf("ExitCode = %d, want 42", result.ExitCode)
	}
}

func TestExecute_CaptureStderr(t *testing.T) {
	exec := New()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var stderr bytes.Buffer
	_, err := exec.Execute(ctx, "echo error >&2", nil, &stderr)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	got := strings.TrimSpace(stderr.String())
	if got != "error" {
		t.Errorf("stderr = %q, want %q", got, "error")
	}
}

func TestExecutor_ShellIdentity(t *testing.T) {
	exec := New()
	exec.SetShellName("hash")

	// When executing, $0 should be "hash"
	name := exec.ShellName()
	if name != "hash" {
		t.Errorf("ShellName() = %q, want %q", name, "hash")
	}
}

func TestExecutor_ShellPath(t *testing.T) {
	exec := New()

	// ShellPath should return a path (not empty)
	path := exec.ShellPath()
	if path == "" {
		t.Error("ShellPath() should not be empty")
	}
}

func TestExecutor_DefaultShellName(t *testing.T) {
	exec := New()

	// Default should be "hash"
	name := exec.ShellName()
	if name != "hash" {
		t.Errorf("ShellName() = %q, want %q", name, "hash")
	}
}

func TestExecutor_HashShellEnvVar(t *testing.T) {
	exec := New()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var stdout bytes.Buffer
	result, err := exec.Execute(ctx, "echo $HASH_SHELL", &stdout, nil)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if result.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0", result.ExitCode)
	}

	got := strings.TrimSpace(stdout.String())
	if got != "1" {
		t.Errorf("HASH_SHELL = %q, want %q", got, "1")
	}
}

func TestExecute_CapturedOutput(t *testing.T) {
	exec := New()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var stdout bytes.Buffer
	result, err := exec.Execute(ctx, "echo captured", &stdout, nil)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	// Output should be written to stdout
	got := strings.TrimSpace(stdout.String())
	if got != "captured" {
		t.Errorf("stdout = %q, want %q", got, "captured")
	}

	// Output should also be captured in result
	captured := strings.TrimSpace(result.CapturedOutput)
	if captured != "captured" {
		t.Errorf("CapturedOutput = %q, want %q", captured, "captured")
	}
}

func TestExecute_CapturedOutput_SimplePath(t *testing.T) {
	exec := New()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Passing stderr forces the simple execution path
	var stdout, stderr bytes.Buffer
	result, err := exec.Execute(ctx, "echo simple", &stdout, &stderr)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	// Output should be captured in result
	captured := strings.TrimSpace(result.CapturedOutput)
	if captured != "simple" {
		t.Errorf("CapturedOutput = %q, want %q", captured, "simple")
	}
}

func TestExecute_PipeChain(t *testing.T) {
	exec := New()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var stdout bytes.Buffer
	result, err := exec.Execute(ctx, "echo 'one two three' | tr ' ' '\\n' | wc -l", &stdout, nil)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0", result.ExitCode)
	}
	got := strings.TrimSpace(stdout.String())
	if got != "3" {
		t.Errorf("stdout = %q, want %q", got, "3")
	}
}

func TestExecute_Conditionals(t *testing.T) {
	exec := New()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var stdout bytes.Buffer
	result, err := exec.Execute(ctx, "test -d / && echo exists", &stdout, nil)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0", result.ExitCode)
	}
	got := strings.TrimSpace(stdout.String())
	if got != "exists" {
		t.Errorf("stdout = %q, want %q", got, "exists")
	}
}

func TestExecute_Subshell(t *testing.T) {
	exec := New()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var stdout bytes.Buffer
	result, err := exec.Execute(ctx, "echo $(date +%Y)", &stdout, nil)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0", result.ExitCode)
	}
	got := strings.TrimSpace(stdout.String())
	if got != "2026" {
		t.Errorf("stdout = %q, want %q", got, "2026")
	}
}

func TestExecutor_EnvPersistsAcrossCommands(t *testing.T) {
	exec := New()
	var stdout, stderr bytes.Buffer

	// Set a variable in first command
	_, err := exec.Execute(context.Background(), "export TEST_VAR=hello", &stdout, &stderr)
	if err != nil {
		t.Fatalf("first command failed: %v", err)
	}

	// Read it in second command
	stdout.Reset()
	_, err = exec.Execute(context.Background(), "echo $TEST_VAR", &stdout, &stderr)
	if err != nil {
		t.Fatalf("second command failed: %v", err)
	}

	output := strings.TrimSpace(stdout.String())
	if output != "hello" {
		t.Errorf("expected 'hello', got '%s'", output)
	}
}

func TestExecutor_PATHUpdateAffectsLookup(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a test script in tmpDir
	scriptPath := tmpDir + "/test-script"
	if err := writeExecutableScript(scriptPath, "#!/bin/sh\necho found-it\n"); err != nil {
		t.Fatalf("failed to create script: %v", err)
	}

	exec := New()
	var stdout, stderr bytes.Buffer

	// Add tmpDir to PATH
	pathCmd := "export PATH=" + tmpDir + ":$PATH"
	_, err := exec.Execute(context.Background(), pathCmd, &stdout, &stderr)
	if err != nil {
		t.Fatalf("PATH update failed: %v", err)
	}

	// Now the script should be findable
	stdout.Reset()
	_, err = exec.Execute(context.Background(), "test-script", &stdout, &stderr)
	if err != nil {
		t.Fatalf("script execution failed: %v, stderr: %s", err, stderr.String())
	}

	output := strings.TrimSpace(stdout.String())
	if output != "found-it" {
		t.Errorf("expected 'found-it', got '%s'", output)
	}
}

func TestExecutor_CdPersistsAcrossCommands(t *testing.T) {
	exec := New()
	var stdout, stderr bytes.Buffer

	// Get initial directory
	_, err := exec.Execute(context.Background(), "pwd", &stdout, &stderr)
	if err != nil {
		t.Fatalf("initial pwd failed: %v", err)
	}
	initialDir := strings.TrimSpace(stdout.String())

	// Change to temp directory
	tmpDir := t.TempDir()
	stdout.Reset()
	_, err = exec.Execute(context.Background(), "cd "+tmpDir, &stdout, &stderr)
	if err != nil {
		t.Fatalf("cd failed: %v", err)
	}

	// Verify we're in new directory
	stdout.Reset()
	_, err = exec.Execute(context.Background(), "pwd", &stdout, &stderr)
	if err != nil {
		t.Fatalf("second pwd failed: %v", err)
	}

	newDir := strings.TrimSpace(stdout.String())
	if newDir == initialDir {
		t.Errorf("cd did not persist: still in %s", initialDir)
	}
}

func writeExecutableScript(path, content string) error {
	return os.WriteFile(path, []byte(content), 0755)
}
