package shell

import (
	"bytes"
	"testing"

	"github.com/tfcace/hash/internal/agent"
)

func TestConversation_MarkerDetectionIntegration(t *testing.T) {
	// Test the full flow: response with marker -> conversation mode
	input := "Here is your answer.\n\nWould you like more details?\n[AWAITING_INPUT]"

	display, expects := agent.ProcessAgentResponse(input)

	if !expects {
		t.Error("expected expectsInput=true for response with marker")
	}

	expectedDisplay := "Here is your answer.\n\nWould you like more details?"
	if display != expectedDisplay {
		t.Errorf("display = %q, want %q", display, expectedDisplay)
	}
}

func TestConversation_UIRendering(t *testing.T) {
	var buf bytes.Buffer
	ui := NewConversationUI(&buf, "#7c3aed")

	// Simulate conversation flow
	ui.WriteTintedLine("Agent: How can I help you?")
	ui.WriteInputPrompt()
	ui.WriteHints()

	output := buf.String()

	// Verify all components present
	if !bytes.Contains([]byte(output), []byte("How can I help you?")) {
		t.Error("missing agent message")
	}
	if !bytes.Contains([]byte(output), []byte("║")) {
		t.Error("missing input prompt")
	}
	if !bytes.Contains([]byte(output), []byte("Esc exit")) {
		t.Error("missing hints")
	}
}

func TestConversation_StateTransitions(t *testing.T) {
	cs := NewConversationState()

	// Simulate full conversation lifecycle
	cs.Activate()
	if cs.SubState() != ConversationStreaming {
		t.Errorf("after activate: want streaming, got %v", cs.SubState())
	}

	cs.SetSubState(ConversationAwaitingInput)
	if cs.SubState() != ConversationAwaitingInput {
		t.Errorf("after user turn: want awaiting_input, got %v", cs.SubState())
	}

	// Shell escape
	cs.SetSubState(ConversationExecutingShell)
	if cs.SubState() != ConversationExecutingShell {
		t.Errorf("during shell: want executing_shell, got %v", cs.SubState())
	}

	cs.SetSubState(ConversationAwaitingInput)
	cs.SetSubState(ConversationStreaming) // User replied

	cs.Deactivate()
	if cs.IsActive() {
		t.Error("should be inactive after deactivate")
	}
}
