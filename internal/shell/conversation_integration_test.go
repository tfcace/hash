package shell

import (
	"bytes"
	"testing"

	"github.com/tfcace/hash/internal/agent"
)

func TestConversation_MarkerDetectionIntegration(t *testing.T) {
	// Test the full flow: response with markers -> conversation mode
	input := "[CONVERSATION]\nHere is your answer.\n\nWould you like more details?\n[AWAITING_INPUT]"

	display, expects := agent.ProcessAgentResponse(input)

	if !expects {
		t.Error("expected expectsInput=true for response with marker")
	}

	expectedDisplay := "Here is your answer.\n\nWould you like more details?"
	if display != expectedDisplay {
		t.Errorf("display = %q, want %q", display, expectedDisplay)
	}
}

func TestConversation_HasConversationStart(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"[CONVERSATION]\nHello", true},
		{"  [CONVERSATION]\nHello", true},
		{"\n[CONVERSATION]\nHello", true}, // Leading newlines are OK
		{"\n\n[CONVERSATION]\nHello", true},
		{"Hello [CONVERSATION]", false}, // Must be at start (after whitespace trim)
		{"Hello", false},
	}

	for _, tt := range tests {
		got := agent.HasConversationStart(tt.input)
		if got != tt.want {
			t.Errorf("HasConversationStart(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestConversation_StripConversationStart(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"[CONVERSATION]\nHello", "Hello"},
		{"  [CONVERSATION]\nHello", "Hello"},
		{"[CONVERSATION]Hello", "Hello"},
		{"\n[CONVERSATION]\n\nHello", "Hello"}, // Leading/trailing whitespace stripped
		{"Hello", "Hello"},
	}

	for _, tt := range tests {
		got := agent.StripConversationStart(tt.input)
		if got != tt.want {
			t.Errorf("StripConversationStart(%q) = %q, want %q", tt.input, got, tt.want)
		}
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
	if !bytes.Contains([]byte(output), []byte("│")) {
		t.Error("missing input prompt")
	}
	if !bytes.Contains([]byte(output), []byte("Ctrl+C exit")) {
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
