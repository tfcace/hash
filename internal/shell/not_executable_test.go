package shell

import (
	"bytes"
	"io"
	"strings"
	"testing"

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
	err := &executor.CommandNotExecutableError{Command: "./deploy.sh", Reason: "Permission denied"}

	sh.handleExecutionResult("./deploy.sh", &executor.Result{ExitCode: 126}, err, capture)

	if sh.lastStderr != "./deploy.sh: Permission denied" {
		t.Errorf("lastStderr = %q, want the error text", sh.lastStderr)
	}
}
