package completion

import (
	"context"
	"strconv"
	"sync/atomic"
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

func TestKillHandler_LimitsLargeProcessList(t *testing.T) {
	processes := make([]processInfo, 5000)
	for i := range processes {
		pid := strconv.Itoa(10000 + i)
		processes[i] = processInfo{PID: pid, Name: "proc-" + pid}
	}
	h := &KillHandler{
		command: "kill",
		listProcesses: func(ctx context.Context) ([]processInfo, error) {
			return processes, nil
		},
	}

	result := h.Complete(context.Background(), nil, "1")
	if len(result.Items) > completionItemLimit {
		t.Fatalf("kill completion returned %d items, want at most %d", len(result.Items), completionItemLimit)
	}
	if len(result.Items) != completionItemLimit {
		t.Fatalf("kill completion returned %d items, want %d", len(result.Items), completionItemLimit)
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

func TestKillHandler_CoalescesFreshBlockedLookupAndRetriesWhenStale(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	var calls atomic.Int32
	h := &KillHandler{
		command: "kill",
		listProcesses: func(ctx context.Context) ([]processInfo, error) {
			call := calls.Add(1)
			if call == 1 {
				<-release
				return []processInfo{{PID: "999", Name: "stale"}}, nil
			}
			return []processInfo{{PID: "123", Name: "bash"}}, nil
		},
	}

	for i := 0; i < 2; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
		result := h.Complete(ctx, nil, "")
		cancel()
		if len(result.Items) != 0 {
			t.Fatalf("expected no items from blocked process lookup, got %#v", result.Items)
		}
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("expected second blocked kill completion to coalesce, got %d process lookups", got)
	}

	time.Sleep(90 * time.Millisecond)
	result := h.Complete(context.Background(), nil, "1")
	if got := calls.Load(); got != 2 {
		t.Fatalf("expected stale process lookup to retry, got %d process lookups", got)
	}
	if len(result.Items) != 1 || result.Items[0].Value != "123" {
		t.Fatalf("expected fresh process completion after stale retry, got %#v", result.Items)
	}
}
