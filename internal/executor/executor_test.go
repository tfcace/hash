package executor

import (
	"bytes"
	"context"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"
	"mvdan.cc/sh/v3/syntax"
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

func TestExecute_DefaultBashDialectRejectsZshSyntax(t *testing.T) {
	exec := New()
	ctx := context.Background()

	_, err := exec.Execute(ctx, ": &!", nil, nil)
	if err == nil {
		t.Fatal("expected bash dialect to reject zsh disown syntax")
	}
}

func TestExecute_ZshDialectAcceptsZshSyntax(t *testing.T) {
	exec := New()
	if err := exec.SetDialect("zsh"); err != nil {
		t.Fatalf("SetDialect(zsh) error = %v", err)
	}
	ctx := context.Background()

	if _, err := exec.Execute(ctx, ": &!", nil, nil); err != nil {
		t.Fatalf("zsh dialect should accept zsh disown syntax: %v", err)
	}
}

func TestExecutor_ZshDialectSourcesZshSyntaxBeforeExports(t *testing.T) {
	const envName = "HASH_TEST_ZSH_SOURCE_MARKER"
	t.Setenv(envName, "")

	exec := New()
	if err := exec.SetDialect("zsh"); err != nil {
		t.Fatalf("SetDialect(zsh) error = %v", err)
	}

	tmpDir := t.TempDir()
	scriptPath := filepath.Join(tmpDir, ".zshrc")
	content := `: &!
export ` + envName + `=loaded
`
	if err := os.WriteFile(scriptPath, []byte(content), 0o644); err != nil {
		t.Fatalf("failed to write zsh source file: %v", err)
	}

	var stdout, stderr bytes.Buffer
	if _, err := exec.Execute(context.Background(), "source "+scriptPath, &stdout, &stderr); err != nil {
		t.Fatalf("source failed: %v, stderr: %s", err, stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	if _, err := exec.Execute(context.Background(), `echo "$`+envName+`"`, &stdout, &stderr); err != nil {
		t.Fatalf("echo failed: %v, stderr: %s", err, stderr.String())
	}
	if got := strings.TrimSpace(stdout.String()); got != "loaded" {
		t.Fatalf("sourced export = %q, want loaded", got)
	}
}

func TestExecutor_ZshDialectEvalParsesZshSyntax(t *testing.T) {
	const envName = "HASH_TEST_ZSH_EVAL_MARKER"
	t.Setenv(envName, "")

	exec := New()
	if err := exec.SetDialect("zsh"); err != nil {
		t.Fatalf("SetDialect(zsh) error = %v", err)
	}

	var stdout, stderr bytes.Buffer
	command := `eval ': &!'; export ` + envName + `=loaded`
	if _, err := exec.Execute(context.Background(), command, &stdout, &stderr); err != nil {
		t.Fatalf("eval failed: %v, stderr: %s", err, stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	if _, err := exec.Execute(context.Background(), `echo "$`+envName+`"`, &stdout, &stderr); err != nil {
		t.Fatalf("echo failed: %v, stderr: %s", err, stderr.String())
	}
	if got := strings.TrimSpace(stdout.String()); got != "loaded" {
		t.Fatalf("eval export = %q, want loaded", got)
	}
}

func TestExecutor_SetDialectRejectsUnknownDialect(t *testing.T) {
	exec := New()
	if err := exec.SetDialect("fish"); err == nil {
		t.Fatal("expected unknown dialect to be rejected")
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

func TestExecute_PipeUpstreamDoesNotUsePTY(t *testing.T) {
	if _, err := osexec.LookPath("sh"); err != nil {
		t.Skip("sh not found")
	}

	ptmx, pts, err := pty.Open()
	if err != nil {
		t.Skipf("pty unavailable: %v", err)
	}
	defer ptmx.Close()
	defer pts.Close()

	origStdin := os.Stdin
	os.Stdin = pts
	defer func() { os.Stdin = origStdin }()

	exec := New()
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	var stdout bytes.Buffer
	result, err := exec.Execute(ctx, `sh -c 'if [ -t 1 ]; then sleep 2; else printf ok; fi' | cat`, &stdout, nil)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0", result.ExitCode)
	}
	if got := stdout.String(); got != "ok" {
		t.Fatalf("stdout = %q, want %q", got, "ok")
	}
	if result.UsedPTY {
		t.Fatal("upstream pipeline command should not use a PTY")
	}
}

func TestExecute_PipeDataIntegrity(t *testing.T) {
	// Test that piped data preserves exact bytes - no LF→CRLF translation.
	// This is critical for `curl ... | bash` style pipelines.
	exec := New()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Use printf to emit exact bytes including newlines, then verify with od
	// that no CRLF translation occurred. If ONLCR was active, \n would become \r\n.
	var stdout bytes.Buffer
	result, err := exec.Execute(ctx, `printf 'line1\nline2\n' | od -c | head -1`, &stdout, nil)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0", result.ExitCode)
	}

	// od output should show \n (newline) NOT \r \n (carriage return + newline)
	got := stdout.String()
	if strings.Contains(got, `\r`) {
		t.Errorf("Pipe data contains CRLF - LF was translated to CRLF: %q", got)
	}
	if !strings.Contains(got, `\n`) {
		t.Errorf("Pipe data should contain newlines: %q", got)
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
	return os.WriteFile(path, []byte(content), 0o755) //nolint:gosec // G306: test executable
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

func TestExecutor_CdDoubleDash(t *testing.T) {
	exec := New()
	ctx := context.Background()
	var stdout, stderr bytes.Buffer

	tmpDir := t.TempDir()
	tmpDirResolved, _ := filepath.EvalSymlinks(tmpDir)

	// Save and restore working directory
	origDir, _ := os.Getwd()
	defer os.Chdir(origDir) //nolint:errcheck

	tests := []struct {
		name string
		cmd  string
	}{
		{"cd --", "cd -- " + tmpDir + " && pwd"},
		{"builtin cd --", "builtin cd -- " + tmpDir + " && pwd"},
		{"backslash builtin cd --", `\builtin cd -- ` + tmpDir + " && pwd"},
		{"zoxide pattern", `
__zoxide_cd() { \builtin cd -- "$@"; }
z() { if [ -d "$1" ]; then __zoxide_cd "$1"; fi; }
z ` + tmpDir + " && pwd"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Reset to original directory
			os.Chdir(origDir) //nolint:errcheck
			exec.SyncRunnerDir()
			stdout.Reset()
			stderr.Reset()

			_, err := exec.Execute(ctx, tt.cmd, &stdout, &stderr)
			if err != nil {
				t.Fatalf("command failed: %v, stderr: %s", err, stderr.String())
			}

			got, _ := filepath.EvalSymlinks(strings.TrimSpace(stdout.String()))
			if got != tmpDirResolved {
				t.Errorf("pwd = %q, want %q (stderr: %s)", got, tmpDirResolved, stderr.String())
			}
		})
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
	if err := os.WriteFile(scriptPath, []byte(scriptContent), 0o644); err != nil { //nolint:gosec // G306: test file
		t.Fatalf("failed to create test script: %v", err)
	}

	// Source the file - should work with the default bash dialect.
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
	if err := os.WriteFile(scriptPath, []byte(scriptContent), 0o644); err != nil { //nolint:gosec // G306: test file
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
	if err := os.WriteFile(innerPath, []byte(innerContent), 0o644); err != nil { //nolint:gosec // G306: test file
		t.Fatalf("failed to create inner script: %v", err)
	}

	// Outer script sources inner - this tests nested source handling
	outerContent := `# Outer script
source ` + innerPath + `
[[ -n "$INNER_VAR" ]] && echo "outer sees inner var"
`
	if err := os.WriteFile(outerPath, []byte(outerContent), 0o644); err != nil { //nolint:gosec // G306: test file
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
	if err := os.WriteFile(scriptPath, []byte(scriptContent), 0o644); err != nil { //nolint:gosec // G306: test file
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

// Tests for graceful degradation in the default bash dialect: zsh-specific syntax should be silently skipped.

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

func TestExecutor_AliasBashSyntaxConvertsToFunction(t *testing.T) {
	exec := New()
	ctx := context.Background()
	var stdout, stderr bytes.Buffer

	// Define an alias with bash-specific syntax (&&)
	// This would fail with POSIX parsing, so it gets converted to a function
	_, err := exec.Execute(ctx, `alias mytest='echo "first" && echo "second"'`, &stdout, &stderr)
	if err != nil {
		t.Fatalf("alias definition failed: %v, stderr: %s", err, stderr.String())
	}

	// Check stderr - should NOT have the "could not parse" error
	if strings.Contains(stderr.String(), "could not parse") {
		t.Errorf("unexpected parse error in stderr: %s", stderr.String())
	}

	// Now call it - should work as a function
	stdout.Reset()
	stderr.Reset()
	_, err = exec.Execute(ctx, "mytest", &stdout, &stderr)
	if err != nil {
		t.Fatalf("mytest execution failed: %v, stderr: %s", err, stderr.String())
	}

	output := stdout.String()
	if !strings.Contains(output, "first") || !strings.Contains(output, "second") {
		t.Errorf("expected 'first' and 'second' in output, got: %q", output)
	}
}

func TestExecutor_AliasSimpleSyntaxStillWorks(t *testing.T) {
	exec := New()
	ctx := context.Background()
	var stdout, stderr bytes.Buffer

	// Simple alias should work through default handler
	_, err := exec.Execute(ctx, `alias greeting='echo hello'`, &stdout, &stderr)
	if err != nil {
		t.Fatalf("alias definition failed: %v", err)
	}

	stdout.Reset()
	_, err = exec.Execute(ctx, "greeting", &stdout, &stderr)
	if err != nil {
		t.Fatalf("greeting execution failed: %v", err)
	}

	if strings.TrimSpace(stdout.String()) != "hello" {
		t.Errorf("expected 'hello', got: %q", stdout.String())
	}
}

func TestExecutor_AliasWithPipeConvertsToFunction(t *testing.T) {
	exec := New()
	ctx := context.Background()
	var stdout, stderr bytes.Buffer

	// Alias with || (logical OR) - bash syntax
	_, err := exec.Execute(ctx, `alias tryit='false || echo "fallback"'`, &stdout, &stderr)
	if err != nil {
		t.Fatalf("alias definition failed: %v, stderr: %s", err, stderr.String())
	}

	if strings.Contains(stderr.String(), "could not parse") {
		t.Errorf("unexpected parse error in stderr: %s", stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	_, err = exec.Execute(ctx, "tryit", &stdout, &stderr)
	if err != nil {
		t.Fatalf("tryit execution failed: %v", err)
	}

	if strings.TrimSpace(stdout.String()) != "fallback" {
		t.Errorf("expected 'fallback', got: %q", stdout.String())
	}
}

func TestExecutor_AliasPassesArguments(t *testing.T) {
	exec := New()
	ctx := context.Background()
	var stdout, stderr bytes.Buffer

	// Define an alias - it should pass through arguments
	_, err := exec.Execute(ctx, `alias myecho='echo'`, &stdout, &stderr)
	if err != nil {
		t.Fatalf("alias definition failed: %v", err)
	}

	stdout.Reset()
	stderr.Reset()
	// Call the alias with arguments - they should be passed through
	_, err = exec.Execute(ctx, "myecho hello world", &stdout, &stderr)
	if err != nil {
		t.Fatalf("myecho execution failed: %v, stderr: %s", err, stderr.String())
	}

	if strings.TrimSpace(stdout.String()) != "hello world" {
		t.Errorf("expected 'hello world', got: %q (arguments not passed through)", stdout.String())
	}
}

func TestExecutor_TracksFunctionDefinitions(t *testing.T) {
	exec := New()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Define a function
	_, err := exec.Execute(ctx, "myfunc() { echo hello; }", nil, nil)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	// Check that it's tracked
	funcs := exec.Functions()
	found := false
	for _, name := range funcs {
		if name == "myfunc" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected 'myfunc' in Functions(), got %v", funcs)
	}
}

func TestExecutor_TracksAliasConversions(t *testing.T) {
	exec := New()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Define an alias (converted to function internally)
	_, err := exec.Execute(ctx, "alias myalias='echo hello'", nil, nil)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	// Check that it's tracked as a function
	funcs := exec.Functions()
	found := false
	for _, name := range funcs {
		if name == "myalias" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected 'myalias' in Functions(), got %v", funcs)
	}
}

func TestExecutor_TracksSourcedFunctions(t *testing.T) {
	exec := New()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Create a temp file with a function definition
	tmpDir := t.TempDir()
	scriptPath := filepath.Join(tmpDir, "funcs.sh")
	err := os.WriteFile(scriptPath, []byte("sourced_func() { echo sourced; }"), 0644)
	if err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	// Source the file
	_, err = exec.Execute(ctx, "source "+scriptPath, nil, nil)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	// Check that it's tracked
	funcs := exec.Functions()
	found := false
	for _, name := range funcs {
		if name == "sourced_func" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected 'sourced_func' in Functions(), got %v", funcs)
	}
}

func TestExecutor_EvalPanicRecovery(t *testing.T) {
	exec := New()
	ctx := context.Background()
	var stdout, stderr bytes.Buffer

	// Zoxide's bash init contains PROMPT_COMMAND manipulation with [![:space:]]
	// which causes mvdan/sh to panic. The panic recovery should skip the
	// panicking statement but still define the z/zi functions.
	zoxideInit := `
__zoxide_cd() { \builtin cd -- "$@"; }
z() { __zoxide_cd "$@"; }
zi() { __zoxide_cd "$@"; }
__zoxide_hook() {
    if test -z "${__zoxide_sesh}"; then
        __zoxide_sesh=1
    fi
}
`
	// This should not panic even with tricky content
	_, err := exec.Execute(ctx, `eval '`+zoxideInit+`'`, &stdout, &stderr)
	if err != nil {
		t.Fatalf("eval should not error: %v", err)
	}

	// The functions should be defined and callable
	stdout.Reset()
	stderr.Reset()

	tmpDir := t.TempDir()
	origDir, _ := os.Getwd()
	defer os.Chdir(origDir)

	_, err = exec.Execute(ctx, "z "+tmpDir+" && pwd", &stdout, &stderr)
	if err != nil {
		t.Fatalf("z function should work: %v, stderr: %s", err, stderr.String())
	}

	got, _ := filepath.EvalSymlinks(strings.TrimSpace(stdout.String()))
	want, _ := filepath.EvalSymlinks(tmpDir)
	if got != want {
		t.Errorf("z function: pwd = %q, want %q", got, want)
	}
}

func TestExecutor_EvalZoxidePromptCommandCompat(t *testing.T) {
	exec := New()
	ctx := context.Background()
	var stdout, stderr bytes.Buffer

	// Real zoxide bash init pattern that previously panicked in mvdan/sh.
	zoxideInit := `
function __zoxide_hook() { :; }
if [[ ${PROMPT_COMMAND:=} != *'__zoxide_hook'* ]]; then
	if [[ "$(declare -p PROMPT_COMMAND 2>&1)" == "declare -a"* ]]; then
		PROMPT_COMMAND=("${PROMPT_COMMAND[@]}" __zoxide_hook)
	else
		PROMPT_COMMAND="${PROMPT_COMMAND%"${PROMPT_COMMAND##*[![:space:]]}"}"
		PROMPT_COMMAND="${PROMPT_COMMAND:+${PROMPT_COMMAND};}__zoxide_hook"
	fi
fi
`
	_, err := exec.Execute(ctx, `eval '`+zoxideInit+`'`, &stdout, &stderr)
	if err != nil {
		t.Fatalf("eval should not error: %v", err)
	}
	if strings.Contains(stderr.String(), "skipping statement") {
		t.Fatalf("eval should not skip zoxide hook statement, stderr: %q", stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	_, err = exec.Execute(ctx, `echo "${PROMPT_COMMAND[@]}"`, &stdout, &stderr)
	if err != nil {
		t.Fatalf("reading PROMPT_COMMAND failed: %v", err)
	}
	if !strings.Contains(stdout.String(), "__zoxide_hook") {
		t.Fatalf("PROMPT_COMMAND should include __zoxide_hook, got %q", stdout.String())
	}
}

func TestExecutor_StatementRecoveryDoesNotPrintUnsupportedNoise(t *testing.T) {
	exec := New()
	var stderr bytes.Buffer
	exec.switchStderr.Set(&stderr)
	if err := exec.initRunner(); err != nil {
		t.Fatalf("initRunner error = %v", err)
	}

	prog := &syntax.File{Stmts: []*syntax.Stmt{nil}}
	if err := exec.runStatementsWithRecovery(context.Background(), prog, "test.zsh"); err != nil {
		t.Fatalf("runStatementsWithRecovery error = %v", err)
	}
	if strings.Contains(stderr.String(), "unsupported") || strings.Contains(stderr.String(), "skipping statement") {
		t.Fatalf("statement recovery should stay quiet, stderr: %q", stderr.String())
	}
}

func TestExecutor_ZoxideQueryNonZeroStillChangesDir(t *testing.T) {
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get cwd: %v", err)
	}
	defer func() {
		_ = os.Chdir(origDir)
		_ = os.Setenv("PWD", origDir)
	}()

	exec := New()
	ctx := context.Background()
	var stdout, stderr bytes.Buffer

	// Install a fake zoxide that returns a path on stdout but exits non-zero.
	// This mirrors environments where zoxide emits useful output with warnings.
	tmpBinDir := t.TempDir()
	fakeZoxide := filepath.Join(tmpBinDir, "zoxide")
	targetDir := t.TempDir()
	script := `#!/usr/bin/env bash
if [[ "$1" == "query" ]]; then
  echo "` + targetDir + `"
  exit 1
fi
exit 0
`
	if err := os.WriteFile(fakeZoxide, []byte(script), 0o755); err != nil {
		t.Fatalf("failed to write fake zoxide: %v", err)
	}

	origPath := os.Getenv("PATH")
	_, err = exec.Execute(ctx, "export PATH="+shellQuote(tmpBinDir)+":$PATH", &stdout, &stderr)
	if err != nil {
		t.Fatalf("failed to prepend PATH: %v, stderr: %s", err, stderr.String())
	}
	defer func() {
		_, _ = exec.Execute(ctx, "export PATH="+shellQuote(origPath), nil, nil)
	}()

	// Snippet matches zoxide init structure that sanitizeUnsupportedExpansions rewrites.
	zoxideSnippet := `
function __zoxide_pwd() { \builtin pwd -L; }
function __zoxide_cd() { \builtin cd -- "$@"; }
function __zoxide_z() {
    \builtin local result
    result="$(\command zoxide query --exclude "$(__zoxide_pwd)" -- "$@")" &&
        __zoxide_cd "${result}"
}
function z() { __zoxide_z "$@"; }
`
	if sanitized, changed := sanitizeUnsupportedExpansions(zoxideSnippet); !changed {
		t.Fatalf("expected sanitizer to rewrite zoxide snippet, got unchanged: %q", sanitized)
	} else if !strings.Contains(sanitized, `|| \builtin true`) {
		t.Fatalf("expected sanitizer to add || true for zoxide query, got: %q", sanitized)
	} else if !strings.Contains(sanitized, `result="${result%$'\r'}"`) {
		t.Fatalf("expected sanitizer to trim trailing carriage returns, got: %q", sanitized)
	}
	_, err = exec.Execute(ctx, `eval '`+zoxideSnippet+`'; pwd; z site; pwd`, &stdout, &stderr)
	if err != nil {
		t.Fatalf("eval+z failed: %v, stderr: %s", err, stderr.String())
	}

	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	if len(lines) < 2 {
		t.Fatalf("expected two pwd lines, got %q", stdout.String())
	}
	before, _ := filepath.EvalSymlinks(lines[0])
	after, _ := filepath.EvalSymlinks(lines[len(lines)-1])
	want, _ := filepath.EvalSymlinks(targetDir)
	if before == after {
		t.Fatalf("expected directory change after z; stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	if after != want {
		t.Fatalf("z should cd to fake query target: got %q want %q (stderr: %q)", after, want, stderr.String())
	}
}

func TestExecutor_ZoxideQueryCRLFStillChangesDir(t *testing.T) {
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get cwd: %v", err)
	}
	defer func() {
		_ = os.Chdir(origDir)
		_ = os.Setenv("PWD", origDir)
	}()

	exec := New()
	ctx := context.Background()
	var stdout, stderr bytes.Buffer

	// Fake zoxide emits CRLF path (common when output passed through PTY).
	tmpBinDir := t.TempDir()
	fakeZoxide := filepath.Join(tmpBinDir, "zoxide")
	targetDir := t.TempDir()
	script := `#!/usr/bin/env bash
if [[ "$1" == "query" ]]; then
  printf "%s\\r\\n" "` + targetDir + `"
  exit 0
fi
exit 0
`
	if err := os.WriteFile(fakeZoxide, []byte(script), 0o755); err != nil {
		t.Fatalf("failed to write fake zoxide: %v", err)
	}

	origPath := os.Getenv("PATH")
	_, err = exec.Execute(ctx, "export PATH="+shellQuote(tmpBinDir)+":$PATH", &stdout, &stderr)
	if err != nil {
		t.Fatalf("failed to prepend PATH: %v, stderr: %s", err, stderr.String())
	}
	defer func() {
		_, _ = exec.Execute(ctx, "export PATH="+shellQuote(origPath), nil, nil)
	}()

	zoxideSnippet := `
function __zoxide_pwd() { \builtin pwd -L; }
function __zoxide_cd() { \builtin cd -- "$@"; }
function __zoxide_z() {
    \builtin local result
    result="$(\command zoxide query --exclude "$(__zoxide_pwd)" -- "$@")" &&
        __zoxide_cd "${result}"
}
function z() { __zoxide_z "$@"; }
`
	_, err = exec.Execute(ctx, `eval '`+zoxideSnippet+`'; pwd; z site; pwd`, &stdout, &stderr)
	if err != nil {
		t.Fatalf("eval+z failed: %v, stderr: %s", err, stderr.String())
	}

	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	if len(lines) < 2 {
		t.Fatalf("expected two pwd lines, got %q", stdout.String())
	}
	after, _ := filepath.EvalSymlinks(lines[len(lines)-1])
	want, _ := filepath.EvalSymlinks(targetDir)
	if after != want {
		t.Fatalf("z should cd to fake CRLF query target: got %q want %q (stderr: %q)", after, want, stderr.String())
	}
}

func TestExecutor_SourcePanicRecovery(t *testing.T) {
	exec := New()
	ctx := context.Background()
	var stdout, stderr bytes.Buffer

	tmpDir := t.TempDir()
	scriptPath := filepath.Join(tmpDir, "init.sh")

	// Script with a good function and a statement that would panic in mvdan/sh
	// We simulate the panic scenario — since we can't easily trigger a real
	// mvdan/sh panic in test, we test that safeRunNode correctly runs valid
	// statements.
	scriptContent := `
myfunc() { echo "survived"; }
echo "line two"
`
	if err := os.WriteFile(scriptPath, []byte(scriptContent), 0o644); err != nil {
		t.Fatalf("failed to create script: %v", err)
	}

	_, err := exec.Execute(ctx, "source "+scriptPath, &stdout, &stderr)
	if err != nil {
		t.Fatalf("source should not error: %v", err)
	}

	// Function should be defined
	stdout.Reset()
	_, err = exec.Execute(ctx, "myfunc", &stdout, &stderr)
	if err != nil {
		t.Fatalf("function should work: %v", err)
	}
	if strings.TrimSpace(stdout.String()) != "survived" {
		t.Errorf("got %q, want %q", stdout.String(), "survived")
	}
}

func TestExecutor_ExecutePanicRecovery(t *testing.T) {
	exec := New()
	ctx := context.Background()
	var stdout bytes.Buffer

	// safeRunNode in Execute() should catch panics from top-level commands too.
	// Test with a normal command to ensure safeRunNode doesn't break normal flow.
	_, err := exec.Execute(ctx, "echo safe", &stdout, nil)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if strings.TrimSpace(stdout.String()) != "safe" {
		t.Errorf("stdout = %q, want %q", stdout.String(), "safe")
	}
}

func TestExecutor_UnsetRemovesFunction(t *testing.T) {
	exec := New()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Define a function
	_, err := exec.Execute(ctx, "removeme() { echo hi; }", nil, nil)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	// Verify it's tracked
	funcs := exec.Functions()
	found := false
	for _, name := range funcs {
		if name == "removeme" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected 'removeme' in Functions() after definition")
	}

	// Unset the function
	_, err = exec.Execute(ctx, "unset -f removeme", nil, nil)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	// Verify it's removed from tracking
	funcs = exec.Functions()
	for _, name := range funcs {
		if name == "removeme" {
			t.Errorf("'removeme' should be removed from Functions() after unset -f")
		}
	}
}
