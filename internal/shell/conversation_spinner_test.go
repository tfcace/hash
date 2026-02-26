package shell

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"
)

func TestRunConversationSpinner_RendersThinkingWhenNotBlocked(t *testing.T) {
	var out bytes.Buffer
	sh := &Shell{
		agentOutput: NewAgentOutputCoordinator(&out),
		convUI:      NewConversationUI(&out, "#7c3aed"),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 220*time.Millisecond)
	defer cancel()
	done := make(chan struct{})
	go sh.runConversationSpinner(ctx, done)
	<-done

	if !strings.Contains(out.String(), "Agent thinking...") {
		t.Fatalf("expected spinner text in output, got %q", out.String())
	}
}

func TestRunConversationSpinner_PausesDuringPermissionPrompt(t *testing.T) {
	var out bytes.Buffer
	sh := &Shell{
		agentOutput: NewAgentOutputCoordinator(&out),
		convUI:      NewConversationUI(&out, "#7c3aed"),
	}
	sh.agentOutput.EnterPermission()

	ctx, cancel := context.WithTimeout(context.Background(), 220*time.Millisecond)
	defer cancel()
	done := make(chan struct{})
	go sh.runConversationSpinner(ctx, done)
	<-done

	if strings.Contains(out.String(), "Agent thinking...") {
		t.Fatalf("spinner should be paused during permission prompt, got %q", out.String())
	}
}
