package executor

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
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
	// Save and restore process CWD since executor syncs shell PWD to process
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get original dir: %v", err)
	}
	defer os.Chdir(origDir)

	exec := New()
	var stdout, stderr bytes.Buffer

	// Get initial directory
	_, err = exec.Execute(context.Background(), "pwd", &stdout, &stderr)
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

func TestExecutor_SyncRunnerDir(t *testing.T) {
	exec := New()
	var stdout, stderr bytes.Buffer

	// Initialize the runner by running a command
	_, err := exec.Execute(context.Background(), "pwd", &stdout, &stderr)
	if err != nil {
		t.Fatalf("initial pwd failed: %v", err)
	}
	initialDir := strings.TrimSpace(stdout.String())

	// Change directory via os.Chdir (simulating builtin cd)
	tmpDir := t.TempDir()
	// Resolve symlinks for comparison (macOS has /var -> /private/var)
	tmpDirResolved, _ := filepath.EvalSymlinks(tmpDir)
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("os.Chdir failed: %v", err)
	}
	defer os.Chdir(initialDir) // Restore

	// Without SyncRunnerDir, the runner would still be in initialDir
	// Call SyncRunnerDir to sync the runner's directory
	exec.SyncRunnerDir()

	// Now pwd should show the new directory
	stdout.Reset()
	_, err = exec.Execute(context.Background(), "pwd", &stdout, &stderr)
	if err != nil {
		t.Fatalf("second pwd failed: %v", err)
	}

	newDir := strings.TrimSpace(stdout.String())
	newDirResolved, _ := filepath.EvalSymlinks(newDir)
	if newDirResolved != tmpDirResolved {
		t.Errorf("SyncRunnerDir did not update runner dir: got %q, want %q", newDir, tmpDir)
	}
}

func TestExecutor_FunctionPersistsAcrossCommands(t *testing.T) {
	exec := New()
	ctx := context.Background()
	var stdout bytes.Buffer

	// First execution: define a function
	_, err := exec.Execute(ctx, `myfunc() { echo "function works!"; }`, &stdout, nil)
	if err != nil {
		t.Fatalf("Error defining function: %v", err)
	}
	stdout.Reset()

	// Second execution: call the function (should persist from first execution)
	_, err = exec.Execute(ctx, `myfunc`, &stdout, nil)
	if err != nil {
		t.Fatalf("Error calling function: %v", err)
	}

	result := strings.TrimSpace(stdout.String())
	if result != "function works!" {
		t.Errorf("Expected 'function works!', got: %q", result)
	}
}

func TestExecutor_FunctionWithArgs(t *testing.T) {
	exec := New()
	ctx := context.Background()
	var stdout bytes.Buffer

	// Define function with arguments
	_, err := exec.Execute(ctx, `greet() { echo "Hello, $1!"; }`, &stdout, nil)
	if err != nil {
		t.Fatalf("Error defining function: %v", err)
	}
	stdout.Reset()

	// Call with argument
	_, err = exec.Execute(ctx, `greet "World"`, &stdout, nil)
	if err != nil {
		t.Fatalf("Error calling function: %v", err)
	}

	result := strings.TrimSpace(stdout.String())
	if result != "Hello, World!" {
		t.Errorf("Expected 'Hello, World!', got: %q", result)
	}
}

func TestExecutor_Reset(t *testing.T) {
	exec := New()
	ctx := context.Background()
	var stdout bytes.Buffer

	// Define a function
	_, err := exec.Execute(ctx, `testfunc() { echo "exists"; }`, &stdout, nil)
	if err != nil {
		t.Fatalf("Error defining function: %v", err)
	}
	stdout.Reset()

	// Reset the executor
	exec.Reset()

	// Try to call the function - should fail now
	result, _ := exec.Execute(ctx, `testfunc`, &stdout, nil)
	if result.ExitCode == 0 {
		t.Error("Expected function to not exist after Reset()")
	}
}

