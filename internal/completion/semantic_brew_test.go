package completion

import (
	"context"
	"testing"
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
