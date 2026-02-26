package shell

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestHandleAgentInterrupt_CancelsConversationTurnOnly(t *testing.T) {
	sh := &Shell{
		conversation: NewConversationState(),
		agentOutput:  NewAgentOutputCoordinator(&bytes.Buffer{}),
	}
	sh.conversation.Activate()
	sh.conversation.SetSubState(ConversationStreaming)

	turnCanceled := false
	sh.setConversationTurnCancel(func() {
		turnCanceled = true
	})

	agentCanceled := false
	exitSignalLoop := sh.handleAgentInterrupt(func() {
		agentCanceled = true
	})

	if exitSignalLoop {
		t.Fatal("expected signal loop to continue after turn-only cancel")
	}
	if !turnCanceled {
		t.Fatal("expected active conversation turn to be canceled")
	}
	if agentCanceled {
		t.Fatal("full agent context should not be canceled for turn-only interrupt")
	}
}

func TestHandleAgentInterrupt_FallsBackToFullCancelWithoutActiveTurn(t *testing.T) {
	sh := &Shell{
		conversation: NewConversationState(),
		agentOutput:  NewAgentOutputCoordinator(&bytes.Buffer{}),
	}
	sh.conversation.Activate()
	sh.conversation.SetSubState(ConversationStreaming)

	agentCanceled := false
	exitSignalLoop := sh.handleAgentInterrupt(func() {
		agentCanceled = true
	})

	if !exitSignalLoop {
		t.Fatal("expected signal loop to exit after full cancel")
	}
	if !agentCanceled {
		t.Fatal("expected full agent cancel when no active turn cancel is set")
	}
}

func TestHandleAgentInterrupt_CancelsFullRequestOutsideConversation(t *testing.T) {
	sh := &Shell{
		conversation: NewConversationState(),
		agentOutput:  NewAgentOutputCoordinator(&bytes.Buffer{}),
	}

	agentCanceled := false
	exitSignalLoop := sh.handleAgentInterrupt(func() {
		agentCanceled = true
	})

	if !exitSignalLoop {
		t.Fatal("expected signal loop to exit for non-conversation interrupts")
	}
	if !agentCanceled {
		t.Fatal("expected full agent cancel outside conversation mode")
	}
}

func TestHandleAgentInterrupt_AwaitingInputSkipsCoordinatorClear(t *testing.T) {
	var out bytes.Buffer
	sh := &Shell{
		conversation: NewConversationState(),
		agentOutput:  NewAgentOutputCoordinator(&out),
	}
	sh.conversation.Activate()
	sh.conversation.SetSubState(ConversationAwaitingInput)

	agentCanceled := false
	exitSignalLoop := sh.handleAgentInterrupt(func() {
		agentCanceled = true
	})

	if !exitSignalLoop {
		t.Fatal("expected signal loop to exit after context cancel in awaiting-input state")
	}
	if !agentCanceled {
		t.Fatal("expected full agent cancel callback to be invoked")
	}
	if strings.Contains(out.String(), "\r\x1b[K") {
		t.Fatalf("expected no coordinator line clear in awaiting-input path, got %q", out.String())
	}
}

func TestCancelConversationTurn_UsesRegisteredCancelFunc(t *testing.T) {
	sh := &Shell{}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sh.setConversationTurnCancel(cancel)

	if !sh.cancelConversationTurn() {
		t.Fatal("expected cancelConversationTurn to return true when cancel exists")
	}
	select {
	case <-ctx.Done():
	default:
		t.Fatal("expected registered turn context to be canceled")
	}
}
