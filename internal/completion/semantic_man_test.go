package completion

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestManHandler_ReturnsWhenAproposCommandIgnoresContext(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	h := &ManHandler{
		runCommand: func(ctx context.Context, name string, args ...string) ([]string, error) {
			<-release
			return []string{"printf (1) - formatted output"}, nil
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	done := make(chan Result, 1)
	start := time.Now()
	go func() {
		done <- h.Complete(ctx, nil, "pri")
	}()

	select {
	case result := <-done:
		elapsed := time.Since(start)
		if elapsed > 100*time.Millisecond {
			t.Fatalf("man completion took %s after context cancellation, want under 100ms", elapsed)
		}
		if len(result.Items) != 0 {
			t.Fatalf("expected no items after context cancellation, got %#v", result.Items)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("man completion did not return after context cancellation")
	}
}

// A blocked lookup is joined rather than duplicated.
func TestManHandler_CoalescesBlockedAproposLookupAndRefetchesAfterItLands(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	var calls atomic.Int32
	h := &ManHandler{
		runCommand: func(ctx context.Context, name string, args ...string) ([]string, error) {
			call := calls.Add(1)
			if call == 1 {
				<-release
				return []string{"stale (1) - old page"}, nil
			}
			return []string{"printf (1) - formatted output"}, nil
		},
	}

	for i := 0; i < 2; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
		result := h.Complete(ctx, nil, "pri")
		cancel()
		if len(result.Items) != 0 {
			t.Fatalf("expected no items from blocked apropos lookup, got %#v", result.Items)
		}
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("expected second blocked man completion to coalesce, got %d apropos calls", got)
	}

	// The blocked lookup lands; with no cache TTL its answer went to its
	// waiters only, so the next Tab queries again.
	release <- struct{}{}
	time.Sleep(20 * time.Millisecond)
	result := h.Complete(context.Background(), nil, "pri")
	if got := calls.Load(); got != 2 {
		t.Fatalf("expected apropos lookup to run again after it landed, got %d calls", got)
	}
	if len(result.Items) != 1 || result.Items[0].Value != "printf" {
		t.Fatalf("expected fresh man completion after the blocked lookup landed, got %#v", result.Items)
	}
}