func TestExecutor_SourceWithBashSyntax(t *testing.T) {
	exec := New()
	ctx := context.Background()
	var stdout, stderr bytes.Buffer

	// Create a script file with bash syntax (== in [[ ]])
	tmpDir := t.TempDir()
	scriptPath := filepath.Join(tmpDir, "test.sh")
	scriptContent := `# Test script with bash syntax
export TEST_VAR="hello"
[[ "$TEST_VAR" == "hello" ]] && echo "comparison works"
if [[ -n "$TEST_VAR" ]]; then
  echo "test var is set"
fi
`
	if err := os.WriteFile(scriptPath, []byte(scriptContent), 0644); err != nil {
		t.Fatalf("failed to create test script: %v", err)
	}

	// Source the file - should work with LangBash parsing
	_, err := exec.Execute(ctx, "source "+scriptPath, &stdout, &stderr)
	if err != nil {
		t.Fatalf("source failed: %v, stderr: %s", err, stderr.String())
	}

	output := stdout.String()
	if !strings.Contains(output, "comparison works") {
		t.Errorf("expected 'comparison works' in output, got: %q", output)
	}
	if !strings.Contains(output, "test var is set") {
		t.Errorf("expected 'test var is set' in output, got: %q", output)
	}
}

func TestExecutor_EvalWithBashSyntax(t *testing.T) {
	exec := New()
	ctx := context.Background()
	var stdout, stderr bytes.Buffer

	// Set up a variable
	_, err := exec.Execute(ctx, `export FOO="bar"`, &stdout, &stderr)
	if err != nil {
		t.Fatalf("export failed: %v", err)
	}
	stdout.Reset()

	// Use eval with bash syntax (== comparison)
	_, err = exec.Execute(ctx, `eval '[[ "$FOO" == "bar" ]] && echo "eval works"'`, &stdout, &stderr)
	if err != nil {
		t.Fatalf("eval failed: %v, stderr: %s", err, stderr.String())
	}

	output := strings.TrimSpace(stdout.String())
	if output != "eval works" {
		t.Errorf("expected 'eval works', got: %q", output)
	}
}

func TestExecutor_SourcePersistsState(t *testing.T) {
	exec := New()
	ctx := context.Background()
	var stdout, stderr bytes.Buffer

	// Create a script that exports a variable and defines a function
	tmpDir := t.TempDir()
	scriptPath := filepath.Join(tmpDir, "setup.sh")
	scriptContent := `export SOURCED_VAR="from-script"
myalias() { echo "alias output"; }
`
	if err := os.WriteFile(scriptPath, []byte(scriptContent), 0644); err != nil {
		t.Fatalf("failed to create test script: %v", err)
	}

	// Source the file
	_, err := exec.Execute(ctx, "source "+scriptPath, &stdout, &stderr)
	if err != nil {
		t.Fatalf("source failed: %v", err)
	}

	// Check that the variable persists
	stdout.Reset()
	_, err = exec.Execute(ctx, `echo "$SOURCED_VAR"`, &stdout, &stderr)
	if err != nil {
		t.Fatalf("echo failed: %v", err)
	}

	output := strings.TrimSpace(stdout.String())
	if output != "from-script" {
		t.Errorf("expected 'from-script', got: %q", output)
	}

	// Check that the function persists
	stdout.Reset()
	_, err = exec.Execute(ctx, "myalias", &stdout, &stderr)
	if err != nil {
		t.Fatalf("function call failed: %v", err)
	}

	output = strings.TrimSpace(stdout.String())
	if output != "alias output" {
		t.Errorf("expected 'alias output', got: %q", output)
	}
}

func TestExecutor_NestedSourceWithBashSyntax(t *testing.T) {
	exec := New()
	ctx := context.Background()
	var stdout, stderr bytes.Buffer

	// Create two script files - outer sources inner
	tmpDir := t.TempDir()
	innerPath := filepath.Join(tmpDir, "inner.sh")
	outerPath := filepath.Join(tmpDir, "outer.sh")

	// Inner script has bash syntax that would fail with POSIX parser
	innerContent := `# Inner script with bash syntax
export INNER_VAR="hello"
[[ "$INNER_VAR" == "hello" ]] && echo "inner comparison works"
`
	if err := os.WriteFile(innerPath, []byte(innerContent), 0644); err != nil {
		t.Fatalf("failed to create inner script: %v", err)
	}

	// Outer script sources inner - this tests nested source handling
	outerContent := `# Outer script
source ` + innerPath + `
[[ -n "$INNER_VAR" ]] && echo "outer sees inner var"
`
	if err := os.WriteFile(outerPath, []byte(outerContent), 0644); err != nil {
		t.Fatalf("failed to create outer script: %v", err)
	}

	// Source the outer file - should work with nested source
	_, err := exec.Execute(ctx, "source "+outerPath, &stdout, &stderr)
	if err != nil {
		t.Fatalf("source failed: %v, stderr: %s", err, stderr.String())
	}

	output := stdout.String()
	if !strings.Contains(output, "inner comparison works") {
		t.Errorf("expected 'inner comparison works' in output, got: %q", output)
	}
	if !strings.Contains(output, "outer sees inner var") {
		t.Errorf("expected 'outer sees inner var' in output, got: %q", output)
	}
}

