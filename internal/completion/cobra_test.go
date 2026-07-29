package completion

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestCobraCompleter_PrefetchAndComplete(t *testing.T) {
	// Check if kubectl is available for testing
	_, err := exec.LookPath("kubectl")
	if err != nil {
		t.Skip("kubectl not available")
	}

	completer := NewCobraCompleter()
	ctx := context.Background()

	// Before prefetch, Complete should return nothing
	result, err := completer.Complete(ctx, "kubectl get ", 12)
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if len(result.Items) != 0 {
		t.Errorf("Expected 0 items before prefetch, got %d", len(result.Items))
	}

	// Trigger prefetch
	completer.Prefetch("kubectl get ", 12)

	// Wait for background prefetch to complete
	time.Sleep(300 * time.Millisecond)

	// Now Complete should return cached results
	result, err = completer.Complete(ctx, "kubectl get ", 12)
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}

	// Should have some completions (pods, deployments, etc.)
	if len(result.Items) > 0 {
		t.Logf("Got %d completions after prefetch", len(result.Items))
	}
}

func TestCobraCompleter_ClampsCursorBeforeSlicing(t *testing.T) {
	completer := NewCobraCompleter()

	if _, err := completer.Complete(context.Background(), "kubectl get ", len("kubectl get ")+10); err != nil {
		t.Fatalf("Complete() with oversized cursor error = %v", err)
	}
	if _, err := completer.Complete(context.Background(), "kubectl get ", -1); err != nil {
		t.Fatalf("Complete() with negative cursor error = %v", err)
	}

	completer.Prefetch("kubectl get ", len("kubectl get ")+10)
	completer.Prefetch("kubectl get ", -1)
}

func TestCobraCompleter_NonCobraCommand(t *testing.T) {
	completer := NewCobraCompleter()
	ctx := context.Background()

	// ls is not a Cobra command - prefetch should fail silently
	completer.Prefetch("ls -", 4)
	time.Sleep(300 * time.Millisecond)

	result, _ := completer.Complete(ctx, "ls -", 4)
	if len(result.Items) != 0 {
		t.Errorf("Items count = %d, want 0 for non-Cobra", len(result.Items))
	}
}

func TestCobraCompleter_CompleteUsesCacheOnly(t *testing.T) {
	tmpDir := t.TempDir()
	cmdPath := filepath.Join(tmpDir, "fakecobra")
	if err := os.WriteFile(cmdPath, []byte("#!/bin/sh\necho unexpected\n"), 0o755); err != nil {
		t.Fatalf("WriteFile(%q): %v", cmdPath, err)
	}
	t.Setenv("PATH", tmpDir)

	completer := NewCobraCompleter()
	result, err := completer.Complete(context.Background(), "fakecobra get ", len("fakecobra get "))
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if len(result.Items) != 0 {
		t.Fatalf("Complete() without prefetch should return no items, got %#v", result.Items)
	}

	completer.lookPathCacheMu.RLock()
	_, cached := completer.lookPathCache["fakecobra"]
	completer.lookPathCacheMu.RUnlock()
	if cached {
		t.Fatal("Complete should not scan PATH or populate lookPath cache")
	}
}

func TestCobraCompleter_SkipsShellBuiltinCd(t *testing.T) {
	tmpDir := t.TempDir()
	cmdPath := filepath.Join(tmpDir, "cd")
	if err := os.WriteFile(cmdPath, []byte("#!/bin/sh\necho cobra-cd\\tdescription\n"), 0o755); err != nil {
		t.Fatalf("WriteFile(%q): %v", cmdPath, err)
	}
	t.Setenv("PATH", tmpDir)

	completer := NewCobraCompleter()
	completer.Prefetch("cd ", len("cd "))
	completer.lookPathCacheMu.RLock()
	_, cachedPath := completer.lookPathCache["cd"]
	completer.lookPathCacheMu.RUnlock()
	if cachedPath {
		t.Fatal("Prefetch should not scan PATH for shell builtin cd")
	}

	cacheKey := cmdPath + ":" + strings.Join([]string{"__complete", ""}, " ")
	completer.lookPathCacheMu.Lock()
	completer.lookPathCache["cd"] = cmdPath
	completer.lookPathCacheMu.Unlock()
	completer.cacheMu.Lock()
	completer.cache[cacheKey] = cachedResult{
		result:    Result{Items: []Item{{Value: "cobra-cd"}}},
		expiresAt: time.Now().Add(time.Minute),
	}
	completer.cacheMu.Unlock()

	result, err := completer.Complete(context.Background(), "cd ", len("cd "))
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if len(result.Items) != 0 {
		t.Fatalf("cd should bypass Cobra completion, got %#v", result.Items)
	}
}

