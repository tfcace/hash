package completion

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tfcace/hash/internal/trace"
)

// MockCompleter for testing
type MockCompleter struct {
	name   string
	items  []Item
	called bool
}

func (m *MockCompleter) Name() string { return m.name }
func (m *MockCompleter) Complete(ctx context.Context, line string, pos int) (Result, error) {
	m.called = true
	return Result{Items: m.items}, nil
}

type cancelingCompleter struct {
	name   string
	cancel func()
	called bool
}

func (m *cancelingCompleter) Name() string { return m.name }
func (m *cancelingCompleter) Complete(ctx context.Context, line string, pos int) (Result, error) {
	m.called = true
	m.cancel()
	return Result{}, nil
}

type blockingCountingCompleter struct {
	release <-chan struct{}
	calls   atomic.Int32
}

func (m *blockingCountingCompleter) Name() string { return "blocking-counting" }
func (m *blockingCountingCompleter) Complete(ctx context.Context, line string, pos int) (Result, error) {
	m.calls.Add(1)
	<-m.release
	return Result{Items: []Item{{Value: "late-result"}}}, nil
}

type namedBlockingCompleter struct {
	name    string
	release <-chan struct{}
	calls   atomic.Int32
}

func (m *namedBlockingCompleter) Name() string { return m.name }
func (m *namedBlockingCompleter) Complete(ctx context.Context, line string, pos int) (Result, error) {
	m.calls.Add(1)
	<-m.release
	return Result{Items: []Item{{Value: "late-result"}}}, nil
}

type blockingPrefetchCompleter struct {
	release <-chan struct{}
	calls   atomic.Int32
}

func (m *blockingPrefetchCompleter) Name() string { return "blocking-prefetch" }
func (m *blockingPrefetchCompleter) Complete(ctx context.Context, line string, pos int) (Result, error) {
	return Result{}, nil
}
func (m *blockingPrefetchCompleter) Prefetch(line string, pos int) {
	m.calls.Add(1)
	<-m.release
}

type countingPrefetchCompleter struct {
	calls atomic.Int32
}

func (m *countingPrefetchCompleter) Name() string { return "counting-prefetch" }
func (m *countingPrefetchCompleter) Complete(ctx context.Context, line string, pos int) (Result, error) {
	return Result{}, nil
}
func (m *countingPrefetchCompleter) Prefetch(line string, pos int) {
	m.calls.Add(1)
}

func TestRouter_StopsWhenContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	first := &cancelingCompleter{name: "canceling", cancel: cancel}
	fallback := &MockCompleter{
		name:  "fallback",
		items: []Item{{Value: "late-result"}},
	}

	router := NewRouter()
	router.Register(first, 100)
	router.Register(fallback, 200)

	result, err := router.Complete(ctx, "test ", 5)
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}

	if !first.called {
		t.Fatal("canceling completer was not called")
	}
	if fallback.called {
		t.Fatal("router should not call lower-priority completers after context cancellation")
	}
	if len(result.Items) != 0 {
		t.Fatalf("expected no completions after cancellation, got %#v", result.Items)
	}
}

func TestRouter_CompleteBoundedDoesNotPileUpStuckWorkers(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	blocking := &blockingCountingCompleter{release: release}

	router := NewRouter()
	router.Register(blocking, PriorityFilesystem)

	ctx1, cancel1 := context.WithTimeout(context.Background(), 10*time.Millisecond)
	_, _ = router.CompleteBounded(ctx1, "cat ", len("cat "))
	cancel1()
	if got := blocking.calls.Load(); got != 1 {
		t.Fatalf("expected first bounded completion to start one worker, got %d", got)
	}

	ctx2, cancel2 := context.WithTimeout(context.Background(), 10*time.Millisecond)
	_, _ = router.CompleteBounded(ctx2, "cat ", len("cat "))
	cancel2()
	if got := blocking.calls.Load(); got != 1 {
		t.Fatalf("expected second bounded completion not to start duplicate stuck worker, got %d calls", got)
	}
}

