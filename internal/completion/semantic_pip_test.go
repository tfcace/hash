package completion

import (
	"context"
	"errors"
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
