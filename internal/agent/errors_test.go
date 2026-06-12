package agent

import (
	"context"
	"errors"
	"os/exec"
	"testing"
)

func TestIsStartupError(t *testing.T) {
	if !IsStartupError(errors.Join(ErrACPStartFailed, exec.ErrNotFound)) {
		t.Fatal("expected startup error to be detected")
	}
	if !IsStartupError(ErrACPUnsupportedAgent) {
		t.Fatal("expected unsupported agent to be detected as startup error")
	}
	if IsStartupError(context.DeadlineExceeded) {
		t.Fatal("deadline exceeded should not be classified as startup error")
	}
}

func TestIsTimeoutError(t *testing.T) {
	if !IsTimeoutError(context.DeadlineExceeded) {
		t.Fatal("expected deadline exceeded to be timeout error")
	}
	if !IsTimeoutError(ErrACPIdleTimeout) {
		t.Fatal("expected idle timeout sentinel to be timeout error")
	}
	if IsTimeoutError(ErrACPStartFailed) {
		t.Fatal("startup failure should not be timeout error")
	}
}

func TestIsRetryableError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "context canceled is not retryable",
			err:  context.Canceled,
			want: false,
		},
		{
			name: "startup failure is not retryable",
			err:  errors.Join(ErrACPStartFailed, exec.ErrNotFound),
			want: false,
		},
		{
			name: "deadline exceeded is retryable",
			err:  context.DeadlineExceeded,
			want: true,
		},
		{
			name: "connection closed is retryable",
			err:  ErrACPConnectionClosed,
			want: true,
		},
		{
			name: "no output is retryable",
			err:  ErrACPNoOutput,
			want: true,
		},
		{
			name: "broken pipe message fallback is retryable",
			err:  errors.New("write request: broken pipe"),
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsRetryableError(tt.err)
			if got != tt.want {
				t.Fatalf("IsRetryableError(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}