func TestRouter_CompleteBoundedRetriesStaleStuckWorker(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	blocking := &blockingCountingCompleter{release: release}

	router := NewRouter()
	router.Register(blocking, PriorityFilesystem)

	ctx1, cancel1 := context.WithTimeout(context.Background(), 10*time.Millisecond)
	_, _ = router.CompleteBounded(ctx1, "cat ", len("cat "))
	cancel1()
	if got := blocking.calls.Load(); got != 1 {
		t.Fatalf("expected first bounded completion to start one worker, got %d", got)
	}

	time.Sleep(90 * time.Millisecond)

	ctx2, cancel2 := context.WithTimeout(context.Background(), 10*time.Millisecond)
	_, _ = router.CompleteBounded(ctx2, "cat ", len("cat "))
	cancel2()
	if got := blocking.calls.Load(); got != 2 {
		t.Fatalf("expected stale stuck worker to be retried, got %d calls", got)
	}
}

func TestRouter_CompleteBoundedUsesFallbackWhileEarlierCompleterIsStuck(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	blocking := &blockingCountingCompleter{release: release}
	fallback := &MockCompleter{
		name:  "fallback",
		items: []Item{{Value: "fallback-result"}},
	}

	router := NewRouter()
	router.Register(blocking, PriorityToolNative)
	router.Register(fallback, PriorityFilesystem)

	go func() {
		_, _ = router.CompleteBounded(context.Background(), "cat ", len("cat "))
	}()
	if !eventuallyTrue(100*time.Millisecond, func() bool {
		return blocking.calls.Load() == 1
	}) {
		t.Fatalf("expected first bounded completion to call stuck completer once, got %d", blocking.calls.Load())
	}

	result, err := completeBoundedWithTestTimeout(t, router, context.Background(), "cat ", len("cat "))
	if err != nil {
		t.Fatalf("CompleteBounded() error = %v", err)
	}
	if got := blocking.calls.Load(); got != 1 {
		t.Fatalf("expected second bounded completion to skip already-stuck completer, got %d calls", got)
	}
	if len(result.Items) != 1 || result.Items[0].Value != "fallback-result" {
		t.Fatalf("expected fallback completion while earlier completer is stuck, got %#v", result.Items)
	}
}

func TestRouter_CompleteReturnsWhenCompleterIgnoresContext(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	blocking := &blockingCountingCompleter{release: release}

	router := NewRouter()
	router.Register(blocking, PriorityToolNative)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	start := time.Now()
	result, err := completeWithTestTimeout(t, router, ctx, "cat ", len("cat "))
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if elapsed > 100*time.Millisecond {
		t.Fatalf("router completion took %s with stuck completer, want under 100ms", elapsed)
	}
	if len(result.Items) != 0 {
		t.Fatalf("expected no completion after context timeout, got %#v", result.Items)
	}
	if got := blocking.calls.Load(); got != 1 {
		t.Fatalf("expected stuck completer to be called once, got %d", got)
	}
}

func TestRouter_CompleteSkipsAlreadyStuckCompleterAndUsesFallback(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	blocking := &blockingCountingCompleter{release: release}
	fallback := &MockCompleter{
		name:  "fallback",
		items: []Item{{Value: "fallback-result"}},
	}

	router := NewRouter()
	router.Register(blocking, PriorityToolNative)
	router.Register(fallback, PriorityFilesystem)

	started := make(chan struct{})
	go func() {
		close(started)
		_, _ = router.Complete(context.Background(), "cat ", len("cat "))
	}()
	<-started
	if !eventuallyTrue(100*time.Millisecond, func() bool {
		return blocking.calls.Load() == 1
	}) {
		t.Fatalf("expected background completion to call stuck completer once, got %d", blocking.calls.Load())
	}

	result, err := completeWithTestTimeout(t, router, context.Background(), "cat ", len("cat "))
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if got := blocking.calls.Load(); got != 1 {
		t.Fatalf("expected second completion to skip already-stuck completer, got %d calls", got)
	}
	if len(result.Items) != 1 || result.Items[0].Value != "fallback-result" {
		t.Fatalf("expected fallback completion after skipping stuck completer, got %#v", result.Items)
	}
}

func TestRouter_StuckCompleterDoesNotSkipDistinctCompleterWithSameNameAndPriority(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	blocking := &namedBlockingCompleter{name: "same", release: release}
	fallback := &MockCompleter{
		name:  "same",
		items: []Item{{Value: "fallback-result"}},
	}

	router := NewRouter()
	router.Register(blocking, PriorityFilesystem)
	router.Register(fallback, PriorityFilesystem)

	started := make(chan struct{})
	go func() {
		close(started)
		_, _ = router.Complete(context.Background(), "cat ", len("cat "))
	}()
	<-started
	if !eventuallyTrue(100*time.Millisecond, func() bool {
		return blocking.calls.Load() == 1
	}) {
		t.Fatalf("expected background completion to call stuck completer once, got %d", blocking.calls.Load())
	}

	result, err := completeWithTestTimeout(t, router, context.Background(), "cat ", len("cat "))
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if got := blocking.calls.Load(); got != 1 {
		t.Fatalf("expected second completion to skip only the already-stuck instance, got %d calls", got)
	}
	if len(result.Items) != 1 || result.Items[0].Value != "fallback-result" {
		t.Fatalf("expected same-name fallback completion after skipping stuck instance, got %#v", result.Items)
	}
}

