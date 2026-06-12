package completion

import (
	"context"
	"testing"
	"time"
)

func TestRunIsolatedCommandReturnsWhenChildKeepsStdoutOpen(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, _ = runIsolatedCommand(ctx, "sh", "-c", "sleep 0.25 &")
	elapsed := time.Since(start)

	if elapsed > 150*time.Millisecond {
		t.Fatalf("isolated command waited %s for child-held stdout pipe, want under 150ms", elapsed)
	}
}
