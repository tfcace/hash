package shell

import (
	"bytes"
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tfcace/hash/internal/agent"
)

type scriptedConversationFlowTransport struct {
	mu            sync.Mutex
	requests      []agent.Request
	callIndex     int
	callStarted   chan int
	firstCanceled chan struct{}
	cancelOnce    sync.Once
}

func newScriptedConversationFlowTransport() *scriptedConversationFlowTransport {
	return &scriptedConversationFlowTransport{
		callStarted:   make(chan int, 8),
		firstCanceled: make(chan struct{}),
	}
}

func (t *scriptedConversationFlowTransport) Name() string { return "scripted-conversation-flow" }

func (t *scriptedConversationFlowTransport) Connect(ctx context.Context) error { return nil }

func (t *scriptedConversationFlowTransport) Close() error { return nil }

//nolint:gocritic // unnamedResult: transport interface parity
func (t *scriptedConversationFlowTransport) SendStreaming(ctx context.Context, req agent.Request) (<-chan string, <-chan error) {
	textCh := make(chan string, 1)
	errCh := make(chan error, 1)

	t.mu.Lock()
	t.callIndex++
	call := t.callIndex
	t.requests = append(t.requests, req)
	t.mu.Unlock()

	select {
	case t.callStarted <- call:
	default:
	}

	go func() {
		defer close(textCh)
		defer close(errCh)

		switch call {
		case 1:
			<-ctx.Done()
			t.cancelOnce.Do(func() {
				close(t.firstCanceled)
			})
			errCh <- ctx.Err()
		case 2:
			textCh <- "You have two Kubernetes clusters configured: docker-desktop and rancher-desktop.\nWant to continue our game?"
		default:
			textCh <- "[AWAITING_INPUT]"
		}
	}()

	return textCh, errCh
}

func (t *scriptedConversationFlowTransport) Requests() []agent.Request {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]agent.Request, len(t.requests))
	copy(out, t.requests)
	return out
}

func TestSendConversationReply_CancelThenFollowupPreservesTranscript(t *testing.T) {
	var out bytes.Buffer

	transport := newScriptedConversationFlowTransport()
	handler := NewAgentHandler(agent.NewClient(transport))

	sh := &Shell{
		agentHandler:    handler,
		agentOutput:     NewAgentOutputCoordinator(&out),
		responseUI:      NewResponseUI(&out),
		conversation:    NewConversationState(),
		convUI:          NewConversationUI(&out, "#7c3aed"),
		convCancelArmed: false,
		convHistory: []conversationMessage{
			{role: "assistant", content: "Let's play 20 questions."},
			{role: "user", content: "I have thought of something."},
		},
	}
	sh.conversation.Activate()
	sh.conversation.SetSubState(ConversationAwaitingInput)

	firstDone := make(chan struct{})
	go func() {
		sh.sendConversationReply(context.Background(), "yes")
		close(firstDone)
	}()

	waitForConversationCall(t, transport.callStarted, 1, 2*time.Second)

	fullCanceled := false
	exitSignalLoop := sh.handleAgentInterrupt(func() {
		fullCanceled = true
	})
	if exitSignalLoop {
		t.Fatal("expected Ctrl+C in conversation streaming to cancel only active turn")
	}
	if fullCanceled {
		t.Fatal("expected full request cancel callback not to be called")
	}

	select {
	case <-transport.firstCanceled:
	case <-time.After(2 * time.Second):
		t.Fatal("expected first conversation turn to be canceled")
	}

	select {
	case <-firstDone:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for canceled conversation turn to return")
	}

	if !sh.conversation.IsActive() {
		t.Fatal("conversation should remain active after turn cancel")
	}
	if got := sh.conversation.SubState(); got != ConversationAwaitingInput {
		t.Fatalf("conversation sub-state after cancel = %v, want %v", got, ConversationAwaitingInput)
	}

	sh.sendConversationReply(context.Background(), "no")

	waitForConversationCall(t, transport.callStarted, 2, 2*time.Second)

	reqs := transport.Requests()
	if len(reqs) < 2 {
		t.Fatalf("expected at least 2 requests, got %d", len(reqs))
	}

	secondPrompt := reqs[1].Prompt
	if !containsAll(secondPrompt,
		"Continue this ongoing terminal conversation.",
		"Assistant: Let's play 20 questions.",
		"User: I have thought of something.",
		"User: yes",
		"Latest user message:\nno",
	) {
		t.Fatalf("second turn prompt missing preserved transcript context:\n%s", secondPrompt)
	}

	if len(sh.convHistory) < 4 {
		t.Fatalf("expected conversation history to include follow-up response, got %d messages", len(sh.convHistory))
	}
}

func TestSendConversationReply_UnmarkedAssistantReplyKeepsConversationActive(t *testing.T) {
	var out bytes.Buffer

	transport := newScriptedConversationFlowTransport()
	handler := NewAgentHandler(agent.NewClient(transport))

	sh := &Shell{
		agentHandler:    handler,
		agentOutput:     NewAgentOutputCoordinator(&out),
		responseUI:      NewResponseUI(&out),
		conversation:    NewConversationState(),
		convUI:          NewConversationUI(&out, "#7c3aed"),
		convCancelArmed: false,
		convHistory: []conversationMessage{
			{role: "assistant", content: "Question 2: Is it something typically found indoors?"},
			{role: "user", content: "yes"},
		},
	}
	sh.conversation.Activate()
	sh.conversation.SetSubState(ConversationAwaitingInput)

	// First call in this transport is cancel-only, so do one canceled turn first.
	done := make(chan struct{})
	go func() {
		sh.sendConversationReply(context.Background(), "dummy")
		close(done)
	}()
	waitForConversationCall(t, transport.callStarted, 1, 2*time.Second)
	if !sh.cancelConversationTurn() {
		t.Fatal("expected active conversation turn to be cancelable")
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for canceled dummy turn")
	}

	// Second call returns an unmarked assistant reply (no [AWAITING_INPUT]).
	sh.sendConversationReply(context.Background(), "let's pause. show me my kubernetes clusters")
	waitForConversationCall(t, transport.callStarted, 2, 2*time.Second)

	if !sh.conversation.IsActive() {
		t.Fatal("conversation should remain active after tool-style assistant reply")
	}
	if got := sh.conversation.SubState(); got != ConversationAwaitingInput {
		t.Fatalf("conversation sub-state = %v, want %v", got, ConversationAwaitingInput)
	}
	if len(sh.convHistory) < 4 {
		t.Fatalf("expected conversation history to include follow-up assistant reply, got %d messages", len(sh.convHistory))
	}
}

func waitForConversationCall(t *testing.T, calls <-chan int, want int, timeout time.Duration) {
	t.Helper()

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	for {
		select {
		case got := <-calls:
			if got == want {
				return
			}
		case <-timer.C:
			t.Fatalf("timed out waiting for conversation call %d", want)
		}
	}
}

func containsAll(text string, needles ...string) bool {
	for _, needle := range needles {
		if !strings.Contains(text, needle) {
			return false
		}
	}
	return true
}