func TestRouter_PrefetchBoundedDoesNotBlock(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	blocking := &blockingPrefetchCompleter{release: release}

	router := NewRouter()
	router.Register(blocking, PriorityToolNative)

	start := time.Now()
	router.PrefetchBounded("kubectl ", len("kubectl "))
	elapsed := time.Since(start)
	if elapsed > 50*time.Millisecond {
		t.Fatalf("PrefetchBounded took %s with blocking prefetcher, want under 50ms", elapsed)
	}
	if !eventuallyTrue(100*time.Millisecond, func() bool {
		return blocking.calls.Load() == 1
	}) {
		t.Fatalf("expected bounded prefetch to start one background prefetch, got %d", blocking.calls.Load())
	}
}

func TestRouter_PrefetchBoundedDoesNotPileUpStuckWorkers(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	blocking := &blockingPrefetchCompleter{release: release}

	router := NewRouter()
	router.Register(blocking, PriorityToolNative)

	router.PrefetchBounded("kubectl ", len("kubectl "))
	if !eventuallyTrue(100*time.Millisecond, func() bool {
		return blocking.calls.Load() == 1
	}) {
		t.Fatalf("expected first bounded prefetch to start one worker, got %d", blocking.calls.Load())
	}
	router.PrefetchBounded("kubectl get ", len("kubectl get "))
	if got := blocking.calls.Load(); got != 1 {
		t.Fatalf("expected second bounded prefetch not to start duplicate stuck worker, got %d calls", got)
	}
}

func TestRouter_PrefetchBoundedRetriesStaleStuckWorker(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	blocking := &blockingPrefetchCompleter{release: release}

	router := NewRouter()
	router.Register(blocking, PriorityToolNative)

	router.PrefetchBounded("kubectl ", len("kubectl "))
	if !eventuallyTrue(100*time.Millisecond, func() bool {
		return blocking.calls.Load() == 1
	}) {
		t.Fatalf("expected first bounded prefetch to start one worker, got %d", blocking.calls.Load())
	}

	time.Sleep(90 * time.Millisecond)

	router.PrefetchBounded("kubectl get ", len("kubectl get "))
	if !eventuallyTrue(100*time.Millisecond, func() bool {
		return blocking.calls.Load() == 2
	}) {
		t.Fatalf("expected stale stuck prefetch worker to be retried, got %d calls", blocking.calls.Load())
	}
}

func TestRouter_PrefetchBoundedDoesNotStarveLaterPrefetchers(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	blocking := &blockingPrefetchCompleter{release: release}
	later := &countingPrefetchCompleter{}

	router := NewRouter()
	router.Register(blocking, PriorityToolNative)
	router.Register(later, PriorityFilesystem)

	router.PrefetchBounded("kubectl ", len("kubectl "))
	if !eventuallyTrue(100*time.Millisecond, func() bool {
		return blocking.calls.Load() == 1
	}) {
		t.Fatalf("expected blocking prefetcher to start once, got %d", blocking.calls.Load())
	}
	if !eventuallyTrue(100*time.Millisecond, func() bool {
		return later.calls.Load() == 1
	}) {
		t.Fatalf("expected later prefetcher to run despite stuck earlier prefetcher, got %d", later.calls.Load())
	}
}

func TestRouter_FirstCompleterWins(t *testing.T) {
	mock1 := &MockCompleter{
		name:  "first",
		items: []Item{{Value: "from-first"}},
	}
	mock2 := &MockCompleter{
		name:  "second",
		items: []Item{{Value: "from-second"}},
	}

	router := NewRouter()
	router.Register(mock1, 100) // Higher priority (lower number)
	router.Register(mock2, 200) // Lower priority

	ctx := context.Background()
	result, err := router.Complete(ctx, "test ", 5)
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}

	// First completer should win (has results)
	if !mock1.called {
		t.Error("First completer was not called")
	}
	if len(result.Items) != 1 {
		t.Errorf("Items count = %d, want 1", len(result.Items))
	}
	if result.Items[0].Value != "from-first" {
		t.Errorf("Value = %q, want %q", result.Items[0].Value, "from-first")
	}
}

