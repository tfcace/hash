package shell

import (
	"testing"
)

func TestConversationState_Transitions(t *testing.T) {
	cs := NewConversationState()

	// Initial state is inactive
	if cs.IsActive() {
		t.Error("expected inactive initially")
	}

	// Activate
	cs.Activate()
	if !cs.IsActive() {
		t.Error("expected active after Activate()")
	}
	if cs.SubState() != ConversationStreaming {
		t.Errorf("expected streaming, got %v", cs.SubState())
	}

	// Transition to awaiting input
	cs.SetSubState(ConversationAwaitingInput)
	if cs.SubState() != ConversationAwaitingInput {
		t.Errorf("expected awaiting_input, got %v", cs.SubState())
	}

	// Deactivate
	cs.Deactivate()
	if cs.IsActive() {
		t.Error("expected inactive after Deactivate()")
	}
}

func TestConversationState_ShellEscape(t *testing.T) {
	cs := NewConversationState()
	cs.Activate()
	cs.SetSubState(ConversationAwaitingInput)

	// Check if input is a shell escape
	if !cs.IsShellEscape("!ls -la") {
		t.Error("expected '!ls -la' to be shell escape")
	}
	if cs.IsShellEscape("ls -la") {
		t.Error("expected 'ls -la' to NOT be shell escape")
	}
	if cs.IsShellEscape("") {
		t.Error("expected empty string to NOT be shell escape")
	}

	// Extract command from shell escape
	cmd := cs.ExtractShellCommand("!ls -la")
	if cmd != "ls -la" {
		t.Errorf("expected 'ls -la', got %q", cmd)
	}
}

func TestConversationState_ExitCommands(t *testing.T) {
	cs := NewConversationState()
	cs.Activate()

	if !cs.IsExitCommand("/done") {
		t.Error("expected '/done' to be exit command")
	}
	if cs.IsExitCommand("done") {
		t.Error("expected 'done' to NOT be exit command")
	}
}
