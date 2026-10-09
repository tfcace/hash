package shell

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tfcace/hash/internal/config"
	"github.com/tfcace/hash/internal/executor"
)

func TestErrorHandler_HandleNotExecutable(t *testing.T) {
	var out bytes.Buffer
	h := &ErrorHandler{out: &out}

	h.HandleNotExecutable("./deploy.sh", "Permission denied")

	for _, want := range []string{"./deploy.sh: Permission denied", "?? to explain"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output should contain %q, got:\n%s", want, out.String())
		}
	}
}

func TestHandleExecutionError_NotExecutableIs126(t *testing.T) {
	var out bytes.Buffer
	sh := &Shell{errors: &ErrorHandler{out: &out}}

	sh.handleExecutionError(&executor.CommandNotExecutableError{Command: "./deploy.sh", Reason: "Permission denied"})

	if sh.lastExitCode != 126 {
		t.Errorf("lastExitCode = %d, want 126", sh.lastExitCode)
	}
	if !strings.Contains(out.String(), "Permission denied") {
		t.Errorf("expected the reason on screen, got:\n%s", out.String())
	}
}

// With no child stderr to capture, the shell's own error text is what the
// learning loop and a bare ?? should see.
func TestHandleExecutionResult_UsesErrorTextWhenStderrIsEmpty(t *testing.T) {
	var out bytes.Buffer
	sh := &Shell{errors: &ErrorHandler{out: &out}}
	capture := newStderrCapture(io.Discard)
	refusal := &executor.CommandNotExecutableError{Command: "./deploy.sh", Reason: "Permission denied"}

	sh.handleExecutionResult("./deploy.sh", &executor.Result{ExitCode: 126, Refusals: []error{refusal}}, nil, capture)

	if sh.lastStderr != "./deploy.sh: Permission denied" {
		t.Errorf("lastStderr = %q, want the refusal text", sh.lastStderr)
	}
	if sh.lastExitCode != 126 {
		t.Errorf("lastExitCode = %d, want 126", sh.lastExitCode)
	}
}

// The shell renders its banner at the point of refusal and the line keeps
// running, so a fallback after || decides the line's status.
func TestNew_RefusalShowsBannerAndKeepsLineRunning(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	cfg := config.Default()
	cfg.History.Path = filepath.Join(t.TempDir(), "history.db")
	sh, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer sh.Close()
	var banner bytes.Buffer
	sh.errors = &ErrorHandler{out: &banner}
	script := filepath.Join(t.TempDir(), "deploy.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := sh.executeRegularCommand(context.Background(), script+" || true"); err != nil {
		t.Fatalf("executeRegularCommand() error = %v", err)
	}

	if sh.lastExitCode != 0 {
		t.Errorf("lastExitCode = %d, want 0: the fallback ran", sh.lastExitCode)
	}
	if !strings.Contains(banner.String(), "Permission denied") {
		t.Errorf("banner should be rendered at the point of refusal, got %q", banner.String())
	}
}