func TestRouter_FallsThrough(t *testing.T) {
	mock1 := &MockCompleter{
		name:  "first",
		items: []Item{}, // No results
	}
	mock2 := &MockCompleter{
		name:  "second",
		items: []Item{{Value: "from-second"}},
	}

	router := NewRouter()
	router.Register(mock1, 100) // Higher priority (lower number)
	router.Register(mock2, 200) // Lower priority

	ctx := context.Background()
	result, err := router.Complete(ctx, "test ", 5)
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}

	// Should fall through to second
	if !mock1.called {
		t.Error("First completer was not called")
	}
	if !mock2.called {
		t.Error("Second completer was not called")
	}
	if len(result.Items) != 1 {
		t.Errorf("Items count = %d, want 1", len(result.Items))
	}
	if result.Items[0].Value != "from-second" {
		t.Errorf("Value = %q, want %q", result.Items[0].Value, "from-second")
	}
}

func TestRouter_PriorityOrdering(t *testing.T) {
	router := NewRouter()

	// Register in wrong order
	mock1 := &MockCompleter{name: "low", items: []Item{{Value: "low"}}}
	mock2 := &MockCompleter{name: "high", items: []Item{{Value: "high"}}}

	router.Register(mock1, 300) // Lower priority (higher number)
	router.Register(mock2, 100) // Higher priority (lower number)

	ctx := context.Background()
	result, _ := router.Complete(ctx, "test ", 5)

	// High priority should be called first and win
	if result.Items[0].Value != "high" {
		t.Errorf("Value = %q, want %q (high priority)", result.Items[0].Value, "high")
	}
}

func TestRouter_FuzzyFiltering(t *testing.T) {
	router := NewRouter()
	router.SetFuzzy(true)

	// Create a mock completer that returns fixed items
	mock := &MockCompleter{
		name: "mock",
		items: []Item{
			{Value: "config.toml"},
			{Value: "context.go"},
			{Value: "container.yaml"},
			{Value: "readme.md"},
		},
	}
	router.Register(mock, PriorityFilesystem)

	// Complete with query "cont" - should fuzzy match context and container
	result, err := router.Complete(context.Background(), "cat cont", 8)
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}

	// Should get fuzzy matches sorted by score
	if len(result.Items) < 2 {
		t.Errorf("Expected at least 2 fuzzy matches, got %d", len(result.Items))
	}

	// First result should be best match (container or context)
	if result.Items[0].Value != "container.yaml" && result.Items[0].Value != "context.go" {
		t.Errorf("First result should be container or context, got %q", result.Items[0].Value)
	}
}

func TestRouter_FuzzyDisabled(t *testing.T) {
	router := NewRouter()
	router.SetFuzzy(false)

	mock := &MockCompleter{
		name: "mock",
		items: []Item{
			{Value: "config.toml"},
			{Value: "readme.md"},
		},
	}
	router.Register(mock, PriorityFilesystem)

	// With fuzzy disabled, should return items as-is
	result, err := router.Complete(context.Background(), "cat ", 4)
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}

	if len(result.Items) != 2 {
		t.Errorf("Expected 2 items unchanged, got %d", len(result.Items))
	}
}

func TestRouter_LimitsOversizedResults(t *testing.T) {
	items := make([]Item, 5000)
	for i := range items {
		items[i] = Item{Value: "item-" + strconv.Itoa(i)}
	}

	router := NewRouter()
	router.Register(&MockCompleter{name: "huge", items: items}, PriorityFilesystem)

	result, err := router.Complete(context.Background(), "cat ", len("cat "))
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if len(result.Items) > 200 {
		t.Fatalf("expected router to cap oversized completion results, got %d items", len(result.Items))
	}
}

