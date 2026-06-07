package completion

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestBrewHandler_Uninstall(t *testing.T) {
	h := &BrewHandler{
		runCommand: func(ctx context.Context, name string, args ...string) ([]string, error) {
			if len(args) > 1 && args[1] == "--formula" {
				return []string{"git", "node", "python"}, nil
			}
			if len(args) > 1 && args[1] == "--cask" {
				return []string{"firefox", "chrome"}, nil
			}
			return nil, nil
		},
	}

	result := h.Complete(context.Background(), []string{"uninstall"}, "")
	if len(result.Items) != 5 {
		t.Fatalf("expected 5 items, got %d: %+v", len(result.Items), result.Items)
	}
}

func TestBrewHandler_PrefixFilter(t *testing.T) {
	h := &BrewHandler{
		runCommand: func(ctx context.Context, name string, args ...string) ([]string, error) {
			return []string{"git", "go", "node"}, nil
		},
	}

	result := h.Complete(context.Background(), []string{"upgrade"}, "g")
	found := 0
	for _, item := range result.Items {
		if item.Value == "git" || item.Value == "go" {
			found++
		}
	}
	if found != 2 {
		t.Errorf("expected 2 items matching 'g', got %d from %+v", found, result.Items)
	}
}

func TestBrewHandler_ListsFormulaeAndCasksInParallel(t *testing.T) {
	h := &BrewHandler{
		runCommand: func(ctx context.Context, name string, args ...string) ([]string, error) {
			select {
			case <-time.After(80 * time.Millisecond):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			if len(args) > 1 && args[1] == "--formula" {
				return []string{"git"}, nil
			}
			if len(args) > 1 && args[1] == "--cask" {
				return []string{"firefox"}, nil
			}
			return nil, nil
		},
	}

	start := time.Now()
	result := h.Complete(context.Background(), []string{"upgrade"}, "")
	elapsed := time.Since(start)

	if elapsed > 130*time.Millisecond {
		t.Fatalf("brew completion took %s, want formula and cask queries in parallel", elapsed)
	}
	if len(result.Items) != 2 {
		t.Fatalf("expected formula and cask items, got %#v", result.Items)
	}
}

func TestBrewHandler_ReturnsWhenListCommandIgnoresContext(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	h := &BrewHandler{
		runCommand: func(ctx context.Context, name string, args ...string) ([]string, error) {
			<-release
			return []string{"git"}, nil
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	done := make(chan Result, 1)
	start := time.Now()
	go func() {
		done <- h.Complete(ctx, []string{"upgrade"}, "")
	}()

	select {
	case result := <-done:
		elapsed := time.Since(start)
		if elapsed > 100*time.Millisecond {
			t.Fatalf("brew completion took %s after context cancellation, want under 100ms", elapsed)
		}
		if len(result.Items) != 0 {
			t.Fatalf("expected no items after context cancellation, got %#v", result.Items)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("brew completion did not return after context cancellation")
	}
}

func TestBrewHandler_CachesInstalledPackages(t *testing.T) {
	var calls atomic.Int32
	h := &BrewHandler{
		cacheTTL: time.Minute,
		runCommand: func(ctx context.Context, name string, args ...string) ([]string, error) {
			calls.Add(1)
			if len(args) > 1 && args[1] == "--formula" {
				return []string{"git"}, nil
			}
			if len(args) > 1 && args[1] == "--cask" {
				return []string{"firefox"}, nil
			}
			return nil, nil
		},
	}

	first := h.Complete(context.Background(), []string{"upgrade"}, "g")
	second := h.Complete(context.Background(), []string{"upgrade"}, "f")

	if calls.Load() != 2 {
		t.Fatalf("expected one formula and one cask query across repeated completions, got %d calls", calls.Load())
	}
	if len(first.Items) != 1 || first.Items[0].Value != "git" {
		t.Fatalf("unexpected first result: %#v", first.Items)
	}
	if len(second.Items) != 1 || second.Items[0].Value != "firefox" {
		t.Fatalf("unexpected cached second result: %#v", second.Items)
	}
}

func TestBrewHandler_UnsupportedSubcommand(t *testing.T) {
	h := &BrewHandler{
		runCommand: func(ctx context.Context, name string, args ...string) ([]string, error) {
			t.Fatal("should not query packages for unsupported subcommand")
			return nil, nil
		},
	}

	result := h.Complete(context.Background(), []string{"install"}, "")
	if len(result.Items) != 0 {
		t.Fatalf("expected 0 items for 'install', got %d", len(result.Items))
	}
}

func TestBrewHandler_EmptyArgs(t *testing.T) {
	h := &BrewHandler{
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
