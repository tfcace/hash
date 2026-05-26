package agent

import (
	"context"
	"testing"
	"time"
)

func TestIdleTimeoutForContext(t *testing.T) {
	t.Run("no deadline uses default idle timeout", func(t *testing.T) {
		got := idleTimeoutForContext(context.Background())
		if got != IdleTimeout {
			t.Fatalf("idleTimeoutForContext(background) = %v, want %v", got, IdleTimeout)
		}
	})

	t.Run("short deadline still keeps default idle timeout", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		defer cancel()

		got := idleTimeoutForContext(ctx)
		if got != IdleTimeout {
			t.Fatalf("idleTimeoutForContext(short) = %v, want %v", got, IdleTimeout)
		}
	})

	t.Run("long deadline extends idle timeout", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*IdleTimeout)
		defer cancel()

		got := idleTimeoutForContext(ctx)
		if got <= IdleTimeout {
			t.Fatalf("idleTimeoutForContext(long) = %v, want > %v", got, IdleTimeout)
		}
	})
}
