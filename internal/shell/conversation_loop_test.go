package shell

import (
	"context"
	"testing"
	"time"

	"github.com/tfcace/hash/internal/config"
)

func TestRunConversationLoop_DefaultIdleTimeoutIsTenMinutes(t *testing.T) {
	cfg := config.Default()
	cfg.Agent.ConversationIdleTimeout = "" // Use default fallback in runConversationLoop

	sh, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer sh.Close()

	sh.conversation.Activate()
	sh.conversation.SetSubState(ConversationAwaitingInput)

	var observed time.Duration
	sh.conversationInputHook = func(ctx context.Context) (string, error) {
		deadline, ok := ctx.Deadline()
		if !ok {
			t.Fatal("expected conversation input context to have a deadline")
		}
		observed = time.Until(deadline)
		return "", ErrEditorEOF // Exit loop quickly
	}

	sh.runConversationLoop(context.Background())

	if sh.conversation.IsActive() {
		t.Fatal("conversation should be inactive after loop exits")
	}

	const want = 10 * time.Minute
	if observed < want-2*time.Second || observed > want+2*time.Second {
		t.Fatalf("default idle timeout = %v, want about %v", observed, want)
	}
}

func TestRunConversationLoop_ConfiguredIdleTimeoutTriggersExit(t *testing.T) {
	cfg := config.Default()
	cfg.Agent.ConversationIdleTimeout = "40ms"

	sh, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer sh.Close()

	sh.conversation.Activate()
	sh.conversation.SetSubState(ConversationAwaitingInput)

	timeoutCancelCalled := false
	sh.agentTimeoutCancel = func() {
		timeoutCancelCalled = true
	}

	var observed time.Duration
	sh.conversationInputHook = func(ctx context.Context) (string, error) {
		deadline, ok := ctx.Deadline()
		if !ok {
			t.Fatal("expected conversation input context to have a deadline")
		}
		observed = time.Until(deadline)
		<-ctx.Done()
		return "", ctx.Err()
	}

	start := time.Now()
	sh.runConversationLoop(context.Background())
	elapsed := time.Since(start)

	if !timeoutCancelCalled {
		t.Fatal("expected per-request timeout cancel to be called when entering conversation loop")
	}
	if sh.conversation.IsActive() {
		t.Fatal("conversation should be inactive after idle timeout")
	}

	if observed < 10*time.Millisecond || observed > 500*time.Millisecond {
		t.Fatalf("configured idle timeout context looked wrong: %v", observed)
	}
	if elapsed < 20*time.Millisecond || elapsed > 2*time.Second {
		t.Fatalf("loop elapsed = %v, expected idle-timeout-driven exit", elapsed)
	}
}
