package executor

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"
)

// A command the shell refuses to run (not found, not executable) sets its
// exit status like any failing command: the rest of the line still runs.
func TestExecute_RefusalDoesNotAbortTheLine(t *testing.T) {
	exec := New()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var stdout, stderr bytes.Buffer

	result, err := exec.Execute(ctx, "nosuchcmd-hash-test || echo fallback", &stdout, &stderr)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if got := strings.TrimSpace(stdout.String()); got != "fallback" {
		t.Errorf("stdout = %q, want the fallback to run", got)
	}
	if result.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0 after the fallback succeeded", result.ExitCode)
	}
	if len(result.Refusals) != 1 || !IsCommandNotFound(result.Refusals[0]) {
		t.Errorf("Refusals = %v, want the one command-not-found refusal", result.Refusals)
	}
	if !strings.Contains(stderr.String(), "nosuchcmd-hash-test: command not found") {
		t.Errorf("stderr should carry the refusal message, got %q", stderr.String())
	}
}

func TestExecute_RefusalSetsExitStatusVariable(t *testing.T) {
	exec := New()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var stdout, stderr bytes.Buffer

	if _, err := exec.Execute(ctx, "nosuchcmd-hash-test; echo status=$?", &stdout, &stderr); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if got := strings.TrimSpace(stdout.String()); got != "status=127" {
		t.Errorf("stdout = %q, want status=127", got)
	}
}

// The shell installs a handler to render its own banner at the point of
// refusal; with one installed the executor prints nothing itself.
func TestExecute_RefusalHandlerReplacesDefaultMessage(t *testing.T) {
	exec := New()
	var seen []error
	exec.SetRefusalHandler(func(err error) { seen = append(seen, err) })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var stdout, stderr bytes.Buffer

	result, err := exec.Execute(ctx, "nosuchcmd-hash-test", &stdout, &stderr)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if len(seen) != 1 || !IsCommandNotFound(seen[0]) {
		t.Fatalf("handler saw %v, want one command-not-found refusal", seen)
	}
	if stderr.Len() != 0 {
		t.Errorf("executor printed %q although a handler was installed", stderr.String())
	}
	if result.ExitCode != 127 {
		t.Errorf("ExitCode = %d, want 127", result.ExitCode)
	}
}