func TestRouter_EmitsCompletionTraceEvents(t *testing.T) {
	tmpDir := t.TempDir()
	tracePath := filepath.Join(tmpDir, "completion-trace.jsonl")
	t.Setenv("HASH_TRACE", "completion")
	t.Setenv("HASH_TRACE_PATH", tracePath)
	t.Setenv("HASH_TRACE_LEVEL", "detailed")

	if err := trace.Init(); err != nil {
		t.Fatalf("trace init: %v", err)
	}
	t.Cleanup(trace.Close)

	router := NewRouter()
	router.Register(&MockCompleter{
		name:  "mock",
		items: []Item{{Value: "result"}},
	}, PriorityFilesystem)

	if _, err := router.Complete(context.Background(), "cat r", 5); err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	trace.Close()

	content, err := os.ReadFile(tracePath)
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", tracePath, err)
	}
	text := string(content)
	if !strings.Contains(text, `"event":"router_start"`) {
		t.Fatalf("trace missing router_start event:\n%s", text)
	}
	if !strings.Contains(text, `"event":"completer_done"`) {
		t.Fatalf("trace missing completer_done event:\n%s", text)
	}
}

func TestRouter_AllCompletersEmpty(t *testing.T) {
	router := NewRouter()

	mock1 := &MockCompleter{name: "first", items: []Item{}}
	mock2 := &MockCompleter{name: "second", items: []Item{}}

	router.Register(mock1, 100)
	router.Register(mock2, 200)

	result, err := router.Complete(context.Background(), "test ", 5)
	if err != nil {
		t.Fatalf("Complete() should not error when all completers empty, got %v", err)
	}

	if len(result.Items) != 0 {
		t.Errorf("Expected 0 items, got %d", len(result.Items))
	}

	// Both should have been called
	if !mock1.called || !mock2.called {
		t.Error("All completers should be tried when none return results")
	}
}

func TestRouter_NoCompletersRegistered(t *testing.T) {
	router := NewRouter()

	result, err := router.Complete(context.Background(), "test ", 5)
	if err != nil {
		t.Fatalf("Complete() should not error with no completers, got %v", err)
	}

	if len(result.Items) != 0 {
		t.Errorf("Expected 0 items with no completers, got %d", len(result.Items))
	}
}

func TestRouter_FuzzyFilteringOnPathQuery(t *testing.T) {
	router := NewRouter()
	router.SetFuzzy(true)

	// Simulates completing "~/Go" - should filter on "Go", not "~/Go"
	mock := &MockCompleter{
		name: "mock",
		items: []Item{
			{Value: "Go/"},
			{Value: "Documents/"},
			{Value: "GoLand/"},
		},
	}
	router.Register(mock, PriorityFilesystem)

	result, _ := router.Complete(context.Background(), "cd ~/Go", 7)

	// Should fuzzy filter on "Go", returning Go/ and GoLand/
	if len(result.Items) != 2 {
		t.Errorf("Expected 2 fuzzy matches for 'Go', got %d: %v", len(result.Items), result.Items)
	}
}

func TestRouter_FuzzySkipsDirectoryListing(t *testing.T) {
	router := NewRouter()
	router.SetFuzzy(true)

	mock := &MockCompleter{
		name: "mock",
		items: []Item{
			{Value: "file1.txt"},
			{Value: "file2.txt"},
			{Value: "other.go"},
		},
	}
	router.Register(mock, PriorityFilesystem)

	// Query ends with "/" - should NOT apply fuzzy filtering (listing dir contents)
	result, _ := router.Complete(context.Background(), "ls mydir/", 9)

	// All items should be returned unfiltered
	if len(result.Items) != 3 {
		t.Errorf("Directory listing should not filter, expected 3, got %d", len(result.Items))
	}
}

func TestRouter_Completers(t *testing.T) {
	router := NewRouter()

	mock1 := &MockCompleter{name: "first", items: []Item{}}
	mock2 := &MockCompleter{name: "second", items: []Item{}}

	router.Register(mock1, 200)
	router.Register(mock2, 100) // Lower priority = higher precedence

	completers := router.Completers()
	if len(completers) != 2 {
		t.Fatalf("Expected 2 completers, got %d", len(completers))
	}

	// Should be sorted by priority (100 before 200)
	if completers[0].Name() != "second" {
		t.Errorf("First completer should be 'second' (priority 100), got %q", completers[0].Name())
	}
	if completers[1].Name() != "first" {
		t.Errorf("Second completer should be 'first' (priority 200), got %q", completers[1].Name())
	}
}

func TestRouter_FuzzyGetter(t *testing.T) {
	router := NewRouter()

	if router.Fuzzy() {
		t.Error("Fuzzy should default to false")
	}

	router.SetFuzzy(true)
	if !router.Fuzzy() {
		t.Error("Fuzzy should be true after SetFuzzy(true)")
	}

	router.SetFuzzy(false)
	if router.Fuzzy() {
		t.Error("Fuzzy should be false after SetFuzzy(false)")
	}
}

