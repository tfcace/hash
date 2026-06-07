package completion

import (
	"context"
	"testing"
	"time"
)

func TestKillHandler_PrefixFilter(t *testing.T) {
	h := &KillHandler{
		command: "killall",
		listProcesses: func(ctx context.Context) ([]processInfo, error) {
			return []processInfo{
				{PID: "123", Name: "bash"},
				{PID: "456", Name: "node"},
				{PID: "789", Name: "nginx"},
			}, nil
		},
	}

	result := h.Complete(context.Background(), nil, "n")
	// Should match "node", "nginx" (names) only.
	found := make(map[string]bool)
	for _, item := range result.Items {
		found[item.Value] = true
	}
	if !found["node"] || !found["nginx"] {
		t.Errorf("expected node and nginx, got %+v", result.Items)
	}
	if found["123"] || found["456"] || found["789"] {
		t.Errorf("killall should not suggest PIDs, got %+v", result.Items)
	}
}

func TestKillHandler_PIDFilter(t *testing.T) {
	h := &KillHandler{
		command: "kill",
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
	if found["bash"] || found["node"] {
		t.Errorf("kill should not suggest process names, got %+v", result.Items)
	}
}

func TestKillHandler_NoMatch(t *testing.T) {
	h := &KillHandler{
		command: "killall",
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
		command: "kill",
		listProcesses: func(ctx context.Context) ([]processInfo, error) {
			return []processInfo{{PID: "123", Name: "bash"}}, nil
		},
	}

	result := h.Complete(context.Background(), nil, "-9")
	if len(result.Items) != 0 {
		t.Fatalf("expected 0 items for flag, got %d", len(result.Items))
	}
}

func TestKillHandler_KillallSkipsNumericInput(t *testing.T) {
	h := &KillHandler{
		command: "killall",
		listProcesses: func(ctx context.Context) ([]processInfo, error) {
			return []processInfo{
				{PID: "123", Name: "bash"},
				{PID: "456", Name: "node"},
			}, nil
		},
	}

	result := h.Complete(context.Background(), nil, "12")
	if len(result.Items) != 0 {
		t.Fatalf("expected no PID suggestions for killall, got %+v", result.Items)
	}
}

func TestKillHandler_CachesProcessList(t *testing.T) {
	calls := 0
	h := &KillHandler{
		command:  "kill",
		cacheTTL: time.Second,
		listProcesses: func(ctx context.Context) ([]processInfo, error) {
			calls++
			return []processInfo{
				{PID: "123", Name: "bash"},
				{PID: "456", Name: "node"},
			}, nil
		},
	}

	first := h.Complete(context.Background(), nil, "1")
	second := h.Complete(context.Background(), nil, "4")

	if calls != 1 {
		t.Fatalf("expected one process lookup across repeated completions, got %d", calls)
	}
	if len(first.Items) != 1 || first.Items[0].Value != "123" {
		t.Fatalf("unexpected first result: %#v", first.Items)
	}
	if len(second.Items) != 1 || second.Items[0].Value != "456" {
		t.Fatalf("unexpected cached second result: %#v", second.Items)
	}
}

func TestKillHandler_ReturnsWhenProcessListIgnoresContext(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	h := &KillHandler{
		command: "kill",
		listProcesses: func(ctx context.Context) ([]processInfo, error) {
			<-release
			return []processInfo{{PID: "123", Name: "bash"}}, nil
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	done := make(chan Result, 1)
	start := time.Now()
	go func() {
		done <- h.Complete(ctx, nil, "")
	}()

	select {
	case result := <-done:
		elapsed := time.Since(start)
		if elapsed > 100*time.Millisecond {
			t.Fatalf("kill completion took %s after context cancellation, want under 100ms", elapsed)
		}
		if len(result.Items) != 0 {
			t.Fatalf("expected no items after context cancellation, got %#v", result.Items)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("kill completion did not return after context cancellation")
	}
}
