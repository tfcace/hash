package completion

import (
	"context"
	"os/exec"
	"testing"
	"time"
)

func TestCobraCompleter_PrefetchAndComplete(t *testing.T) {
	// Check if kubectl is available for testing
	_, err := exec.LookPath("kubectl")
	if err != nil {
		t.Skip("kubectl not available")
	}

	completer := NewCobraCompleter()
	ctx := context.Background()

	// Before prefetch, Complete should return nothing
	result, err := completer.Complete(ctx, "kubectl get ", 12)
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if len(result.Items) != 0 {
		t.Errorf("Expected 0 items before prefetch, got %d", len(result.Items))
	}

	// Trigger prefetch
	completer.Prefetch("kubectl get ", 12)

	// Wait for background prefetch to complete
	time.Sleep(300 * time.Millisecond)

	// Now Complete should return cached results
	result, err = completer.Complete(ctx, "kubectl get ", 12)
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}

	// Should have some completions (pods, deployments, etc.)
	if len(result.Items) > 0 {
		t.Logf("Got %d completions after prefetch", len(result.Items))
	}
}

func TestCobraCompleter_NonCobraCommand(t *testing.T) {
	completer := NewCobraCompleter()
	ctx := context.Background()

	// ls is not a Cobra command - prefetch should fail silently
	completer.Prefetch("ls -", 4)
	time.Sleep(300 * time.Millisecond)

	result, _ := completer.Complete(ctx, "ls -", 4)
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

func TestCobraCompleter_NeverBlocks(t *testing.T) {
	completer := NewCobraCompleter()
	ctx := context.Background()

	// Complete should return immediately without results (no prefetch done)
	start := time.Now()
	result, _ := completer.Complete(ctx, "kubectl get ", 12)
	elapsed := time.Since(start)

	// Should complete in under 10ms (just cache lookup)
	if elapsed > 10*time.Millisecond {
		t.Errorf("Complete took %v, expected < 10ms", elapsed)
	}

	if len(result.Items) != 0 {
		t.Errorf("Expected 0 items without prefetch, got %d", len(result.Items))
	}
}
