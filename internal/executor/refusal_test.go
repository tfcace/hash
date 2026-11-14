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

// A refused command whose stderr is not the line's own (2>/dev/null, 2>&1,
// a pipe) gets the plain message there, as bash's: no banner, and it is
// not one of the line's refusals. conda's and pyenv's init lines
// (`x=$(nosuch 2>/dev/null)`) drew a banner at every start.
func TestExecute_RefusalBannerOnlyOnTheLinesStderr(t *testing.T) {
	for _, tt := range []struct {
		line, stdout string
		banners      int
	}{
		{"nosuch-hash-a 2>/dev/null; echo st=$?", "st=127\n", 0},
		{"x=$(nosuch-hash-a 2>/dev/null); echo st=$?", "st=127\n", 0},
		{"nosuch-hash-a 2>&1 | tr a A", "hAsh: nosuch-hAsh-A: commAnd not found\n", 0},
		{"f() { nosuch-hash-a; }; f 2>/dev/null; echo st=$?", "st=127\n", 0},
		{"x=$(nosuch-hash-a); echo st=$?", "st=127\n", 1},
		{"nosuch-hash-a; { nosuch-hash-b; } 2>/dev/null", "", 1},
	} {
		e := New()
		var seen []error
		e.SetRefusalHandler(func(err error) { seen = append(seen, err) })
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		var stdout, stderr bytes.Buffer
		res, err := e.Execute(ctx, tt.line, &stdout, &stderr)
		cancel()
		if err != nil {
			t.Fatalf("%q: %v", tt.line, err)
		}
		if stdout.String() != tt.stdout || stderr.Len() != 0 || len(seen) != tt.banners || len(res.Refusals) != tt.banners {
			t.Errorf("%q: stdout %q stderr %q, %d banners, refusals %v; want stdout %q, %d banners and refusals",
				tt.line, stdout.String(), stderr.String(), len(seen), res.Refusals, tt.stdout, tt.banners)
		}
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
