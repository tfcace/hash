package completion

import (
	"context"
	"testing"
)

func TestKillHandler_PrefixFilter(t *testing.T) {
	h := &KillHandler{
		listProcesses: func(ctx context.Context) ([]processInfo, error) {
			return []processInfo{
				{PID: "123", Name: "bash"},
				{PID: "456", Name: "node"},
				{PID: "789", Name: "nginx"},
			}, nil
		},
	}

	result := h.Complete(context.Background(), nil, "n")
	// Should match "node", "nginx" (names) and no PIDs starting with "n"
	found := make(map[string]bool)
	for _, item := range result.Items {
		found[item.Value] = true
	}
	if !found["node"] || !found["nginx"] {
		t.Errorf("expected node and nginx, got %+v", result.Items)
	}
}

func TestKillHandler_PIDFilter(t *testing.T) {
	h := &KillHandler{
		listProcesses: func(ctx context.Context) ([]processInfo, error) {
			return []processInfo{
				{PID: "123", Name: "bash"},
				{PID: "1245", Name: "node"},
			}, nil
		},
	}

	result := h.Complete(context.Background(), nil, "12")
	found := make(map[string]bool)
	for _, item := range result.Items {
		found[item.Value] = true
	}
	if !found["123"] || !found["1245"] {
		t.Errorf("expected PIDs 123 and 1245, got %+v", result.Items)
	}
}

func TestKillHandler_NoMatch(t *testing.T) {
	h := &KillHandler{
		listProcesses: func(ctx context.Context) ([]processInfo, error) {
			return []processInfo{{PID: "123", Name: "bash"}}, nil
		},
	}

	result := h.Complete(context.Background(), nil, "xyz")
	if len(result.Items) != 0 {
		t.Fatalf("expected 0 items, got %d", len(result.Items))
	}
}

func TestKillHandler_SkipsFlags(t *testing.T) {
	h := &KillHandler{
		listProcesses: func(ctx context.Context) ([]processInfo, error) {
			return []processInfo{{PID: "123", Name: "bash"}}, nil
		},
	}

	result := h.Complete(context.Background(), nil, "-9")
	if len(result.Items) != 0 {
		t.Fatalf("expected 0 items for flag, got %d", len(result.Items))
	}
}
