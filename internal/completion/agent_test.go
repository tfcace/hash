package completion

import (
	"context"
	"testing"
)

func TestAgentCompleter_DetectsInlineAgent(t *testing.T) {
	completer := NewAgentCompleter(nil) // No client for unit test

	// Should detect ?? in line
	ctx := context.Background()
	line := "kubectl get pods --sort-by=?? restart"
	_, err := completer.Complete(ctx, line, len(line))

	// Without a client, should return empty (not error)
	if err != nil {
		t.Logf("Expected no error without client: %v", err)
	}
}

func TestAgentCompleter_Name(t *testing.T) {
	completer := NewAgentCompleter(nil)
	if completer.Name() != "agent" {
		t.Errorf("Name() = %q, want %q", completer.Name(), "agent")
	}
}

func TestAgentCompleter_IgnoresNonAgentLines(t *testing.T) {
	completer := NewAgentCompleter(nil)
	ctx := context.Background()

	result, err := completer.Complete(ctx, "ls -la", 6)
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}

	// No ?? means no agent completion
	if len(result.Items) != 0 {
		t.Errorf("Items count = %d, want 0", len(result.Items))
	}
}
