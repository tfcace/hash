package completion

import (
	"context"
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
