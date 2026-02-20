package completion

import (
	"context"
	"testing"
)

func TestPipHandler_Uninstall(t *testing.T) {
	h := &PipHandler{
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