func TestExecutor_EvalInsideScript(t *testing.T) {
	exec := New()
	ctx := context.Background()
	var stdout, stderr bytes.Buffer

	// Create a script that uses eval with bash syntax
	tmpDir := t.TempDir()
	scriptPath := filepath.Join(tmpDir, "test.sh")
	scriptContent := `# Script that uses eval with bash syntax
export TEST_VAR="bar"
eval '[[ "$TEST_VAR" == "bar" ]] && echo "eval in script works"'
`
	if err := os.WriteFile(scriptPath, []byte(scriptContent), 0644); err != nil {
		t.Fatalf("failed to create test script: %v", err)
	}

	// Source the file - eval inside should work
	_, err := exec.Execute(ctx, "source "+scriptPath, &stdout, &stderr)
	if err != nil {
		t.Fatalf("source failed: %v, stderr: %s", err, stderr.String())
	}

	output := strings.TrimSpace(stdout.String())
	if output != "eval in script works" {
		t.Errorf("expected 'eval in script works', got: %q", output)
	}
}

// Tests for graceful degradation - zsh-specific syntax should be silently skipped

func TestExecutor_SourceZshSpecificFile_GracefulSkip(t *testing.T) {
	exec := New()
	ctx := context.Background()
	var stdout, stderr bytes.Buffer

	tmpDir := t.TempDir()

	// Create a file with zsh-specific parameter expansion syntax
	// This mimics what's in zsh-autosuggestions and bun completions
	zshFile := filepath.Join(tmpDir, "zsh-specific.zsh")
	zshContent := `# Zsh-specific syntax that should fail to parse
typeset -g var
(( ${+commands[foo]} )) && echo "has foo"
${(%):-%n}
`
	if err := os.WriteFile(zshFile, []byte(zshContent), 0644); err != nil {
		t.Fatalf("failed to create zsh file: %v", err)
	}

	// Source should silently skip unparseable content (no error)
	_, err := exec.Execute(ctx, "source "+zshFile, &stdout, &stderr)
	if err != nil {
		t.Fatalf("source should not error on unparseable zsh file: %v", err)
	}

	// Stderr should be empty (graceful skip, no error messages)
	if stderr.String() != "" {
		t.Errorf("expected no stderr output, got: %q", stderr.String())
	}
}

func TestExecutor_EvalZshHook_GracefulSkip(t *testing.T) {
	exec := New()
	ctx := context.Background()
	var stdout, stderr bytes.Buffer

	// Simulate direnv hook zsh output (contains zsh-specific syntax)
	// The actual output contains things like: (( ${+commands[direnv]} ))
	zshHookContent := `_direnv_hook() {
  (( ${+commands[direnv]} )) || return
  eval "$(direnv export zsh)"
}
typeset -ag precmd_functions
precmd_functions=(_direnv_hook $precmd_functions)
`

	// Eval should silently skip unparseable content
	_, err := exec.Execute(ctx, `eval '`+zshHookContent+`'`, &stdout, &stderr)
	if err != nil {
		t.Fatalf("eval should not error on unparseable zsh content: %v", err)
	}

	// Stderr should be empty
	if stderr.String() != "" {
		t.Errorf("expected no stderr output, got: %q", stderr.String())
	}
}