func eventuallyTrue(timeout time.Duration, fn func() bool) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if fn() {
			return true
		}
		time.Sleep(time.Millisecond)
	}
	return fn()
}

func completeWithTestTimeout(t *testing.T, router *Router, ctx context.Context, line string, pos int) (Result, error) {
	t.Helper()
	type completeResult struct {
		result Result
		err    error
	}
	done := make(chan completeResult, 1)
	go func() {
		result, err := router.Complete(ctx, line, pos)
		done <- completeResult{result: result, err: err}
	}()
	select {
	case result := <-done:
		return result.result, result.err
	case <-time.After(100 * time.Millisecond):
		t.Fatal("router completion did not return within test timeout")
		return Result{}, nil
	}
}

func completeBoundedWithTestTimeout(t *testing.T, router *Router, ctx context.Context, line string, pos int) (Result, error) {
	t.Helper()
	type completeResult struct {
		result Result
		err    error
	}
	done := make(chan completeResult, 1)
	go func() {
		result, err := router.CompleteBounded(ctx, line, pos)
		done <- completeResult{result: result, err: err}
	}()
	select {
	case result := <-done:
		return result.result, result.err
	case <-time.After(100 * time.Millisecond):
		t.Fatal("bounded router completion did not return within test timeout")
		return Result{}, nil
	}
}

// mockEnvProvider implements EnvProvider for testing
type mockEnvProvider struct {
	environ []string
}

func (m *mockEnvProvider) Environ() []string {
	return m.environ
}

func TestRouter_EnvVarCompletion(t *testing.T) {
	router := NewRouter()

	// Register env completer with mock provider
	envProvider := &mockEnvProvider{
		environ: []string{"HASH_SRC=/home/user/hash", "HOME=/home/user", "PATH=/usr/bin"},
	}
	envCompleter := NewEnvCompleter(envProvider)
	router.Register(envCompleter, PriorityEnv)

	// Also register file completer to verify env completer wins for $
	fileCompleter := NewFileCompleter()
	router.Register(fileCompleter, PriorityFilesystem)

	// Complete "echo $HA"
	result, err := router.Complete(context.Background(), "echo $HA", 8)
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}

	// Should get $HASH_SRC from env completer
	if len(result.Items) != 1 {
		t.Errorf("Expected 1 item ($HASH_SRC), got %d: %v", len(result.Items), result.Items)
		return
	}
	if result.Items[0].Value != "$HASH_SRC" {
		t.Errorf("Expected $HASH_SRC, got %s", result.Items[0].Value)
	}
}

// pendingCompleter matches the input but has no data yet.
type pendingCompleter struct{}

func (pendingCompleter) Name() string { return "pending" }
func (pendingCompleter) Complete(ctx context.Context, line string, pos int) (Result, error) {
	return Result{Pending: true}, nil
}

// itemsCompleter always answers, standing in for the filesystem fallback.
type itemsCompleter struct{ items []Item }

func (itemsCompleter) Name() string { return "items" }
func (c itemsCompleter) Complete(ctx context.Context, line string, pos int) (Result, error) {
	return Result{Items: c.items}, nil
}

// A pending higher-priority completer owns the argument: answering it with
// filenames would be actively wrong, so the router must stop and report
// pending instead.
func TestRouter_PendingCompleterStopsFallthrough(t *testing.T) {
	r := NewRouter()
	r.Register(itemsCompleter{items: []Item{{Value: "some-file.go"}}}, PriorityFilesystem)
	r.Register(pendingCompleter{}, PriorityPlugin)

	result, err := r.Complete(context.Background(), "docker rm ", len("docker rm "))
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if len(result.Items) != 0 {
		t.Errorf("items = %+v, want none while a matching completer is pending", result.Items)
	}
	if !result.Pending {
		t.Error("router should report pending so the UI can say fetching")
	}
}

// Nothing pending: the fallback still answers as before.
func TestRouter_FallsThroughWhenNothingPending(t *testing.T) {
	r := NewRouter()
	r.Register(itemsCompleter{items: []Item{{Value: "some-file.go"}}}, PriorityFilesystem)
	r.Register(itemsCompleter{}, PriorityPlugin)

	result, err := r.Complete(context.Background(), "ls ", len("ls "))
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if len(result.Items) != 1 {
		t.Errorf("items = %+v, want the fallback result", result.Items)
	}
}