// Narrowing an open cobra menu must not fall through to filenames: a cache
// miss for a tool that has answered __complete before reports pending,
// fetches in the background, and notifies so the menu can refresh.
func TestCobraCompleter_PendingOnMissForKnownCobraTool(t *testing.T) {
	tmpDir := t.TempDir()
	cmdPath := filepath.Join(tmpDir, "fakekubectl")
	script := "#!/bin/sh\nif [ \"$1\" = \"__complete\" ]; then printf 'annotate\\tdesc\\napply\\tdesc\\n:4\\n'; exit 0; fi\nexit 1\n"
	if err := os.WriteFile(cmdPath, []byte(script), 0o755); err != nil { //nolint:gosec // test fixture must be executable
		t.Fatalf("WriteFile: %v", err)
	}

	completer := NewCobraCompleter()
	completer.resolvePath = func(name string) (string, error) { return cmdPath, nil }
	completer.prefetchTimeout = 5 * time.Second // Loaded CI machines exceed the production 100ms.
	ready := make(chan struct{}, 8)
	completer.SetOnReady(func() { ready <- struct{}{} })

	waitReady := func(step string) {
		t.Helper()
		select {
		case <-ready:
		case <-time.After(5 * time.Second):
			t.Fatalf("%s: no ready notification", step)
		}
	}

	// Warm the subcommand list, as the space-triggered prefetch would.
	completer.Prefetch("fakekubectl ", len("fakekubectl "))
	waitReady("warm prefetch")

	// The user types "a" while the menu is open: novel key, known tool.
	line := "fakekubectl a"
	result, err := completer.Complete(context.Background(), line, len(line))
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if !result.Pending || len(result.Items) != 0 {
		t.Fatalf("result = %+v, want pending with no items (never filenames)", result)
	}

	waitReady("pending fetch")
	result, err = completer.Complete(context.Background(), line, len(line))
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if result.Pending || len(result.Items) != 2 {
		t.Fatalf("after fetch: result = %+v, want the two candidates", result)
	}
}

