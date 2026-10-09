package shell

import (
	"bytes"
	"context"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tfcace/hash/internal/agent"
	"github.com/tfcace/hash/internal/config"
	"github.com/tfcace/hash/internal/history"
	"github.com/tfcace/hash/internal/parser"
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
	sh.responseUI = NewResponseUI(io.Discard)
	return sh
}

// A pipe-mode turn has no confirmation step, and is still a ?? exchange
// worth recalling.
func TestHandleAgentFullStreaming_PipeTurnIsRecorded(t *testing.T) {
	store, err := history.NewStore(":memory:")
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()
	mock := agent.NewMockTransport(agent.Response{Type: agent.ResponseTypeExplanation, Explanation: "Two files changed."})
	sh := &Shell{
		config:       config.Default(),
		agentHandler: NewAgentHandler(agent.NewClient(mock)),
		responseUI:   NewResponseUI(io.Discard),
		agentOutput:  NewAgentOutputCoordinator(io.Discard),
		history:      store,
	}

	sh.handleAgentFullStreaming(context.Background(), parser.ParseResult{
		Type:        parser.CommandTypeAgentPipe,
		Command:     "git diff",
		AgentPrompt: "summarize",
	}, "test-model")

	turns, err := store.GetAgentInteractions("", 10)
	if err != nil || len(turns) != 1 {
		t.Fatalf("GetAgentInteractions() = %v, %v; want the pipe turn recorded", turns, err)
	}
	if turns[0].Prompt != "summarize" || !turns[0].Accepted || turns[0].CommandID != 0 {
		t.Errorf("recorded turn = %+v, want the prompt, accepted, no command", turns[0])
	}
}

// A command reply is accepted only once a command ran. A blank command
// stands in for an edit the user abandons: nothing ran, so nothing was
// accepted.
func TestHandleAgentConfirmAction_CommandTurnWithoutARunIsNotAccepted(t *testing.T) {
	sh := newShellForAgentCommand(t)

	sh.handleAgentConfirmAction(context.Background(), ConfirmRun, ConfirmTypeCommand,
		agent.Response{Type: agent.ResponseTypeCommand, Command: "   "}, agentTurn{prompt: "noop", response: "   "}, 1)

	turns, err := sh.history.GetAgentInteractions("", 10)
	if err != nil || len(turns) != 1 {
		t.Fatalf("GetAgentInteractions() = %v, %v; want one recorded turn", turns, err)
	}
	if turns[0].Accepted {
		t.Errorf("turn recorded as accepted although no command ran: %+v", turns[0])
	}
}

// A command the agent hands back runs like a typed line, so when it fails
// the shell's last error names it, and a bare ?? explains the right thing.
func TestHandleAgentConfirmAction_FailedCommandBecomesLastError(t *testing.T) {
	sh := newShellForAgentCommand(t)
	sh.lastCommand = "echo hello" // the line typed before the ?? turn
	missing := filepath.Join(t.TempDir(), "nope.txt")
	cmd := "cat " + missing

	sh.handleAgentConfirmAction(context.Background(), ConfirmRun, ConfirmTypeCommand,
		agent.Response{Type: agent.ResponseTypeCommand, Command: cmd}, agentTurn{response: cmd}, 1)

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
		agent.Response{Type: agent.ResponseTypeCommand, Command: cmd}, agentTurn{response: cmd}, 1)

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
		agent.Response{Type: agent.ResponseTypeCommand, Command: "exit"}, agentTurn{response: "exit"}, 1)

	if err != errExit {
		t.Fatalf("handleAgentConfirmAction() error = %v, want errExit", err)
	}
}

// Running the agent's command links the ?? turn to that command in history,
// so the question and what it produced can be recalled together.
func TestHandleAgentConfirmAction_RunLinksTheTurnToItsCommand(t *testing.T) {
	sh := newShellForAgentCommand(t)
	turn := agentTurn{prompt: "print linked", response: "echo linked"}

	sh.handleAgentConfirmAction(context.Background(), ConfirmRun, ConfirmTypeCommand,
		agent.Response{Type: agent.ResponseTypeCommand, Command: "echo linked"}, turn, 1)

	recent, err := sh.history.GetRecent(1)
	if err != nil || len(recent) != 1 || recent[0].Command != "echo linked" {
		t.Fatalf("GetRecent() = %v, %v; want the agent command recorded", recent, err)
	}
	turns, err := sh.history.GetAgentInteractions("", 10)
	if err != nil || len(turns) != 1 {
		t.Fatalf("GetAgentInteractions() = %v, %v; want one recorded turn", turns, err)
	}
	got := turns[0]
	if got.Prompt != "print linked" || got.Response != "echo linked" {
		t.Errorf("recorded turn = %+v, want the prompt and response", got)
	}
	if !got.Accepted {
		t.Error("a turn whose command ran should be recorded as accepted")
	}
	if got.CommandID != recent[0].ID {
		t.Errorf("CommandID = %d, want the history id of the command that ran (%d)", got.CommandID, recent[0].ID)
	}
}

// A turn the user cancels is still remembered, as declined and with no command.
func TestHandleAgentConfirmAction_CancelRecordsTheTurnAsDeclined(t *testing.T) {
	sh := newShellForAgentCommand(t)
	sh.responseUI = NewResponseUI(io.Discard)
	turn := agentTurn{prompt: "delete everything", response: "rm -rf /"}

	sh.handleAgentConfirmAction(context.Background(), ConfirmCancel, ConfirmTypeCommand,
		agent.Response{Type: agent.ResponseTypeCommand, Command: "rm -rf /"}, turn, 1)

	turns, err := sh.history.GetAgentInteractions("", 10)
	if err != nil || len(turns) != 1 {
		t.Fatalf("GetAgentInteractions() = %v, %v; want one recorded turn", turns, err)
	}
	if turns[0].Accepted || turns[0].CommandID != 0 {
		t.Errorf("declined turn recorded as %+v, want not accepted and no command", turns[0])
	}
}

// Running the agent's command replaces the confirmation hint with the command
// on its own input line, so scrollback reads as if the user had typed it.
func TestHandleAgentConfirmAction_RunShowsTheCommandAsTyped(t *testing.T) {
	sh := newShellForAgentCommand(t)
	var out bytes.Buffer
	sh.responseUI = NewResponseUI(&out)

	sh.handleAgentConfirmAction(context.Background(), ConfirmRun, ConfirmTypeCommand,
		agent.Response{Type: agent.ResponseTypeCommand, Command: "true"}, agentTurn{response: "true"}, 1)

	got := out.String()
	if !strings.HasPrefix(got, "\x1b[A\x1b[K\x1b[A\x1b[K") {
		t.Errorf("want the hint line and the blank line after it cleared first, got %q", got)
	}
	if !strings.Contains(got, "\x1b[1mtrue\x1b[0m") {
		t.Errorf("want the command shown as a submitted input line, got %q", got)
	}
}
