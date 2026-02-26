package shell

import (
	"bytes"
	"context"
	"strings"
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

func TestRunConversationLoop_DoubleCtrlCShowsBottomBorder(t *testing.T) {
	cfg := config.Default()

	sh, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer sh.Close()

	var out bytes.Buffer
	sh.convUI = NewConversationUI(&out, "#7c3aed")
	sh.conversation.Activate()
	sh.conversation.SetSubState(ConversationAwaitingInput)

	attempt := 0
	sh.conversationInputHook = func(ctx context.Context) (string, error) {
		attempt++
		return "", ErrEditorCanceled
	}

	sh.runConversationLoop(context.Background())

	if sh.conversation.IsActive() {
		t.Fatal("conversation should be inactive after second Ctrl+C cancel")
	}
	if attempt < 2 {
		t.Fatalf("expected two input attempts (arm + exit), got %d", attempt)
	}

	output := out.String()
	if !strings.Contains(output, "Press Ctrl+C again to exit") {
		t.Fatalf("expected cancel hint in output, got %q", output)
	}
	if !strings.Contains(output, "╰") {
		t.Fatalf("expected bottom border in output, got %q", output)
	}
}

func TestRunConversationLoop_ContextCanceledShowsBottomBorder(t *testing.T) {
	cfg := config.Default()

	sh, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer sh.Close()

	var out bytes.Buffer
	sh.convUI = NewConversationUI(&out, "#7c3aed")
	sh.conversation.Activate()
	sh.conversation.SetSubState(ConversationAwaitingInput)

	sh.conversationInputHook = func(ctx context.Context) (string, error) {
		<-ctx.Done()
		return "", ctx.Err()
	}

	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()

	sh.runConversationLoop(canceledCtx)

	if sh.conversation.IsActive() {
		t.Fatal("conversation should be inactive after context cancellation")
	}
	output := out.String()
	if !strings.Contains(output, "╰") {
		t.Fatalf("expected bottom border in output after context cancel, got %q", output)
	}
	if got := strings.Count(output, "\x1b[2K"); got < 4 {
		t.Fatalf("expected context-cancel path to clear user frame before exit, got %d clear ops in %q", got, output)
	}
}
