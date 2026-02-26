package shell

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/tfcace/hash/internal/agent"
)

func TestBuildConversationPrompt_IncludesTranscript(t *testing.T) {
	sh := &Shell{
		convHistory: []conversationMessage{
			{role: "assistant", content: "Let's play 20 questions."},
			{role: "user", content: "I have thought of something."},
		},
	}

	prompt := sh.buildConversationPrompt("yes")

	if !strings.Contains(prompt, "Conversation so far:") {
		t.Fatalf("missing transcript header in prompt: %q", prompt)
	}
	if !strings.Contains(prompt, "Assistant: Let's play 20 questions.") {
		t.Fatalf("missing assistant transcript line in prompt: %q", prompt)
	}
	if !strings.Contains(prompt, "User: I have thought of something.") {
		t.Fatalf("missing user transcript line in prompt: %q", prompt)
	}
	if !strings.Contains(prompt, "Latest user message:\nyes") {
		t.Fatalf("missing latest user message in prompt: %q", prompt)
	}
}

func TestAppendConversationMessage_StripsMarkersAndSkipsEmpty(t *testing.T) {
	sh := &Shell{}

	sh.appendConversationMessage("assistant", "  [CONVERSATION]\nhello\n[AWAITING_INPUT] ")
	sh.appendConversationMessage("assistant", "   ")

	if len(sh.convHistory) != 1 {
		t.Fatalf("history len = %d, want 1", len(sh.convHistory))
	}
	if strings.Contains(sh.convHistory[0].content, agent.ConversationStartMarker) {
		t.Fatalf("conversation marker should be stripped: %q", sh.convHistory[0].content)
	}
	if strings.Contains(sh.convHistory[0].content, agent.AwaitingInputMarker) {
		t.Fatalf("awaiting marker should be stripped: %q", sh.convHistory[0].content)
	}
	if sh.convHistory[0].content != "hello" {
		t.Fatalf("stored content = %q, want %q", sh.convHistory[0].content, "hello")
	}
}

func TestAppendConversationMessage_BoundsHistorySize(t *testing.T) {
	sh := &Shell{}

	for i := 0; i < 30; i++ {
		sh.appendConversationMessage("user", "x")
	}

	if len(sh.convHistory) > 24 {
		t.Fatalf("history len = %d, want <= 24", len(sh.convHistory))
	}
}

func TestConversationContextSurvivesTurnCancelInterrupt(t *testing.T) {
	sh := &Shell{
		conversation: NewConversationState(),
		agentOutput:  NewAgentOutputCoordinator(&bytes.Buffer{}),
	}
	sh.conversation.Activate()
	sh.conversation.SetSubState(ConversationStreaming)
	sh.appendConversationMessage("assistant", "Let's play 20 questions.")
	sh.appendConversationMessage("user", "I have thought of something.")

	turnCtx, turnCancel := context.WithCancel(context.Background())
	defer turnCancel()
	sh.setConversationTurnCancel(turnCancel)
	defer sh.clearConversationTurnCancel()

	agentCanceled := false
	exitSignalLoop := sh.handleAgentInterrupt(func() {
		agentCanceled = true
	})

	if exitSignalLoop {
		t.Fatal("expected turn-cancel interrupt to keep signal loop active")
	}
	if agentCanceled {
		t.Fatal("expected turn-cancel interrupt to avoid full agent cancel")
	}

	select {
	case <-turnCtx.Done():
	default:
		t.Fatal("expected active turn context to be canceled")
	}

	// Conversation transcript should still be available for the next turn.
	prompt := sh.buildConversationPrompt("yes")
	if !strings.Contains(prompt, "Assistant: Let's play 20 questions.") {
		t.Fatalf("missing assistant context after cancel: %q", prompt)
	}
	if !strings.Contains(prompt, "User: I have thought of something.") {
		t.Fatalf("missing user context after cancel: %q", prompt)
	}
}