// A tool that has never answered __complete keeps the old fall-through on a
// cache miss; pending would wrongly block file completion for it.
func TestCobraCompleter_MissForUnknownToolFallsThrough(t *testing.T) {
	tmpDir := t.TempDir()
	cmdPath := filepath.Join(tmpDir, "plaintool")
	if err := os.WriteFile(cmdPath, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil { //nolint:gosec // test fixture must be executable
		t.Fatalf("WriteFile: %v", err)
	}

	completer := NewCobraCompleter()
	completer.resolvePath = func(name string) (string, error) { return cmdPath, nil }
	completer.lookPathCacheMu.Lock()
	completer.lookPathCache["plaintool"] = cmdPath
	completer.lookPathCacheMu.Unlock()

	line := "plaintool a"
	result, err := completer.Complete(context.Background(), line, len(line))
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if result.Pending || len(result.Items) != 0 {
		t.Fatalf("result = %+v, want plain fall-through for an unknown tool", result)
	}
}

func TestCobraCompleter_Name(t *testing.T) {
	completer := NewCobraCompleter()
	if completer.Name() != "cobra" {
		t.Errorf("Name() = %q, want %q", completer.Name(), "cobra")
	}
}

func TestCobraCompleter_CachesTTL(t *testing.T) {
	completer := NewCobraCompleter()

	// TTL should be set
	if completer.cacheTTL == 0 {
		t.Error("cacheTTL should not be zero")
	}
}

func TestCobraCompleter_NeverBlocks(t *testing.T) {
	completer := NewCobraCompleter()
	ctx := context.Background()

	// Complete should return immediately without results (no prefetch done)
	start := time.Now()
	result, _ := completer.Complete(ctx, "kubectl get ", 12)
	elapsed := time.Since(start)

	// Should complete in under 10ms (just cache lookup)
	if elapsed > 10*time.Millisecond {
		t.Errorf("Complete took %v, expected < 10ms", elapsed)
	}

	if len(result.Items) != 0 {
		t.Errorf("Expected 0 items without prefetch, got %d", len(result.Items))
	}
}

func TestCobraCompleter_PrefetchCoalescesCommandPathLookup(t *testing.T) {
	completer := NewCobraCompleter()
	release := make(chan struct{})
	defer close(release)
	started := make(chan struct{}, 4)
	var calls atomic.Int32
	completer.resolvePath = func(name string) (string, error) {
		calls.Add(1)
		started <- struct{}{}
		<-release
		return "", errors.New("not found")
	}

	completer.Prefetch("kubectl get ", len("kubectl get "))
	select {
	case <-started:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("first command path lookup did not start")
	}

	for _, line := range []string{
		"kubectl describe ",
		"kubectl logs ",
		"kubectl apply ",
	} {
		completer.Prefetch(line, len(line))
	}
	time.Sleep(20 * time.Millisecond)

	if got := calls.Load(); got != 1 {
		t.Fatalf("expected one in-flight path lookup for kubectl, got %d", got)
	}
}

func TestCobraCompleter_PrefetchRetryAfterBusyCommandLookup(t *testing.T) {
	completer := NewCobraCompleter()
	release := make(chan struct{})
	defer close(release)
	started := make(chan struct{}, 4)
	var calls atomic.Int32
	completer.resolvePath = func(name string) (string, error) {
		calls.Add(1)
		started <- struct{}{}
		<-release
		return "", errors.New("not found")
	}

	const first = "kubectl get "
	const second = "kubectl describe "
	completer.Prefetch(first, len(first))
	select {
	case <-started:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("first command path lookup did not start")
	}

	completer.Prefetch(second, len(second))
	if !eventually(100*time.Millisecond, func() bool {
		completer.prefetchMu.RLock()
		defer completer.prefetchMu.RUnlock()
		_, ok := completer.prefetched["kubectl:__complete describe "]
		return !ok
	}) {
		t.Fatal("prefetch skipped by busy command lookup should be retryable")
	}
}

func TestCobraCompleter_PrefetchRetriesStaleCommandPathLookup(t *testing.T) {
	completer := NewCobraCompleter()
	release := make(chan struct{})
	defer close(release)
	started := make(chan struct{}, 4)
	var calls atomic.Int32
	completer.resolvePath = func(name string) (string, error) {
		if calls.Add(1) == 1 {
			started <- struct{}{}
			<-release
			return "", errors.New("stale lookup")
		}
		started <- struct{}{}
		return "", errors.New("not found")
	}

	completer.Prefetch("kubectl get ", len("kubectl get "))
	select {
	case <-started:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("first command path lookup did not start")
	}

	time.Sleep(90 * time.Millisecond)
	completer.Prefetch("kubectl describe ", len("kubectl describe "))
	if !eventually(100*time.Millisecond, func() bool {
		return calls.Load() == 2
	}) {
		t.Fatalf("expected stale command path lookup to be retried, got %d calls", calls.Load())
	}
}

func TestCobraCompleter_RetriesStaleFailedPrefetch(t *testing.T) {
	completer := NewCobraCompleter()
	completer.cacheTTL = time.Millisecond
	var calls atomic.Int32
	completer.resolvePath = func(name string) (string, error) {
		calls.Add(1)
		return "", errors.New("temporary lookup failure")
	}

	const line = "kubectl get "
	completer.Prefetch(line, len(line))
	if !eventually(100*time.Millisecond, func() bool {
		return calls.Load() == 1
	}) {
		t.Fatalf("expected first prefetch to attempt path lookup, got %d calls", calls.Load())
	}

	time.Sleep(2 * time.Millisecond)
	completer.Prefetch(line, len(line))
	if !eventually(100*time.Millisecond, func() bool {
		return calls.Load() == 2
	}) {
		t.Fatalf("expected stale failed prefetch to retry, got %d calls", calls.Load())
	}
}

func TestCobraCompleter_PrefetchReturnsWhenChildKeepsStdoutOpen(t *testing.T) {
	tmpDir := t.TempDir()
	cmdPath := filepath.Join(tmpDir, "fakecobra")
	if err := os.WriteFile(cmdPath, []byte("#!/bin/sh\nprintf 'pods\\tpod resources\\n'\nsleep 0.25 &\n"), 0o755); err != nil {
		t.Fatalf("WriteFile(%q): %v", cmdPath, err)
	}

	completer := NewCobraCompleter()
	start := time.Now()
	completer.doPrefetch(cmdPath, []string{"__complete"}, cmdPath+":__complete")
	elapsed := time.Since(start)

	if elapsed > 150*time.Millisecond {
		t.Fatalf("cobra prefetch waited %s for child-held stdout pipe, want under 150ms", elapsed)
	}
}

func TestCobraCompleter_ParseOutputLimitsLargeResultSet(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 5000; i++ {
		b.WriteString("resource-")
		b.WriteString(strconv.Itoa(i))
		b.WriteString("\tdescription\n")
	}

	result := NewCobraCompleter().parseOutput(b.String())
	if len(result.Items) > completionItemLimit {
		t.Fatalf("parseOutput returned %d items, want at most %d", len(result.Items), completionItemLimit)
	}
	if len(result.Items) != completionItemLimit {
		t.Fatalf("parseOutput returned %d items, want %d", len(result.Items), completionItemLimit)
	}
	if result.Items[0].Value != "resource-0" || result.Items[len(result.Items)-1].Value != "resource-199" {
		t.Fatalf("parseOutput should preserve first parsed entries, got first=%q last=%q",
			result.Items[0].Value,
			result.Items[len(result.Items)-1].Value,
		)
	}
}

func eventually(timeout time.Duration, fn func() bool) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if fn() {
			return true
		}
		time.Sleep(time.Millisecond)
	}
	return fn()
}
