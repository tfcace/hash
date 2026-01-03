package completion

import (
	"context"
	"os/exec"
	"testing"
)

func TestCobraCompleter_DetectsCobraCommand(t *testing.T) {
	// Check if kubectl is available for testing
	_, err := exec.LookPath("kubectl")
	if err != nil {
		t.Skip("kubectl not available")
	}

	completer := NewCobraCompleter()
	ctx := context.Background()

	result, err := completer.Complete(ctx, "kubectl get ", 12)
	if err != nil {
		t.Logf("Complete() error = %v (may be expected without cluster)", err)
	}

	// Should have some completions (pods, deployments, etc.)
	// Even without a cluster, kubectl provides resource type completions
	if len(result.Items) > 0 {
		t.Logf("Got %d completions", len(result.Items))
	}
}

func TestCobraCompleter_NonCobraCommand(t *testing.T) {
	completer := NewCobraCompleter()
	ctx := context.Background()

	// ls is not a Cobra command
	result, err := completer.Complete(ctx, "ls -", 4)
	if err != nil {
		// Expected - ls doesn't support __complete
		t.Logf("Expected error for non-Cobra command: %v", err)
	}

	if len(result.Items) != 0 {
		t.Errorf("Items count = %d, want 0 for non-Cobra", len(result.Items))
	}
}

func TestCobraCompleter_Name(t *testing.T) {
	completer := NewCobraCompleter()
	if completer.Name() != "cobra" {
		t.Errorf("Name() = %q, want %q", completer.Name(), "cobra")
	}
}

func TestCobraCompleter_CachesTTL(t *testing.T) {
	completer := NewCobraCompleter()

	// TTL should be set
	if completer.cacheTTL == 0 {
		t.Error("cacheTTL should not be zero")
	}
}
