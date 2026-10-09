package shell

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tfcace/hash/internal/agent"
	"github.com/tfcace/hash/internal/config"
)

func newShellForAgentCommand(t *testing.T) *Shell {
	t.Helper()
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	cfg := config.Default()
	cfg.History.Path = filepath.Join(t.TempDir(), "history.db")
	cfg.Agent.Command = "hash-test-no-such-adapter" // nothing here should reach an agent
	sh, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() { sh.Close() }) //nolint:errcheck
	return sh
}

// A command the agent hands back runs like a typed line, so when it fails
// the shell's last error names it, and a bare ?? explains the right thing.
func TestHandleAgentConfirmAction_FailedCommandBecomesLastError(t *testing.T) {
	sh := newShellForAgentCommand(t)
	sh.lastCommand = "echo hello" // the line typed before the ?? turn
	missing := filepath.Join(t.TempDir(), "nope.txt")
	cmd := "cat " + missing

	sh.handleAgentConfirmAction(context.Background(), ConfirmRun, ConfirmTypeCommand,
		agent.Response{Type: agent.ResponseTypeCommand, Command: cmd}, cmd, 1)

	if sh.lastCommand != cmd {
		t.Errorf("lastCommand = %q, want the agent command %q", sh.lastCommand, cmd)
	}
	if sh.lastExitCode != 1 {
		t.Errorf("lastExitCode = %d, want 1", sh.lastExitCode)
	}
	if !strings.Contains(sh.lastStderr, "No such file") {
		t.Errorf("lastStderr = %q, want cat's error text", sh.lastStderr)
	}
}

// The agent's reply is a shell command, never a new ?? request: a ?? inside
// it (a glob, a grep pattern) runs as shell syntax instead of going back to
// the agent.
func TestHandleAgentConfirmAction_CommandContainingDoubleQuestionMarkRunsAsShell(t *testing.T) {
	sh := newShellForAgentCommand(t)
	cmd := "echo a??b"

	sh.handleAgentConfirmAction(context.Background(), ConfirmRun, ConfirmTypeCommand,
		agent.Response{Type: agent.ResponseTypeCommand, Command: cmd}, cmd, 1)

	if sh.lastCommand != cmd {
		t.Errorf("lastCommand = %q, want %q to have run as a shell line", sh.lastCommand, cmd)
	}
	if sh.lastExitCode != 0 {
		t.Errorf("lastExitCode = %d, want 0", sh.lastExitCode)
	}
}

// An agent that hands back "exit" ends the shell on Enter, as a typed exit would.
func TestHandleAgentConfirmAction_ExitEndsTheShell(t *testing.T) {
	sh := newShellForAgentCommand(t)

	err := sh.handleAgentConfirmAction(context.Background(), ConfirmRun, ConfirmTypeCommand,
		agent.Response{Type: agent.ResponseTypeCommand, Command: "exit"}, "exit", 1)

	if err != errExit {
		t.Fatalf("handleAgentConfirmAction() error = %v, want errExit", err)
	}
}