func TestExecutor_EvalZoxideHook_GracefulSkip(t *testing.T) {
	exec := New()
	ctx := context.Background()
	var stdout, stderr bytes.Buffer

	// Simulate zoxide init zsh output
	zoxideContent := `__zoxide_hook() {
  (( ${+__zoxide_hooked} )) && return
  __zoxide_hooked=1
}
`

	_, err := exec.Execute(ctx, `eval '`+zoxideContent+`'`, &stdout, &stderr)
	if err != nil {
		t.Fatalf("eval should not error on zoxide zsh hook: %v", err)
	}

	if stderr.String() != "" {
		t.Errorf("expected no stderr output, got: %q", stderr.String())
	}
}

func TestExecutor_SourceMixedContent_PartialExecution(t *testing.T) {
	exec := New()
	ctx := context.Background()
	var stdout, stderr bytes.Buffer

	tmpDir := t.TempDir()

	// Create a file that sources both valid bash and invalid zsh files
	validFile := filepath.Join(tmpDir, "valid.sh")
	validContent := `export VALID_VAR="from-valid"
echo "valid file loaded"
`
	if err := os.WriteFile(validFile, []byte(validContent), 0644); err != nil {
		t.Fatalf("failed to create valid file: %v", err)
	}

	invalidFile := filepath.Join(tmpDir, "invalid.zsh")
	invalidContent := `# This will fail to parse
(( ${+foo} )) && bar
`
	if err := os.WriteFile(invalidFile, []byte(invalidContent), 0644); err != nil {
		t.Fatalf("failed to create invalid file: %v", err)
	}

	// Source the valid file - should work
	_, err := exec.Execute(ctx, "source "+validFile, &stdout, &stderr)
	if err != nil {
		t.Fatalf("source valid file failed: %v", err)
	}

	if !strings.Contains(stdout.String(), "valid file loaded") {
		t.Errorf("expected 'valid file loaded' in output")
	}

	stdout.Reset()
	stderr.Reset()

	// Source the invalid file - should silently skip
	_, err = exec.Execute(ctx, "source "+invalidFile, &stdout, &stderr)
	if err != nil {
		t.Fatalf("source invalid file should not error: %v", err)
	}

	// The valid var should still be set from before
	stdout.Reset()
	_, err = exec.Execute(ctx, `echo "$VALID_VAR"`, &stdout, &stderr)
	if err != nil {
		t.Fatalf("echo failed: %v", err)
	}

	if strings.TrimSpace(stdout.String()) != "from-valid" {
		t.Errorf("expected VALID_VAR to be 'from-valid', got: %q", stdout.String())
	}
}

func TestExecutor_EvalValidBashStillWorks(t *testing.T) {
	exec := New()
	ctx := context.Background()
	var stdout, stderr bytes.Buffer

	// Make sure valid bash eval still works after our changes
	_, err := exec.Execute(ctx, `eval 'echo "eval works"'`, &stdout, &stderr)
	if err != nil {
		t.Fatalf("eval failed: %v", err)
	}

	if strings.TrimSpace(stdout.String()) != "eval works" {
		t.Errorf("expected 'eval works', got: %q", stdout.String())
	}
}

func TestExecutor_SourceValidBashStillWorks(t *testing.T) {
	exec := New()
	ctx := context.Background()
	var stdout, stderr bytes.Buffer

	tmpDir := t.TempDir()
	scriptPath := filepath.Join(tmpDir, "valid.sh")
	scriptContent := `export TEST_FROM_SOURCE="hello"
echo "sourced successfully"
`
	if err := os.WriteFile(scriptPath, []byte(scriptContent), 0644); err != nil {
		t.Fatalf("failed to create script: %v", err)
	}

	_, err := exec.Execute(ctx, "source "+scriptPath, &stdout, &stderr)
	if err != nil {
		t.Fatalf("source failed: %v", err)
	}

	if !strings.Contains(stdout.String(), "sourced successfully") {
		t.Errorf("expected 'sourced successfully' in output")
	}

	// Verify the export persisted
	stdout.Reset()
	_, err = exec.Execute(ctx, `echo "$TEST_FROM_SOURCE"`, &stdout, &stderr)
	if err != nil {
		t.Fatalf("echo failed: %v", err)
	}

	if strings.TrimSpace(stdout.String()) != "hello" {
		t.Errorf("expected 'hello', got: %q", stdout.String())
	}
}
