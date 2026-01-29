package completion

import (
	"context"
	"testing"
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

	if router.Fuzzy() != false {
		t.Error("Fuzzy should default to false")
	}

	router.SetFuzzy(true)
	if router.Fuzzy() != true {
		t.Error("Fuzzy should be true after SetFuzzy(true)")
	}

	router.SetFuzzy(false)
	if router.Fuzzy() != false {
		t.Error("Fuzzy should be false after SetFuzzy(false)")
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
