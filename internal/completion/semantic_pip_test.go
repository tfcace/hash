package completion

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestPipHandler_Uninstall(t *testing.T) {
	h := &PipHandler{
		command: "pip3",
		runCommand: func(ctx context.Context, name string, args ...string) ([]string, error) {
			return []string{
				"requests==2.28.0",
				"flask==2.2.0",
				"numpy==1.23.0",
			}, nil
		},
	}

	result := h.Complete(context.Background(), []string{"uninstall"}, "")
	if len(result.Items) != 3 {
		t.Fatalf("expected 3 items, got %d", len(result.Items))
	}
	// Should strip version
	for _, item := range result.Items {
		if item.Value == "requests" || item.Value == "flask" || item.Value == "numpy" {
			continue
		}
		t.Errorf("unexpected item %q (should strip version)", item.Value)
	}
}

func TestPipHandler_PrefixFilter(t *testing.T) {
	h := &PipHandler{
		command: "pip3",
		runCommand: func(ctx context.Context, name string, args ...string) ([]string, error) {
			return []string{
				"requests==2.28.0",
				"flask==2.2.0",
			}, nil
		},
	}

	result := h.Complete(context.Background(), []string{"uninstall"}, "req")
	if len(result.Items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(result.Items))
	}
}

func TestPipHandler_OnlyUninstall(t *testing.T) {
	h := &PipHandler{
		command: "pip3",
		runCommand: func(ctx context.Context, name string, args ...string) ([]string, error) {
			t.Fatal("should not query for non-uninstall")
			return nil, nil
		},
	}

	result := h.Complete(context.Background(), []string{"install"}, "")
	if len(result.Items) != 0 {
		t.Fatalf("expected 0 items for 'install', got %d", len(result.Items))
	}
}

func TestPipHandler_EmptyArgs(t *testing.T) {
	h := &PipHandler{
		command: "pip3",
		runCommand: func(ctx context.Context, name string, args ...string) ([]string, error) {
			t.Fatal("should not query for empty args")
			return nil, nil
		},
	}

	result := h.Complete(context.Background(), nil, "")
	if len(result.Items) != 0 {
		t.Fatalf("expected 0 items, got %d", len(result.Items))
	}
}

func TestPipHandler_UsesConfiguredCommand(t *testing.T) {
	var gotName string
	h := &PipHandler{
		command: "pip",
		runCommand: func(ctx context.Context, name string, args ...string) ([]string, error) {
			gotName = name
			return []string{"requests==2.28.0"}, nil
		},
	}

	result := h.Complete(context.Background(), []string{"uninstall"}, "")
	if len(result.Items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(result.Items))
	}
	if gotName != "pip" {
		t.Fatalf("expected pip command, got %q", gotName)
	}
}

func TestPipHandler_CachesInstalledPackages(t *testing.T) {
	calls := 0
	h := &PipHandler{
		command:  "pip3",
		cacheTTL: time.Minute,
		runCommand: func(ctx context.Context, name string, args ...string) ([]string, error) {
			calls++
			return []string{"requests==2.28.0", "flask==2.2.0"}, nil
		},
	}

	first := h.Complete(context.Background(), []string{"uninstall"}, "req")
	second := h.Complete(context.Background(), []string{"uninstall"}, "fla")

	if calls != 1 {
		t.Fatalf("expected one pip freeze query across repeated completions, got %d", calls)
	}
	if len(first.Items) != 1 || first.Items[0].Value != "requests" {
		t.Fatalf("unexpected first result: %#v", first.Items)
	}
	if len(second.Items) != 1 || second.Items[0].Value != "flask" {
		t.Fatalf("unexpected cached second result: %#v", second.Items)
	}
}

func TestPipHandler_CachesFailedPackageLookup(t *testing.T) {
	calls := 0
	h := &PipHandler{
		command:  "pip3",
		cacheTTL: time.Minute,
		runCommand: func(ctx context.Context, name string, args ...string) ([]string, error) {
			calls++
			return nil, errors.New("pip unavailable")
		},
	}

	first := h.Complete(context.Background(), []string{"uninstall"}, "")
	second := h.Complete(context.Background(), []string{"uninstall"}, "")

	if calls != 1 {
		t.Fatalf("expected failed pip lookup to be cached, got %d calls", calls)
	}
	if len(first.Items) != 0 || len(second.Items) != 0 {
		t.Fatalf("expected no completions, got first=%#v second=%#v", first.Items, second.Items)
	}
}

func TestPipHandler_ReturnsWhenFreezeCommandIgnoresContext(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	h := &PipHandler{
		command: "pip3",
		runCommand: func(ctx context.Context, name string, args ...string) ([]string, error) {
			<-release
			return []string{"requests==2.28.0"}, nil
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	done := make(chan Result, 1)
	start := time.Now()
	go func() {
		done <- h.Complete(ctx, []string{"uninstall"}, "")
	}()

	select {
	case result := <-done:
		elapsed := time.Since(start)
		if elapsed > 100*time.Millisecond {
			t.Fatalf("pip completion took %s after context cancellation, want under 100ms", elapsed)
		}
		if len(result.Items) != 0 {
			t.Fatalf("expected no items after context cancellation, got %#v", result.Items)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("pip completion did not return after context cancellation")
	}
}

func TestPipHandler_CoalescesFreshBlockedFreezeAndRetriesWhenStale(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	var calls atomic.Int32
	h := &PipHandler{
		command: "pip3",
		runCommand: func(ctx context.Context, name string, args ...string) ([]string, error) {
			if calls.Add(1) == 1 {
				<-release
				return []string{"stale==1.0"}, nil
			}
			return []string{"fresh==1.0"}, nil
		},
	}

	for i := 0; i < 2; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
		result := h.Complete(ctx, []string{"uninstall"}, "")
		cancel()
		if len(result.Items) != 0 {
			t.Fatalf("expected no items from blocked freeze, got %#v", result.Items)
		}
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("expected second blocked freeze completion to coalesce, got %d calls", got)
	}

	time.Sleep(90 * time.Millisecond)
	result := h.Complete(context.Background(), []string{"uninstall"}, "")
	if got := calls.Load(); got != 2 {
		t.Fatalf("expected stale blocked freeze to be retried, got %d calls", got)
	}
	if len(result.Items) != 1 || result.Items[0].Value != "fresh" {
		t.Fatalf("expected fresh pip package after stale retry, got %#v", result.Items)
	}
}

func TestPipHandler_DoesNotCacheCanceledPackageLookup(t *testing.T) {
	release := make(chan struct{})
	finished := make(chan struct{})
	var first atomic.Bool
	first.Store(true)
	h := &PipHandler{
		command:  "pip3",
		cacheTTL: time.Minute,
		runCommand: func(ctx context.Context, name string, args ...string) ([]string, error) {
			if first.Swap(false) {
				<-release
				close(finished)
			}
			return []string{"requests==2.28.0"}, nil
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	result := h.Complete(ctx, []string{"uninstall"}, "")
	cancel()
	if len(result.Items) != 0 {
		t.Fatalf("expected no items after timeout, got %#v", result.Items)
	}

	close(release)
	<-finished

	result = h.Complete(context.Background(), []string{"uninstall"}, "")
	if len(result.Items) != 1 || result.Items[0].Value != "requests" {
		t.Fatalf("canceled lookup should not cache an empty result, got %#v", result.Items)
	}
}
